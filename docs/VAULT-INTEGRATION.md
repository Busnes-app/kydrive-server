# Vault control plane

KyDrive can store and commit signed vault membership records. A separate witness can
checkpoint those records. Linked devices can store immutable ciphertext versions and
bind one CryptPad session to a signed vault head.

## Start a fixture

Install the command with `go install ./cmd/vault-witness` or build the local image with
`docker build --target vault-witness -t kydrive-vault-witness:local .`. The witness image
runs as UID and GID 1000. For a container, mount owner-only durable state read-write and
mount the signing and TLS files read-only. Listen on `0.0.0.0:9443` inside the container.
Run the witness under a separate account with a private key file and a TLS certificate. The
witness signing key, accounts and storage should be independent of the KyDrive deployment.

- Run `vault-witness -generate-key -key-file /private/publisher.key` on the KyDrive machine.
  Save the printed base64 public key for the witness's `-publisher-key` flag.
- Run the same command with `/private/witness.key` on the witness machine. Set
  `KYDRIVE_VAULT_WITNESS_KEY` to the printed public key and
  `KYDRIVE_VAULT_PUBLISHER_KEY_FILE` to the absolute publisher key file path on KyDrive.
  Omitting both keeps the routes off.
- Run the witness once with `-initialize -state-dir /private/state -key-file
  /private/witness.key -publisher-key <publisher-public-key>`. Keep the database on durable
  storage.
- Serve with the same flags, replacing `-initialize` with `-listen <address> -tls-cert <chain>
  -tls-key <key>`. The listener requires TLS 1.3. The default bind is loopback. The witness
  accepts only the stored publisher and witness identities after initialization.

Retain the publisher key separately from the KyDrive database backup. Preserve the latest
witness database and signing key independently. Starting with an older witness database and
the same signing key can let a device with no newer checkpoint accept an old head. Witness
state rollback detection and coordinated restore are release gates. Keep both machines and
the trusted clients on synchronized clocks.

## Device protocol

The routes in `internal/api/vault.go` require a signed linked-device request. The authenticated
account establishes the caller's organization. The key route enrolls and reads public keys.
Enrollment is immutable, and the signed log pins each member's fingerprint. The client
checks the pins independently and creates and opens each age wrap locally.

A manager submits a signed record to `POST /api/vaults/{id}/records`. Membership, key
rotation and the new head commit together. The response carries a publisher receipt. On a
lost response, a current member can fetch missing records and receipts with
`GET /api/vaults/{id}/records?after=<sequence>`, verify them, and resume from the last record
returned. The witness accepts receipt arrays at `POST /v1/logs/{log}` and serves the latest
signed checkpoint at `GET /v1/logs/{log}`. The client reaches that independent origin
directly and verifies its pinned key. The witness receives only opaque links and signatures.

`POST /api/vaults/{id}/check` checks the caller's membership, edit role if requested, and
the exact head signed by the witness within the last five minutes. KyDrive refuses the old
head as soon as it commits a membership change. A trusted client also verifies the member
signature chain, the recipient and the independently witnessed head before using a wrap.
A fresh checkpoint cannot substitute for the member-signature checks.

## File versions and editor sessions

A linked device uploads binary age ciphertext to
`PUT /api/vaults/{id}/files/{file}/versions/{revision}`. Send URL-safe unpadded base64 JSON
in `KyDrive-Vault-Revision` and `KyDrive-Vault-Checkpoint`. The signed revision commits
to the ciphertext's size and SHA-256, organization, vault, file, revision, generation and
head. Versions begin at 1 and advance by one. An existing revision only acknowledges a
retry of the same signed contents. KyDrive seals the ciphertext again in the normal blob
store and includes it in local bulk backup and orphan accounting. This slice caps each
ciphertext version at 32 MiB and each vault at 1 GiB of retained ciphertext.

`GET /api/vaults/{id}/files/{file}/versions/{revision|latest}` returns ciphertext plus
its signed manifest. The native client checks its pinned signer, the exact requested
document, revision and digest, then decrypts locally.

A signed `PUT /api/vaults/{id}/files/{file}/editor` binds the current saved revision and
witness head to fresh Netflux rooms. Each member receives a separate capability encrypted
to their public recipient. A reader receives a view capability. Once a binding is
active, a new file version must cite it. A new membership head retires the prior binding
and its rooms. A client rotates with a fresh room and editor keys and imports the
latest verified Office export.

`POST /api/vaults/{id}/files/{file}/editor/session` issues a scoped bearer for up to
one minute within the current witness checkpoint. The relay sends it in Authorization to
`POST /api/vault-relay/check` before forwarding a protected operation. The check requires
the active binding, vault head and linked device. Submit exactly one room or blob ID.
Room writes require an edit session. Blob reads require an ID reserved in the signed
binding's optional `blobs` list. A blob ID belongs to one binding.

## Private staging gateway

`deploy/vault-gateway/compose.yml` builds an HTTPS gateway and runs the pinned
CryptPad data relay on an isolated internal network. Docker Engine must support isolated
bridge mode. Only the gateway has a published port, on loopback. Keep the relay as the
sole other service on its private network.

Provide the following environment values. Read-only certificate mounts must be readable
by the gateway's UID 1000.

- `KY_VAULT_RELAY_AUTHORITY` names the staging KyDrive HTTPS origin. The gateway pins
  the normal destination and extends its CA trust from `KY_VAULT_AUTHORITY_CA`.
- `KY_VAULT_GATEWAY_CERT` and `KY_VAULT_GATEWAY_KEY` name its TLS certificate and key
  files. `KY_VAULT_GATEWAY_PORT` defaults to 18960.
- `KY_VAULT_RELAY_ORIGIN` and `KY_VAULT_SANDBOX_ORIGIN` configure upstream CryptPad's
  two distinct origins. `KY_VAULT_CLIENT_ORIGINS` lists up to two permitted origins,
  separated by a comma. Use HTTPS for remote origins.

From the repository root, run
`docker compose -f deploy/vault-gateway/compose.yml up -d --build` after setting these
values. This project uses its own data volumes. For the disposable acceptance use the
command below, which starts and cleans up a separate project with synthetic data and TLS
keys.

A client opens `wss://<gateway>/cryptpad_websocket?room=<registered-room>` with
subprotocols `kydrive-vault` and `grant.<KyDrive bearer>`. The gateway selects only
`kydrive-vault` and omits the bearer from upstream requests. Never log the offered
WebSocket subprotocol header or the HTTP Authorization header.

The gateway checks the connection and each forwarded WebSocket frame against KyDrive
without a lease cache. It allows room joins, leaves, messages and specific CryptPad
history operations. CryptPad's ordinary `GET_HISTORY` can create metadata and requires
an edit lease. A view lease can use `GET_FULL_HISTORY` or `GET_HISTORY_RANGE`. Unknown
commands, direct peer messages and general history-keeper RPC are refused.

The HTTPS file surface accepts authenticated GET, HEAD and single-byte-range reads at
`/blob/<first-two-hex-digits>/<48-character-blob-id>`. Reads require an active signed blob
reservation. The gateway blocks upstream account, config, datastore and upload routes.
This profile supports 32 WebSocket connections and 1 MiB text frames, with bounded
forwarding queues. It checks authorization before each HTTP request to the relay.
KyDrive allows 6,000 relay checks per source IP per minute. A shared gateway consumes
that budget for both incoming frames and each outgoing copy of a room message. Measure
authorization denials and KyDrive load alongside relay memory during capacity testing.

### Gateway acceptance

Run with Docker Compose, OpenSSL, Chrome and the installed web dependencies.

```sh
KYDRIVE_VAULT_GATEWAY_FIXTURE=1 go test -race -count=1 -v -timeout=6m \
  ./internal/api -run '^TestVaultWitnessAndRevocation$'
```

Chromium sent live room traffic through the real gateway and pinned private relay. The
test verified a write, history replay, bound blob download and range read before
revocation. A foreign blob, reader write, raw relay path, and unapproved history or RPC
failed. When KyDrive returned 503, the gateway refused the operation. After witnessed
removal, open sockets closed and reconnects and direct blob reads failed. The remaining
member could join a new signed binding. An edge-network peer also failed to reach the
relay's private address directly.

## DOCX integration fixture

Use a fresh copy of the previous pinned editor proof directory with `prepare.sh`,
`verified-front.mjs`, `browser-fixture.mjs`, `check-export.py` and the synthetic
`fixtures/edit.docx`. Run its `prepare.sh` to start the loopback CryptPad relay and
build the locally signed editor bundle. The Go test launches the browser runner and the
loopback origins. Use Chrome and the repo's installed Playwright Core.

```sh
KYDRIVE_DOCX_FIXTURE=/path/to/prepared-proof \
OFFICE_PLAYWRIGHT="$PWD/web/node_modules/playwright-core/index.mjs" \
go test -count=1 -v -timeout=5m ./internal/api -run '^TestVaultWitnessAndRevocation$'
```

The fixture uses disposable browser accounts and keys. The native fixture controller
encrypts the file and capabilities before sending them to KyDrive. It decrypts the stored
version for local editor import. The fixture proxy checks registered rooms with the live
KyDrive relay endpoint. It lets the stock CryptPad client bootstrap its test account and
originate candidate rooms on the isolated relay.

The local DOCX run on 2026-11-27 wrote three immutable versions. It recovered both
collaborators' edits from a saved export. After membership removal the active browser
connection's write was refused. The old editor key opened zero of ten signed frames
in the new rooms. A live edit confirmed by the second browser survived restarting the
relay before a KyDrive export. The relay working set peaked at 512 MiB for this fixture.
`TestBulkRestoresVaultCiphertext` also recovered the exact saved bytes from the Restic
snapshot and decrypted them with the client key.

## Qualification status

The current signed catalogue supports manager-only membership changes and up to 64
members. Each role change rotates the vault key. The independent witness uses the persisted
KyDrive commit as its append authority.

Review the wire format and device recovery flow before distributing a client. Full
production qualification still needs an independently signed installed client wired to
the strict gateway, including scoped room and encrypted-blob provisioning. It also
needs coordinated recovery of KyDrive, live CryptPad history and the witness state, plus
a representative peak-memory comparison. Stage with synthetic data until those gates
pass. Consult the [vault design](superpowers/specs/2026-11-22-e2e-vault-design.md)
for the remaining protocol.

`TestVaultWitnessAndRevocation` exercises signed ciphertext storage, mismatched and stale
exports, read-only sessions, rotation, organization boundaries, competing commits and
receipt recovery after a lost response. `TestWitnessMonotonicHead` checks expiry,
independent key verification, racing extensions and rollback refusal.
