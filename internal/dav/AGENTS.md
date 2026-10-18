# WebDAV

## Purpose
Serve the drive over WebDAV (`golang.org/x/net/webdav`) for a user's own clients: file managers, LibreOffice, rclone.

## Ownership
Owns the per-request `webdav.FileSystem` (`fs.go`) and the request composition (`serve.go`). Authentication, the HTTPS rule and the `/dav/` mount belong to `internal/api`; authorization stays in `internal/drive`.

## Local Contracts
- `New` builds one `FS` per request for one user; nothing is cached across requests. Every operation calls the drive store, which authorizes it; this package never decides access.
- Paths are `/<workspace>/<folder>/.../<name>`. `/` lists readable workspaces by name; duplicates get ` (2)`, ` (3)` in workspace ID order, so names are per user. Segments resolve one at a time with `Store.Lookup` (`name_hmac` equality), listings with `Store.Children`: no request lists a whole workspace.
- Workspaces cannot be created, renamed or removed (403). `DELETE` trashes (`SetTrash`, `SetFolderTrash`), never purges. `MOVE` maps to `MoveFile` (cross-workspace only where the store allows it) and `MoveFolder` (same workspace only, else 403); with `Overwrite: T` the destination is trashed first. `COPY` is x/net's generic read-and-write copy, so a copy gets its own blob.
- `PUT` streams the body through a pipe into `WriteBlob` (capped at `maxFile`) and publishes on `Close` against the revision seen at open. Without `If-Match`, a conflict retries against the newest revision (last writer wins; the history keeps the loser). With `If-Match`, `Serve` compares it to the ETag first (412) and a conflict is final. A failed body read (`FS.Body`) makes `Close` fail, so a cut connection publishes nothing.
- Client mtime: `Mtime` parses `X-OC-Mtime` (integer Unix seconds, as ownCloud clients and rclone's `owncloud` vendor send it) and `New` takes the result; a published `PUT` stores it on the version and answers `X-OC-Mtime: accepted`, which rclone checks. `getlastmodified` reports `File.Mtime`. rclone 1.75.2 never sends PROPPATCH for a date-only change: KyDrive offers no `oc:checksums`, so it uploads again, which stores the new date as a new version.
- `GET`/`HEAD` always answer `application/octet-stream`, `Content-Disposition: attachment` and `Content-Security-Policy: sandbox; default-src 'none'`: stored files are user content on the app origin and must never render or run. PROPFIND still reports the extension's type (`ContentType`), so x/net never reads a blob to sniff it. ETag is the quoted file ID and revision (`"<id>-<rev>"`), so a recreated file never accepts its predecessor's ETag. `PROPPATCH` answers 200 and stores nothing (Windows sets `Win32*` times on every copy).
- `PROPFIND` needs `Depth: 0` or `1`; a missing or infinite `Depth` gets 403 with the RFC 4918 `propfind-finite-depth` precondition, so no request walks a whole workspace.
- `Serve` replaces x/net's catch-all failure statuses with the drive error's: 507 quota, 413 body limit, 409 conflict (405 for `MKCOL`, 412 under `If-Match`), 403 denied, 400 invalid.
- Locks (`locks.go`): one in-memory table per server (`NewLocks`), lost on restart, seen per request through a view that names locks `/<workspace ID>/<rest>` so users whose workspaces share a display name never share a lock. `LOCK` needs edit access to the workspace and is refused on `/` and on workspace roots (403); its timeout is capped at one hour (the `Timeout` header is rewritten before x/net parses it, so the response tells the truth). Tokens are `urn:kydrive:lock:<id>:<workspace ID>:<HMAC(server key, user, id, workspace)>`: another user cannot present, refresh or release them, and a refresh needs current edit access to that workspace, else the lock is released and the refresh gets 412. The same check runs when a token is presented on any request (`Confirm`): a demoted owner's token releases the lock and matches nothing, so the request gets 412 before its body is read. The lock x/net takes around every write needs the same edit access, so a reader's slow request cannot block editors (403 before the body is read).

## Work Guidance
None yet.

## Verification
- `go test -race ./internal/dav/` (real store, sealed blobs and `webdav.Handler`)

## Child DOX Index
None.
