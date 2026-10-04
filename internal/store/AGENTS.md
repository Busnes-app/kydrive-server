# Storage Layer

## Purpose
Provides the unified Database Abstraction Layer (DAL) supporting pluggable backends (SQLite zero-CGO default and PostgreSQL enterprise) with automated dialect-aware migrations.

## Ownership
Owns data models, store interfaces (`UserStore`, `SessionStore`, `DeviceStore`, `GroupStore`, `AuditStore`, `SettingsStore`), dialect translations, and schema migrations.

## Local Contracts
- `CompletePasswordChange` atomically compares the old password, updates a flagged local account, clears the flag, deletes sessions/MFA challenges/device pairings and records `auth.password_changed`. Session/MFA issuance locks the same user row against the verified hash; MFA challenges persist the creation-time password hash, and consumption returns that snapshot to reject stale completions. Migration 4 discards preexisting challenges because their credential snapshot is unknown.
- `ResetAdminPassword` reactivates a local administrator with the replacement flag set and shares the atomic grant purge and audit path with `CompletePasswordChange`; it also works for disabled accounts.
- `SetDirectoryRole` is an explicit operator grant to exactly one active SCIM/KyIdentity subject. It accepts only admin/user, preserves directory identity, revokes sessions/MFA/device grants and records the role change atomically. Local and inactive accounts are excluded.
- `RenameLocalAdmin` atomically renames only a local administrator and records an audit row. Identity, credentials and privileges stay intact; it cannot rename directory accounts or replace another account.
- `store.Open(ctx, cfg)` initializes and auto-migrates the configured database backend.
- SQLite runs in WAL mode with foreign keys enabled.
- PostgreSQL queries are rebound dynamically from standard positional parameters.
- MFA challenges and device pairings are consumed with database state transitions that permit exactly one successful use.
- Recovery-code hash updates use optimistic concurrency so simultaneous redemption cannot reuse a code.

- Production drive storage is SQLite-only. Every SQLite connection enables WAL, foreign keys, busy timeout and synchronous FULL even when the caller supplied other DSN pragmas. `Drive()` exposes the domain repository, never its SQL handle.
- `ReplaceGroup` writes group attributes, the complete membership set and its audit in one transaction; invalid members roll back the entire replacement. Nonempty SCIM external subjects/group identifiers are unique.

## Verification
- `go test -v ./internal/store/...`

## Child DOX Index
None.
