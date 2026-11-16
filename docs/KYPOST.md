# KyPost: saving and sharing files from mail

[Permissions](PERMISSIONS.md) · [Security](../SECURITY.md)

KyPost saves mail attachments into a person's My files and shares them by organisation link,
so a large attachment travels as a link. KyDrive is the resource owner: it decides whether a
person allows it and what KyPost may do. This is section C of the suite integration contract
(wave 1, 2026-11-14) and sections I2, J and K (wave 2); this file is KyDrive's durable copy. Both sides change together.

## Email a link (KyDrive → KyPost)

Section J of the wave 2 contract. A file's menu in KyDrive offers **Email a link** when the
person may share the file and KyPost is in their suite app list from KyIdentity with an
`https://` address. A dialog takes the expiry (1, 7 or 30 days, or never; default 30) and
**Write email** makes an ordinary organisation reader link (`POST /api/drive/files/{id}/links`,
listed and revocable with the file's others), then opens, in a new tab with no opener,
`https://<kypost-origin>/compose?subject=<file name>&body=<name, link and expiry>` (subject at
most 255 bytes, body at most 2000, cut at a character boundary; no `to`). Nothing is sent
until the person sends it in KyPost. No pairing is needed: the browser carries the link.

## Pairing

KyDrive and KyPost share one secret, the pair secret, and every server-to-server request is
signed with `ky-primitives/syncauth` v1 under it.

- **From the environment:** `KYDRIVE_KYPOST_URL` (`https://host`, nothing else) and
  `KYDRIVE_KYPOST_SECRET_FILE` (a private file holding at least 32 bytes). Either without the
  other, or a bad value, fails startup.
- **Otherwise from KyIdentity:** `suitedir.Peer(ctx, "kypost")` with KyDrive's own sign-in
  client (`KY_KYSIGNON_ISSUER`, `_CLIENT_ID`, `_SECRET`), once at startup (5 s), then hourly;
  a failure retries from one minute, doubling to an hour. KyIdentity answering `no_peer` or
  refusing the client unlinks at once; an unreachable KyIdentity keeps the current link. A
  signature that fails under a KyIdentity link asks KyIdentity again, at most once a minute,
  since the secret may have rotated.
- The secret lives in memory only and is never logged. **Server settings → System → KyPost**
  shows whether KyPost is connected, how, its address, or why not.
- KyDrive never calls KyPost, so it makes no outbound request to it and has no
  `KYDRIVE_KYPOST_ALLOW_PRIVATE` (see Deviations).

## The switch

**Settings → Files → Let KyPost save and share my files**, shown while KyPost is paired. It
is on by default when KyIdentity paired the apps and off when the environment did; a stored
choice wins. Off, every route below answers 409 `sharing_off` (status answers
`"enabled":false`), including uploads with tickets issued earlier. The switch is read again
inside the transaction that publishes a file or creates a link, so an upload still streaming
when it is turned off publishes nothing; its staged bytes and ticket are removed. The same
check refuses a write once the pairing it came under has ended or changed address or secret
(404 `kypost_off`). Each ticket also records a keyed fingerprint of the pairing that issued
it (HMAC under the pair secret of source and address, never the secret); after a rotation or
re-pairing an old ticket answers 404 `kypost_off` and is removed with any staged bytes.

### Guest links through KyPost

**Settings → Files → Let KyPost create guest links for people outside the organisation** is a
second switch, off by default, shown only while KyPost is paired and the administrator's
guest-link policy (Server settings → Guest links) is on; it cannot be turned on while the
policy is off (409 `guest_links_off`) and is disabled while the first switch is off. It is
stored only when the person turns it on (`drive_peer_guest`) and audited
`kypost.guest_switch`.

## Where files go

Into the person's own My files, folder `Mail attachments`, made at its root on first use. It
is an ordinary folder: the person can rename or move it within My files and it stays KyPost's
folder; trashed or purged, the next upload makes a new one (a live root folder already named
`Mail attachments` is adopted; a file holding the name makes `Mail attachments (2)`). A name
already taken in it gets ` (2)`, ` (3)` before the extension, at the first byte and again at
completion if it was taken meanwhile. Saved files are ordinary files of that person:
encrypted at rest, versioned, in the change feed and quota, audited as theirs
(`kypost.file_saved`, with `peer=kypost` and the ticket ID).

## Signed routes

| Event type | Route |
|---|---|
| `kypost.drive.ticket` | `POST /api/suite/kypost/upload-tickets` |
| `kypost.drive.link` | `POST /api/suite/kypost/links` |
| `kypost.drive.status` | `POST /api/suite/kypost/status` |
| `kypost.drive.fetch` | `POST /api/suite/kypost/fetch` |

Every request is checked in this order; refusals are `{"error","code"}`:

| Status | Code | When |
|---|---|---|
| 429 | `rate_limited` | Over 600 signed requests a minute from one address (before any check), or the per-user limit below. `Retry-After` set. |
| 404 | `kypost_off` | Not paired. |
| 401 | `bad_signature` | Signature, timestamp (±5 minutes) or event type wrong, or a body over 64 KiB. |
| 200 | (`{"outcome":"duplicate"}`) | A verified replay of an event ID already accepted: not acted on again. |
| 400 | `bad_body` | Not JSON, or no `sub`/`issuer`. |
| 404 | `no_user` | `issuer` is not KyDrive's KyIdentity, or no active account is linked to `sub` (sign-in or SCIM), or it is a dedicated installer administrator. |
| 429 | `rate_limited` | Per user: tickets, links and fetches 60 a minute, status 120. |
| 409 | `sharing_off` | The person's switch is off (not for status). |

An event refused with 429 or a 5xx is forgotten by the replay guard, so KyPost's retry under
the same event ID is processed. Each verified request is audited with IDs only
(`kypost.ticket`, `kypost.link`, `kypost.status`, `kypost.fetch`; resource `event:<id>`, details `outcome=ok`
or `refused=<code>`). Unsigned refusals are not audited, so nobody can fill the audit log.

### Upload ticket

```json
{"sub":"…","issuer":"…","name":"report.pdf","size":7340032}
```

201:

```json
{"upload_url":"https://drive.example/api/suite/upload/<ticket>","expires":"2026-11-14T15:04:05Z","max_chunk":67108864}
```

The ticket is 256 random bits (43 base64url characters), stored as SHA-256 only, for one file
of exactly `size` bytes (0 allowed) named `name`, for that person, valid 15 minutes, at most 8
live per person. Refusals: 400 `bad_body` (name rules as for ordinary uploads, and the name
with ` (100)` added must still fit 255 bytes; size missing or negative), 413 `too_large`
(over `KYDRIVE_MAX_FILE_SIZE`), 507 `quota` (more than My files has free now), 429
`too_many_tickets`.

### Upload

`PUT <upload_url>` with the raw bytes. No cookie, no CSRF token, no signature: the ticket is
the credential. Up to `max_chunk` bytes in one request with `Content-Length` equal to `size`;
larger files in `Content-Range: bytes start-end/size` chunks of at most `max_chunk`, in
order, to the same URL. Each request's `Content-Length` must equal its range and is enforced
while streaming. The bytes go through KyDrive's ordinary resumable upload session, sealed on
disk as they arrive; the session expires with the ticket and the hourly sweep discards the
partial bytes.

| Status | Code / body | When |
|---|---|---|
| 201 | `{"file_id":"…","name":"report (2).pdf","size":7340032}` | The last bytes: the file is published and the ticket spent. |
| 204 | `Upload-Offset: <received>` | A chunk stored; send the next. |
| 403 | `bad_origin` | An `Origin` other than the paired KyPost's. |
| 404 | `kypost_off` / `not_found` | Not paired / ticket unknown, spent or expired. |
| 409 | `sharing_off` | The person's switch is off. |
| 409 | `conflict` | A chunk not at the received offset (`Upload-Offset` says where to resume). |
| 400 | `bad_range` / `size_mismatch` | A malformed range or one whose total is not `size` / a body that is not exactly its declared length. |
| 413 | `too_large` | A request over `max_chunk`. |
| 507 | `quota` | My files has no room at completion. |
| 429 | `rate_limited` | 240 a minute per address, 600 a minute per person. |

This one route answers CORS for exactly the paired KyPost origin: `Access-Control-Allow-Origin`
reflected, `Allow-Methods: PUT, OPTIONS`, `Allow-Headers: Content-Type, Content-Range`,
`Expose-Headers: Upload-Offset`, never credentials, `Vary: Origin`, `Referrer-Policy:
no-referrer`. KyDrive's global CORS and CSP are unchanged.

### Link

```json
{"sub":"…","issuer":"…","file_id":"…","days":30}
```

Creates an organisation link (signed-in members only), role reader, through KyDrive's ordinary
link code. `days` is 1, 7, 30 or 0 for no expiry; absent means 30; anything else 400
`bad_body`. 201 `{"url":"https://drive.example/l/#<token>","expires":"<RFC 3339>"}`, with
`"expires":null` for no expiry. 404 `not_found` unless `file_id` is a live file directly in
the person's own Mail attachments folder.

`"audience":"guest"` (contract K; absent or `"organisation"` is the organisation link above)
makes a KyDrive guest link instead, with the same rights as one made in KyDrive's Share
dialog: view and download that one file, no upload, no password. It needs the
administrator's guest-link policy on and the person's guest switch on (read again inside the
transaction that makes the link; else 409 `guest_links_off`) and `days` 1 to the policy's `max_days` (absent
means 30; 0 or anything else 400 `bad_body`); the file rule is the same (404 `not_found`). 201
`{"url":"https://drive.example/g/#<token>","expires":"<RFC 3339>"}`. The link is listed with
the file's guest links, so the person can revoke it in the Share dialog; it is audited
`guest_link.created` (`file:<id>`, `link=<id> peer=kypost`) and the drive event says
`peer=kypost`. Turning the policy or either switch off refuses new ones; links already made
keep working until they expire or are revoked, and none works while the policy is off.
An unknown `audience` is 400 `bad_body`.

### Status

`{"sub":"…","issuer":"…"}` → 200
`{"enabled":true,"free_bytes":123,"max_file_bytes":123,"viewer":true,"guest_links":false,"guest_max_days":0}`.
`free_bytes` is My files' free quota (0 while the switch is off); `max_file_bytes` is
`KYDRIVE_MAX_FILE_SIZE`; `viewer` is whether Euro-Office is configured (viewing copies, below); `guest_links` is whether a guest link would be made now (policy,
both switches) and `guest_max_days` the policy's `max_days` then, else 0.

### Fetch

```json
{"sub":"…","issuer":"…","file_id":"…","max_bytes":26214400}
```

200 with the file's bytes, `Content-Type: application/octet-stream`, `Content-Length` and
`Content-Disposition: attachment; filename=…`. Only a file the person chose in the picker's
copy mode ([SUITE.md](SUITE.md)) within the last 24 hours, still live and still readable by
them; anything else 404 `not_found`. `max_bytes` (KyPost's attachment limit) is required, a
whole number of at least 0, else 400 `bad_body`; a larger file 413 `too_large`. Each answer is
also audited `kypost.file_fetched` (`file:<id>`). Picks expire with the hourly ticket sweep.

## Viewing copies

Open an attachment read-only in Euro-Office (design:
`docs/superpowers/specs/2026-11-15-office-attachments-design.md`). KyPost offers **Open** only
while status says `"viewer":true` (Euro-Office configured), never for encrypted mail.

| Event type | Route |
|---|---|
| `kypost.drive.view` | `POST /api/suite/kypost/view-tickets` |

Checked like the routes above (same refusals, per user 60 a minute, the switch).

```json
{"sub":"…","issuer":"…","name":"report.docx","size":7340032}
```

201 `{"upload_url":"https://drive.example/api/suite/view/<ticket>","expires":"<RFC 3339>"}`.
The name's extension must be docx, xlsx, pptx, odt, ods, odp, csv or pdf (any case) and the
size 1 to the smaller of `KYDRIVE_MAX_FILE_SIZE` and 25 MiB. Refusals: 400 `bad_body`,
413 `too_large`, 507 `quota` (the size counts against My files from now until the copy is
gone), 429 `too_many_tickets` (4 live copies or unused tickets per person), 409 `editor_off`.
The ticket is 256 random bits stored as SHA-256, valid 15 minutes and spent by its upload.

`PUT <upload_url>` from KyPost's server: exactly `size` bytes in one request, no cookie, no
CSRF, no signature, and no `Origin` (403 `bad_origin`; the route answers no CORS). The first
512 bytes must sniff as the extension's kind (`http.DetectContentType`: zip for the Office and
OpenDocument types, pdf, text for csv), else 415 `bad_type`; the ticket stays usable. Wrong
length 400 `size_mismatch`; unknown, spent or expired 404 `not_found`; switch off 409
`sharing_off`; another pairing 404 `kypost_off`; 60 a minute per person, 240 per address.
201 `{"view_url":"https://drive.example/view.html?copy=<id>","expires":"<RFC 3339>"}`.

The copy (`drive_view_copies`, schema 24; sealed bytes in `<data>/view/`) is not a file: no
folder, share, link, guest link, search, change feed, WebDAV, MCP or sync sees it, and bulk
backups skip it. Only its owner's browser sign-in opens it (`/view.html`, then
`POST /api/drive/view-copies/{id}/editor`; service, device and peer contexts refused; another
person 404). Euro-Office gets view mode with edit, download, print, comment, review and form
filling off, key `view-<id>`, and fetches `GET /api/editor/view/{id}` with its own signature.
A callback for that key with status 2 or 6 is refused (403 `{"error":1}`); status 4 deletes it.
The first open binds the copy to that browser sign-in, as an editor session is bound, and no other sign-in can open it: Euro-Office
fetches only an opened copy, and a logout, SSO back-channel logout, password reset or
deactivation that revokes the sign-in's editor sessions revokes the copy at once and queues a
Euro-Office drop of its key (retried every minute until it succeeds).
The copy is deleted when the viewer page closes (`POST /api/drive/view-copies/{id}/close`; an opened copy's row stays, revoked and without bytes, until Euro-Office has dropped its key, in case another tab still shows it),
when Euro-Office closes the document, or at 24 hours (access ends then; the hourly sweep
removes rows and bytes, and anything in `<data>/view/` older than 25 hours). Audited with IDs
only: `kypost.view` (`event:<id>`), `kypost.view_stored`, `kypost.view_opened` (`view:<id>`);
drive events `kypost.view_ticket_issued`, `kypost.view_stored`, `view.closed`, `view.expired`.

## What KyPost can do with the pair secret

The routes act through a peer context (`internal/drive/access.go`). It holds no rank:
reading, listing, downloading, searching, moving, trashing, purging, sharing with people,
guest links (but through the guest switch, below), credentials, editor sessions, settings and every shared workspace are refused
(but for a picked file, below), and `peer_access_test.go` proves each refusal. It may only add new files directly in the
person's Mail attachments folder, put a reader organisation link (or, with the guest switch,
a guest link) on a file there, read a
file the person picked for it in the last 24 hours (`peerRead`), and reserve and store a
viewing copy (`IssueViewTicket`, `StoreViewCopy`), which it can never read back.

## Deviations from the contract text

- Fetch reads only files the person picked in copy mode within 24 hours, not any file they
  may read: the pair secret alone never reads a person's drive. The wire is unchanged.

- No `KYDRIVE_KYPOST_ALLOW_PRIVATE`: KyDrive makes no request to KyPost, so there is nothing
  for it to allow. KyIdentity's KyPost URL must still be `https://host`.
- Links are refused (404 `not_found`) for files outside the Mail attachments folder, which is
  narrower than "a file the user owns in their personal workspace".
- The upload route's answers other than the final 201 are KyDrive's (table above):
  intermediate chunks answer 204 with `Upload-Offset`, exposed to the browser through CORS.
