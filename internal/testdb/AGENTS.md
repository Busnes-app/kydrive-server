# TestDB

## Purpose
Supplies tests with an isolated, empty database configuration so the same suite runs on either supported backend.

## Ownership
Owns test-only database provisioning and teardown. Never imported by production code.

## Local Contracts
- `Config(t *testing.T) config.DatabaseConfig` returns a SQLite temp file by default. `Key` is the fixed deployment key tests pass to `store.Open`.
- With `KY_TEST_POSTGRES_DSN` set, it returns a Postgres config scoped to a per-caller schema, dropped on cleanup.
- `Open(t, cfg)` opens that database directly for tests that read or write rows (SQLite with foreign keys on, PostgreSQL through `qmark`), closed on cleanup.
- `Postgres()` reports the PostgreSQL run. `SQLiteOnly(t, why)` skips a test on it; `why` names what only SQLite has (an older schema to upgrade, `EXPLAIN QUERY PLAN`, content search until R2, backups until R3).
- Test files must obtain database config here, never by hardcoding a driver, so CI can switch backends.

## Verification
- `go test ./...` (SQLite)
- `KY_TEST_POSTGRES_DSN=... go test -count=1 ./...` (PostgreSQL; CI's `postgres` job)

## Child DOX Index
None.
