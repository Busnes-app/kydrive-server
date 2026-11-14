# KyPost: saving and sharing files from mail

[Permissions](PERMISSIONS.md) · [Security](../SECURITY.md)

KyPost saves mail attachments into a person's My files and shares them by organisation link,
so a large attachment travels as a link. KyDrive is the resource owner: it decides whether a
person allows it and what KyPost may do. This is section C of the suite integration contract
(wave 1, 2026-11-14); this file is KyDrive's durable copy. Both sides change together.

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
`"enabled":false`), including uploads with tickets issued earlier.

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

Every request is checked in this order; refusals are `{"error","code"}`:

| Status | Code | When |
|---|---|---|
| 429 | `rate_limited` | Over 600 signed requests a minute from one address (before any check), or the per-user limit below. `Retry-After` set. |
| 404 | `kypost_off` | Not paired. |
| 401 | `bad_signature` | Signature, timestamp (±5 minutes) or event type wrong, or a body over 64 KiB. |
| 200 | (`{"outcome":"duplicate"}`) | A verified replay of an event ID already accepted: not acted on again. |
| 400 | `bad_body` | Not JSON, or no `sub`/`issuer`. |
| 404 | `no_user` | `issuer` is not KyDrive's KyIdentity, or no active account is linked to `sub` (sign-in or SCIM), or it is a dedicated installer administrator. |
| 429 | `rate_limited` | Per user: tickets and links 60 a minute, status 120. |
| 409 | `sharing_off` | The person's switch is off (not for status). |

An event refused with 429 or a 5xx is forgotten by the replay guard, so KyPost's retry under
the same event ID is processed. Each verified request is audited with IDs only
(`kypost.ticket`, `kypost.link`, `kypost.status`; resource `event:<id>`, details `outcome=ok`
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
the person's own Mail attachments folder. No guest link is ever made here.

### Status

`{"sub":"…","issuer":"…"}` → 200 `{"enabled":true,"free_bytes":123,"max_file_bytes":123}`.
`free_bytes` is My files' free quota (0 while the switch is off); `max_file_bytes` is
`KYDRIVE_MAX_FILE_SIZE`.

## What KyPost can do with the pair secret

The routes act through a peer context (`internal/drive/access.go`). It holds no rank:
reading, listing, downloading, searching, moving, trashing, purging, sharing with people,
guest links, credentials, editor sessions, settings and every shared workspace are refused,
and `peer_access_test.go` proves each refusal. It may only add new files directly in the
person's Mail attachments folder and put a reader organisation link on a file there.

## Deviations from the contract text

- No `KYDRIVE_KYPOST_ALLOW_PRIVATE`: KyDrive makes no request to KyPost, so there is nothing
  for it to allow. KyIdentity's KyPost URL must still be `https://host`.
- Links are refused (404 `not_found`) for files outside the Mail attachments folder, which is
  narrower than "a file the user owns in their personal workspace".
- The upload route's answers other than the final 201 are KyDrive's (table above):
  intermediate chunks answer 204 with `Upload-Offset`, exposed to the browser through CORS.
