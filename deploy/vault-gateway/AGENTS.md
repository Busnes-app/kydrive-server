# Vault staging gateway

## Purpose
Own the fail-closed CryptPad transport and its isolated Compose deployment.

## Local Contracts
- Netflux rules target the pinned relay version. Unknown commands, room targets, peer messages
  and raw relay HTTP routes are refused. A room lease authorizes the connection and each frame.
- Blobs are fetched by canonical ID after KyDrive confirms the signed binding. The gateway never
  forwards client credentials upstream. Its public traffic uses TLS. Only the gateway joins both
  the edge and the internal relay networks.
- Stage with synthetic data. The runbook owns the installed-client and release gates.

## Verification
CI runs `node --test deploy/vault-gateway/netflux.test.mjs`. Run the staging acceptance in `../../docs/VAULT-INTEGRATION.md`.
