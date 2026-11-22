# qmark

## Purpose
Opens PostgreSQL through pgx so SQL written with SQLite's `?` placeholders runs unchanged.

## Ownership
Owns the rewriting connector. Imports only pgx.

## Local Contracts
- `Open(dsn)` returns a `*sql.DB` whose every query has `?` and `?NNN` renumbered to `$n` before pgx sees it, with SQLite's rule for a bare `?` after a numbered one. Literals, quoted identifiers, comments and dollar-quoted bodies are left alone. Never mix `$n` and `?` in one query.
- A Go `bool` bound to an integer parameter is 1 or 0, as on SQLite; the shared schema keeps SQLite's INTEGER flag columns. A PostgreSQL `boolean` result still scans only into `bool`.
- `Is(db)` reports a handle from `Open`; the drive store reads its dialect from it.
- Every connection sets `default_transaction_isolation=serializable`, so main-store and drive writes keep the one-writer guarantee between them. Do not lower it per transaction.

## Verification
- `go test ./internal/qmark` (rebind cases); the whole suite on PostgreSQL (CI's `postgres` job) covers the connector.

## Child DOX Index
None.
