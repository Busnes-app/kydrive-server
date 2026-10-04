# Drive domain

## Purpose

Own personal and organization workspaces, files, folders, immutable versions and group access grants.

## Ownership

Own drive schema and domain operations over the application's database and blob directory. Identity rows remain owned by the inherited identity stores.

## Local Contracts

- Shared documents belong to workspaces, independent of user deletion.
- Personal workspaces are unique per immutable account ID, default to 10GiB, and allow only the active owner. Group grants are rejected for personal workspaces, including administrator grants. Retain ownership/data after offboarding; session revocation covers both personal and shared files.
- Read current user status and group memberships when authorizing each operation. Global admin can manage workspace configuration; content requires an explicit group grant.
- Publish immutable, generated blob IDs before transactionally committing version metadata. Check expected revisions and quotas under the transaction. Do not derive disk paths from user filenames.
- SQLite is the initial supported drive database. Keep domain inputs independent of HTTP.

## Work Guidance

- Keep edits pure where possible; validate filenames, sizes and revision inputs at boundaries.
- Separate upload staging paths before concurrent writes.

- Editor revision pointers and save digests make force-save sequences atomic and exact retries idempotent. Competing uploads conflict; session rows survive user deletion for revocation delivery.
- Service credentials are hashed, workspace-scoped reader/editor grants tied to current issuer membership and expire after 90 days.

## Verification
- `go test -race ./internal/drive` covers concurrent saves, offboarding, quotas, trash, atomic memberships and editor replay/conflict.

## Child DOX Index
