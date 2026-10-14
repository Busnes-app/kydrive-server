# Storage Layer

## Purpose
Provides the unified Database Abstraction Layer (DAL) supporting pluggable backends (SQLite zero-CGO default and PostgreSQL enterprise) with automated dialect-aware migrations.

## Ownership
Owns data models, store interfaces (`UserStore`, `SessionStore`, `GroupStore`, `AuditStore`, `SettingsStore`), dialect translations, and schema migrations.

## Local Contracts
- `ApplyInstaller` creates only missing issuer-bound SCIM accounts, dedicated roles and owned configuration references; one transaction includes every ownership record and audit. Foreign accounts and drift are refused. Check/resume never create resources or audit rows. `InstallerAdministrator` identifies installer-created dedicated accounts without altering existing mixed-use accounts.
- `CompletePasswordChange` atomically compares the old password, updates a flagged local account, clears the flag, deletes sessions/MFA challenges and records `auth.password_changed`. Session/MFA issuance locks the same user row against the verified hash; MFA challenges persist the creation-time password hash, and consumption returns that snapshot to reject stale completions. Migration 4 discards preexisting challenges because their credential snapshot is unknown.
- `ResetAdminPassword` reactivates a local administrator with the replacement flag set and shares the atomic grant purge and audit path with `CompletePasswordChange`; it also works for disabled accounts.
- `SetDirectoryRole` is an explicit operator grant to exactly one active SCIM/KyIdentity subject. It accepts only admin/user, preserves directory identity, revokes sessions/MFA challenges and records the role change atomically. Local and inactive accounts are excluded.
- `RenameLocalAdmin` atomically renames only a local administrator and records an audit row. Identity, credentials and privileges stay intact; it cannot rename directory accounts or replace another account.
- `OpenInNewTab`/`SetOpenInNewTab` read and write `users.open_in_new_tab` (migration 5, default true) outside `User`, so profile, SCIM and directory updates never reset it.
- `store.Open(ctx, cfg, key)` initializes and auto-migrates the configured database backend. `key` is the deployment key (`cfg.Security.EncryptionKey`); the SQLite drive store refuses to open without a 32-byte key.
- SQLite runs in WAL mode with foreign keys enabled.
- PostgreSQL queries are rebound dynamically from standard positional parameters.
- MFA challenges are consumed with database state transitions that permit exactly one successful use.
- `migrations.Run` applies each migration in one transaction that first excludes concurrent runners (SQLite: a no-op write takes the write lock; Postgres: `pg_advisory_xact_lock`), then checks `schema_migrations`, so processes starting together apply each version once.
- Migrations are append-only: add a version, never edit a shipped one, and give both SQLite and PostgreSQL DDL. Migration 6 drops the inherited `device_pairings` table and `users.push_device_id`.
- Recovery-code hash updates use optimistic concurrency so simultaneous redemption cannot reuse a code.
- A pending TOTP enrolment is a sealed `totp_secret_enc` with `totp_enabled` false. `SetTOTPSecret` compare-and-swaps the secret (enrolment, legacy reseal); `EnableTOTP`/`DisableTOTP` compare-and-swap the state and, in the same transaction, delete every other session and all MFA challenges and write the audit row. A lost race is `ErrAlreadyExists`.
- `ApplyDirectoryUser` is the only write path for directory input (SCIM, KySignOn webhook, SSO auto-provision). It never writes the role (new accounts are `user`), validates usernames (`ErrInvalidUsername`: 1-128 of letters, digits, `._@+-`) except when disabling an existing account, which changes only its status so a legacy username can always be switched off, touches only `scim`/`kysignon` rows, and deletes sessions and MFA challenges in the same transaction when the account ends inactive. A nonzero `EventTime` applies only when newer than `users.directory_ts` (`ErrStale` otherwise); on a same-second tie a deactivation or `DeleteDirectoryUser` applies and an activating update does not. `CreateSession` takes the MFA state the caller authenticated against and refuses (`ErrNotFound`) if it changed, inside the same transaction that checks the password hash. `DirectorySubject` resolves a subject KySignOn-first, then SCIM. `ListDirectoryUsers` pages SCIM accounts only.
- Migration 7 adds `users.directory_ts`; migration 8 adds a unique index on `LOWER(username)`. A `Migration.Check` runs in the migration transaction first: v8 refuses to apply while usernames differ only by case, naming them; rename one, then restart.

- Every path that revokes an account's sessions (`revokeGrants`: password change/reset, directory deactivation, role change; plus `totpTransition`, `DeleteDirectoryUser`, `DeleteUser`, `DeleteUserSessions` and sign-out `EndSession`) also calls `drive.Store.RevokeUserEditors` in the same transaction. Sign-out revokes all of the account's editor sessions: they are not bound to one browser session, and Euro-Office drops a user per key anyway.
- Production drive storage is SQLite-only. Every SQLite connection enables WAL, foreign keys, busy timeout and synchronous FULL even when the caller supplied other DSN pragmas. `Drive()` exposes the domain repository, never its SQL handle.
- `ReplaceGroup` writes group attributes, the complete membership set and its audit in one transaction; invalid members roll back the entire replacement. Nonempty SCIM external subjects/group identifiers are unique.

## Verification
- `go test -v ./internal/store/...`

## Child DOX Index
None.
