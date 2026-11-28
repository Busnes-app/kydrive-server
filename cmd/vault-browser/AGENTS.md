# Vault browser WebAssembly

## Purpose
Own the staging-only WebAssembly acceptance adapter. Q13 bars this separate-origin site
from shipping. Put client protocol changes in `pkg/vaultclient`.

## Local Contracts
- Use the existing age hybrid identity and vault signature and checkpoint implementations.
- The browser encrypts private key material before local storage. Publish only public keys,
  signed requests and encrypted vault state.
- Verify the genesis fingerprint from the trusted invitation, each membership transition,
  and the pinned witness checkpoint before unwrapping. Removal rotates the recipient and
  carries the prior generation encrypted to the new one.
- Verify revision signers against their historical membership and editor binding signers
  against the current head. Refresh a checkpoint only after verifying the same head with
  the pinned witness. The runbook owns staging scope and hosting.
- Serve the plaintext revision cap from WebAssembly. Check the encrypted size before signing.

## Verification
CI runs shared client revision tests through `go_js_wasm_exec`.
Run the browser acceptance in [Vault integration](../../docs/VAULT-INTEGRATION.md).
