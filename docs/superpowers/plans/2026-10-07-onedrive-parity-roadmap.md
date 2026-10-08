# KyDrive OneDrive/SharePoint parity roadmap

> **For agentic workers:** this is the milestone map. Each milestone gets its own task-level plan in this directory before work starts; only M1 has one so far (`2026-10-07-m1-file-management.md`). Do not start a milestone from this file alone.

**Goal:** close the gaps that stop KyDrive replacing OneDrive/SharePoint for a small organisation, in the order a user hits them.

**Decisions (user, 2026-10-07):**
- Space reclamation: **opt-in retention policy** per workspace (empty trash after N days, keep N versions), off by default, admin-set, plus manual permanent delete by a workspace manager. This replaces the "no automatic destructive garbage collection" contract; Restic snapshots are still never pruned automatically.
- Leavers: **admin hands over** a deactivated account's My files to a chosen shared workspace or a named colleague. Audited; no automatic access, no admin content browsing.
- Sharing scope: **share with a person, per-folder permissions, organisation links, external guest links.** Guest links un-defer anonymous sharing.
- Bulk backup: **offsite or cloud** Restic repository (S3-compatible or rest-server over HTTPS).
- Out of scope for KyDrive integrations: Joplin (licence-incompatible; KyNotes is built in-house) and LimeSurvey (dropped). Third-party integrations only need to speak OIDC.

**Out of scope throughout:** SharePoint metadata columns/content types, lists, intranet pages, workflow engines, eDiscovery/DLP/sensitivity labels, HA/multi-writer, desktop sync client (WebDAV is the stepping stone).

## Global constraints

- One SQLite writer, immutable filesystem blobs, `linux/amd64`. No PostgreSQL drive deployment.
- Administration never bypasses content grants. Retention settings and handover are configuration actions; they must not give the admin a way to read content.
- Every state change writes a `drive_events` row. Every privileged route is wrapped in `requireAdmin` in `routes()`.
- Domain code (`internal/drive`) stays HTTP-free; authorization is rechecked inside each mutation's transaction.
- Retain Restic snapshots; no automatic pruning of the bulk repository.
- Follow the DOX chain: `KyDrive-server/AGENTS.md` → `internal/drive`, `internal/api`, `web`, `internal/backup`, `docs` children. Each milestone updates the docs it changes.

## Milestones

### M1 — File management and reclaiming space (plan written)

Rename, move (including My files → shared workspace), copy, folder trash/restore, manual permanent delete of files/folders/versions, opt-in retention with an hourly worker, and the UI for all of it. Drive-owned schema migrations land here and are reused by every later milestone.

**Exit:** a manager can tidy a workspace, move a draft from My files into a team workspace while a colleague has it open (their editor is revoked if they lose access), empty trash, and set "empty trash after 30 days / keep 20 versions" and watch usage fall. Purging never leaves a version pointing at a deleted blob.

### M2 — Large uploads, drag-and-drop, search, folder download

- **Resumable uploads.** Upload sessions: `POST /api/drive/workspaces/{id}/upload-sessions` returns an ID; `PUT .../upload-sessions/{sid}` with `Content-Range` appends to a per-session staging file under `<data>/staging/<sid>`; `POST .../complete` hashes, fsyncs, renames into `blobs/` and calls `Publish` with the expected revision. Per-file cap becomes `KYDRIVE_MAX_FILE_SIZE` (default 10 GiB) and is checked against quota at session creation and at completion. Abandoned sessions expire after 24h; staging files are not user data and may be deleted. Keep the single-request upload route for service tokens and small files.
- **Drag-and-drop** of files and whole folders (`DataTransferItem.webkitGetAsEntry`) with per-file progress and resumable retry. Folder structure is created with `AddFolder` before files are uploaded.
- **Filename search** across every workspace the user can read: `GET /api/drive/search?q=`, `LIKE` with escaped wildcards, filtered by the same authorization as listing, capped at 200 results. Move to FTS5 trigram only if `LIKE` is measured too slow on the pilot corpus.
- **Folder download as zip**, streamed with `archive/zip`, every member re-authorized, total size capped by config.

**Exit:** a 5 GiB upload survives a dropped connection and resumes; a dragged folder tree arrives intact; search finds a file by partial name in a shared workspace and never shows one from a workspace the user cannot read.

### M3 — Sharing

Order matters; each step is its own task group.

1. **Single authorization definition.** Today the "who can access what" rule exists three times: `authorize`, the `Workspaces` query and the `PendingRevocations` UPDATE (`internal/drive/store.go:125`, `:173`, `sessions.go` `PendingRevocations`). Replace them with one SQL definition (a view or one Go-built CTE) and pin current behaviour with tests before adding anything. Without this, every new grant type would have to be added in three places and revocation would drift.
2. **Grants on folders and files, to groups or people.** Generalise `drive_grants` into `drive_shares(resource_kind workspace|folder|file, resource_id, principal_kind group|user|link, principal_id, role, expires, created_by)`. Effective role = max of the workspace grant, every ancestor folder grant and the file grant. Grants only add access; "break inheritance" (narrowing) is not offered. Managers of the resource can share; editors can share at most their own role if the workspace allows it (workspace setting, off by default).
3. **Share from My files to a person.** Changes the personal-workspace contract ("allow only the active owner") to "owner plus explicit item shares". Shares from a deactivated owner's My files stop working until handover (M6) moves the items.
4. **"Shared with me"** sidebar entry listing items shared directly to the user or their groups outside workspaces they already belong to.
5. **Organisation links.** Random 256-bit token, stored hashed, resolving to resource + role for any active signed-in user; optional expiry; revocable; every use audited. Link holders open documents in Euro-Office through the normal editor-session path.
6. **External guest links.** Disabled suite-wide by default; an admin turns them on. Read/download only (no guest editing in M3); mandatory expiry (default 7 days, maximum configurable); optional password (hashed with the existing password hasher, rate-limited per link); optional upload-only "request files" mode into one folder. Served from routes that send `Referrer-Policy: no-referrer`, `X-Robots-Tag: noindex`, never log the token, and never set a session cookie.

Contract changes to record: KyDrive `AGENTS.md` (sharing model, personal workspaces), `internal/drive/AGENTS.md`, and the suite root `AGENTS.md`/`DRIVE_IMPLEMENTATION_PLAN.md` line deferring anonymous public sharing.

**Exit:** a folder-level grant gives exactly that subtree; removing a group from KyIdentity revokes an open editor on a folder-shared file within one revocation tick; an expired or revoked link returns 404; a guest link never reveals sibling files.

### M4 — WebDAV

- `golang.org/x/net/webdav` with a custom `webdav.FileSystem` backed by the drive store (not the blob directory) and the in-memory `LockSystem` (single instance).
- **Authentication:** users sign in with OIDC and have no password, so add per-user **app passwords** (generated once, shown once, hashed, named, revocable, last-used shown) used as HTTP Basic credentials over HTTPS only. They carry the user's own access, are refused by admin routes, and are revoked by deactivation.
- Paths: `/dav/` lists workspaces by name (duplicates get a ` (2)` suffix, stable by ID order); `MOVE`/`COPY` map to M1's `MoveFile`/`MoveFolder`/`CopyFile`; `DELETE` moves to trash, never purges; `PUT` publishes a new version using `If-Match` (ETag = revision) when sent and last-writer-wins otherwise (history keeps the loser).
- Verify with the `litmus` suite, rclone, KDE/GNOME, macOS Finder and Windows Explorer; record Windows' 50 MB default limit in the README.

**Exit:** a workspace mounts in all three desktop OSes; save from LibreOffice over WebDAV creates a version; a deactivated user's app password stops working immediately.

### M5 — Previews and thumbnails

- In-browser preview for a fixed MIME allowlist only: raster images (not SVG), PDF, audio/video (with `http.ServeContent` range support), plain text. Served `Content-Disposition: inline` with `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`. Everything else downloads.
- Thumbnails: raster images decoded in-process with a dimension cap checked by `image.DecodeConfig` before decoding; Office/PDF first pages via Euro-Office's conversion service using the existing JWT client. Cache under `<data>/thumbs/<digest>-<size>.png`: derived data, outside backups, safe to delete.
- Quick-look modal in the file list; Excalidraw shows its type icon.

**Exit:** previews render for the allowlist; an HTML or SVG upload downloads instead of rendering; a decompression-bomb PNG is refused without a large allocation.

### M6 — Leaver handover and offsite bulk backup

**Handover.**
- Admin-only screen listing deactivated or deleted accounts that still own a personal workspace, with its size.
- `Handover(admin, fromUser, target Location)` moves the whole personal tree (live files, folders, versions; trash included) into a new folder `Handover from <name> <date>` in a shared workspace or a colleague's My files. Reuses M1's cross-workspace move. Checks quota once for the whole tree. Item shares from M3 are dropped and audited.
- The same operation as an audited CLI command, matching `set-directory-role`.
- The admin chooses the destination but never sees file names or contents before handover.

**Offsite bulk backup.**
- Accept `KYDRIVE_BULK_BACKUP_REPOSITORY` as a local path (today) **or** an `s3:https://…` / `rest:https://…` URL. Plain HTTP and other schemes are refused.
- Credentials from files (`KYDRIVE_BULK_BACKUP_CREDENTIALS_FILE`, an env-format file read at start-up and passed only to the `restic` child process), sealed into `config/integrations.json` so a restore can reach the repository.
- Recommend a repository the drive host cannot delete from: rest-server `--append-only` offsite, or S3 with Object Lock/versioning. Document both in the README and restore runbook.
- A weekly `restic check --read-data-subset=5%` with its result on the Recovery screen.
- Update `docs/PILOT.md` and the suite root contract: the pilot's "independent bulk storage deferred" no longer holds.

**Exit:** a fresh host restores from the offsite repository plus a custodian-opened capsule with every blob verified; a handed-over tree is browsable by the recipient and absent from the leaver's workspace.

## Dependencies

```
M1 ──► M2 (drag-drop folders reuse folder ops)
M1 ──► M4 (MOVE/COPY/DELETE)
M1 ──► M6 handover (cross-workspace move)
M3.1 ──► M3.2–6 (single authorization definition first)
M5, M6 offsite backup: independent; can run in parallel with M2–M4
```
