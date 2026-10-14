# Crypto

## Purpose
Provides cryptographic primitives for data-at-rest encryption (AES-256-GCM), HMAC signing, SHA-256 digests, randomness, and PKCE key pairs. Password hashing lives in `ky-primitives/password`, not here.

## Ownership
Owns core cryptographic implementations, key derivation, and constant-time equality checks.

## Local Contracts
- Never store or log plaintext cryptographic keys or master secrets.
- Password hashing parameters are `ky-primitives/password`'s to choose; this package must not grow a second implementation.
- AES-256-GCM uses fresh 12-byte random IVs per encryption operation. `EncryptAESGCMAAD`/`DecryptAESGCMAAD` add associated data; the plain functions are the nil-AAD case with an identical format. Drive names use the AAD form bound to their row. Blob encryption lives in `internal/drive/sealed.go` and uses counter nonces instead, safe only because every blob has its own HKDF key from a random salt.

## Verification
- `go test -v ./internal/crypto/...`

## Child DOX Index
None.
