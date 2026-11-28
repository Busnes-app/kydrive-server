# Vault client protocol

## Purpose

Share membership verification, revision encryption and editor bindings across native clients
and the staging WebAssembly adapter.

## Local Contracts

- Verify the independently supplied genesis fingerprint, member keys and current witnessed
  head before opening an account's wrap. Bind revisions and editor access to that verified
  history and scope.
- Callers own transport, enrollment, session serialization and durable key storage. Native
  clients keep secrets in the OS keystore. The standalone staging adapter keeps its fixture
  key storage local.
- Recovery keys are age hybrid identities; the signing half derives from their text. Show it
  once and never send it. Accept a replaced key only through `VerifyRecoveryChain` from a
  fingerprint the caller already trusts, and a `rekey` record only when the target's chain in
  `HistoryInput.Recovery` moves the pinned key to the new one. Keep `HistoryInput` additive.
- Keep crypto portable between native Go and WebAssembly. Use the protocol in
  `internal/vault`, age hybrid recipients and `ky-primitives/hybridsig`.

## Verification

- `go test -race -count=1 ./pkg/vaultclient`
- CI runs the same tests through `go_js_wasm_exec` with `GOOS=js GOARCH=wasm`.
