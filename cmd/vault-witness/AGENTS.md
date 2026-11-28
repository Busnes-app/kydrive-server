# Vault witness command

## Purpose
Runs the independent HTTPS witness over a private, durable SQLite database.

## Local Contracts
- Initialize a database explicitly. Serving requires an existing private database, a dedicated
  signing key, a pinned KyDrive publisher key and TLS.
- Keep state and key material separate from KyDrive. The
  [vault runbook](../../docs/VAULT-INTEGRATION.md) owns operator setup and recovery boundaries.

## Verification
Run `go test -race ./internal/vault ./cmd/vault-witness`.
