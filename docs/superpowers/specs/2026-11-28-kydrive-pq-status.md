# KyDrive vaults: what is post-quantum, and what we may say

Date: 2026-11-28. Status: research for marketing. It changes no product behaviour.

**Read this first.** Everything below is about the **vault** with CryptPad co-editing. It is
staging-only today: `cmd/vault-browser` cannot ship, and the signed desktop app and extension
are not finished (`AGENTS.md`, Local Contracts). The rest of KyDrive is not end-to-end
encrypted yet: the server and Euro-Office read plaintext. Publish no claim below until the
vault ships in a signed client.

Sources are pinned:

- **Our code:** KyDrive `master` at `66b5454`. Desktop is `Busnes-app/kydrive-desktop`
  `feat/vault-native` at `6fea547`.
- **Libraries:** `filippo.io/age` v1.3.2, `filippo.io/hpke` v0.4.0 and
  `ky-primitives` v0.12.0, all read from the Go module cache.
- **CryptPad:** tag `2026.10.1` (commit `fd2850b`). The relay image
  `cryptpad/cryptpad@sha256:b1129d95…` (`deploy/vault-gateway/Dockerfile:1`) reports
  version 2026.10.1, `chainpad-crypto` 0.3.0 and `tweetnacl` 1.0.3. `chainpad-crypto` lines
  are from that image's `node_modules/chainpad-crypto/crypto.js`.

"Unproven" marks anything this note did not test or read in code.

"HNDL" means harvest now, decrypt later: an attacker records traffic or copies storage today,
then decrypts it once a large quantum computer exists. Shor's algorithm breaks X25519,
Ed25519, P-256 and curve25519 `box`. Grover's algorithm only halves symmetric key strength,
so 256-bit keys stay safe.

## The table

| Component | Algorithm | PQ status | HNDL exposure |
|---|---|---|---|
| Vault file revisions | age: ChaCha20-Poly1305 payload, file key wrapped to the vault key | **Hybrid** | None while ML-KEM-768 holds |
| Vault key, previous-generation chain, name-index key, member wraps | age `mlkem768x25519` (HPKE, X-Wing) | **Hybrid** | None while ML-KEM-768 holds |
| CryptPad edit capability (pad seed, rooms, blob IDs) | wrapped to each member with age `mlkem768x25519` | **Hybrid** | None while ML-KEM-768 holds |
| Vault log, revisions, bindings, receipts, witness checkpoints | `hybridsig`: ML-DSA-65 + Ed25519, both must verify | **Hybrid** | None. Signatures carry no secrets. Forgery needs both broken. |
| CryptPad room messages (the live document) | XSalsa20-Poly1305 `secretbox`; key from an 18-byte random seed via SHA-512 | **Symmetric** | None. The 144-bit seed bounds Grover search at about 2^72 steps. |
| CryptPad message signatures (`validateKey`) | Ed25519, derived from the seed | Classical | No secrets exposed. A future attacker could sign frames but not encrypt them. |
| CryptPad checkpoints (encrypted Office snapshots) | `secretbox` chunks under a fresh random 32-byte key, stored inside the encrypted pad | **Symmetric** | None |
| CryptPad mailboxes, teams, contacts (`Curve`, `Mailbox`, `Team`) | curve25519 `box` | Classical | **Total if used.** Our profile does not use them. |
| Server at rest (KYB2 over everything) | AES-256-GCM under the deployment key or org key | Symmetric | None |
| Relay grant | HMAC-SHA256 bearer, one minute | Symmetric | None. It expires. |
| TLS: client to gateway | Node 26.13.1 / OpenSSL 3.5.9, TLS 1.3 | **Hybrid if the client offers it** (tested) | Classical clients fall back to X25519. Payload is CryptPad ciphertext. |
| TLS: client to witness | Go 1.27 `crypto/tls`, TLS 1.3, default groups | Hybrid if the client offers it | Public checkpoints only |
| TLS: gateway to KyDrive; KyDrive outbound | Node `fetch` offers X25519MLKEM768 first (tested); Go outbound is tested | Hybrid if the edge accepts it | Short-lived grants and metadata |
| TLS: client to KyDrive edge (proxy) | the operator's proxy; KyDrive itself serves plain HTTP | **Unproven** | If the edge is classical: tokens, metadata, and **plaintext of every non-vault file** |
| Gateway to relay | plain HTTP/WS on an isolated Docker bridge | No TLS | CryptPad ciphertext only; must stay on one host |
| Relay storage | CryptPad datastore: signed `secretbox` messages, channel metadata | Symmetric content | None for content; IDs, sizes and timing are readable |
| Browser fixture key store | age scrypt passphrase, `localStorage` | Symmetric | None from quantum; passphrase guessing is classical |
| Device request signatures | browser fixture P-256; desktop `hybridsig` by default, P-256 for old servers | Mixed | None (authentication only) |
| Recovery key, Key Steward | designed as age hybrid identities | **Not built** | Unproven |

## Evidence

### Vault (ours)

- **Member keys.** Each member key is an age hybrid identity plus a `hybridsig` signing key
  (`pkg/vaultclient/session.go:29-37`). The vault key and its name-index key are wrapped to it
  (`session.go:102-115`). Rotation re-wraps the previous key and the index to the next
  generation (`shared.go:191-214`).
- **Hybrid recipients only.** The server refuses a non-hybrid recipient at enrollment
  (`internal/drive/vault.go:27`, through `internal/vault/record.go:65-71`), and so do records
  and pins (`record.go:101`, `shared.go:80`).
  - `TestValidRecipientRequiresHybrid` (`internal/vault/record_test.go`, added here) proves it.
    Swapping `ParseHybridRecipient` for `age.ParseRecipients` makes the test fail with
    "classical X25519 recipient accepted".
- **The algorithm.** age's recipient type is `mlkem768x25519`: HPKE with the
  MLKEM768-X25519 KEM, HKDF-SHA256 and ChaCha20-Poly1305 (age `pq.go:18,72`). hpke calls the
  KEM "MLKEM768-X25519 (a.k.a. X-Wing) from draft-ietf-hpke-pq" (hpke `pq.go:38-40`). It
  stays secret while either ML-KEM-768 or X25519 holds.
- **Signatures.** `hybridsig` is ML-DSA-65 (FIPS 204) plus Ed25519 (RFC 8032). A signature
  "stands while either algorithm does" (`hybridsig.go:4,11,137-142`). It signs the vault
  log, revisions and bindings (`internal/vault/document.go:51-101`). It also signs
  KyDrive's receipts and the witness checkpoints (`checkpoint.go:84-114`). The witness key
  is `hybridsig` too (`cmd/vault-witness/main.go:110-121`).
- **Server at rest.** KyDrive seals vault ciphertext a second time with KYB2: AES-256-GCM
  under HKDF from the deployment key or org key (`internal/drive/sealed.go:22-31`).

### CryptPad's own crypto (2026.10.1)

- **Pad keys.** A new pad takes an 18-byte random seed (`chainpad-crypto` `crypto.js:239`).
  - SHA-512 of the seed gives the Ed25519 signing pair (first half) and the view seed
    (second half; `crypto.js:252-262`).
  - SHA-512 of the view seed gives the 16-byte channel ID and the 32-byte `cryptKey`
    (`crypto.js:209-211`).
  - `Hash.createRandomHash` calls this for our `integration` pad (CryptPad
    `src/common/common-hash.js:155-166`). Our patch calls it in `PREPARE_VAULT`
    (`deploy/vault-browser/cryptpad-patches.mjs:30`). The site checks the
    24-character, 18-byte edit hash (`deploy/vault-browser/vault.mjs:276`).
- **Messages.** Each message is `secretbox` under `cryptKey` with a random 24-byte nonce,
  then Ed25519-signed (`crypto.js:35-41,112-123`). Only the signature check uses the public
  `validateKey` (`crypto.js:140`).
  - Breaking `validateKey` with Shor yields the signing scalar. That scalar comes from the
    first SHA-512 half; `cryptKey` comes from the second. So it reveals neither the seed
    nor `cryptKey`, and a forged frame still fails `secretbox` decryption.
- **Checkpoints.** Office checkpoints are `secretbox`-encrypted in 128 KiB chunks with
  counter nonces (`www/file/file-crypto.js:12,162-212`).
  - Our adapter makes a fresh 32-byte key for each upload
    (`deploy/vault-browser/cryptpad-adapter.js:48-51`).
  - It records the key only in a `/file/#…` link (`cryptpad-adapter.js:66-69`). That link
    sits in the pad content, which is encrypted under `cryptKey`.
- **Seed strength.** The 144-bit seed is the weakest secret in the room key. Grover search
  over it is about 2^72 sequential steps, above the AES-128 baseline of NIST's PQC
  category 1. Multi-target search over N recorded pads divides the work by N (by √N for
  Grover). This is adequate, but do not market co-editing as "256-bit".
- **curve25519 features.** CryptPad uses curve25519 `box` for `Curve` (one-to-one chat),
  `Mailbox` (notifications) and `Team` (`crypto.js:314-424,425-652,653-882`). Our profile
  never reaches them:
  - Integration mode sets `neverDrive` (`www/common/sframe-common-outer.js:356`), and our
    patch makes that skip the worker (`cryptpad-patches.mjs:63`).
  - Our patch removes the temporary RPC signing keys (`cryptpad-patches.mjs:68`).
  - The owner-mailbox metadata path needs a logged-in `edPublic`
    (`sframe-common-outer.js:789,1254-1262`). The adapter has no account.
  - The gateway forwards only room joins, leaves, messages and four history queries. It
    rejects RPC and direct peer messages (`deploy/vault-gateway/netflux.mjs:4-48`, tested by
    `netflux.test.mjs:9-35`).

### How CryptPad keys are handed out

The pad seed never takes a CryptPad path.

1. The editor iframe makes the seed (`PREPARE_VAULT`).
2. The site puts it in the edit capability (`vault.mjs:346-353`).
3. The vault core wraps the capability with age `mlkem768x25519` separately for each member
   (`pkg/vaultclient/documents.go:146-150`), inside a `hybridsig`-signed binding.
4. Each member unwraps it locally (`documents.go:191`).

The desktop branch uses the same `pkg/vaultclient` (`core/vault/client.go:23`). The relay
sees only channel IDs, `validateKey`s and ciphertext.

### TLS on each hop

**Tested on Major, 2026-11-28.** We ran a Node TLS 1.3 server with a P-256 certificate in the
gateway's pinned base image `node:26.13.1-alpine@sha256:6bd9ab43…`, which reports OpenSSL
3.5.9:

- `openssl s_client` (OpenSSL 4.0.3) with default groups printed
  `Negotiated TLS1.3 group: X25519MLKEM768`.
- With `-groups X25519` the handshake still succeeded, classically.
- Node `fetch` to `openssl s_server -groups X25519MLKEM768` succeeded. It offered
  `X25519MLKEM768:x25519:secp256r1:…`.

Per hop:

- **Client to gateway.** The gateway sets only `minVersion: 'TLSv1.3'`
  (`deploy/vault-gateway/gateway.mjs:242-246`), so it takes the hybrid group when the client
  offers it.
- **Client to witness.** The witness sets only `MinVersion: tls.VersionTLS13`
  (`cmd/vault-witness/main.go:91`). `TestPQTLSHandshake` (`internal/api/pqtls_test.go:57`)
  shows that the same Go server config negotiates X25519MLKEM768, also under `fips140=on`.
  The witness binary itself was not handshake-tested (unproven).
- **KyDrive outbound.** Go's outbound client is covered by that same test. In CI,
  `scripts/check-pqc-tls.sh` refuses `CurvePreferences` and `tlsmlkem` overrides in shipped
  Go code, and runs the test in both modes.
- **Client to KyDrive edge.** KyDrive serves plain HTTP to its proxy. `docs/STRONG-CRYPTO.md:83-95`
  sets protocols and ciphers but no key-exchange groups. Whether the pilot or hosted edge
  negotiates hybrid is **unproven**.
- **Gateway to relay.** This hop is plain HTTP (`deploy/vault-gateway/compose.yml:7`) on an
  internal, isolated bridge.
- **Desktop.** The desktop app uses Go's default TLS with its certificate pin. The TLS pin
  statement is `kydrive-tls-pin/2` `hybridsig` (`internal/api/tls_pin.go:38`), and the
  desktop never downgrades it (`core/api/tlspin.go:30`).
- **Browsers.** Which browser versions offer X25519MLKEM768 by default was not tested here
  (unproven).

### Desktop, extension, recovery

- **Desktop.**
  - Vault keys live in the OS keyring (`docs/NATIVE-VAULT.md` on the branch).
  - Device requests are signed with `hybridsig` by default, with P-256 only as a fallback
    for older servers (`core/api/sign.go:24-56`).
  - The editor window and live co-editing proof are open.
- **Extension.** It is a design only (`2026-11-28-vault-browser-extension.md`): the same
  vault client source compiled into the extension, with keys in extension storage. Its key
  store, device signature and TLS are unproven.
  - The staging browser fixture seals keys under an age scrypt passphrase
    (`cmd/vault-browser/main_js.go:219-223`).
  - The fixture signs device requests with P-256 ECDSA (`main_js.go:55,185`).
- **Recovery key and Key Steward.** Decided (D12), not built. The vault design makes both age
  hybrid identities (`2026-11-22-e2e-vault-design.md:60-63`).

## What we may say, word for word

Use these only once the vault ships in a signed client. Every claim that names a feature
assumes that release.

| Say this | Never say this | Why not |
|---|---|---|
| "In a KyDrive vault, the keys that open your files are wrapped with hybrid post-quantum encryption: ML-KEM-768 combined with X25519." | "KyDrive is quantum-proof." / "Quantum-safe file storage." | Edge TLS, device sign-in and non-vault files are not post-quantum. |
| "Someone who records vault traffic or copies our storage today cannot use a future quantum computer to open your vault files." | "Nothing you store with KyDrive can ever be decrypted." | That holds only for vault content and only while ML-KEM-768 holds. Non-vault files are readable by the server. |
| "Vault changes, file versions and editor sessions are signed with hybrid post-quantum signatures (ML-DSA-65 and Ed25519). A signature counts only when both check out." | "Every signature in KyDrive is post-quantum." | Device-request signatures can be P-256, and CryptPad frames use Ed25519. |
| "Live co-editing is end-to-end encrypted with symmetric encryption that known quantum attacks do not break. Its keys reach your collaborators only through the post-quantum vault." | "CryptPad is post-quantum." / "256-bit quantum-safe co-editing." | CryptPad's own public-key crypto is classical. The room key comes from a 144-bit seed. |
| "Our editing and witness services support hybrid post-quantum TLS with browsers and apps that offer it." | "Every connection uses post-quantum TLS." | The KyDrive edge is unproven, classical clients fall back, and the relay hop has no TLS. |
| "Each document's encryption keys are created on your device and never reach our servers unencrypted." | "End-to-end encrypted Drive" (for all of KyDrive) | Whole-drive E2E is decided, not built. |
| (nothing yet) | "Post-quantum recovery keys." / "FIPS-validated post-quantum crypto." | Recovery is not built. No PQ module validation is claimed or tested. |

## Gaps and the smallest fix for each

1. **KyDrive edge TLS is unproven.** This is the biggest HNDL exposure while non-vault files
   are plaintext on the server. *Fix:*
   - Extend `docs/STRONG-CRYPTO.md`'s edge section to require OpenSSL ≥ 3.5 with
     `ssl_ecdh_curve X25519MLKEM768:X25519:prime256v1;`.
   - Add a rollout check: `openssl s_client -connect <edge>:443` must print
     `Negotiated TLS1.3 group: X25519MLKEM768`.
   - Run that check against the hosted edge.
2. **Classical fallback on the gateway and witness.** A client that offers no hybrid group
   still connects with X25519. *Fix:* the signed desktop app and extension are the only
   intended clients. Once each target build is shown to offer the hybrid group (unproven for
   browsers), set:
   - `ecdhCurve: 'X25519MLKEM768'` in the gateway `createServer`;
   - `pqtls.HybridOnly` on the witness listener and on the desktop's witness and gateway
     clients.
3. **CryptPad's classical features must stay off.** These are curve25519 mailboxes, teams,
   contacts, notifications and account RPC. *Fix:* keep `neverDrive`, the RPC-key patch and
   the gateway allowlist. Any new CryptPad feature (chat, sharing with contacts, teams,
   shared folders) must stay out of the vault profile. Record that in
   `deploy/vault-browser/AGENTS.md` and keep the `netflux.test.mjs` refusals.
4. **The room key rests on a 144-bit seed.** This is adequate; the fix is optional hardening.
   *Fix:* put a random 32-byte CryptPad "password" in the wrapped capability, and pass it to
   `Hash.getSecrets` in the `START` patch. `createEditCryptor2` then hashes password ‖ seed
   (`crypto.js:245-250`). Until then, market co-editing as "symmetric", never "256-bit".
5. **Gateway to relay has no TLS.** *Fix:* keep the relay on the gateway's host and isolated
   bridge, as `compose.yml` does. A relay on another host needs TLS first.
6. **Device-request signatures can be P-256.** This is authentication, not HNDL. *Fix:* the
   extension signs with `hybridsig`, as desktop does. Then the server can refuse new P-256
   enrollments for vault devices.
7. **Recovery key not built.** *Fix:* build it as an age `mlkem768x25519` identity, as
   designed, and add a test like `TestValidRecipientRequiresHybrid` for the recovery copy.

## Standards

- **ML-KEM:** FIPS 203. **ML-DSA:** FIPS 204.
- **Classical curves:** X25519 is RFC 7748; Ed25519 is RFC 8032.
- **HPKE:** RFC 9180, with the PQ KEMs from draft-ietf-hpke-pq. X-Wing is
  draft-connolly-cfrg-xwing-kem.
- **age:** the C2SP age specification (https://c2sp.org/age).
- **TLS:** TLS 1.3 is RFC 8446. The X25519MLKEM768 group is draft-ietf-tls-ecdhe-mlkem;
  `ky-primitives/pqtls` cites it as RFC 10024 (that number is unproven).
- **NaCl `secretbox`:** XSalsa20-Poly1305 (https://nacl.cr.yp.to/secretbox.html).
- **Security categories:** NIST's PQC call for proposals (2016), section 4.A.5.
