# M2 Large Uploads, Drag-and-Drop, Search and Folder Download Implementation Plan

> **For agentic workers:** execute task by task; steps use checkbox (`- [ ]`) syntax. Write each failing test first, watch it fail, then implement.

**Goal:** a 5 GiB upload survives a dropped connection and resumes; a dragged folder tree arrives intact; search finds a file by partial name in any workspace the user can read and never in one they cannot; a folder downloads as a zip.

**Architecture:** domain logic in `internal/drive` (HTTP-free, authorization rechecked inside each transaction). Upload sessions are rows in a new drive table; their received bytes are **sealed segments** under `<data>/staging/<session>/`, re-sealed into one blob by `WriteBlob` at completion. Search and zip decrypt names in Go. The web drive page gains a drop zone, multi-file upload with per-file progress and resume, a search box, and a folder "Download zip" action.

**Spec:** `docs/superpowers/plans/2026-10-07-onedrive-parity-roadmap.md` milestone M2. Contracts: `AGENTS.md`, `internal/drive/AGENTS.md`, `internal/api/AGENTS.md`, `internal/config/AGENTS.md`, `web/AGENTS.md`, `web/browser/AGENTS.md`.

## Deviations from the roadmap (encryption at rest landed after it was written)

1. **No plaintext staging.** The roadmap appends raw bytes to `<data>/staging/<sid>`. Blobs (`KYB1`, per-blob salt, counter nonces, final-chunk flag) cannot be appended to, and plaintext on disk would break the at-rest promise. Instead each `PUT` chunk is sealed on arrival as its own segment; `complete` streams the decrypted segments through `WriteBlob`, which hashes, seals, fsyncs and renames as today. Cost: completion rewrites the file once (about a minute for 10 GiB on a 200 MB/s disk), well inside the 15 m write-timeout ceiling.
2. **Search decrypts in Go, not SQL `LIKE`.** Names are AES-GCM with random nonces plus an equality-only `name_hmac`; the contract says never filter or order by `name` in SQL. A substring index (trigram HMACs) would leak name structure, so search loads the live names of each readable workspace, decrypts, and matches in Go. A benchmark pins the cost; revisit only if it is measured too slow on the pilot corpus.

## Global Constraints

- Read the DOX chain first: `AGENTS.md` → `internal/drive/AGENTS.md`, `internal/api/AGENTS.md`, `internal/config/AGENTS.md`, `web/AGENTS.md`, `web/browser/AGENTS.md`.
- SQLite only; schema changes are appended to `internal/drive/schema.go` (never edit a shipped entry).
- Every state change writes one `drive_events` row in its transaction. Reads (search, zip) write none, matching downloads today.
- Nothing plaintext touches disk: staging segments use the blob seal format under the deployment key.
- Staging is not user data: it is outside backups (backup is an allowlist, verified) and may be deleted when its session is gone.
- Request bodies stay capped at 1 MiB except the existing single-request upload (128 MiB) and the new chunk `PUT` (64 MiB).
- New config: `KYDRIVE_MAX_FILE_SIZE` (default 10 GiB, 1 MiB–1 TiB) and `KYDRIVE_MAX_ZIP_SIZE` (default 10 GiB, same bounds), parsed and validated in `internal/config`; invalid values stop start-up.
- No new dependencies (Go or npm). UI copy short and plain, existing theme classes.
- Run Go tests in a capped scope: `systemd-run --user --scope -q -p MemoryMax=6G -p MemorySwapMax=0 -- go test ./...`.

## Review Focus

1. **A completed upload whose user lost access mid-session.** Expected: `complete` fails `ErrDenied`, no blob is published, staging is removed. `publishTX` re-authorizes; pinned by `TestUploadCompleteRechecksAccess`.
2. **Two concurrent `PUT`s for the same offset.** Expected: exactly one segment is recorded; the loser gets 409 and its file is removed. Pinned by `TestUploadSegmentRace`.
3. **Staging filling the disk.** Expected: a user holds at most 8 open sessions, each bounded by its declared size, which is bounded by `KYDRIVE_MAX_FILE_SIZE` and the workspace's free quota at creation; sessions expire after 24 h and the hourly worker removes them. Pinned by `TestUploadSessionLimits`, `TestExpiredUploadSessionsRemoved`.
4. **Search leaking across workspaces.** Expected: results only from workspaces where `authorize(…,1)` holds; service tokens see only their workspace; trashed items never appear. Pinned by `TestSearchRespectsAccess`.
5. **Zip path traversal or size blow-up.** Expected: entry paths are built only from `ValidName` names joined with `/`; the total plaintext size is checked against `KYDRIVE_MAX_ZIP_SIZE` before the first byte is written (413). Pinned by `TestFolderZip`.

Known limitations (document, do not fix in M2): a zip larger than the write timeout allows (≈15 m of transfer) is cut off; completion of a very large upload holds the request open while it re-seals.

---

### Task 1: Config for upload and zip caps

**Files:** modify `internal/config/config.go`, `internal/config/config_test.go`, `internal/config/AGENTS.md`.

**Produces:** `Config.Drive.MaxFileSize int64`, `Config.Drive.MaxZipSize int64`.

- [ ] Test: defaults are 10 GiB; out-of-range or non-numeric values return a load error naming the variable.
- [ ] Implement with the existing `getEnv*`/`invalid` pattern.
- [ ] Document both variables in `internal/config/AGENTS.md` and `README.md`.

### Task 2: Upload sessions in the store

**Files:** create `internal/drive/uploads.go`, `internal/drive/uploads_test.go`; modify `internal/drive/schema.go` (append one entry), `internal/drive/AGENTS.md`.

**Schema (one appended entry):**
```sql
CREATE TABLE drive_upload_sessions(id TEXT PRIMARY KEY, owner TEXT NOT NULL, workspace TEXT NOT NULL REFERENCES drive_workspaces(id),
  parent TEXT NOT NULL, name TEXT NOT NULL, name_hmac TEXT NOT NULL, file TEXT NOT NULL, expected INTEGER NOT NULL,
  size INTEGER NOT NULL CHECK(size>=0), received INTEGER NOT NULL DEFAULT 0, created TEXT NOT NULL, expires TEXT NOT NULL);
CREATE TABLE drive_upload_segments(session TEXT NOT NULL REFERENCES drive_upload_sessions(id) ON DELETE CASCADE,
  start INTEGER NOT NULL, path TEXT NOT NULL, size INTEGER NOT NULL, PRIMARY KEY(session, start));
```
The name is sealed like every other name (`names.seal("drive_upload_sessions", id, name)`).

**Interfaces:**
```go
type UploadSession struct{ ID, Workspace, Parent, Name, File string; Expected, Size, Received int64; Expires time.Time }
func (s *Store) StartUpload(ctx context.Context, user, workspace, parent, name, file string, expected, size, max int64) (UploadSession, error)
func (s *Store) Upload(ctx context.Context, user, id string) (UploadSession, error)
func (s *Store) AppendUpload(ctx context.Context, staging, user, id string, offset int64, r io.Reader, limit int64) (UploadSession, error)
func (s *Store) CompleteUpload(ctx context.Context, staging, blobs, user, id string) (File, error)
func (s *Store) CancelUpload(ctx context.Context, staging, user, id string) error
func (s *Store) ExpireUploads(ctx context.Context, staging string, now time.Time) (int, error)
```

Rules:
- `StartUpload`: `ValidName`; rank 2; `parentOK`; if `file` is set it must be a live file in that workspace and `expected` its revision; `size ≤ max` else `ErrQuota`; `fits(size)`; at most 8 unexpired sessions per user (`ErrQuota`). Expires 24 h after creation. Event `upload.started`.
- Sessions belong to their creator: every other call loads by `(id, owner)` and returns `ErrNotFound` otherwise.
- `AppendUpload`: `offset` must equal `received` (else `ErrConflict`, carrying nothing). Seals `r` (capped at `limit` and at `size-received`) to `staging/<id>/.part-<uuid>` with `seal`, fsyncs, renames to `staging/<id>/<uuid>`, then in one transaction inserts the segment and sets `received=received+n WHERE received=offset`. If that update hits zero rows, remove the file and return `ErrConflict`. A dropped connection leaves only a `.part-` file, removed on the error path.
- `CompleteUpload`: requires `received == size`; streams `io.MultiReader` of `OpenBlob(segment)` in offset order into `WriteBlob(blobs, key, r, size)`; checks the digest-of-plaintext size equals `size`; `Publish` with `expected`; on publish error discards the new blob; on success deletes the session (cascade) and `staging/<id>`. Event comes from `Publish` (`file.version_created`).
- `CancelUpload` and `ExpireUploads` delete rows then directories; `ExpireUploads` also removes `staging/*` directories with no session row (startup residue). Event `upload.cancelled` for cancel only.

- [ ] Tests (each failing first): `TestUploadSessionRoundTrip` (3 chunks → file with matching SHA-256, revision 1, staging dir gone), `TestUploadResumeAfterDroppedChunk` (reader errors midway → `received` unchanged, no `.part-` left, retry succeeds), `TestUploadSegmentRace`, `TestUploadSessionLimits` (size > max, size > free quota, 9th session), `TestUploadCompleteRechecksAccess` (grant removed before complete → `ErrDenied`, no version row, no new blob), `TestUploadNewVersion` (`file`+`expected`; stale `expected` → `ErrConflict`), `TestUploadOwnedByCreator`, `TestExpiredUploadSessionsRemoved`, `TestStagingIsSealed` (segment files start with `KYB1` and contain no plaintext marker).
- [ ] Contract: `internal/drive/AGENTS.md` gets the staging rules above (replacing the "separate upload staging paths" note).

### Task 3: Upload session HTTP routes and the worker

**Files:** modify `internal/api/drive_handlers.go`, `internal/api/server.go` (body limit), `internal/api/drive_retention.go` (call `ExpireUploads` in the hourly pass), create `internal/api/drive_uploads_test.go`; modify `internal/api/AGENTS.md`.

Routes (all `requireAuthenticated`, so service tokens work within their scope):
- `POST /api/drive/workspaces/{id}/upload-sessions` body `{parent,name,file,revision,size}` → 201 `UploadSession`.
- `GET /api/drive/upload-sessions/{sid}` → session (clients resume from `received`).
- `PUT /api/drive/upload-sessions/{sid}` with `Content-Range: bytes start-end/total`; `total` must equal the session size and `end-start+1` the body length; 204 on success with `Upload-Offset: <received>`; 409 with the current offset on a mismatch.
- `POST /api/drive/upload-sessions/{sid}/complete` → 201 `File`.
- `DELETE /api/drive/upload-sessions/{sid}` → 204.
- `server.go`: chunk `PUT` gets a 64 MiB + 1 body limit by exact route match, not prefix guessing.
- Staging root: `filepath.Join(DataDir, "staging")`, created `0700` at start-up.

- [ ] Tests: full round trip through HTTP with three ranges; malformed or out-of-order `Content-Range` → 400/409; a 65 MiB chunk → 413; another user's session → 404; a service token scoped to another workspace → 403 at creation; `TestSealedUploadDownloadRanges`-style download of the result matches.
- [ ] Contract: `internal/api/AGENTS.md` body-cap line lists the three caps; root `AGENTS.md` upload line updated.

### Task 4: Filename search

**Files:** create `internal/drive/search.go`, `internal/drive/search_test.go`; modify `internal/api/drive_handlers.go`, `internal/drive/AGENTS.md`, root `AGENTS.md:107` (filename search no longer deferred; full-text still is).

**Interface:**
```go
type Hit struct{ Kind, ID, Workspace, Parent, Name, Path string } // Path: decrypted ancestor names joined with "/"
func (s *Store) Search(ctx context.Context, user, q string, limit int) ([]Hit, error)
```
- `q` trimmed, 2–100 runes, else `ErrInvalid`. Match is case-insensitive substring on `strings.ToLower`.
- Workspaces from `Workspaces(ctx, user, false)` (already applies service scope), then per workspace `authorize(…,1)` inside a read transaction that loads live folders and live files. Trashed items and items under trashed folders are excluded.
- Results sorted by name then path in Go, capped at `limit` (200 from HTTP).
- HTTP: `GET /api/drive/search?q=` → `{"results": [...], "truncated": bool}`.

- [ ] Tests: `TestSearchRespectsAccess` (hit in readable workspace, none in an unreadable one, none for a revoked group member, service token sees only its workspace), `TestSearchExcludesTrash`, `TestSearchCapsResults`, `TestSearchRejectsShortQuery`, `BenchmarkSearch` over 50 000 names (record the number in the commit message; target under 250 ms).

### Task 5: Folder download as zip

**Files:** create `internal/drive/zip.go`, `internal/drive/zip_test.go`; modify `internal/api/drive_handlers.go`, `internal/api/AGENTS.md`.

**Interface:**
```go
type ZipEntry struct{ Path string; Blob string; Sealed bool; Size int64; Modified time.Time }
func (s *Store) FolderTree(ctx context.Context, user, folder string) (name string, entries []ZipEntry, err error)
```
- One read transaction: `authorize(…,1)` on the folder's workspace, walk live descendants (reuse `subtree`), current version of each live file, empty folders as `dir/` entries.
- HTTP `GET /api/drive/folders/{id}/zip`: sum sizes; over `MaxZipSize` → 413 before any write. Then `Content-Type: application/zip`, `Content-Disposition: attachment` with the folder name, `Cache-Control: no-store`, and stream with `archive/zip` using `zip.Store` (contents are mostly already compressed; no CPU spike), each member read through `OpenBlob`. A read error mid-stream aborts the response (the client sees a truncated download, never a silently short file inside a valid archive).
- When M3 adds folder and file grants, `FolderTree` must re-authorize per member; note this in `internal/drive/AGENTS.md`.

- [ ] Tests: `TestFolderZip` (nested tree incl. empty folder and a non-ASCII name round-trips through `archive/zip` reader with matching bytes; trashed child absent; reader without access → `ErrDenied`; cap exceeded → 413 with empty body).

### Task 6: Web — multi-file and folder upload with progress and resume

**Files:** create `web/src/pages/drive/upload.ts`, `web/src/pages/drive/upload.test.ts`, `web/src/pages/drive/DropZone.tsx`, `web/src/pages/drive/DropZone.test.tsx`; modify `web/src/pages/Drive.tsx`, `web/src/pages/drive/client.ts`, `web/AGENTS.md`.

- `upload.ts`: `uploadFile(workspace, parent, file, onProgress, signal)` — start session, send 32 MiB slices with `Content-Range` via `secureFetch`, on a network error or 409 `GET` the session and continue from `received` (3 retries with backoff, then surface the error and keep the session so "Retry" resumes), then `complete`. Progress from bytes acknowledged, not bytes sent.
- Folder drops: `webkitGetAsEntry` walk (read entries in batches until empty), create folders parent-first with `AddFolder`; if the name already exists in the parent, reuse that folder (look it up from the folder list); then upload files. Hidden OS files are uploaded as-is (no silent filtering).
- The "Upload file" button becomes multi-select and uses the same queue. A small per-file list shows name, percent, and error with Retry.
- Existing file with the same name: the file fails with "A file with this name exists" (no silent overwrite).

- [ ] vitest: chunking and `Content-Range` headers, resume from server offset after a failed chunk, folder walk creating folders before files, existing-folder reuse, existing-file error.

### Task 7: Web — search and zip

**Files:** create `web/src/pages/drive/Search.tsx`, `web/src/pages/drive/Search.test.tsx`; modify `web/src/pages/Drive.tsx`, `web/src/pages/drive/client.ts`.

- Search box above the file list; debounced 300 ms, minimum 2 characters; results show name, workspace and path; selecting one opens its folder with the file highlighted. "Showing first 200" when truncated.
- Folder rows get "Download zip" (plain link to the route; the browser handles the download).

- [ ] vitest: debounce and min length, result rendering, truncated notice.

### Task 8: Browser test, docs and acceptance

**Files:** create `web/browser/uploads.spec.mjs`; modify `README.md`, `docs/ACCEPTANCE.md`, `docs/superpowers/plans/2026-10-07-onedrive-parity-roadmap.md` (mark M2 plan written / shipped).

- [ ] Playwright: drop a nested folder (via `DataTransfer` in the page), see it arrive intact; abort a chunk with `page.route` then see the upload resume and complete; search finds the file by partial name; zip of the folder downloads with the expected entries.
- [ ] Manual acceptance (record in `docs/ACCEPTANCE.md`): a 5 GiB upload with the network dropped mid-way resumes and the downloaded file's SHA-256 matches.
- [ ] README limitations line: remove "no search", add zip and timeout limits.
- [ ] Full check: `go vet ./...`, capped `go test ./...`, `npm --prefix web test`, `npm --prefix web run build`, `npm --prefix web run test:browser`.
