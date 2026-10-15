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
- ETag is the quoted revision; `Content-Type` comes from the extension so x/net never reads a blob to sniff it. `PROPPATCH` answers 200 and stores nothing (Windows sets `Win32*` times on every copy).
- `Serve` replaces x/net's catch-all failure statuses with the drive error's: 507 quota, 413 body limit, 409 conflict (405 for `MKCOL`, 412 under `If-Match`), 403 denied, 400 invalid.
- Locks come from the caller's single in-memory `LockSystem`: one instance, lost on restart. Lock paths are per-user names, so two users whose workspaces share a display name can collide on a lock path; locks only block writes, never grant access.

## Work Guidance
None yet.

## Verification
- `go test -race ./internal/dav/` (real store, sealed blobs and `webdav.Handler`)

## Child DOX Index
None.
