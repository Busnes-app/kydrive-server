# Vault protocol

## Purpose
Owns membership verification and the witness protocol. See the [vault runbook](../../docs/VAULT-INTEGRATION.md).

## Local Contracts
- Sign each record type with its own hybridsig domain. Membership uses a manager's pinned
  hybrid signing key. KyDrive signs receipts only after the store commits the record.
- Witness checkpoints bind the exact log ID, sequence, hash and issue time. A delayed response
  must not renew a superseded head. The witness signs only extensions or the exact current head.
- The witness receives opaque hashes and keeps its own durable state and distinct signing key.
- Accept only age `mlkem768x25519` hybrid recipients (`ValidRecipient`). Post-quantum marketing
  claims rest on it (`docs/superpowers/specs/2026-11-28-kydrive-pq-status.md`).
- Browser WebAssembly builds the wire verification code. The SQLite witness is native-only.

- Sign document revisions and room bindings under separate domains. The signed scope includes
  tenant, vault, file, signer and witnessed head. Wrap the editor capability separately for
  each member. Readers receive the CryptPad view capability. Optional sorted blob IDs
  reserve encrypted CryptPad files to the signed binding.

- Recovery records (`recovery.go`) chain per account. Each is signed under separate domains by
  its account key and recovery key, and after registration by the previous recovery key. A
  `rekey` membership record changes only its target's pin and rotates the generation.

## Verification
Run `go test -race ./internal/vault` and `go test -race ./internal/api -run TestVault`.
