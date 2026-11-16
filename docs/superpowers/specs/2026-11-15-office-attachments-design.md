# Open mail attachments in Euro-Office — design

Owner request 2026-11-15 (worker `g-office`). Repos: `kydrive-server` (this file, first PR)
and `KyPost-Server` (second PR). Outlook-on-the-web behaviour: an Office attachment opens
read-only in the browser; **Edit in KyDrive** saves a copy to My files and opens the editor.

## Scope

1. **View.** KyPost shows **Open** on a received attachment whose extension and declared
   content type are both one of docx, xlsx, pptx, odt, ods, odp, csv, pdf. KyPost's server
   sends the part to KyDrive as a *viewing copy*; the browser opens it in Euro-Office in view
   mode.
2. **Edit.** **Edit in KyDrive** is the existing `POST /api/kydrive/save-attachment`, then
   KyDrive's ordinary editor page (`/editor.html?file=<id>`) on the saved file.
3. **Never for protected mail.** Neither action exists for a PGP- or S/MIME-encrypted
   message. KyPost's server refuses by the same raw-message verdict its agent tools use
   (`rawEncrypted`, fail closed), and the web client hides both actions on `pgpEncrypted`
   messages and on client-decrypted attachments (which never pass through the server list).

No new suite pair: this rides the existing KyPost↔KyDrive pairing, its pair secret, its
per-user switch (**Let KyPost save and share my files**, resource owner side) and the
`syncauth` v1 signed-route rules of `docs/KYPOST.md`.

## Threat model

| Threat | Answer |
|---|---|
| A hostile attachment exploits the parser | Euro-Office already parses every uploaded document in its own container; the viewing copy goes through the same path, no new parser in KyDrive or KyPost. KyDrive sniffs the first 512 bytes (`http.DetectContentType`): zip for OOXML/ODF, `%PDF-` for pdf, text for csv; a mismatch is 415 `bad_type`. |
| A type confusion (an `.exe` named `.docx`, an HTML file as `text/csv`) | KyPost requires extension and declared type to agree (`application/octet-stream` accepted as "unspecified", see Q1); KyDrive re-checks extension and magic bytes. Euro-Office is told the extension's own type, never a sender-supplied one. |
| Decrypted end-to-end content reaches a server-side editor | Server refusal on any encrypted part, armor or unparseable message (fail closed); client hides the actions. Test on both sides. |
| Another user's message or attachment | KyPost reads only the caller's own mailbox (`withMailAuth`/`withMailbox`, existing); KyDrive opens a copy only for its owner's cookie session. |
| The view config is reused to save | The document key is `view-<id>`, a namespace `ParseDocumentKey` refuses; the callback refuses status 2/6 for it, and the config sets `edit:false`. The JWT signs `mode:"view"`, so a browser cannot flip it. |
| A revoked sign-in keeps viewing | Opening binds the copy to the browser sign-in (as editor sessions are); the revocations that end its editor sessions revoke the copy, Euro-Office's download stops and a drop of its key is queued. |
| Euro-Office download URL guessed | `/api/editor/view/{id}` needs Euro-Office's outbox JWT naming that exact URL, as `/api/editor/download/{id}` does. |
| Replay of a signed request | `syncauth` replay guard: 200 `duplicate`, nothing issued twice. |
| Ticket replay | The upload ticket is single-use: the PUT that stores the bytes clears its hash in the same transaction. |
| A viewing copy is shared or found | It is not a `drive_files` row: shares, organisation links, guest links, search, the change feed, WebDAV, MCP and desktop sync never see it. Its blob is not a version, so bulk backups skip it. |
| Disk exhaustion | Counted in My files quota while it exists, at most 4 live per person, at most `min(KYDRIVE_MAX_FILE_SIZE, 25 MiB)` each, 24-hour life. |
| Names in logs | Audit and logs carry IDs only (`view:<id>`, `event:<id>`); the name is sealed at rest like every file name. |

## Data KyDrive stores

Schema 24, `drive_view_copies`:

| Column | Meaning |
|---|---|
| `id` | UUID, the copy and its blob name; not a credential |
| `owner` | user ID |
| `workspace` | the owner's My files, for quota |
| `name` | sealed name (binding `drive_view_copies:<id>`) |
| `size` | declared size, exact |
| `token_hash` | SHA-256 of the 256-bit upload ticket; cleared when the bytes land |
| `ticket_expires` | Unix seconds, issue + 15 minutes |
| `expires` | Unix seconds, issue + 24 hours |
| `stored` | 0 until the bytes land |

The sealed bytes live in `<data>/view/<id>`, outside `<data>/blobs`, so neither the blob
orphan sweep nor bulk backup touches them.

**Deletion**, whichever comes first: the viewer page closes (`pagehide` →
`POST /api/drive/view-copies/{id}/close`), Euro-Office reports the document closed (callback
status 4 for its key), the ticket expires unused (15 minutes), or 24 hours pass. Access ends
at `expires` exactly; the hourly sweep removes rows and bytes, and
`SweepOrphans(<data>/view, 25h)` removes any file a crash left behind.

## Routes

### KyDrive

| Route | Auth | Purpose |
|---|---|---|
| `POST /api/suite/kypost/view-tickets` (`kypost.drive.view`) | `syncauth` under the pair secret, `sub`+`issuer`, switch on, 60/min/person | Issue a viewing-copy ticket: `{"sub","issuer","name","size"}` → 201 `{"upload_url","expires"}` |
| `PUT /api/suite/view/<ticket>` | the ticket (256 bits, single use); any `Origin` refused | Store the bytes: exactly `size`, one request → 201 `{"view_url","expires"}` |
| `GET /view.html?copy=<id>` | signed-in browser session | Viewer page |
| `POST /api/drive/view-copies/{id}/editor` | session + CSRF; owner only; service, device and peer contexts refused | View-mode editor config |
| `POST /api/drive/view-copies/{id}/close` | session + CSRF; owner only | Delete now |
| `GET /api/editor/view/{id}` | Euro-Office outbox JWT for that URL | The copy's bytes |
| `POST /api/editor/callback` (existing) | Euro-Office JWT | `view-<id>` key: status 4 deletes, 2/6 refused |

Ticket refusals: 400 `bad_body` (name rules, extension not in the list, size missing or
negative), 413 `too_large`, 507 `quota`, 429 `too_many_tickets` (4 live), 409 `editor_off`
(Euro-Office not configured), plus every signed-route refusal of `docs/KYPOST.md`.
Upload refusals: 403 `bad_origin`, 404 `not_found` (unknown, spent or expired),
409 `sharing_off`, 400 `size_mismatch`, 415 `bad_type`, 507 `quota`, 429 `rate_limited`.

Status (`kypost.drive.status`) gains `"viewer": true` while Euro-Office is configured, so
KyPost offers Open and Edit only then; an older KyDrive omits it and the actions stay hidden.

### KyPost

| Route | Auth | Purpose |
|---|---|---|
| `POST /api/kydrive/view-attachment` | mail session (`withMailAuth`, `withMailbox`), per-user KyDrive limit, KyDrive available and `viewer` | `{"messageId","mailbox","index"}` → 201 `{"viewUrl"}` |
| `POST /api/kydrive/save-attachment` (existing) | as today | `"edit":true` also requires `viewer` and answers `editUrl` |

KyPost refusals: 409 `encrypted` (protected message), 415 `unsupported_type` (extension or
type outside the list, or the two disagree), 413 `too_large` (over the save-attachment
limit: KyDrive's `max_file_bytes`, and 25 MiB, the inbound cap), 404 `not_found` (no such
message or index in the caller's mailbox), 409 `kydrive_off`.

## Defaults

- On whenever the pairing and the person's existing KyPost switch are on and Euro-Office is
  configured. Not outsider-facing, so no administrator switch.
- View mode with `edit`, `download`, `print`, `comment`, `review` and `fillForms` all false.
- 24-hour life, 4 live copies per person, quota counted.

## Audit

KyDrive: the signed-route record `kypost.view` (`event:<id>`), `kypost.view_stored` and
`view.opened` (`view:<id>`), `view.closed`/`view.expired` events. KyPost: `kydrive attachment
opened` / `kydrive attachment edit` log lines with actor, action and result, never the file
name or message subject.

## Not built

- No preview without Euro-Office (no in-KyPost renderer, no image or text preview): a second
  parser of hostile files in KyPost's process is the risk this design avoids.
- No viewing for client-decrypted (end-to-end) attachments, ever.
- No "Save a copy" button inside the viewer: Edit in KyDrive is that action, from KyPost.
- No reuse of a viewing copy across opens: each Open uploads again (bounded by 4 live, 24 h).
- No Euro-Office cache purge on close: its converted cache expires on its own schedule, as
  for every document KyDrive opens today.
- No deletion on account deactivation beyond the sweep: a deactivated account cannot sign in
  to open one, and every copy is gone within 24 hours.

## Owner questions (defaults chosen)

1. **`application/octet-stream`.** Many mailers send Office files and PDFs with it. Default:
   accepted as "type unspecified", with KyDrive's magic-byte check as the backstop; any
   other disagreeing type is refused.
2. **Closing the tab deletes the copy.** Reloading the viewer then says the copy has closed
   and to open it again from KyPost. Default: delete on close, as the brief says.
