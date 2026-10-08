# Drive domain

## Purpose

Own personal and organization workspaces, files, folders, immutable versions and group access grants.

## Ownership

Own drive schema and domain operations over the application's database and blob directory. Identity rows remain owned by the inherited identity stores.

## Local Contracts

- Shared documents belong to workspaces, independent of user deletion.
- Personal workspaces are unique per immutable account ID, default to 10GiB, and allow only the active owner. Group grants and shared-directory management are rejected for personal workspaces, including administrator grants. Retain ownership/data after offboarding; session revocation covers both personal and shared files.
- Read current user status and group memberships when authorizing each operation. Global admin can manage workspace configuration; shared content requires an explicit group grant and personal content requires the active owner.
- Publish immutable, generated blob IDs before transactionally committing version metadata. Check expected revisions and quotas under the transaction. Do not derive disk paths from user filenames.
- SQLite is the initial supported drive database. Keep domain inputs independent of HTTP.
- Drive schema changes are append-only entries in `schema.go`, applied once each and recorded in `drive_schema`. Name insert columns explicitly so added columns do not break inserts. Never edit a shipped entry.
- Only SQLite unique-constraint violations (`uniqueViolation`) and domain state checks map to `ErrConflict`; wrap it with a short reason (`fmt.Errorf("%w: name exists", ErrConflict)`), which the API returns to the client. Unknown ids map to `ErrDenied`/`ErrInvalid` only on `sql.ErrNoRows`; other database errors pass through.
- Trash is batched: trashing a folder trashes its live subtree under one `trash_batch`; restore brings back that batch only; an item whose parent is still trashed returns to the workspace root. Trashed folders cannot receive items. `SetFolderTrash` authorizes before reporting state, so an unauthorized caller gets `ErrDenied` whether the folder is live, trashed or missing.
- Moves never create versions. A same-workspace file move needs editor; a cross-workspace file move needs rank 3 (manager or personal owner) on the source and editor on the target, plus target quota for every version, and, in the same transaction, revokes every open editor session on the file, closes its editor documents and increments `drive_files.editor_epoch` (sessions may come from a credential scoped to the source; closed documents refuse in-flight callbacks). Trash and editor-session writes require the workspace they authorized. Folder moves stay in their workspace and cannot enter their own subtree. Copies share the immutable blob and count against target quota. Copy and restore read the reused blob inside their publishing transaction.
- Purge needs rank 3. `purgeFile` fails closed before any delete: unknown is `ErrDenied`, a live file is `ErrConflict`. Purging a file or a non-current version is blocked while any matching editor session has `drop_done=false`, including expired sessions the revocation worker has not swept, so Euro-Office always receives its drop before the rows go.
- Retention: `trash_days` and `keep_versions` per shared workspace, 0 = off, admin-set (`SetRetention`); personal workspaces are refused with `ErrDenied`. `ApplyRetention` pages by cursor so stuck items cannot starve later ones, re-checks each item's policy inside that item's own transaction, writes `file.retention_purged`, `version.retention_purged` and `folder.retention_purged` as actor `system`, and logs one line with the skipped count when above 0. Empty expired trashed folders only. `export_test.go` exposes test hooks (`PurgeFileUnauthorized`, `ExecSQL`, `RetainFile`, `RetainVersion`).

## Work Guidance

- Keep edits pure where possible; validate filenames, sizes and revision inputs at boundaries.
- Separate upload staging paths before concurrent writes.

- Editor document keys are `<file>-<revision>` at epoch 0 and `<file>-<revision>e<epoch>` after a cross-workspace move (`DocumentKey`, `ParseDocumentKey`); sessions store their epoch so revocation drops target the key they opened. Editor revision pointers and save digests make force-save sequences atomic and exact retries idempotent. Competing uploads conflict; session rows survive user deletion for revocation delivery.
- Roll back only to a version that understands personal ownership after private workspaces are created; earlier versions do not provide personal access or filtering.
- Service credentials are hashed, workspace-scoped reader/editor grants tied to current issuer membership and expire after 90 days.

## Verification
- `go test -race ./internal/drive` covers concurrent saves, offboarding, quotas, atomic memberships and editor replay/conflict; `schema_test.go` (migrations, legacy upgrade), `trash_test.go`, `move_test.go`, `purge_test.go` and `retention_test.go` cover the file-management contracts.

## Child DOX Index
