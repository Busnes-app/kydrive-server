# KyDrive end-to-end vaults, design

Date: 2026-11-22. Status: draft for the owner's approval. Nothing here is built.

A **vault** is a workspace whose files and names are encrypted on the member's device. The
server stores, versions and syncs the ciphertext, and enforces membership and quota, but it
cannot read what is inside. Everything outside vaults is unchanged: the server keeps reading
plaintext there, so search, previews, Euro-Office and admin recovery keep working.

Owner decisions of 2026-11-22 (fixed, not reopened here):

1. Vaults exist in both modes. On the multi-tenant service they sit beside the per-org data
   keys of the tenancy spec (`2026-11-20-tenancy-design.md`, D14). The vault is what protects
   against the operator; per-org keys do not.
2. **Escrow on single-tenant installs:** the org admin may hold an escrow key that opens
   every vault on the install.
3. **No escrow on the multi-tenant service.** Only staff are administrators there (tenancy
   owner decision 4), so escrow would hand vaults to the operator. Each member has only their
   own recovery key.
4. Orgs cannot bring their own key.

Line references are to KyDrive `origin/master` at `efdd9a4`. "Unverified" marks a claim this
note did not check in code or by running it.

## What a vault gives up

Inside a vault, these do not work, and the server refuses them for vault items:

- content search and the search index;
- previews, thumbnails and text extraction;
- Euro-Office viewing and editing;
- WebDAV, MCP and AI agents;
- zip downloads;
- organisation links, guest links and KyPost peer access;
- server-side handover, and admin recovery other than escrow.

The web app lists a vault and its sizes, but shows no names or contents in v1. It says to
open the vault in the desktop or Android app. A web client is served by the server it would
have to protect against, so it cannot keep this promise until its code can be verified
independently of the server (Residual risks).

## Formats

Everything is an age file (`filippo.io/age` v1.3.2, `go.mod:8`), and every recipient is
age's post-quantum hybrid, ML-KEM-768 with X25519 (`HybridRecipient`, `pq.go`). The desktop
client's core is Go (`kydrive-desktop/core`), so it uses the same library. The Android app
has no age implementation today, so choosing or writing one is part of V4 (unverified:
which Kotlin implementation supports the hybrid recipient).

Signatures are hybrid too: Ed25519 and ML-DSA-65 together, and a signature counts only when
both verify. ML-DSA is Go's `crypto/mldsa`, which the Go 1.27 toolchain the repositories
build with provides. Using it moves the client module's `go` line to 1.27 (unverified:
whether that suits every client build). Android needs an ML-DSA implementation as well (V4).

| Key | Made where | Held by | Opens |
|---|---|---|---|
| User key, an age hybrid identity and a hybrid signing key per user | The user's first device that opens a vault | Each of the user's devices, in the OS keystore. The server holds a copy encrypted to the recovery key. | Vault keys wrapped to the user. The signing half signs the user's vault log records. |
| Recovery key, an age hybrid identity | The same device, shown once as text and a QR code | The user, offline | The server's copy of the user key |
| Vault key, an age hybrid identity per vault | The creating member's device | The server, as one copy wrapped to each member's user key and, on single-tenant, one wrapped to the escrow key | The vault's files and names |
| Escrow key, an age hybrid identity and a hybrid signing key per install (single-tenant only) | The org admin's device | The admin, offline | Every vault key wrapped to it. The signing half signs an escrow access record. |

- **Files.** Each file version is an age file encrypted to the vault key's recipient. The
  client uploads it through the existing upload paths as opaque bytes. The server then seals
  it at rest as it seals any blob (KYB2, `sealed.go:19-23`), under the deployment key or the
  org key, so a purge's crypto-shredding covers vault ciphertext too.
- **Names.** A name is encrypted by the client under a key derived from the vault key. The
  client also sends `name_hmac`, an HMAC of the name under another key derived from the vault
  key.

  For a vault item, the server's name sealing (`names.go:67`, `seal`) takes the client's
  ciphertext as the name and seals it again under its own name key, so the org key's
  crypto-shredding covers vault names too. It stores the client's `name_hmac` unchanged
  instead of computing one, so the live-name unique index (`names.go:19`,
  `drive_live_names`) works without plaintext. `open` returns the client's ciphertext.
- **Sizes.** Quota counts the ciphertext, which is what the server stores.

## Server changes

- **Schema** (both backends):
  - `drive_user_keys(user_id, public_key, signing_key, fingerprint, recovery_copy, created)`,
    the fingerprint covering both public keys;
  - `drive_workspaces.vault` (boolean);
  - `drive_vault_keys(workspace, generation, previous, created)`: one row per vault key
    generation. `previous` is the generation before, encrypted to this one (Key generations);
  - `drive_vault_members(workspace, user_id, generation, wrapped_key, added_by, added)`, with
    one row per member, wrapping the current generation;
  - `drive_vault_escrow(workspace, generation, wrapped_key)`, one row per vault, wrapping the
    current generation to the escrow key;
  - `drive_versions.vault_generation`, the generation a vault version was encrypted under;
  - `drive_vault_log(workspace, seq, record, signer, signature)`, the vault log (The vault
    log), which the server stores but cannot forge;
  - `drive_escrow_key(public_key, signing_key, fingerprint, created, created_by)`,
    single-tenant only.

  On the multi-tenant service every table is org-owned (tenancy D4).
- **Rank.** Inside a vault, a member's rank comes only from their `drive_vault_members` row.
  Group grants, shares and links never apply, because a group or a link holder cannot
  receive the vault key. Members are individual accounts.
- **Plaintext paths refuse vault items.** One check sits in each path that reads content:
  - `drive_preview.go`, thumbnails and text extraction;
  - the indexer;
  - `drive_zip.go`;
  - the WebDAV and MCP handlers;
  - editor sessions;
  - link and guest creation and opening;
  - KyPost peer paths;
  - `handover.go`;
  - name sealing (`names.go`), which stores the client's ciphertext and `name_hmac` as above.

  A source scan in the style of `TestOrgSQLNamesOrgID` fails when a new content path omits
  the check.
- **Membership.** Adding a member is a call from an existing member's client. It carries the
  vault key wrapped to the new member's public key and a signed `add` record for the vault log.
  The server checks that the caller is a member and the target is an active account (in the
  caller's org on the multi-tenant service). It cannot check that the wrap is correct. Its
  checks keep the rows consistent; the log, which clients verify, is what makes membership
  trustworthy.

  Removing a member deletes their row at once, and the server stops serving them. A
  remaining member's client then rotates the vault key (Key generations). Files the removed
  member already downloaded stay readable to them.
- **Key generations.** A rotation makes generation n+1 and uploads, in one call:
  - the `drive_vault_keys` row, with generation n encrypted to n+1's recipient as `previous`;
  - a wrap of n+1 for every remaining member;
  - on single-tenant, n+1's escrow wrap;
  - the signed `rotate` record for the vault log.

  The server accepts it only when the call names generation n as the current one and
  carries a wrap for every current member. Two rotations racing then cannot both win. New
  versions record n+1.

  A member or the escrow key holds only the current generation. They reach every older one
  by walking `previous` back, so every version stays readable, whatever generation it
  records. A member added later gets the current generation and so reads the whole history.
  A removed member holds no wrap past their last generation.
- **Audit.** Vault creation, member changes, escrow registration and every escrow access are
  audited, with no names.

## The vault log

The server is an adversary here, so its checks above only stop races. Clients trust nothing
about a vault's members or keys unless the vault log says so.

- **Records.** The log is a hash-chained list of records, each signed by its author's
  signing key:
  - `create` (generation 1);
  - `add` and `remove` (a member, with their key fingerprint);
  - `rotate` (generation n+1);
  - on single-tenant, `escrow` (the escrow key's fingerprint) and `escrow-access` (Escrow).

  Each record names the workspace, its `seq` and the hash of the record before it.
  - A `create` or `rotate` record also holds the new generation's public recipient, the hash
    of its encrypted `previous`, the full member list with fingerprints and, on single-tenant,
    the escrow fingerprint.
  - Every record but `escrow-access` must be signed by a member of the vault as the log stood
    before it. In v1 every member may add, remove and rotate.
- **What a client checks** before it uses a vault:
  - it walks the log from `create`, or from the last record it trusted;
  - it verifies every signature against the keys it has pinned;
  - it checks that each signer was a member at that point.
- **What a client refuses:**
  - a record that fails any of those checks;
  - a log shorter than, or forked from, the head it stored;
  - a vault key whose public recipient differs from the one in its verified `create` or
    `rotate` record.
- **What a client does with it:**
  - It encrypts new versions only to the newest verified generation.
  - It wraps keys only to the members, and the escrow key, that the verified log names.
  - It keeps the verified head (`seq` and hash) for each vault on the device, so a server
    that rolls the log back, or shows two members two different logs, is caught.
- **What this stops.** A malicious server cannot slip in a generation it knows, add a
  member, or undo a removal, even while every pinned fingerprint stays the same. It can only
  withhold records, which clients see as a vault that stops updating.

## Devices and recovery

- **A new device** gets the user key in one of two ways:
  - from a linked device, by a QR code that carries a one-time key, with only ciphertext
    passing through the server;
  - from the server's recovery copy, opened with the recovery key.
- **Losing every device and the recovery key loses the user's vault access.** On the
  multi-tenant service it loses the vault itself, unless another member holds it. The client
  says so, in those words, when the vault is created.
- **Recovery-key rotation** re-encrypts the server's recovery copy from a linked device.

## Escrow (single-tenant only)

- The org admin generates the escrow key in the desktop app. The private key is exported
  once (file, text and QR code) and never sent to the server. Only the public key is
  uploaded, after step-up, and its fingerprint is shown to the admin and in every client.
- While an escrow key exists, clients also wrap every vault key generation to it. The server
  refuses a new vault or rotation that carries no escrow wrap for the new generation. It can check that the wrap is present,
  not that it is correct (Residual risks).
- A vault made before the escrow key gets its escrow wrap of the current generation the
  next time a member's client opens it. The admin screen lists the vaults with no
  `drive_vault_escrow` row, or one behind the current generation.
- **Using escrow.** It takes two server events, and both are audited:
  1. After step-up, the admin calls the vault's escrow-access route. The server returns the
     escrow wrap and the vault log, and records the call as the escrow access.
  2. The admin's desktop app opens the vault key locally, and then appends an
     `escrow-access` record signed by the escrow key. That record adds the admin, or a
     successor for a leaver's vault, as a member, wrapping the vault key to them.

  From then on that person reads through the ordinary member routes. A non-member, admin or
  not, has no route to a vault's ciphertext. The record is in the log, so every member's
  client shows that escrow was used and by whom. The server cannot see the local unwrap
  itself; the route call and the record are what it audits.
- The multi-tenant service refuses to register an escrow key.

## Key authenticity

The server hands out members' public keys and the escrow key, so a malicious server could
substitute its own and receive the next vault key wrapped to it. In v1:

- Clients pin each public key the first time they see it, and warn loudly when it changes.
- Each member's fingerprint can be compared in person or over another channel, the way
  messaging apps show safety numbers.
- The escrow fingerprint is pinned at first sight and shown in every client.

Pinning covers the members' keys, and the vault log (above) covers everything signed with
them. A server can still substitute a key the first time a client sees it.

Key transparency (a signed, append-only log of public keys) is a later step, not v1.

## Threat model

| Attacker | Goal | Defence |
|---|---|---|
| Operator, staff, or anyone with the database, the storage and the deployment key | Read vault files or names | Only ciphertext and wrapped keys are on the server. No escrow on the multi-tenant service. |
| The same, serving altered web code | Steal keys through the browser | The web app never handles vault keys or contents in v1. |
| A malicious server at the moment a member is added | Get the vault key | Pinned keys, change warnings and fingerprint comparison. Residual for members who never compare. |
| A malicious server | Hand clients a vault key it knows, add a member, or undo a removal | The vault log: clients use only generations and members from records signed by a member as the log stood before, and refuse rollback and forks. |
| A removed member | Read new versions | Their row is deleted at once, and the vault key moves to a new generation they hold no wrap for (Key generations). They keep what they downloaded. |
| A member's modified client | Leave escrow out | The server checks that an escrow wrap is present, not that it opens. Residual. |
| Holder of the escrow key (single-tenant) | Read every vault | Intended: the org admin's recovery. Every use is audited and shown to members. |
| Thief of one unlocked device | Read the vaults that device opens | The OS keystore, and device revocation, which deletes the device's access. Residual while it is unlocked. |

## Residual risks

- **Metadata stays visible:** sizes, counts, times, folder structure, membership and access
  patterns.
- **First-contact key substitution** by a malicious server, for members who never compare
  fingerprints.
- **No web access** to vault contents in v1.
- **Lost keys mean lost data** on the multi-tenant service. There is no one to recover it.
- **Escrow is honest-client only.** A modified client can create a vault the escrow key
  cannot open.
- **Old versions stay readable to a removed member** if they downloaded them.
- **A server can withhold vault log records.** Clients then see a vault that stops updating, never a forged one.

## Build plan

Each PR is shippable on its own, with vaults off until V3.

1. **V1. This spec, then an outside review of the protocol** (owner's choice of reviewer)
   before any code.
2. **V2. Server:** schema, membership and rotation API, the plaintext-path refusals and their
   source scan, quota on ciphertext, audit. Tests:
   - every refused path, membership checks across orgs, and a vault whose items never reach
     the index or a preview;
   - a rotation refused when it names a stale generation or misses a current member's wrap;
   - after two rotations, a member added afterwards and the escrow key both open the first
     version (in the client tests, V3);
   - in the client protocol tests (V3), with every member fingerprint unchanged:
     - a server-supplied generation that no member signed;
     - a server-added member;
     - a rolled-back log;
     - a log forked between two members.

     Each is refused before any content is encrypted to it.
3. **V3. Desktop app:** user and recovery keys, vault create, sync, membership, rotation and
   fingerprint pinning, in `kydrive-desktop/core`. Vaults are turned on here, desktop only.
4. **V4. Android:** the same, after the age library choice.
5. **V5. Escrow** for single-tenant installs: generation, the presence check, back-filling
   older vaults, admin open and member add, audit.
6. **V6. Web:** vault listing and the "open in the app" screen.

There is no KyDrive iOS app today (the organisation's repositories have none), so iOS waits
for one.

## Questions for the owner

1. **Who may create vaults?** Recommendation: any member, with a server setting an admin can
   turn off on a single-tenant install.
2. **Shared vaults in v1, or personal only?** Recommendation: both. Members are individual
   accounts only.
3. **Fingerprint comparison when adding a member: required or offered?** Recommendation:
   offered, with a loud warning when a pinned key changes. Required comparison is safer, but
   most households would skip vaults.
4. **The outside protocol review before V2.** Recommendation: yes, for the key formats and the
   device-linking flow.
