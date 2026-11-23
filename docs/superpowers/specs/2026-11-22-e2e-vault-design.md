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
   - Since 2026-11-23 every paid plan, Business included, runs on that service (tenancy owner
     decision 5). So a Business team's vaults have no escrow either.
   - Only an Enterprise install, which is single-tenant, can give its admin one.
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
- **Names.** A name is encrypted by the client under a name key derived from the current
  generation's vault key. The row records that generation (`vault_generation` on
  `drive_files` and `drive_folders`), so a client knows which key opens each name. A rename
  re-encrypts under the current generation.

  `name_hmac` is an HMAC of the name under the vault's **name-index key**: one random key made
  at `create` and kept for the vault's life. Each generation's `drive_vault_keys` row carries it
  as `name_index`, encrypted to that generation, so every member and the escrow key can read it. One
  name has one `name_hmac` in every generation, so the live-name unique index and a
  client's exists-already lookup work across rotations. The cost is that a removed member
  who kept the name-index key can test whether a name they guess exists in the vault
  (Residual risks).

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
  - `drive_workspaces.vault` (boolean). A vault is created by its first member's client, in
    the member's own org, and that member is its first log signer. This is an exception to
    the tenancy spec's D10 (multi-tenant shared workspaces only from suite groups): a suite
    group cannot receive a vault key, so a vault can never come from one. It does not wait
    on that spec's question 15. The org's storage pool bounds it like any workspace;
  - `vault_generation` on `drive_files` and
    `drive_folders`, the generation that sealed each name;
  - `drive_vault_keys(workspace, generation, previous, name_index, created)`: one row per vault
    key generation. `previous` is the generation before, and `name_index` the vault's
    name-index key (Formats, Names), both encrypted to this one (Key generations);
  - `drive_vault_members(workspace, user_id, generation, wrapped_key, added_by, added)`, with
    one row per member, wrapping the current generation;
  - `drive_vault_escrow(workspace, generation, wrapped_key)`, one row per vault, wrapping the
    current generation to the escrow key;
  - `drive_versions.vault_generation`, the generation a vault version was encrypted under,
    and `drive_versions.vault_upload`, the version's signed upload record (Freshness), stored
    and returned with the version;
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

  Removing a member is one `remove` record that also rotates the vault key: it carries
  everything a rotation carries (Key generations). Removal and rotation are therefore one
  commit, with one witness checkpoint, and no head ever names a removal whose newest generation
  the removed member still holds. The server deletes their row at once and stops serving
  them. Files the removed member already downloaded stay readable to them.
- **Key generations.** A rotation, alone or inside a `remove`, makes generation n+1 and
  uploads, in one call:
  - the `drive_vault_keys` row, with generation n encrypted to n+1's recipient as `previous`;
  - a wrap of n+1 for every remaining member;
  - on single-tenant, n+1's escrow wrap;
  - the signed `rotate` or `remove` record for the vault log.

  The server accepts it only when the call names generation n as the current one and
  carries a wrap for every member remaining after the record, and none for a member it
  removes. Two rotations racing then cannot both win. New
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
  - `create` (generation 1). It marks two things, for good:
    - whether the vault is `personal` or `shared`. A client refuses an `add` record in a
      personal vault's log unless an `escrow-access` record comes before it. That record turns
      the vault shared from there on, so its members may then add and remove others;
    - whether it is `escrowable`, which every vault on a single-tenant install is and no vault
      on the multi-tenant service is: a client refuses any `escrow-access` record in a vault
      that is not;
  - `add` (a member, with their key fingerprint);
  - `rotate` (generation n+1), and `remove` (a member, and generation n+1 in the same record);
  - on single-tenant, `escrow` (the escrow key's fingerprint) and `escrow-access` (Escrow).

  Each record names the workspace, its `seq` and the hash of the record before it.
  - A `create`, `rotate` or `remove` record also holds the new generation's public recipient,
    the hash of its whole `drive_vault_keys` row (its encrypted `previous` and `name_index`),
    the full member list with fingerprints and, on single-tenant, the escrow fingerprint.
  - A client refuses a `remove` that names no new generation, and refuses a key row, whether
    loaded normally or in a restore replay, whose hash differs from its record's.
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
  member, or undo a removal, even while every pinned fingerprint stays the same. It can still
  withhold records from a device, which Freshness bounds.

### Freshness

Signatures cannot show a device that a newer record exists. A server that carries every
message can keep a whole group of members on a head another member has moved past, and those
members' own signed statements would only vouch for each other. The removed member would then
read what they upload. Only a party the server cannot control can say which head is current,
so freshness rests on an independent **witness**. It applies to every **witnessed vault**:
every vault except a personal one that is not `escrowable` (below).

- **The witness** is a checkpoint service that clients reach directly, never through the
  KyDrive server. It is operated apart from the KyDrive server, and on the hosted service
  apart from our staff (question 5).
  - It keeps, per vault log, the newest head it has seen: `seq` and hash, under an opaque log
    ID, the hash of the `create` record. It learns no names, members or contents.
  - It signs a checkpoint of that head with the time, and accepts a new head only if it
    extends the old one: `seq` higher, with the client showing the records in between. So it
    never signs a fork or a rollback.
  - The checkpoint format follows C2SP's `tlog-checkpoint` and `tlog-witness` (unverified:
    whether an existing public witness suits, or ours must be run apart).
- **Committing, server first.** A client's `add`, `remove`, `rotate` or `escrow-access`
  goes to the server first, which accepts it only as the next record of its log (the
  acceptance rules above). Only a record the server accepted is sent to the witness. So two
  racing changes are settled by the server, and the witness only ever sees one of them.
  - The change takes effect once the witness holds a checkpoint naming its head. A removal is
    effective for everyone at that checkpoint's time.
  - A record the server holds but the witness has not checkpointed, because its client
    crashed between the two steps, is sent to the witness by the next device that verifies
    it. Any device can do it, since the records carry their authors' signatures and the
    witness checks they extend its head.
  - Until then, the checkpoint names an older head than a device's own, so uploads to the
    vault block (The rule), and the device forwards the records at once.
- **The rule.** Before each upload to a witnessed vault, the client asks the witness for its
  current checkpoint. It reuses one for at most 5 minutes, for a batch of uploads (question 6).
  It encrypts only when that checkpoint names exactly the head it has verified.

  A checkpoint naming a newer head proves records are being withheld. Then, and whenever the
  witness cannot be reached, the client blocks the upload and says it cannot confirm the vault
  is up to date. There is no offline fallback, and it never falls back to the generation it
  has. The rule does not depend on what other members' devices say, so a group the server
  keeps on an old head blocks at its next upload.
- **When a removal takes effect.** At the removal's checkpoint, for every device: a device
  the server withholds it from blocks at its next upload after that, or within the 5-minute
  reuse of a checkpoint it already held.
- **Only a vault that can never gain a member skips the witness:** one whose signed `create`
  record marks it both `personal` and not `escrowable`. That is a personal vault on the
  multi-tenant service. It can never gain a member, so no device can skip the witness by
  holding an out-of-date view.
  - Every other vault uses the witness from creation, even while it has one member: every
    shared vault, and every vault on a single-tenant install, personal ones included.
  - On a single-tenant install, `escrow-access` can add the admin or a successor to a personal
    vault, which may later add and remove members as in any shared vault. Its devices already
    check the witness, so none of them depends on seeing that record before freshness
    applies.
  - The `create` record fixes both marks, so a client's decision never rests on the current
    member list or on whether it has seen a later record.
- **Upload records.** Every version carries a signed upload record, signed by the uploader.
  It holds:
  - the workspace, the file ID and the version's revision;
  - the SHA-256 digest of the version's ciphertext;
  - the head the uploader used, its generation, and the witness checkpoint it held.

  Anyone can encrypt to a vault's public recipient, the server included. So a client accepts
  a downloaded version only when its upload record verifies against a member's pinned key, the
  uploader was a member at that head, and the record's workspace, file, revision and digest
  match what it downloaded. A file the server made up, or one moved from another file or
  vault, is refused.
- **Detection.** A member who
  sees a version encrypted under a generation an earlier committed record revoked is warned
  that records were withheld, and so is the uploader once their device catches up.

### After a capsule restore

A restore brings `drive_vault_log` back to the snapshot. Devices that saw later records then
find a log shorter than their stored head, which they would otherwise refuse as a rollback.

- **Replaying.** A device's client keeps the verified records, and the `drive_vault_keys` rows
  of every generation it has seen, not just the head. When the server's log is a prefix of its
  own, the client replays in one call:
  - its later records;
  - the `drive_vault_keys` row of every later generation, each with its `previous` and
    `name_index`, whose hash is in that generation's `rotate` or `remove` record;
  - a wrap of the newest generation for every member those records name, and on single-tenant
    its escrow wrap.

  The server accepts a replay only for a vault marked restored. It checks that the records
  extend its log in order, that each is signed by a member as the log stood before it, and
  that each key row matches its record's hash. The normal rotation check (current generation
  named, a wrap per member) does not apply to replayed records. Every other device then
  verifies the longer log as usual, and walks `previous` back from the newest generation to
  every older one. A `remove` written after the snapshot is restored this way,
  so a removed member stays removed.
- **Until a member replays,** the vault is read-only on every device that is ahead of the
  server. If no device holds the later records, the vault stays read-only, showing which
  head is missing. There is no re-baseline: accepting a shorter log is exactly what a
  rollback attack needs.
- The restore reset (`ResetAfterRestore`) marks every vault as restored, so clients ask to
  replay instead of reporting an attack. Files uploaded after the snapshot are lost with the
  rest of the restore.

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
| A malicious server | Hand clients a vault key it knows, add a member, or undo a removal | The vault log: clients use only generations and members from records signed by a member as the log stood before, and refuse rollback and forks. In every witnessed vault (all but a multi-tenant personal one), an upload also needs a fresh witness checkpoint naming the client's head (Freshness). |
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
- **A removed member keeps the name-index key,** so they can test whether a name they guess exists in the vault, though not read names written after their removal.
- **A checkpoint is reused for up to 5 minutes** (Freshness). A removal committed in that time reaches a device that already holds a checkpoint only at its next fetch, so a batch of uploads in those minutes can be readable to the removed member.
- **Witnessed vaults depend on the witness:** every shared vault, and on a single-tenant install every vault, personal ones included. While the witness cannot be reached, uploads to them block, and adding or removing members waits. Reading still works.
- **The witness learns each vault log's activity:** when heads move, under an opaque ID.

## Build plan

Each PR is shippable on its own, with vaults off until V3.

1. **V1. This spec, then an outside review of the protocol** (owner's choice of reviewer)
   before any code.
2. **V2. Server and witness:** the witness service (checkpoints, extension check, its own
   deployment), and on the server the schema, including `vault_upload` stored and returned with
   each version, membership and rotation API, the plaintext-path refusals and their
   source scan, quota on ciphertext, audit. Tests:
   - every refused path, membership checks across orgs, and a vault whose items never reach
     the index or a preview;
   - a rotation refused when it names a stale generation or misses the wrap of a member
     remaining after it, and a `remove` refused when it carries a wrap for the member it
     removes;
   - after two rotations, a member added afterwards and the escrow key both open the first
     version (in the client tests, V3);
   - in the client protocol tests (V3), with every member fingerprint unchanged:
     - a server-supplied generation that no member signed;
     - a server-added member;
     - a rolled-back log;
     - a log forked between two members;
     - a committed `remove` the server withholds from a group of devices that
       keep signing statements for the old head: each blocks at its next upload, and is warned
       when the records arrive;
     - a device holding a 1-hour-old checkpoint for the old head, with the witness reachable
       and the removal checkpointed: its upload blocks at once;
     - a shared vault that went from one member to two, then had the second removed and the
       key rotated, all withheld from one device of the first member: that device still asks
       the witness and blocks;
     - an `add` record in a personal vault's log with no `escrow-access` before it, which is
       refused, and an `escrow-access` record in a vault not marked `escrowable`, which is
       refused;
     - on a single-tenant install, a personal vault where escrow added a member who then
       added and removed another, with all of it withheld from one device of the original
       member still holding the personal head: that device asks the witness before its next
       upload and blocks;
     - a downloaded version whose ciphertext digest, or file or revision, differs from its
       signed upload record, which is refused, as is one with no valid upload record;
     - a witness that cannot be reached, which blocks uploads to every witnessed vault: a
       shared vault, and a single-tenant personal vault; a multi-tenant personal vault still
       uploads;
     - a witness asked to sign a head that does not extend its last one, which refuses;
     - two racing `remove` records: the server takes one, the witness checkpoints only that
       one, and the losing client rebuilds on the new head;
     - a client that crashes after the server accepted its record and before the witness
       checkpointed it: the next device that verifies the record forwards it, and uploads
       block until it does;
     - a `remove` with no new generation, which is refused, so an interrupted removal leaves
       the old head, and its generation, in force until the whole `remove` commits;
     - a recovered device given a changed `name_index` while the signed log and generation
       recipient are unchanged, which refuses the key row.

     Each is refused before any content is encrypted to it.
   - a restore to before a `remove`, after which a member's device replays the record and its
     key row, the removed member stays removed, and every member and the escrow key still
     open the oldest version;
   - a restore to before a `remove` when no device holds the later records: the vault stays
     read-only;
   - a name written before a rotation and the same name checked after it: one `name_hmac`, so
     the duplicate is refused;
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

5. **Who runs the witness (Freshness)?** Recommendation: a small witness service of our own,
   deployed on infrastructure and accounts separate from KyDrive's, with its signing key held
   by someone other than KyDrive's operators. An existing public C2SP witness may serve instead
   (unverified). Without an independent witness, witnessed vaults cannot promise timely
   revocation, and the spec would have to say so.
6. **How long a client may reuse a witness checkpoint.** Recommendation: 5 minutes, with no
   offline fallback. Longer reuse means fewer witness requests on a busy sync, but a longer gap
   after a removal.
