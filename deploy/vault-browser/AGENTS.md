# Vault browser staging site

## Purpose
Own the standalone browser link, shared vault proof and CryptPad transport acceptance.

## Local Contracts
- Serve the build on the configured trusted editor origin. The KyDrive app handles approval
  and the browser site handles keys. The site's own config pins the witness.
- Clear the approval code from the address bar before requests and check the PKCE state.
- Keep credentials out of requests except signed vault authentication and token exchange.
- Confirm a member fingerprint via an independent channel before wrapping the vault key.
  Member invitations carry the independently verified owner fingerprint and vault ID.

- DOCX staging uses the pinned, patched CryptPad integration from a separate asset
  origin. Send document data and single-document leases only to its exact iframe.
  The first editor initializes a reserved encrypted checkpoint. Peers load the checkpoint
  and signed rooms. Renew against the exact witnessed head and encrypt exports in the vault site.

## Verification
Run the browser acceptance in [Vault integration](../../docs/VAULT-INTEGRATION.md).
It reopens 33 witnessed records across two signed history pages.
