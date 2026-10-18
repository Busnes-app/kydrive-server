# M7 Desktop Sync Implementation Plan

> **For agentic workers:** execute task by task; steps use checkbox (`- [ ]`) syntax. Write each failing test first, watch it fail, then implement. Server tasks land in this repo, one PR per task. Client tasks land in the new `kydrive-desktop` repo.

**Goal:** a linked Windows, macOS or Linux computer keeps chosen workspaces in a local folder that works offline. Edits sync both ways. A stale edit produces a conflict copy, never a silent overwrite. Losing access removes the synced copies. Revoking the device stops it at once and, by default, wipes it the next time it reaches the server.

**Architecture:**
- **Server.** Stores each version's client mtime. Each workspace gets an ordered change log (`drive_changes`) written in the same transaction as every content mutation. The log is read through a per-workspace cursor API, with a snapshot endpoint for full enumeration. KyDrive mints its own per-device bearer credentials after a browser approval behind the normal KyIdentity sign-in, using loopback redirect and PKCE.
- **Client.** One Go sync core per user (state DB, poller, transfer, conflict rules, keyring) talks to the server's REST API. A thin native shell per OS talks to the core over a local socket or named pipe: WinUI 3 on Windows, SwiftUI on macOS, Qt6/Kirigami on Linux.

**Spec:** roadmap M7 (`2026-10-07-onedrive-parity-roadmap.md`), design and evidence in `docs/desktop-sync-options.md` ("What a Ky client looks like"). Contracts: `AGENTS.md`, `internal/drive/AGENTS.md`, `internal/api/AGENTS.md`, `internal/dav/AGENTS.md`, `web/AGENTS.md`.

**Builds on:**
- M2 resumable upload sessions;
- M3 single access definition (`internal/drive/access.go`);
- M4 app passwords and `/dav/`;
- the WebDAV ETag fix (#40, ETag `"<file-id>-<revision>"`).

## Decisions

1. **Feed per workspace, not per user.** Workspace-level access changes come from SCIM (group membership, deactivation) in `internal/store` and never touch drive tables. A per-user feed would need hooks there and would still drift. Instead:
   - A client lists `GET /api/drive/workspaces` on every poll.
   - A workspace that disappears, or a cursor read that answers 403/404, means access was lost.
   - Shares only add access inside a workspace, so a readable workspace has no partly unreadable items.
2. **Shared with me stays out of v1** (Yoshi, 2026-10-17). Item-level shares outside the user's workspaces need a per-user slice and are planned with v2.
3. **Rows name items; the reader returns current state.** A change row is `(seq, workspace, kind, item)`, nothing more.
   - The read endpoint returns each changed item once, with its current state, or `removed`.
   - Writes stay one tiny INSERT, and replayed or duplicated rows are harmless, as in Graph `delta`.
4. **Trash counts as removal; restore as upsert.** Purges and retention only touch trashed items, so they need no row of their own.
   - A cross-workspace move writes a row in both workspaces.
   - Folder trash, restore, move and handover write one row for the subtree root. A client receiving an unknown or restored folder enumerates it with the snapshot endpoint.
5. **Cursor retention.** Rows older than 90 days are pruned hourly. A cursor older than the oldest kept row gets `410 Gone`, and the client re-enumerates.
6. **No propagated folder ETags and no new checksum property.** These are deliberate changes from the research note:
   - The feed answers "what changed below here", so propagated folder ETags would add a write per ancestor for nothing.
   - REST already returns each file's SHA-256 `digest`.
   - With stored mtimes, rclone detects same-size edits through modtime.
7. **Client mtime** is optional metadata on a version and never changes content.
   - It is set from the REST upload's `mtime` (RFC 3339) or the DAV `X-OC-Mtime` header (Unix seconds; rclone's `owncloud` vendor sends it).
   - When absent, it falls back to the version's creation time.
   - DAV answers `X-OC-Mtime: accepted` when stored, which rclone checks.
   - `getlastmodified` and the REST `mtime` field report it.
8. **Device credentials** are separate from app passwords and service tokens.
   - **Format and storage:** a 256-bit random token with prefix `kyd_`, stored as SHA-256 like service tokens.
   - **Scope:** the user's own access across all workspaces, on `/api/drive/*` (Bearer) and `/dav/` (Bearer).
   - **Admin and setup routes:** refused, like service tokens.
   - **Liveness:** every request requires an active user. A token unused for 90 days is dead.
   - **Visibility:** listed on the Account page beside app passwords and revocable there; at most 20 per user.
9. **Linking uses loopback plus PKCE, implemented by KyDrive, not KyIdentity.**
   - The client listens on `127.0.0.1:<any port>` and opens `/link?redirect=http://127.0.0.1:<port>/cb&challenge=<S256>&state=…&name=…&platform=…`.
   - The user signs in normally through KyIdentity, then sees the device name, platform, IP and time, and approves.
   - KyDrive redirects to the loopback address with a one-time code (60 s, single use). The client exchanges code plus verifier for the token.
   - Only `127.0.0.1` and `[::1]` redirects with an explicit port are accepted (RFC 8252 §7.3).
   - **Why this beats a device code:** a phishing link carrying an attacker's code cannot deliver the token anywhere but the victim's own machine. There is no polling secret to steal.
   - Headless machines keep using app passwords; there is no device-code flow (YAGNI).
10. **Revocation and wipe** (Yoshi, 2026-10-17):
    - **The dialog.** "Revoke" offers "Also remove KyDrive files from this computer". It is checked by default. Drive-wide settings `drive_device_wipe_default` (default `true`) and `drive_device_wipe_locked` (default `false`) are admin-set; when locked, the checkbox is fixed at the default.
    - **The revoked row.** It keeps its hash and wipe flag for 30 days, so the device can be told.
    - **What the device sees.** Any request with the token then gets `401 {"error":"device_revoked","wipe":true|false}`. After 30 days the row is deleted, and the token gets a plain 401 with no wipe.
    - **Limits shown in the UI:** a device offline for 30 days never wipes, and a wipe is not a guarantee.
    - **Deactivation** kills tokens without a wipe instruction.
11. **The client keeps the user's work.** Lost access or a wipe deletes synced copies, but files with edits that never uploaded move to `<sync root>/Unsynced/` first.
12. **Client repo:** one new repo, `kydrive-desktop` (MIT): `core/` (Go), `windows/`, `macos/`, `linux/`. It is one repo so the core and shells version together. KyPost uses per-OS repos, but KyPost has no shared core.

## Global Constraints

- Read the DOX chain first. SQLite only, with drive schema entries append-only (next is version 12, `internal/drive/schema.go`).
- `internal/drive` stays HTTP-free. Every change row is written inside the mutation's existing `transaction` (`store.go:177`), next to its `event(...)`.
- Every state change still writes a `drive_events` row (device linked, device revoked, wipe settings changed).
- New routes are registered in `routes()`; admin settings use `requireAdmin` and `server_settings` (pattern: `drive_trash_days`, `internal/api/drive_retention.go`).
- Server: no new Go dependencies. Client core: `modernc.org/sqlite` (CGO-free), `github.com/zalando/go-keyring`, `github.com/fsnotify/fsnotify`. Verify each licence when it is added.
- Run Go tests in a capped scope (`systemd-run --user --scope -p MemoryMax=6G -p MemorySwapMax=0 -- …`).

## Review Focus

1. **The feed missing a change.** Expected: every content mutator writes its row in the same transaction. Pinned by `TestEveryMutatorWritesAChange`, a table test over the mutator list in Task 2.
2. **The feed or snapshot leaking another workspace.** Expected: both authorize rank ≥ 1 on the workspace, per request. Pinned by `TestChangesAuthorize`.
3. **Device token acceptance.** Expected: only with an active user, unrevoked and unexpired; never on admin routes; the loopback-only redirect; a single-use PKCE code. Pinned by `TestDeviceLink`, `TestDeviceToken`.
4. **The client destroying user work.** Expected: no path deletes an unuploaded local edit. Pinned by client tests `TestLostAccessKeepsUnsynced`, `TestWipeKeepsUnsynced`, `TestConflictKeepsBoth`.

---

## Part A — Server (this repo)

### Task 1: Client mtime

**Files:** `internal/drive/schema.go`, `store.go`, `uploads.go`; `internal/api/drive_handlers.go`, `drive_uploads.go`, `dav.go`; `internal/dav/fs.go`, `serve.go`; AGENTS for drive, api, dav; `docs/DESKTOP-ACCESS.md`.

- Schema 12: `ALTER TABLE drive_versions ADD COLUMN mtime TEXT NOT NULL DEFAULT ''`; `ALTER TABLE drive_upload_sessions ADD COLUMN mtime TEXT NOT NULL DEFAULT ''`.
- `Version.Mtime`, written by `publishTX` (INSERT at `store.go:594`). `File.Mtime` (json `mtime`) is read through `fileColumns`/`scanFile` and falls back to `created`. Reject mtimes before 1970 or more than one day in the future (`ErrInvalid`).
- REST:
  - `mtime` on the single-request upload query (`drive_handlers.go:278`);
  - `mtime` in the upload-session start JSON, stored on the session and applied before `Publish` in `CompleteUpload` (`uploads.go:228`).
- DAV:
  - `dav.New` takes the parsed `X-OC-Mtime`; `publish` sets it on the version (`fs.go:331`);
  - the response carries `X-OC-Mtime: accepted`;
  - `getlastmodified` reports it.
- **Unproven, so test it:** whether rclone's `SetModTime` falls back cleanly when PROPPATCH stays discarded. Run `rclone sync` of an unchanged file with a new local mtime using `vendor=owncloud`. Add PROPPATCH `{DAV:}lastmodified` only if that fails, as a metadata update of the current version that still writes an event.
- [ ] Tests:
  - REST and DAV mtime round-trip;
  - absent mtime falls back to created;
  - invalid mtime gives 400;
  - an interrupted upload stores nothing.
- [ ] Re-run the rclone check against the built binary:
  - `vendor=owncloud` keeps mtimes;
  - `bisync --compare size,modtime` catches a same-size edit without `--download-hash`.
- [ ] Update `docs/DESKTOP-ACCESS.md`: switch to `vendor=owncloud`, and drop `--download-hash` from the bisync steps.

### Task 2: Change log

**Files:** `internal/drive/schema.go`, `store.go`, `move.go`, `trash.go`, `handover.go`, `guest.go`, `sessions.go`; new `internal/drive/changes.go`, `changes_test.go`; `internal/drive/AGENTS.md`.

- Schema 13: `CREATE TABLE drive_changes(seq INTEGER PRIMARY KEY AUTOINCREMENT, workspace TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('file','folder')), item TEXT NOT NULL, created INTEGER NOT NULL); CREATE INDEX drive_changes_ws ON drive_changes(workspace, seq);`
- `change(ctx, tx, workspace, kind, item)` in `changes.go`, called next to `event(...)` in:

  | Mutator | Rows |
  |---|---|
  | `publishTX` (covers `Publish`, `RestoreVersion`, `CopyFile`, `PublishEditor`, `GuestUpload`, `CompleteUpload`) | file |
  | `AddFolder` | folder |
  | `moveFile` | file; cross-workspace: source **and** destination |
  | `MoveFolder` | folder (root) |
  | `setTrash`, `SetFolderTrash` | file / folder root |
  | `handover` | folder root in the destination; one per top-level item in the source |

  Purges, retention, grants, shares, links and quota need no row (Decision 4 and Decision 1).
- `TestEveryMutatorWritesAChange`: a table of the mutators above, each asserting a new row with the right workspace and item. It also has a guard that fails when a new exported mutator in `internal/drive` is missing from the table. Use a list in the test reviewed against `go doc`; there is no reflection magic.
- `PruneChanges(ctx, before)` deletes old rows but always keeps the newest row per workspace. Without that, an idle workspace's cursor would expire just because nothing happened.
- [ ] Tests: the mutator table; a rolled-back mutation leaves no row; seq is strictly increasing across concurrent mutations (one writer).

### Task 3: Change and snapshot API

**Files:** new `internal/drive/changes.go` readers; `internal/api/drive_changes.go`, `drive_changes_test.go`; `internal/api/drive_retention.go`; `internal/api/AGENTS.md`.

```go
type Change struct{ Kind string; ID string; Removed bool; File *File; Folder *Folder }
func (s *Store) Changes(ctx context.Context, user, workspace string, after int64, limit int) (cs []Change, next int64, err error) // ErrGone below the oldest kept seq
func (s *Store) Snapshot(ctx context.Context, user, workspace string, afterID string, limit int) (folders []Folder, files []File, head int64, nextID string, err error)
```
- Both authorize rank ≥ 1 on the workspace in the same read transaction they query in.
- `Changes` dedupes by item and resolves current state:
  - **Removed:** the item is trashed, purged or now in another workspace.
  - **Otherwise:** the item's live metadata.
- `Snapshot` pages all live folders and files of a workspace by ID. Its `head` is read first, in the same transaction, so `Changes(head)` continues exactly where the snapshot stopped.
- Routes:
  - `GET /api/drive/workspaces/{id}/changes?after=&limit=` (≤ 1000) → `{changes, next, more}`;
  - `410` for `ErrGone`;
  - `GET /api/drive/workspaces/{id}/snapshot?after=&limit=`;
  - both `requireAuthenticated`.
  - `GET /api/drive/workspaces` gains `head` per workspace, so a client skips unchanged workspaces.
- `RunRetention` calls `PruneChanges(now-90d)` after `ExpireUploads`.
- [ ] Tests:
  - `TestChangesAuthorize`: an outsider and a former member get 403;
  - dedupe; a move out shows removed; restore shows live;
  - snapshot plus changes from head misses nothing under concurrent writes;
  - a pruned cursor gets 410; an idle workspace keeps a valid cursor.

### Task 4: Device credentials and linking

**Files:** `internal/drive/schema.go`; new `internal/drive/devices.go`, `devices_test.go`; `internal/api/devices.go`, `devices_test.go`, `server.go` (Bearer branch), `dav.go`; AGENTS for drive and api.

- Schema 14:
  - `drive_devices(id, user_id, name, platform, hash UNIQUE, created, last_used, revoked_at, wipe)`;
  - `drive_device_codes(code_hash PRIMARY KEY, user_id, name, platform, challenge, redirect, expires, used)`.
- Store:
  - `ApproveLink(user, name, platform, challenge, redirect) (code)` validates the loopback redirect and caps devices at 20.
  - `RedeemLink(code, verifier) (token)`: single use, 60 s, S256 check.
  - `DeviceUser(token) (user, error)`: active user, unrevoked, `last_used` within 90 days, `last_used` written at most every 5 minutes. A revoked row returns `ErrDeviceRevoked{Wipe}`.
  - `Devices`, `RevokeDevice(user, id, wipe)`, `PruneDevices` (revoked older than 30 days).
  - Events: `device.linked`, `device.revoked`.
- API:
  - SPA route `/link` (Task 5);
  - `POST /api/devices/approve` (session + CSRF) → `{redirect}` with code and state;
  - `POST /api/devices/token` (public, form `code`, `verifier`, rate-limited like login) → `{token, user, devices_url}`;
  - `GET /api/devices`, `DELETE /api/devices/{id}?wipe=` (session + CSRF). The server forces `wipe` to the default when locked.
  - `DELETE /api/devices/self` (Bearer `kyd_` only, CSRF-exempt, same-site checked) revokes the presenting device with `wipe=false`.
  - `GET`/`PUT /api/drive/device-policy` (`requireAuthenticated` / `requireAdmin`).
- Bearer:
  - `kyd_` tokens are accepted on `/api/drive/*` without `WithServiceAccess` narrowing, CSRF-exempt like service tokens, refused on `requireAdmin` and installer routes.
  - `/dav/` accepts `Authorization: Bearer kyd_…` beside Basic, with the same rate limits on failure.
  - `device_revoked` answers 401 JSON.
- [ ] Tests:
  - `TestDeviceLink`: a non-loopback, portless or `localhost` redirect is refused; a reused code is refused; a wrong verifier is refused; an expired code is refused.
  - `TestDeviceToken`:
    - a deactivated user gets 401 without wipe;
    - a revoked token gets 401 carrying the wipe flag;
    - a token unused for 90 days is dead;
    - admin routes give 403;
    - DAV Bearer works;
    - a locked policy overrides the request's `wipe`.

### Task 5: Web UI

**Files:** `web/src/pages/AppPasswords.tsx` (or a sibling `Devices.tsx`), new `web/src/pages/LinkDevice.tsx`, `Settings.tsx`; their tests; `web/AGENTS.md`.

- **`/link`:** after sign-in, shows "Link *name* (*platform*) to your KyDrive?" with the request IP and time and an **Approve** button. Nothing happens on page load, and an invalid request shows an error, never a redirect.
- **Account page:** a **Computers** list (name, platform, linked, last used) with Revoke. The dialog has the wipe checkbox (disabled when locked) and the offline caveat.
- **Settings:** a **Computers** section for the wipe default and lock.
- [ ] Tests: approve posts once; the locked checkbox can't change; revoke sends the wipe flag.

### Task 6: Server docs and release

- [ ] Update the README (WebDAV mtime, linked computers) and the contracts in `AGENTS.md:107` (desktop sync no longer deferred once the client ships) and the roadmap M7 status.
- [ ] Add the feed, snapshot and device API to `internal/api/AGENTS.md`.

**Part A exit:**
- A script drives the built server through link → token → snapshot → changes → upload with mtime → conflict → revoke with wipe, all over HTTP.
- `go test -race ./...` passes.

---

## Part B — Client (`kydrive-desktop` repo)

### Task 7: Repo and core skeleton

- [ ] Create the repo with MIT licence, `AGENTS.md`, CI (Go tests on Linux, Windows and macOS runners), and `core/` with `kydrive-sync` (daemon) and `kydrive-sync ctl` (debug CLI).
- [ ] Config and state: per-user config dir; a SQLite state DB with an `items` table (server id, workspace, kind, parent id, name, revision, digest, size, mtime, local path, local size, local mtime, local inode/file-id, state).
- [ ] IPC: JSON-RPC over a Unix socket (Linux/macOS) or named pipe (Windows), owner-only permissions. Methods: status, link, unlink, choose workspaces, pause, conflicts, activity.
- [ ] Keyring via go-keyring; never a token on disk. Windows uses `CRED_PERSIST_LOCAL_MACHINE`; macOS uses this-device-only Keychain items.

### Task 8: Linking

- [ ] Loopback listener on a random port, an S256 verifier, and opening the system browser to `/link`. Callback state check, token exchange, keyring store. A timeout after 10 minutes.
- [ ] Unlink: `DELETE /api/devices/self` with the device token (best effort; it never asks for a wipe), then delete the keyring item, and keep local files unless the user chooses removal.

### Task 9: Download path

- [ ] Choosing workspaces lists `GET /workspaces` and maps each chosen workspace to `<root>/<workspace name>`; duplicate names get ` (2)` as on DAV.
- [ ] Initial sync: snapshot pages into the state DB, folders created, files downloaded to a temp file in the same dir, SHA-256 verified against `digest`, then renamed into place with the server mtime.
- [ ] Poll every 45 s, and on wake and network change:
  1. list workspaces;
  2. read `changes` for each workspace whose `head` moved;
  3. apply upserts and moves by server ID;
  4. apply removals (the local copy is deleted only if it is unchanged since the last sync; otherwise it becomes a conflict, Task 11);
  5. on 410, re-snapshot.

### Task 10: Upload path

- [ ] Local detection uses fsnotify plus a full scan every 10 minutes and at start; a path counts as changed when its size, mtime or inode moved.
- [ ] Ignore rules: `~$*`, `.~lock.*`, `*.tmp`, `.DS_Store`, `Thumbs.db`, `desktop.ini`, and `Unsynced/`.
- [ ] Uploads: new and changed files use resumable upload sessions with `expected` = the last-seen revision and `mtime`. A file is uploaded only once it has been stable for 3 s.
- [ ] Folders: `AddFolder`.
- [ ] Moves and renames are detected by inode/file-ID and sent as move calls; when that's impossible, they become delete plus create.
- [ ] Local deletes go to the trash API.
- [ ] **Large-delete guard:** a pass that would remove more than 50% of a workspace (and more than 20 items) pauses and asks in the shell.

### Task 11: Conflicts, lost access, revoke

- [ ] **Conflict:** a refused upload (409) or a remote change to a locally edited file keeps both. The local file is renamed `<name> (conflict from <device> <YYYY-MM-DD>).<ext>`, uploaded as a new file, and listed in the shell.
- [ ] **Name hazards:**
  - Two server names differing only in case on Windows or macOS: sync the first; skip the other and list it.
  - Names invalid on Windows (`CON`, a trailing dot, `<>:"|?*`): skip and list.
- [ ] **Lost access** (a workspace leaves the list, or 403/404): stop syncing it, move locally edited files to `Unsynced/`, delete the rest, and notify.
- [ ] **Revoked:** on `device_revoked`, stop everything and delete the keyring item. If `wipe`, move unuploaded edits to `Unsynced/`, delete synced copies, and notify. Deactivation (plain 401) stops without deleting.
- [ ] Tests: `TestConflictKeepsBoth`, `TestLostAccessKeepsUnsynced`, `TestWipeKeepsUnsynced`, `TestLargeDeletePauses`, run against the real `kydrive-server` binary built from this repo.

### Task 12: Shells and packaging

- [ ] **Windows (`windows/`):**
  - WinUI 3 app with a tray icon, status, workspace picker, conflicts and activity; the core runs as a per-user background process started by the app.
  - **Unproven: WinUI 3 has no built-in tray API.** Pick and licence-check a tray approach first (e.g. `H.NotifyIcon`).
  - Signed MSIX with auto-update via App Installer.
- [ ] **macOS (`macos/`):**
  - SwiftUI `MenuBarExtra` app, with the core as a bundled launch agent.
  - Developer ID signing and notarization (paid Apple account), updates via Sparkle.
- [ ] **Linux (`linux/`):**
  - Qt6/Kirigami tray (StatusNotifierItem), with the core as a systemd user unit.
  - Flatpak with `--filesystem=~/KyDrive` and the Secret Service portal, plus .deb/.rpm and AUR.
- [ ] Each shell: link, choose workspaces, pause, open folder, open in browser, conflicts list, unlink.

**Part B exit (phase 1, the roadmap's M7 exit):**
- A linked device survives a dropped connection mid-upload.
- A stale edit yields a conflict copy, never a silent overwrite.
- Removing a grant removes synced copies but keeps unuploaded edits.
- A revoked device stops at once, and wipes when told to.
- All of this holds on Windows, macOS and Linux runners.

---

## Part C — Phase 2: files on demand (separate plan when Part B ships)

- **Windows:** a Cloud Files API sync root in the Windows shell (or a native helper). Placeholders are created from the snapshot, hydrated by ranged download (the server already decrypts only the 64 KiB chunks a range touches), with pin/unpin mapped to the core's offline set.
- **macOS:** an `NSFileProviderReplicatedExtension`. The cursor maps to the sync anchor, and 410 maps to `syncAnchorExpired`.
- **Linux:** stays the sync folder.
- **Push:** if 45 s polling feels slow, add long-poll on `changes`. Measure first.

## Order and size

| # | Task | Size | Depends on |
|---|---|---|---|
| 1 | Client mtime | S | — |
| 2 | Change log | M | — |
| 3 | Change and snapshot API | M | 2 |
| 4 | Device credentials and linking | M | — |
| 5 | Web UI | S–M | 4 |
| 6 | Server docs | S | 1–5 |
| 7–11 | Client core | L | 3, 4 |
| 12 | Shells and packaging | L (×3) | 7–11 |
| C | Files on demand | XL | 12 |

Tasks 1, 2 and 4 can run in parallel. Task 1 alone improves rclone users' experience and can ship in the first post-release update.
