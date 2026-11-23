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
- `Retry(ctx, f)` reruns `f`, one whole transaction, on a serialization failure or deadlock (up to `MaxAttempts`, random pause). Every drive and main-store transaction runs through it; `f` must only touch the database and reset, at its start, every outside variable it writes (`TestRerunClosuresResetWhatTheyWrite` in `internal/drive`).
- The connection reruns, through `Retry`, a statement it runs by `Exec` outside a transaction when that statement fails the same way: PostgreSQL rolled it back whole. It tracks whether it is inside a transaction (`BeginTx`, then the transaction's `Commit` or `Rollback`); inside one, the failure is returned, so the transaction's own `Retry` reruns it whole. `Query` is never rerun: its error can arrive while rows are read (`TestStatementsOutsideTransactionsRerun`).

## Verification
- `go test ./internal/qmark` (rebind cases; the rerun test needs `KY_TEST_POSTGRES_DSN`); the whole suite on PostgreSQL (CI's `postgres` job) covers the connector.

## Child DOX Index
None.
