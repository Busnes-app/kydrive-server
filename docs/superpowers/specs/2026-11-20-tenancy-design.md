# KyDrive tenancy: single-tenant and multi-tenant (SaaS), design

Date: 2026-11-20. Status: design approved, with the owner's answers to its questions (2026-11-20). Amended 2026-11-22 by owner decision: multi-tenant requires PostgreSQL and runs as several replicas across hosts (D2, D13, build steps R1 to R4). The owner approved that amendment (#162), and R1 is built (#168, #169). Amended again 2026-11-22 by owner decision: per-org data keys in multi-tenant mode (D14, step K), and an end-to-end vault in its own spec (D15). This second amendment awaits the owner's approval. Amended 2026-11-23 by owner decision: every paid plan, Business included, is an org on the shared install, and only Enterprise gets a dedicated install (owner decision 5, D16).

KyDrive today serves one organisation per install. This note adds a second mode to the same
binary: **multi-tenant**, one shared install for every paid plan on the hosted service
(Personal, Couple, Family and Business), with data separated by organisation (org). **Single-tenant** is today's behaviour,
and it does not change for existing installs.

Owner decisions (fixed, not reopened here):

1. One binary, both modes. Single-tenant stays unchanged on upgrade. Multi-tenant is a mode.
2. Organisations under one sign-in: one KyIdentity issuer for every customer. Each customer
   account is an org (decision 5), and tokens carry an opaque `org_id`. KyDrive runs as
   one shared multi-tenant install. Per-customer realms were rejected.
3. One org per account. There is no org switcher.
4. No administrators inside customer orgs. Only our staff are administrators, and customers
   are members. Single-tenant installs keep their own administrators.
5. **Every paid account is an org** (2026-11-23, replacing "businesses get their own
   install"). Personal is an org of 1 member, Couple 2, Family up to 6, and Business N seats
   for small teams. All four run on the shared multi-tenant, multi-server install. Only
   Enterprise gets a dedicated install, which is single-tenant (SQLite is fine there).
6. Stripe lives only in a separate billing service, built later. KyDrive has no billing code.

Line references are to KyDrive `origin/master` at `cb492e0`. **T** is KyIdentity's tenancy
spec, `docs/superpowers/specs/2026-11-19-tenancy-design.md` at `origin/master` `2e304a4`.
"Unverified" marks a claim this note did not check in code or by running it. A bare file
name is the only file of that name in the repository, except `store.go`, `devices.go`,
`guest.go` and `handover.go`, which mean the `internal/drive` file unless a path is given.
`AGENTS.md` alone is the root one. The 2026-11-22 amendment's references are to `origin/master`
at `949537b`, and "the replica pattern" is ky-server-base `docs/REPLICAS.md`.

KyIdentity's step 3 (`org_id` in tokens, userinfo and logout tokens, signed webhooks and
`/api/suite/orgs`, T "Phased build plan") has not landed. Nothing below can be built past
PR 3 until it does.

## Summary

| | Single-tenant (self-hosted, Enterprise) | Multi-tenant (hosted: Personal, Couple, Family, Business) |
|---|---|---|
| Mode | `KYDRIVE_TENANCY=single` (default) | `KYDRIVE_TENANCY=multi`, fresh database only |
| Database | SQLite (default), one writer (root `AGENTS.md`, "One SQLite writer"), unchanged; or PostgreSQL, moved to with `migrate-postgres` (R1) | PostgreSQL only (D2) |
| Deployment | one instance | several replicas across hosts: one database, shared file storage, one elected worker (D2, D13) |
| Orgs | one default org, never named in tokens (T D4) | one per customer, mirrored from `GET /api/suite/orgs` (D3) |
| Accounts | SCIM from KyIdentity, sign-in links by `sub` (`docs/PERMISSIONS.md:9,13`) | created at first sign-in from the verified token, kept current by the signed webhook (D5) |
| Administrators | local recovery admin and directory accounts given the role (`docs/PERMISSIONS.md:32`) | staff accounts only (D9) |
| Shared workspaces | created by administrators or from suite groups | from suite groups only (D10) |
| Organisation links | anyone signed in to the install | anyone signed in to **the link's org** (D6) |
| Guest links | unchanged | unchanged: the only deliberate cross-org path (D6) |
| Quota | per workspace (`drive_workspaces.quota`, `store.go:98`) | one pool per org, `storage_bytes` from `/api/suite/orgs` (D7) |
| Blob layout | `<data>/blobs/<uuid>` (`drive_handlers.go:371`) | the same flat names, in shared storage outside every replica's data directory (D8, D13) |

## Decisions

| # | Decision | Why |
|---|---|---|
| D1 | `KYDRIVE_TENANCY=single` (default) or `multi`, recorded in `server_settings` at first start. A later start with the other value refuses to start. `multi` is accepted only on an empty database. Converting a single-tenant install is out of scope. | Install-time, as for KyIdentity (T D1). An empty database avoids rewriting existing rows, and lets the multi-only DDL (see Triggers, "Mode-only DDL") be created on empty tables. |
| D2 | **Multi-tenant requires PostgreSQL and runs as several replicas across hosts** (amended 2026-11-22). SQLite stays single-tenant only, unchanged byte for byte. Startup refuses `KYDRIVE_TENANCY=multi` unless the install is replica-capable (D13). PostgreSQL also serves single-tenant installs, which can move to it with `migrate-postgres` (R1). Multi-tenant still starts only on an empty database (D1). | Owner decisions of 2026-11-22: KyDrive is in scope for distributed hosting, and multi-tenant on one SQLite writer and one instance contradicts that. KyIdentity (T D2), KyCalendar (its D2) and KyPost (K2) already require PostgreSQL for multi-tenant. Today the drive store opens only on SQLite (`internal/store/sqlstore.go:54-60`; root `AGENTS.md:90`), so R1 to R4 are new work. The original D2 (SQLite, one writer, one instance) is withdrawn. |
| D3 | KyDrive keeps a local org registry, `drive_orgs(id, status, revision, enabled, storage_bytes, purged, seeded, retention_after)`, filled from `GET /api/suite/orgs` (T "Suite directory"), with one exception: the staff org's row, seeded from `KYDRIVE_STAFF_ORG` and never overwritten by the poll (Local accounts). Polled about every 60 s with `since=<revision>`. Single-tenant has one row, the default org, with the fixed ID T uses (`00000000-0000-0000-0000-000000000001`), `active`, unlimited. A multi-tenant database has no default org row (see Triggers). | The org's status and quota must be readable inside the same SQL statement that authorizes a request. The default org gives single-tenant one code path. |
| D4 | Every org-owned table gets `org_id TEXT NOT NULL`. Triggers with the same names on both backends make it immutable, require it to exist in `drive_orgs`, and refuse a row that links two orgs (shares, grants, group members, link openers, personal-workspace owners). Isolation is enforced in the application, and the triggers are a second layer. | T D3 makes the same choice. On PostgreSQL `org_id` is also a real foreign key to `drive_orgs(id)` (R1). Both backends run the same statements, and every access decision already goes through `rankSQL` and `itemRankSQL` (`access.go:17,33`), so adding one org predicate there covers most paths. The triggers catch a path that forgets it. |
| D5 | In multi-tenant mode, inbound SCIM is off (`/scim/v2` answers 404). A local account is created at first sign-in from the verified ID token's `sub` and `org_id`, so auto-provisioning is forced on. The signed webhook keeps accounts current. Groups come only from suite groups. | KyIdentity turns generic outbound SCIM targets off in multi-tenant v1 (T "Per-org features"), and SCIM bodies carry no org. Members of every plan, Business included, are added through KyIdentity (the billing portal or staff, within the org's seats), so first sign-in creates every account KyDrive needs. A business that syncs its own directory is Enterprise. If T's business-org amendment turns per-org SCIM on for Business, this decision is revisited (unverified: that amendment has not landed). |
| D6 | Organisation links (`scope='org'`) become org-scoped: only an active member of the link's org can open one. Guest links (`scope='guest'`) stay as they are: they are the only deliberate way for content to leave an org. | Today an organisation link gives "any active signed-in user" its role (`links.go:14-16`). On a shared install that means every customer. |
| D7 | One storage pool per org: the sum of every workspace's version bytes and viewing copies must stay within `quotas.storage_bytes` from `/api/suite/orgs`. It is checked in `fits` (`store.go:682`), the one function every byte-adding path already calls. Per-workspace `quota` is not enforced in multi-tenant mode. A missing or unreadable quota refuses writes. | A plan's storage is one allowance shared by the org's members (pooled per org: a recommendation for Family, not a decision; D16 keeps a per-member allowance possible). Every path that adds bytes already ends in `fits` (see Quotas). Failing closed means a polling bug cannot hand out unlimited storage. |
| D8 | Blob storage stays flat (`blobs/<uuid>`: under `<data>` on SQLite, under the shared storage root in multi-tenant mode, D13). A trigger on `drive_versions` refuses a version that names a blob already named by a version in another org. | Blob IDs are fresh random UUIDs (`blobs.go:21`), with no content addressing, so there is no dedup across orgs. A blob is only reachable through a version row. The one path that shares a blob, copy (`move.go:131-132`), already needs ranks in both workspaces. A per-org directory would add an org argument to every `BlobPath` call and protect nothing the trigger doesn't. |
| D9 | In multi-tenant mode, the store refuses `role='admin'` for any account outside the staff org, which is `KYDRIVE_STAFF_ORG` recorded at first start (question 2). Admin routes are classed as `platform`, `org` or `refused` (see Admin surface). | Owner decision 4, and T D8. |
| D10 | In multi-tenant mode, shared workspaces exist only as suite group workspaces (`suitegroups.go:57`). Creating, renaming, deleting and re-quotaing a shared workspace through the admin API answers 404. | Customers have no administrator to create workspaces (owner decision 4). Suite groups give every plan shared folders, a Business team's included, and staff should not curate customer folders. Whether a member may create a shared workspace is question 15. |
| D11 | An org whose `drive_orgs.status` is not `active`, or whose `enabled` is false, is served nothing, by any credential. Its data is kept, and retention, version pruning and workspace purge pause for it. Only `deleted` triggers an org-wide purge. The one other purge, a leaver's unshared files 90 days after a verified `user.deleted` (A member leaves), also pauses while the org is not `active`. | T downstream contract point 5. A trash age must not delete a suspended customer's files while they settle a bill. |
| D12 | The org comes from exactly three places: the verified ID token (`org_id`), a `syncauth`-verified webhook body (`org_id`), and a suite directory answer. One named exception: the local bootstrap and recovery administrators take the staff org from operator configuration (`KYDRIVE_STAFF_ORG`), since no token exists when they are created. Every KyDrive-issued credential (session, app password, agent token, device token, service token, guest link, upload ticket, editor token) takes its org from its user or workspace row, never from the request. | T downstream contract point 4. |
| D13 | A multi-tenant install is replica-capable or it does not start (2026-11-22). Startup in multi mode requires PostgreSQL; shared file storage for blobs and every derived file (R2); the deployment key from `KY_ENCRYPTION_KEY` and the device identity key from `KYDRIVE_IDENTITY_KEY_FILE`, both outside the data directory and never minted there; and the replica plumbing of R3 and R4. The refusal names each missing piece. Each replica keeps its own data directory, never shared. | The replica pattern, "Product rollout contract" (f): state outside the database makes a product not replica-ready until it has a shared-storage contract. Today, without `KY_ENCRYPTION_KEY`, a fresh data directory mints its own key (`config.go:258-271`, `keyfile.LoadOrCreate`), so two replicas would hold two keys and could not read each other's sealed blobs. The identity key is read from the data directory unless `KYDRIVE_IDENTITY_KEY_FILE` names another file (`config.go:182-194`). |
| D14 | **Per-org data keys, multi-tenant only** (2026-11-22). Each org has one random 32-byte data key, stored only wrapped under a key-encryption key (KEK) derived from the deployment key. The server unwraps it at runtime. Everything KyDrive seals for the org is sealed under keys derived from it: blob and cache file keys, file and folder names, and search keys. Deleting the org destroys its wrapped key (crypto-shredding). Orgs cannot bring their own key. Single-tenant installs keep today's deployment-key sealing, unchanged byte for byte. | Owner decisions of 2026-11-22: the KEK is the deployment key, not a KyIdentity-held key, and there is no bring-your-own-key. Today every key is derived from the one deployment key (`sealed.go:75`, `names.go:26,30`, `search/index.go:61-73`). A multi-tenant database always starts empty (D1), so nothing is re-sealed. See "Per-org data keys". |
| D15 | **End-to-end vaults** are specified separately, in `2026-11-22-e2e-vault-design.md`: client-side encryption the server cannot read, in both modes. On a single-tenant install the org admin may hold an escrow key. The multi-tenant service has no escrow, because only staff are administrators there (owner decision 4). | Owner decisions of 2026-11-22. A vault trades search, previews, editing and admin recovery for content the platform cannot read. Per-org keys (D14) protect against storage loss and bugs, not against the operator. |
| D16 | **Plans are orgs** (2026-11-23). Personal, Couple, Family and Business are all orgs on the shared install. KyDrive treats every org the same: the org's size limit is KyIdentity's (`max_seats`, T), and KyDrive enforces no seat count of its own. The storage limit is the org's `quotas.storage_bytes` (D7), set per org by the billing service, so each plan's allowance is a per-org value, not a KyDrive constant. A dedicated Enterprise install runs single-tenant. | Owner decision 5. KyIdentity is adding a `business` org kind and seat caps in parallel (folder `kyidentity-business-orgs`; unverified until it lands). KyDrive needs the kind only for UI copy (question 16), which would add it to `drive_orgs` from the poll. A pooled Family allowance is a recommendation, not a decision. D7 already pools per org; a per-member allowance would add one per-org quota key, `member_storage_bytes`, that `fits` would also check against the member's My files. |

## KyDrive's half of the downstream contract

T "Tokens and suite contracts (multi-tenant)", **Downstream contract**, point by point:

| # | T requires | KyDrive does | Where today | Proven by |
|---|---|---|---|---|
| 1 | Refuse a token without `org_id` | The sign-on callback refuses an ID token with no `org_id` (403 "User account not provisioned", like a refused provision today), before any account lookup. So do the back-channel logout receiver (400 `invalid_request`) and the webhook (400). Single-tenant ignores the claim. | `claimsFromIDToken` reads no org (`oauth.go:145-169`); the callback looks up `sub` only (`sso_handlers.go:119`) | Callback, logout and webhook tests with the claim absent, in both modes |
| 2 | Key every row by `org_id` | Every table in the inventory marked **O** gets `org_id` (D4). Derived tables (**D**) reach it through a foreign key. Platform tables (**P**) hold no customer data. | No `org_id` anywhere (`grep -rn org_id internal` finds none) | `TestEveryTableIsClassified`, `TestOrgOwnedTablesHaveOrgID` (see Isolation) |
| 3 | Bind a local account to `(sub, org_id)`; refuse a known `sub` with a different `org_id` | `users.org_id` is set once, at account creation, from the token or webhook, and is immutable (trigger). A partial unique index on `(sso_provider, sso_subject)` for `kysignon` accounts, multi-tenant only and created by the mode-only DDL (see Triggers), makes `sub` resolve to one account. Callback, webhook, logout, suite group members and KyPost all compare the incoming `org_id` with the account's, and refuse a mismatch with the same answer as an unknown subject, plus an audit row `tenancy.org_mismatch`. | `DirectorySubject` resolves by subject alone (`sso_handlers.go:119`, `kysignon.go:92`, `kypost.go:334`, `suitegroups.go:215`); `idx_users_sso` is not unique (`migrations.go:50`) | A test per entry point: a known `sub` arrives with org B's ID |
| 4 | Take the org only from a verified token, a signed webhook or a suite answer | D12, including its one exception (local staff administrators, from operator configuration). No route reads an org from a URL, form, cookie or header, except staff's org selection on `org` admin routes (see Admin surface), which applies only after the caller is proven staff. | n/a | `TestOrgSQLNamesOrgID`'s allowlist, plus review: the only `X-Ky-Org` reader is the admin middleware |
| 5 | Stop serving an org whose status is not `active`; keep its data unless `deleted` | D11 and Lifecycle. Every principal resolution reads `drive_orgs` in the same statement as the user's status. | Principals check `users.status='active'` only (`access.go:20,22,43`, `apppasswords.go:223,248`, `devices.go:217,249`, `session.go:108`) | `TestSuspendedOrgServesNothing`: every credential type, before and after a status change |
| 6 | Enforce its own quotas from `/api/suite/orgs` | D7: `storage_bytes`, in `fits`. | `fits` checks one workspace (`store.go:682-692`) | Quota tests on every byte-adding path (see Quotas) |

## Inventory

Classes:

- **O** (org-owned): gets `org_id`.
- **D** (derived): scoped through the parent foreign key named, with no column of its own.
- **P** (platform): global, holds no customer content.
- **X**: outside the main database, scoped as described.

The rule for **D** is a real foreign key to an **O** or **D** row. A table whose parent column
has no foreign key gets its own `org_id`, even when every reader joins the parent today: that
is cheaper to prove than every join (T makes the same call for `sync_delivery_attempts`).

### Main database (`<data>/ky_server.db`), base tables

| Table | Defined | Class | Reasoning |
|---|---|---|---|
| `schema_migrations` | `migrations.go:373` | P | Schema version |
| `users` | `migrations.go:28` | O | Root of each org's accounts. `username` stays globally unique (`:30`, and `idx_users_username_lower` at `:251`): KyIdentity makes username equal the email, which is globally unique (T D5). |
| `sessions` | `migrations.go:52` | D | `user_id` FK |
| `groups` | `migrations.go:77` | O | Suite groups and, in single-tenant, SCIM groups. `display_name` is globally `UNIQUE` (`:79`). Multi-tenant creates only suite groups, named `suite-group:<kyid>` (`suitegroups.go:76`), and KyIdentity group IDs are unique, so the global unique stays and no table rebuild is needed. |
| `group_members` | `migrations.go:85` | D | `group_id` and `user_id` FKs. A trigger refuses a row whose user and group are in different orgs. |
| `audit_records` | `migrations.go:91` | O | `org_id` nullable: NULL is a platform event (as T's `audit_events`). |
| `server_settings` | `migrations.go:102` | P | Platform settings, `tenancy_mode` and `staff_org` included |
| `mfa_challenges` | `migrations.go:194` | D | `user_id` FK. Unused since local MFA was removed (`internal/store/AGENTS.md`). |
| `sso_logouts` | `migrations.go:263` | P | Fence by `sid`. One issuer, so `sid` is global. |
| `sso_subject_logouts` | `migrations.go:267` | P | Fence by provider and subject. Subjects are global at one issuer. |
| `device_pairings` | `migrations.go:63` | n/a | Dropped by migration 6 (`migrations.go:231-240`) |

### Main database, drive tables

| Table | Defined | Class | Reasoning |
|---|---|---|---|
| `drive_schema` | `schema.go:243` | P | Drive schema version |
| `drive_key` | `names.go:44` | P | Deployment key fingerprint |
| `drive_name_scrub` | `names.go:115` | P | Migration marker |
| `drive_workspaces` | `store.go:98` | O | Root of every org's content |
| `drive_personal_workspaces` | `store.go:99` | D | `workspace` FK. `owner` has no FK, so a trigger requires the owner's org to equal the workspace's. |
| `drive_grants` | `store.go:100` | D | `workspace` and `group_id` FKs. A trigger requires one org. |
| `drive_folders` | `store.go:101`, rebuilt `schema.go:19` | D | `workspace` FK |
| `drive_files` | `store.go:102` | D | `workspace` FK |
| `drive_versions` | `store.go:104` | D | `file_id` FK. D8's blob trigger lives here. |
| `drive_events` | `store.go:105` | O | No FK, and events outlive their user. `org_id` nullable: NULL for `system` events that touch no org. |
| `drive_service_tokens` | `store.go:97` | O | `user_id` and `workspace` have no FK |
| `drive_editor_sessions` | `store.go:106` | D | `file_id` FK |
| `drive_editor_documents` | `store.go:107` | O | `file_id` has no FK. Euro-Office finds it by document key. |
| `drive_editor_saves` | `store.go:108` | O | `key` has no FK |
| `drive_blob_retire` | `schema.go:30` | P | Maintenance queue of blob IDs: no names, no content |
| `drive_upload_sessions` | `schema.go:43` | D | `workspace` FK |
| `drive_upload_segments` | `schema.go:45` | D | `session` FK |
| `drive_app_passwords` | `schema.go:47`, scopes `:71-73` | O | `user_id` has no FK. Holds WebDAV passwords and agent tokens. |
| `drive_shares` | `schema.go:51` | D | `workspace` FK. A trigger requires a `user` or `group` principal in the workspace's org. |
| `drive_links` | `schema.go:56` | D | `workspace` FK |
| `drive_link_users` | `schema.go:58` | D | `link` FK. A trigger requires the opener's org to equal the link's (D6, at the database level). |
| `drive_changes` | `schema.go:63` | O | `workspace` has no FK |
| `drive_change_floor` | `schema.go:65` | O | `workspace` has no FK |
| `drive_devices` | `schema.go:66`, key `:130` | O | `user_id` has no FK |
| `drive_device_codes` | `schema.go:68` | O | `user_id` has no FK |
| `drive_blob_kyb1` | `schema.go:75` | P | Maintenance queue of blob IDs |
| `drive_peer_settings` | `schema.go:110` | O | `user_id` has no FK |
| `drive_peer_folders` | `schema.go:111` | O | `owner` has no FK |
| `drive_peer_tickets` | `schema.go:112` | O | `owner` has no FK |
| `drive_peer_picks` | `schema.go:116` | O | `owner` has no FK |
| `drive_peer_guest` | `schema.go:118` | O | `user_id` has no FK |
| `drive_view_copies` | `schema.go:122` | O | `user_id` and `workspace` have no FK |
| `drive_suite_groups` | `schema.go:127` | D | `workspace` and `grp` FKs. A trigger requires one org. |

Tables this note adds:

| Table | Class | Reasoning |
|---|---|---|
| `drive_orgs` | P | The tenant registry (D3), target of every `org_id`. A deleted org's row stays as a tombstone with `status='deleted'` and `purged` set, so its ID is never reused (T "Data model"). |

### Search index (`<data>/search/index.db`, `search_index.go:61`)

Its own SQLite file (`search/index.go:37-40`), never backed up and always rebuildable (search
`AGENTS.md`). Class **X**: derived and mixed across orgs.

- `docs` rows carry only `Ref`, a keyed HMAC of blob and kind. `postings` hold term hashes.
  `queue` holds file IDs. `meta` is platform.
- Isolation comes from the caller. `Match` scores only the refs the caller passes, which are
  the caller's readable files (search `AGENTS.md`, `Match`), so another org's documents
  neither match nor shape the ranking. Org scoping of readable files (Isolation) is enough.
- The prefix vocabulary (`<data>/search/vocab`) is drive-wide, and nothing but a full
  rebuild removes a word from it (search `AGENTS.md`, "Vocabulary"). Expansion keeps only
  words present in the caller's refs, so the answer is scoped. But a deleted org's words stay
  in the sealed vocabulary until a rebuild, so an org purge requests one (see Lifecycle).
- **Multi-tenant (D2, R2).** The index cannot stay a file: SQLite on a network filesystem is
  not a replication mode (the replica pattern, first section), and each replica's data
  directory is its own. R2 moves `docs`, `postings`, `queue`, `meta` and the sealed vocabulary
  into PostgreSQL tables, which the catalogue test classes **X**. Only the elected worker's
  indexer writes them (R3). `Match` and its caller-scoped refs are unchanged, so the
  isolation argument above still holds.

### Filesystem under `<KY_DATA_DIR>`

| Path | Defined | Class | Reasoning |
|---|---|---|---|
| `blobs/<uuid>` | `drive_handlers.go:362`, `blobs.go:20-36` | O, through `drive_versions.blob` | Sealed under the deployment key, bound to the blob ID (`blobs.go:22`). Named by a random UUID, never by digest. |
| `blobs/.upload-*` | `blobs.go:48` | transient | Staging before rename. The orphan sweep removes them after an hour (`blobs.go:113-148`). |
| `staging/<session>/…` | `drive_uploads.go:17`, `uploads.go:159-206` | D, through `drive_upload_sessions` | Resumable upload chunks |
| `view/<blob>` | `kypost_view.go:27`, `viewcopies.go:158-169` | O, through `drive_view_copies` | KyPost viewing copies |
| `thumbs/<blob>` | `drive_preview.go:32`, `thumbs.go:294-306` | derived | Named by blob, never digest. Swept once no version names the blob (`drive_retention.go:91`). |
| `text/<blob>` | `document_text.go:35` | derived | Sealed extracted text. Swept like thumbnails (`drive_retention.go:94`). |
| `search/index.db`, `search/vocab` | `search_index.go:61` | X | Above |
| `encryption.key`, `identity.key` | `restorereset.go:67`, `config.go:89` | P | Deployment and device-signing keys |
| `installer/<setup UUID>/` | root `AGENTS.md` | P | Installer channel, single-tenant only (as in T) |
| `drill/` | `drill.go:216` | P | Restore-drill residue |
| `initial-admin-password` | `main.go:351` | P | Bootstrap |
| Backups: capsules and the Restic bulk repository | `payload.go:251`, `bulk.go:273-294` | P (whole install) | Every org in one capsule and one snapshot, as in T "Replicas, Postgres and backups" |

**Multi-tenant (D13).**
- `blobs/`, `staging/`, `view/`, `thumbs/` and `text/` move to the shared storage of R2. All
  five are rooted at the data directory today (`drive_handlers.go:371`, `drive_uploads.go:17`,
  `kypost_view.go:27`, `drive_preview.go:32`, `document_text.go:35`). Every one of them is
  sealed under the deployment key, staging segments included (`uploads.go:18-20,193`).
- `search/` moves into PostgreSQL (Search index, above).
- `encryption.key` and `identity.key` must not exist in the data directory.
- `installer/` is refused (4a).
- `drill/` stays per replica. `initial-admin-password` is written once, by the replica that
  creates the bootstrap administrator (step 6), to its own data directory. The others write none.
- Backups have no SQLite-only payload. `payload.go:70-72` refuses any driver but SQLite
  today. R3 ports the replica pattern's PostgreSQL snapshot (one transaction, under the
  `backup-run` lease), and the Restic bulk reads blobs from the shared storage.

### In-memory state

Every row is per process today. On replicas each must be shared, or a request that lands on
another replica sees none of it. R4 moves each into PostgreSQL, the replica pattern's way
(atomic upserts and unique keys with indexed expiry, removed by the elected worker). SQLite
keeps the in-memory versions, as the base does.

| State | Defined | Class | Multi-tenant (R4) |
|---|---|---|---|
| WebDAV lock table | `dav/locks.go:22-33` (`webdav.NewMemLS`, and a random token key per process) | Keyed by workspace ID, not by display name, so two orgs' `/My files/a.txt` never meet | A lock table with expiry. Tokens signed under a key derived from the deployment key. |
| Euro-Office thumbnail tickets | `server.go:58-59` | P, single use | A ticket table, spent with a conditional delete |
| Sign-in attempt windows | `server.go:57,60,202` | P, keyed by account and route | One atomic upsert per key, with database time setting the window (the replica pattern, "How shared security behaves") |
| Device-signature nonces | `devicesig.go:92-97` | P, per device | A table unique on (device, nonce), with expiry |
| Spent CAPTCHA challenges | `internal/auth/pow.go:114` | P, keyed by salt | A table unique on the salt (the base's) |
| KyPost replay cache | `kypost.go:105` | P, keyed by event ID | A database `syncauth.Replay`, keeping `Forget` for 429 and 5xx |
| In-process mutexes: the org poll (`orgsMu`) and the editor revocation sweep (`revokeMu`) | `server.go:48-49` | P | The elected worker (R3). `orgActive`'s one synchronous poll may still run on any replica: `ApplyOrgs` applies only a newer revision (`tenancy.go:350-352`), and R3 makes its `orgs_since` write monotonic. |
| Per-IP buckets | `allowPerIP`, `server.go:309` (the same `attempts` map) | P, keyed by route and IP | Shared with sign-in attempts |
| Search vocabulary | `search/vocab.go` | X, above | R2 |

## Isolation

### Where the org enters every query

Almost every access decision goes through two SQL fragments. `rankSQL` (`access.go:17-23`)
gives a user's rank on a workspace, and `itemRankSQL` (`:33-46`) raises it by shares and
organisation links. `authorize`, `authorizeItem`, `itemRank`, `mayShare` and
`ServiceToken` all use them (`store.go:164-180`, `access.go:51-78`, `shares.go:69-83`,
`service.go:43-53`). So:

1. **Rank.** Both fragments gain one condition: the workspace's `org_id` equals the user's,
   and that org is `active` and `enabled` in `drive_orgs`. A user then holds rank 0 on every
   other org's workspace and item, which `authorize` already turns into a refusal.
2. **Principal.** The inline `status='active'` checks on users (about 20 in production code,
   in `access.go`, `shares.go`, `store.go`, `apppasswords.go`, `devices.go`, `handover.go`,
   `sqlstore.go`) become one named fragment, `activePrincipal(u)`: the user is active and
   the user's org is active and enabled. Every credential path reads it, as T's
   `active_principals` does. Inactive-org answers are the same as an inactive user's today.
3. **Lookups outside rank.** These name users, groups or workspaces without going through
   rank, so each gains an org filter equal to the caller's org:

   | Lookup | Where | Today | Multi-tenant |
   |---|---|---|---|
   | People picker | `shares.go:271-280` | Searches every active user on the install | The caller's org only |
   | Share principal name | `shares.go:99-106` | Any active user or any group by ID | The workspace's org only. Otherwise the same answer as a missing ID. |
   | Grant a group | `store.go:349-374` | Any existing group ID (FK only) | The workspace's org only, plus the trigger |
   | Handover target | `handover.go:85`, `:120-139` | Any user or shared workspace | The source workspace's org only |
   | Workspace name free | `workspaces.go:17-29` | Install-wide | Per org. Otherwise two orgs' suite groups called "Family" or "Team" become "Family (2)", which reveals that another org has one. |
   | Suite group members | `suitegroups.go:211-227` | By `sub`, any account | The group's org only (see Identity binding) |
   | Open an organisation link | `links.go:119-162` | Any active user | The link's org only (D6) |
   | Shared with me | `shares.go:217-235` | Joins shares and links install-wide | Through rank, unchanged once item 1 holds. The trigger on `drive_link_users` backs it. |
   | Admin directory and audit | `drive_handlers.go:42-43` | The whole install | The selected org (Admin surface) |

4. **Platform workers.** Retention, workspace purge, orphan sweeps, blob sealing and upgrade,
   suite group reconcile, the search indexer and expiries read across orgs, as today
   (`drive_retention.go:62-100`). They reach content only by row ID, never on a user's
   behalf, and every audit row they write is stamped with the row's own org. Retention and
   workspace purge skip orgs that are not `active` (D11).

5. **Loads by ID.** Rank alone is not enough to answer like a missing ID. Some paths load
   a row by ID first and authorize second: `resource` (`shares.go:48-57`), `File`
   (`store.go:464`), and the in-transaction loads in move and copy (`move.go:37`, `:124`).
   For such a path, "exists but denied" and "no such row" can give different answers. In
   multi-tenant mode each of these loads joins the row's workspace and requires the
   caller's org, so another org's row is "no rows" and takes the same branch as a random
   ID. As built (step 3a), `resource`, `File` and `MoveFolder` do this, since each returns
   or branches on what it loaded. The file loads in `moveFile` and `CopyFile` do not:
   `MoveFile` loads through `File` first, and rank refuses the next statement with the same
   error and nothing returned, so an org filter there could not change any answer.

A cross-org ID then fails at the same point, and for the same reason, as a random one.
Today a missing item is usually `ErrDenied`, which is 403 (`move.go:38-39`,
`drive_handlers.go:111-113`), and sometimes `ErrNotFound`, which is 404 (`links.go:131`).
The test below compares the two answers instead of assuming 404.

### Triggers (second layer)

Created by one drive schema entry (appended to `schema.go:15`, append-only). On PostgreSQL
(R1) every trigger exists under the same name as a PL/pgSQL function. `org_id` is also a
foreign key to `drive_orgs(id)`, and the existence trigger stays, because a foreign key cannot
refuse a `deleted` org. Each trigger raises a message that starts `tenancy:`, so `asDenied`
(`tenancy.go:168`) maps it to `ErrDenied` on both backends.

- `org_id` existence (`BEFORE INSERT`) and immutability (`BEFORE UPDATE OF org_id`) on every
  **O** table. The existence check requires a `drive_orgs` row whose `status` is not
  `deleted`, so nothing can be written into a deleted org. That covers a late guest upload,
  a KyPost ticket, or a worker racing the purge.
- Single-tenant rows get `DEFAULT '<default org id>'` so the upgrade fills them in place.
  SQLite allows `ADD COLUMN … NOT NULL DEFAULT` with a constant. The shared schema entry
  serves both modes, so a multi-tenant database has the same column default. A multi-tenant
  database therefore never holds the default org's row in `drive_orgs`, as in T ("A
  multi-tenant install has no default org row"). An insert that forgets `org_id` hits the
  existence check and fails, instead of landing in the default org.
  `TestMultiTenantRefusesDefaultOrg` inserts into every **O** table without `org_id` on a
  multi-tenant database and expects each insert to fail.
- **Mode-only DDL.** The appended drive schema entry is mode-agnostic: the same columns,
  defaults and triggers for both modes. Nothing in it depends on the mode, so a
  single-tenant upgrade runs exactly what every install runs.
  - DDL only multi-tenant needs lives in one separate function, `initMultiTenant`. Today
    that is the partial unique index on `users(sso_provider, sso_subject) WHERE
    sso_provider='kysignon'`.
  - It runs once, in the transaction that first records `tenancy_mode=multi` on an empty
    database (D1), and writes a `tenancy_multi_ddl` version to `server_settings`.
  - No migration calls it. A single-tenant database can never be switched to multi
    (D1), so it never runs on an upgrade, and the non-unique `idx_users_sso`
    (`migrations.go:50`) stays as it is on single-tenant installs.
  - Later multi-only DDL appends a numbered step to `initMultiTenant`. Each step runs only
    on multi-tenant databases, at start, while the stored `tenancy_multi_ddl` version is
    below that step.
  - A test upgrades a single-tenant database that holds duplicate `kysignon` subjects and
    expects success.
- Same-org checks (`BEFORE INSERT` and `BEFORE UPDATE`) on `group_members`,
  `drive_personal_workspaces`, `drive_grants`, `drive_shares` (user and group principals),
  `drive_link_users` and `drive_suite_groups`.
- D8's blob check on `drive_versions`: refuse a new row whose `blob` is already named by a
  version whose file's workspace is in another org.
- `drive_files` and `drive_folders` changing `workspace` (move, `move.go:78`; handover) must
  stay in one org.

Each raises a message that maps to the store's existing refusal (`ErrDenied`), the way the
name-clash triggers map to `ErrConflict` (`schema.go:97-106`).

### Tests

- `TestEveryTableIsClassified` reads both catalogues (`sqlite_master`, and
  `information_schema.tables` on PostgreSQL, R1) and requires each table to appear in
  exactly one of `orgOwnedTables`, `derivedTables` (each with its parent FK) or
  `platformTables`. A new table with no class fails the build (as in T).
- `TestOrgOwnedTablesHaveOrgID` requires `org_id NOT NULL` and both triggers on every **O**
  table, and on PostgreSQL the foreign key too. `nullableOrgID = {"audit_records",
  "drive_events"}` are the only exceptions.
- `TestBackendsHaveTheSameColumns` (as T's) requires the same drive tables, columns and
  trigger names on both backends. From R1, every drive, tenancy and cross-org test runs on
  both.
- `TestOrgSQLNamesOrgID` is a go/ast scan of SQL string literals in `internal/drive`,
  `internal/store` and `internal/api`. A literal that names an **O** table must also name
  `org_id`, call `rankSQL`, `itemRankSQL`, `activePrincipal`, `sameOrg`, `userOrg` or
  `workspaceOrg`, or have its declaration on an allowlist with a reason (as KyIdentity's
  scan does). **D** tables are left out: they reach their org through a parent the caller
  was ranked on, and scanning them would flag nearly 400 by-ID statements, which would make
  the allowlist meaningless.
- `TestCrossOrgMatrix`, differential, in the style of the route-table tests in
  `internal/api`:
  - Orgs A and B each have two members, a suite group workspace, files, folders, a share,
    an organisation link, a guest link, an app password, an agent token, a device and a
    service token. The staff org has a staff admin.
  - For every `/api/drive/*`, `/dav/*` and `/mcp` route and every MCP tool, call it as each
    A principal (session, app password, device, agent token, service token) twice: once with
    a random UUID that never existed, once with B's real ID.
  - Require the same status and the same body (IDs masked) for both, and B's rows and blobs
    unchanged. A cross-org ID that answers differently from a missing one fails the test,
    so does a success.
  - Open B's organisation link token as an A member: same answer as an unknown token, and
    no `drive_link_users` row.
  - Search as A for a word only B's files hold: no result, and `pending` counts A's files
    only.
  - Try every same-org trigger with direct SQL: each refuses.
  - Copy, move and handover from A to B: refused at the store, and again at the trigger with
    authorization bypassed in the test.
  - Run in both modes. Single-tenant has one org, so the cross-org half is skipped and the
    rest of the suite must pass unchanged.

## Identity binding

### Sign-in

1. The callback (`sso_handlers.go:87-162`) verifies the ID token as today (`oauth.go:86-104`,
   `sso` `AGENTS.md`). `claimsFromIDToken` (`oauth.go:145`) also reads `org_id`.
2. Multi-tenant: no `org_id` is refused (point 1). An unknown org gets one synchronous
   `/api/suite/orgs` poll. If the org is still unknown, not `active` or not `enabled`, the
   sign-in is refused, with no session.
3. `DirectorySubject` (`sso_handlers.go:119`) finds the account by `sub`. A known account
   whose `org_id` differs is refused (point 3), audited as `tenancy.org_mismatch`, and no
   session is issued.
4. An unknown `sub` is provisioned (D5) through `ApplyDirectoryUser`
   (`sso_handlers.go:121-124`), the only write path for directory input
   (`internal/store/AGENTS.md`), now with `org_id`. The role is never taken from the token
   (`sso` `AGENTS.md`): new accounts are `user`.
5. Then today's `IssueSSOSession` (`sso_handlers.go:148`).

Step-up (`finishSSOStepUp`, `sso_handlers.go:189-221`) re-checks the account's `org_id`
against the step-up token's.

### Signed webhook

- Today `/api/sso/kysignon/sync` verifies a hex HMAC of the body (`kysignon.go:76-90`,
  `sso_handlers.go:223-255`) and reads `KySignOnSyncPayload` (`kysignon.go:57-65`), which
  has no org.
- Checked on 2026-11-20 (KyIdentity `origin/master` `49e8aa6`): the two sides do not
  interoperate, so the receiver has never worked against KyIdentity's suite webhook.
  KyDrive is documented to pair by SCIM (`docs/PERMISSIONS.md:13`).
  - KyIdentity signs with `syncauth.Sign` (KyIdentity `internal/sync/sync.go:582`): `v1=`
    and an HMAC over the timestamp, event type, event ID and the body's SHA-256. KyDrive
    expects a bare hex HMAC of the body.
  - KyIdentity sends a SCIM user resource (KyIdentity `internal/sync/sync.go:112-125`), with the event type and time
    in headers. KyDrive parses its own `event`/`timestamp` body.
  - KyIdentity already adds `org_id` to suite webhook bodies in multi-tenant mode
    (`internal/sync/sync.go:466-468`). No KyIdentity change is needed.
- The owner chose to fix this ahead of PR 4, in both modes: #154 makes the receiver verify
  with `syncauth.Verify`, read the SCIM resource and order events by its `meta.version`
  revision, as KyMessage's receiver does.
- Multi-tenant (PR 4) then adds `org_id`, which T authenticates through the signature (T
  "Outbound sync").
  - No `org_id`: 400.
  - A known `sub` with another org: refused and audited (point 3).
  - `user.created` for an unknown `sub`: creates the account in that org.
  - Events are ordered by revision, as #154 leaves it (`sso` `AGENTS.md`).

### Back-channel logout

`VerifyLogoutToken` (`backchannel.go:24-50`) also reads `org_id`. Multi-tenant: no claim is
400. With `sid`, the sessions ended are those whose account is in the claimed org. With `sub`
only, the account's org must match (`sso_handlers.go:274-282`). Because `sub` is globally
unique and `users.org_id` is immutable, a mismatch means a KyIdentity bug. It is logged as an
alert. Logouts are not refused for it, since a logout only takes access away.

### Suite groups

- Today one poll lists every suite group (`suite_groups.go:33-47`) and maps members by `sub`
  alone (`suitegroups.go:211-227`).
- In multi-tenant mode `/api/suite/groups` needs `org_id` (T "Suite directory"). So the
  poll walks `drive_orgs` (active and enabled orgs) and asks per org.
- A new group's workspace and local group take that org (`suitegroups.go:71-79`).
  `suiteMembers` keeps only accounts in the same org. A `sub` from another org is skipped
  and audited, never added.
- Cost: one request per org per pass. Unverified whether T's `/api/suite/groups` pages or
  takes `since`.

### Local accounts

- The bootstrap and recovery local administrator (`docs/PERMISSIONS.md:32`) are staff
  accounts in multi-tenant mode, in the staff org (D9).
- **Seeding the staff org.** On the first start of an empty multi-tenant database, before
  bootstrap creates its administrator, KyDrive inserts one `drive_orgs` row for
  `KYDRIVE_STAFF_ORG`: `active`, `seeded=1`, no storage limit. That row exists before any
  poll, so the administrator can be created while KyIdentity is unreachable, and the
  `users` insert passes the existence trigger.
  - The row is sticky. The poller never updates the `seeded` row, so the staff org is
    always `active` locally, and there is no recovery command to fight the poll.
  - KyIdentity still decides staff access where it matters: a directory staff account
    needs a fresh ID token to sign in, and KyIdentity refuses one for a suspended org
    (T `active_principals`). So the sticky row keeps only the local recovery
    administrator working during a KyIdentity-side problem, which is what it is for.
  - If a poll reports the staff org as anything but `active`, KyDrive logs an alert and
    changes nothing.
  - The staff org gets no storage pool check (staff hold no customer content).
  - `KYDRIVE_STAFF_ORG` is recorded at first start and immutable (question 2). An
    ID token whose `org_id` equals it is a staff account like any other.
  - Startup refuses a `KYDRIVE_STAFF_ORG` that is not a canonical UUID, or that equals the
    default org ID (`00000000-0000-0000-0000-000000000001`). Otherwise the staff row would
    be seeded under the default ID, and an insert that forgets `org_id` would land in the
    staff org instead of failing the existence trigger (Triggers). A config test covers
    both refusals.
- `set-directory-role -role admin` is refused for an account outside the staff org.
- `ApplyInstaller` (installer channel) is refused in multi-tenant mode, as in T.

### KyDrive-issued credentials

| Credential | Resolves at | Org from | Multi-tenant change |
|---|---|---|---|
| Browser session | `session.go:100-112` | the user row | `activePrincipal` |
| WebDAV app password (Basic) | `apppasswords.go:220-236` | the user row | `activePrincipal` |
| Agent token (`kya_`, `/mcp`) | `apppasswords.go:241-248` | the user row | `activePrincipal`. A folder limit stays inside its workspace, so inside the org. |
| Device token (`kyd_`) | `devices.go:210-261` | the user row | `activePrincipal`. Revoked and wipe answers unchanged. |
| Service token | `service.go:43-53` | its workspace, through `authorize` | Workspace's org must be active (rank, item 1) |
| Guest link | `guest.go:155-182` | creator and workspace, through `guestRights` | Creator through `activePrincipal` and rank, so a suspended org's guest links stop |
| KyPost upload ticket and viewing copy | `drive_peer_tickets`, `drive_view_copies` | the owner row | `activePrincipal` at each use |
| Editor session (Euro-Office) | `drive_editor_sessions` | file, through workspace | Revoked when an org stops being `active` (Lifecycle) |

Device linking (`POST /api/devices/approve`, `internal/api/devices.go:29`) needs a signed-in session with
step-up, so a device takes the approving user's org and can never change it.

## Drive specifics

### Per-org data keys (multi-tenant, D14)

- **Today.** A KYB2 blob is an age file whose one `kydrive` stanza wraps the random per-file
  key under AES-256-GCM with HKDF-SHA256(deployment key, stanza salt, binding)
  (`sealed.go:19-23,75`). Names use HKDF keys `kydrive:name` and `kydrive:name-hmac`
  (`names.go:26,30`), and the search index uses `kydrive:search-token`, `-doc`, `-check` and
  `-vocab` (`search/index.go:61-73`). All of them come from the deployment key.
- **Org keys.** A new table, `drive_org_keys(org_id PRIMARY KEY REFERENCES drive_orgs,
  kek, wrapped, created)`, holds one row per org. The org key is 32 random bytes, created in
  the transaction that first records the org (the poll, or the staff org's seed). `wrapped` is
  AES-256-GCM under HKDF-SHA256(deployment key, "kydrive:org-kek"), with a random nonce and
  the additional data `kydrive:org-key:<kek>:<org_id>`, so a row copied onto another org fails
  to open. `kek` is 1, the deployment key. It leaves room for a later rewrap, such as a
  deployment-key rotation or a two-part KEK with KyIdentity, without re-sealing a file.
- **Blobs and caches.** In multi mode the stanza is `kydrive-org`, with the org ID and the
  salt as arguments. It wraps the file key under HKDF-SHA256(org key, salt,
  `kydrive:kyb2:org:<org_id>:` + binding), and the KYB2 framing is unchanged. The stanza
  names its org, so a platform worker reading by blob ID (sealing, thumbnails, text) picks the
  key without a lookup. The org is in the binding too, so a blob opened under the wrong org
  fails authentication. Multi mode refuses a `kydrive` stanza, and single-tenant mode
  refuses a `kydrive-org` one. View copies, staging segments, thumbnails and text are sealed
  the same way, under their blob's org.
- **Names.** The name and name-HMAC keys are derived from the org key. The same name in two
  orgs then has two different `name_hmac` values. Uniqueness is per workspace, inside one
  org, so nothing else changes.
- **Search.** The token, document and check keys and the sealed vocabulary become per org,
  and the index's rows carry `org_id`, so a purge deletes an org's index by org ID without its
  key (Lifecycle, `deleted` step 4).
  An org's term hashes then cannot be linked to another org's, which narrows the residual
  risk "The search index". The index lives where question 14 decides (R2); per-org keys
  apply either way.
- **Runtime.** Unwrapped org keys sit in a bounded in-memory cache on each replica. A key
  is evicted when its org leaves `active`, and dropped at once when the org is deleted.
  Every replica already has the deployment key (D13), so every replica can unwrap.
- **Single-tenant** keeps the deployment-key scheme and has no `drive_org_keys` row. Its
  blob, name and index bytes are unchanged byte for byte (`TestTenancyUpgradeKeepsEveryRow`
  and the other upgrade tests).

### Blob storage

- **Layout.** Flat, `<data>/blobs/<uuid>` (D8). Every new blob gets a fresh random ID
  (`blobs.go:21`). `BlobPath` accepts only a canonical UUID (`blobs.go:85-90`), so no path
  from a request reaches the filesystem.
- **No content addressing.** The SHA-256 digest is stored on the version (`blobs.go:59-74`)
  but never used as a name or a lookup key. Thumbnails and text are named by blob, "never
  by digest, so the cache cannot confirm what a file holds" (`thumbs.go:294-295`). Two orgs
  uploading the same bytes get two blobs, so nothing can be learned by uploading a guess.
- **Sharing a blob.** Only copy shares one between files (`move.go:116-135`). It needs read
  on the source and editor on the target, both through rank, which item 1 limits to one org.
  Move across workspaces needs manager on the source (`move.go:44-47`). D8's trigger refuses
  the cross-org case even if both checks were bypassed.
- **Reading a blob.** Download, preview, zip, DAV, MCP and the editor all load the file row
  through an authorized store call first (`drive_handlers.go:432`, `drive_zip.go:49`,
  `dav.go:122`). The only blob reads that skip a user are platform workers, by blob ID.
- **Removal.** Unchanged: metadata commits first, then `RemoveBlobs` deletes blobs the
  transaction found unreferenced (`workspaces.go:242-263`, `blobs.go:93-108`). The hourly
  orphan sweep catches the rest (`blobs.go:113-148`).

### Sharing and links

| Path | Single-tenant | Multi-tenant |
|---|---|---|
| Share to a person or group (`drive_shares.go:15`, `shares.go:118`) | Any active user or group | Same org only: picker, principal lookup and trigger |
| Workspace grant (`drive_handlers.go:45`) | Any group | Same org only. In practice only suite group workspaces have grants (D10). |
| Organisation link, create and open (`links.go:29`, `:119`) | Anyone signed in to the install | Anyone signed in to the link's org (D6). An opener from another org gets `ErrNotFound`, the same as an unknown token (`links.go:131`). No `drive_link_users` row is written. |
| Guest link (`guest.go:51`, public routes `internal/api/guest.go:30-32`) | Anyone with the token, mandatory expiry, optional password | Unchanged: the one deliberate public path. It stops while the creator's org is not `active`. Guest upload counts against the creator's org pool (`guest.go:303-330` publishes through `publishTX`). |
| KyPost guest link (`guest.go:60-65`) | Per-user switch | Unchanged, same org rules |

UI copy: in multi-tenant mode an organisation link was to be called a **Household link**
(owner, question 8). "Shared link" was considered and rejected: guest links are shared links
too, so it would suggest the link reaches people outside the org. Business plans make
"Household" wrong for some orgs, so the name is reopened as question 16.

### WebDAV, desktop sync and AI agents

- **WebDAV** (`/dav/`, `server.go:533`; `internal/api/AGENTS.md` `/dav`) authenticates with
  HTTP Basic and an app password, or a device bearer token, never a cookie.
  - The username is the account's username, which is the globally unique email, so one
    shared host name works for every customer.
  - The user's org bounds the namespace: `WorkspaceNames` (`dav/fs.go:128`) lists only
    workspaces the user can read.
  - Locks are keyed by workspace ID (`dav/locks.go:22-24`).
- **Desktop sync** (`kydrive-desktop`, unreleased) uses device tokens. It links by browser
  approval with step-up (`internal/api/devices.go:29-31`) and reads the per-workspace change feed and
  snapshot (`drive_changes.go:14-15`). Both go through rank.
  - A suspended org's device gets `ErrDenied` (`devices.go:249`), never the wipe answer.
  - Unverified: that the desktop client keeps its local copy on a plain 401.
- **MCP agents** (`/mcp`, `server.go:488`) use agent tokens, run every tool as the user
  (`internal/api/AGENTS.md`, "MCP tools"), and are limited by rank.
- None of these channels names an org, and none can.

### KyPost and other peer payloads

- **KyPost signed routes** (`server.go:339-343`) are verified with `syncauth` under the pair
  secret (`kypost.go:277-299`). Their body names `{sub, issuer}` only (`kypost.go:239-241`),
  and `kypostAccount` resolves the account by `sub` (`kypost.go:330-349`).
  - Multi-tenant: every body also carries `org_id`. A missing one is 400 `bad_body`.
  - `kypostAccount` refuses an account whose org differs, with the same `404 no_user` an
    unknown subject gets, and audits it.
  - The pair secret is per pair, not per org (T "Suite directory", `/api/suite/peer`).
    That is acceptable because both sides are platform-run, and the org check limits what
    a stolen secret reaches to orgs whose subjects it can name.
  - This is a KyPost change too (`docs/KYPOST.md` is the shared contract; both sides change
    together, `docs/AGENTS.md`).
- **Upload tickets and viewing copies** (`kypost.go:535-600`, `viewcopies.go`) are bound to
  their owner when issued (`drive_peer_tickets.owner`, `drive_view_copies.user_id`), so
  they carry the owner's org. They add bytes through `fits` (`peer.go:141`,
  `viewcopies.go:91`) and count against the org pool.
- **Picker and fetch** (`suite_pick.go:38-39`, `kypost.go:486`): a pick is made by the
  signed-in user in KyDrive's own page, and fetch re-checks the user's rank
  (`access.go:186-205`). Both stay inside the org.
- **Suite apps** (`GET /api/suite/apps`, `server.go:337`, `suite_apps.go:44-60`) asks
  KyIdentity with the user's `sub`, and T scopes the answer to that user's org. No change.
- **Euro-Office** is the other peer. Its JWT and callbacks are install-wide
  (`internal/editor`, `drive_handlers.go:89-91`), and a document key resolves to one file.
  No org travels to it or from it, and none needs to. The document server sees every org's
  plaintext while editing (residual risk, as today: root `AGENTS.md` "not end-to-end").

### Quotas

- **Source.** `GET /api/suite/orgs` returns, for KyDrive's client only, `enabled` and
  `quotas` (T "Entitlements"; `org_apps.quotas_json` is opaque to KyIdentity, at most
  4 KiB). KyDrive reads `{"storage_bytes": <integer > 0>}`.
  - A missing or invalid value, or an org KyDrive has not polled, refuses every write with
    `ErrQuota` (D7, fail closed).
  - Reads keep working.
- **Usage.** `usedBytes` (`store.go:676`) is today's per-workspace sum of version bytes plus
  live viewing copies. The org's usage is the same sum over every workspace with that
  `org_id`. Trashed files and old versions count, as today.
- **Enforcement.** One check in `fits` (`store.go:682-692`), inside the write transaction.
  SQLite's one writer serialises check and insert. PostgreSQL has no single writer, so `fits`
  first locks the row its check reads (`SELECT … FOR UPDATE`): the org's `drive_orgs` row for
  the multi-tenant pool, and the target `drive_workspaces` row for the single-tenant
  per-workspace quota. Two uploads, on one instance or on two replicas, then cannot both take
  the last bytes. The org check uses the org's net change, not the bytes written to the target
  workspace. Bytes that move between two workspaces of one org change nothing, so an
  over-quota org can still move files and receive a handover. Every path that adds bytes
  ends there:

  | Path | Call |
  |---|---|
  | New version: upload, DAV `PUT`, MCP `write_file`, new document, editor save, copy, guest upload | `publishTX` (`store.go:630-640`), from `store.go:494,602`, `sessions.go:316`, `move.go:132`, `guest.go:319` |
  | Resumable upload, at start and again at publish | `uploads.go:92`, then `publishTX` |
  | Move to another workspace | `move.go:63`. Inside one org the net change is 0, so the org check always passes. The single-tenant workspace check is unchanged. |
  | Handover | `handover.go:139`. Same as move: net 0 inside one org. |
  | KyPost upload ticket | `peer.go:141` |
  | KyPost viewing copy | `viewcopies.go:91` |

  The only `INSERT INTO drive_versions` is in `publishTX` (`store.go:664`), and the only
  `INSERT INTO drive_view_copies` is behind `fits` (`viewcopies.go:91-105`). The quota test
  asserts both by source scan, so a new byte-adding path cannot skip the check.
- **Not counted:** staging chunks of unfinished uploads (bounded by the uploads-in-progress
  cap, `uploads.go:100`, and `KYDRIVE_MAX_FILE_SIZE`, `config.go:328`), thumbnails, text
  caches and the search index. That is question 7.
- **Downgrade.** Lowering `storage_bytes` below usage deletes nothing. Writes are refused
  until usage drops. Reads, trash, purge, moves and handover keep working. A test lowers
  the pool below usage and expects a move and a handover to succeed and an upload to be
  refused.
- **Per-workspace quota** (`drive_workspaces.quota`, the 10 GiB My files default at
  `peer.go:24` and `store.go:276`, 1 GiB for suite groups at `suitegroups.go:18`) is not
  enforced in multi-tenant mode. The column stays for single-tenant.

## Admin surface

`requireAdmin` checks `role='admin'` (`server.go:404-421`), and the store's `admin` check
also refuses service, device and peer contexts (`store.go:181-194`). In multi-tenant mode
only staff hold the role (D9). Administration grants no content access in either mode: "Administration does not bypass content grants" (root `AGENTS.md`), and rank gives
administrators nothing (`access.go:16`).

Each admin route gets a class, as T adds a `platform` flag. A route that names no class
fails the build.

| Class | Routes | Multi-tenant behaviour |
|---|---|---|
| **platform** | `GET /api/drive/status`; `/api/backup/*` (`server.go:362-370`); `PUT /api/settings/theme`; rate-limit networks (`server.go:375-376`); `GET/PUT /api/drive/trash-retention`; `PUT /api/drive/guest-links-policy`; `PUT /api/drive/device-policy`; `GET /api/settings/kypost` | Staff admins, with today's step-up where it exists. One setting for the whole install. Audited as platform events (`org_id` NULL). |
| **org** | `GET /api/drive/directory`; `GET /api/drive/audit`; `GET /api/drive/leavers`; `POST /api/drive/leavers/{workspace}/handover`; `GET /api/drive/deleted-workspaces`; `POST /api/drive/workspaces/{id}/restore` | Need `X-Ky-Org` naming an org in `drive_orgs`. Without it: `400 org_required`. Bound to that org only. A target or object in another org answers like a missing one. Every request writes `staff.read` or its existing audit row into that org's log, naming the staff account (as in T "Roles"). Single-tenant ignores the header and binds the default org. |
| **refused** | `POST /api/drive/workspaces`; `PUT /api/drive/workspaces/{id}/quota`; `PUT …/name`; `DELETE /api/drive/workspaces/{id}`; `PUT …/retention`; inbound `/scim/v2/*` | 404 (D5, D7, D10). Suite groups create, rename and archive shared workspaces. Quotas come from billing through KyIdentity. Per-workspace retention stays a single-tenant feature. |

- **Org selection.** Staff find an org by its ID or by a member's exact email (the local
  `users` table). `/api/suite/orgs` returns no names (T), so the UI shows the ID, and the
  member list once it is selected.
- **What staff see.** The org's directory (names, emails, groups), usage, workspace names
  and its audit. Never file names or content, because no admin route opens an item.
  - Residual: workspace names stay plaintext (root `AGENTS.md`), and for suite groups they
    are the org's group names, a Business team's included.
- **Handover** is the one content-moving staff action. It runs only within the selected org
  and is audited in that org's log. A leaver's shared files move automatically (see "A
  member leaves"); staff `Handover` remains for the rest, on the account holder's request.

## Lifecycle

The poll (D3) applies a status change in one transaction per org, which also writes an audit
row (`tenancy.org_status`, platform and org log; platform log only for `deleted`, see
below). At most about 60 s pass between KyIdentity's
change and KyDrive's.

### `suspended`, `deletion_scheduled`, or `enabled: false`

| What | Effect |
|---|---|
| Browser sessions | Refused through `activePrincipal`. KyIdentity also sends back-channel logouts for every client session (T "Suspension", step 3), which delete them (`sso_handlers.go:264-289`). |
| App passwords, agent tokens, device tokens, service tokens | Refused at each use. Not revoked: a resume brings them back without re-linking every computer. Devices get the plain refusal, never wipe. |
| Editor sessions | Revoked in the status transaction (`RevokeUserEditors` for each of the org's users, as `internal/store/AGENTS.md` describes for revocation paths), so Euro-Office callbacks in flight are fenced. |
| Guest links, organisation links, KyPost tickets and routes, viewing copies | Refused, through the creator's or owner's `activePrincipal` |
| Suite group reconcile | Skips the org |
| Trash retention, version pruning, deleted-workspace purge | Paused for the org (D11). On resume, `drive_orgs.retention_after` is set to the resume time plus 14 days. Until then, retention, version pruning and workspace purge skip the org, so a customer whose trash aged during the suspension has two weeks to restore it. After that, the usual ages apply. A test suspends an org past its trash age, resumes it, and expects nothing purged until `retention_after`. |
| Data | Nothing changed or deleted |

`resume` (back to `active`) needs no action in KyDrive beyond the status row. Sessions do not
come back (T), but long-lived credentials do.

### `deleted`

A purge job runs from the hourly retention pass (`drive_retention.go:62`), on the elected worker (R3). It is idempotent:
each step deletes what is left, and `drive_orgs.purged` is set only after the last step. A
crash halfway repeats safely.

0. Revoke the org's editor sessions, in the transaction that records `deleted`. This is
   the same step as suspension's. Do not rely on an earlier transition: a poll can see
   `active` go straight to `deleted`, because `since=<revision>` returns only the latest
   state, and one missed intermediate status between polls is enough. Every move from
   `active` to any other status runs that step.
1. For each workspace of the org, today's purge steps in one transaction
   (`workspaces.go:242-264` and `removeWorkspace`, `:298-312`). Each collects the blobs it
   left unreferenced. Step 0 revoked the editor sessions, so the "undelivered drop" skip
   (`:235-241`) only waits for drops in flight to be delivered, as today.
   - This includes suite group workspaces, which today's purge keeps forever
     (`workspaces.go:216-217`), and their `drive_suite_groups` rows, which are otherwise
     never deleted (`schema.go:125-126`). The org's tombstone is what stops a reused ID.
2. Then every other **O** row of the org, users included. `DeleteUser` already ends the
   account's sessions and editor sessions in its transaction (`internal/store/AGENTS.md`).
   Derived rows cascade or are deleted by their parent.
3. After commit, `RemoveBlobs` for the collected blobs, and the view copies' blobs. The
   orphan sweeps then clear thumbnails and text within an hour (`drive_retention.go:86-95`).
   Staging is cleared by `ExpireUploads`.
4. The search index drops the org's refs on its next keep scan, and a full vocabulary
   rebuild is requested, so the org's words leave the sealed vocabulary. In multi mode
   (D14), the index's rows carry `org_id` and each org has its own sealed vocabulary. This
   step therefore deletes the org's index rows and its vocabulary by org ID. It needs no key
   and no keep scan or rebuild. With the index in PostgreSQL (question 14) that happens in
   the purge's transaction. With a per-replica index, each replica does it when it next sees
   the org deleted.
5. In the transaction that sets `purged=1`, the org's `drive_org_keys` row is deleted
   and every replica evicts the key (D14). Any file the steps above missed is then
   unreadable: a leftover blob on the shared storage, an object version the storage kept, or
   a replica's cache. No step reads the org's data after this, step 4 included, since it
   deletes by org ID. With the index in PostgreSQL (question 14), steps 4 and 5 commit
   together. With a per-replica index, each replica deletes the org's rows when it next sees
   the org deleted, which can be after the key is gone. Those rows are term hashes under the
   destroyed key, so nothing can link them to words any more, but they take up space until
   then.
6. `drive_orgs` keeps the tombstone (`status='deleted'`, `purged=1`), and a platform audit
   row records the org ID and row counts per table, with no names.

A capsule restore brings back every org key it held. In multi mode the restore reset
(`ResetAfterRestore` today, the replica pattern's `prepareRestoredDatabase` in R3) therefore
sets every org other than the staff org to `suspended` and `orgs_since` to 0. Nothing is
served until the first full poll restores each org's real status, and an org KyIdentity
reports `deleted` is purged again, key included. KyIdentity's org record is the durable
tombstone.

The `org_id` existence trigger refuses any new row once `drive_orgs.status` is `deleted`
(see Triggers), during the purge and after it. That includes the two audit tables, whose
org rows the purge deletes with everything else.
- Every audit or event row written about a deleted org is a platform row instead: `org_id`
  NULL, with the org ID in `resource` or `details`. That covers the status change itself,
  each purge step and `DeleteUser`'s audit.
- The audit and event helpers (`event`, `store.go:195`, and the base audit writer) choose
  NULL when the row's org is `deleted`. So no purge transaction trips the trigger on its
  own audit row, and a retried purge does not fail for that reason.
- A test runs the whole purge against the triggers and expects `purged=1`, no org-stamped
  row left, and the platform rows present.

Copies of the org's
data stay in capsules and Restic snapshots until backup retention drops them (residual risk,
question 5).

KyDrive purges on `deleted` from `/api/suite/orgs` only. It does not depend on KyIdentity
also sending a `user.deleted` webhook per member. Unverified: whether T's `DELETE /orgs/{id}`
sends those.

### A member leaves

Owner decision (question 12). When KyIdentity deletes a member's account, KyDrive acts on
that member's My files at once, inside the org:

- **Trigger:** only a `syncauth`-verified `user.deleted` webhook for a known `sub` in the
  same org. Nothing else starts it. A member missing from any poll or group list, or a
  poll that fails or comes back truncated, never starts it: the process moves files and
  starts a purge, so it fails closed like D7 and question 4.

- **Shared files move to the account holder, and stay shared.** An item is shared if it,
  or a folder above it, has a share to a person or group or an unrevoked organisation link.
  The test reads the share records, not whether they work right now, so it gives the same
  answer whether or not KyIdentity deactivated the member first.
  - A deactivated owner's person and group shares are stopped, not deleted (root
    `AGENTS.md`), so they still count.
  - Deactivation does not revoke organisation links (root `AGENTS.md`: organisation links
    stay), so a revoked one was revoked by its owner and does not count.
  - Guest links never count (owner decision, 2026-11-20). A file shared only with
    guests has nobody in the org who keeps it, so it goes to the trash with the
    unshared files below. This also means the rule never needs to know why a guest link
    was revoked, which KyDrive does not record.
- Only the topmost shared item on each path moves, with its whole subtree intact, so
  a folder share still covers everything under it. They all go into one "Handover from
  <owner> <date>" folder in the account holder's My files.
  - This is a new store path, not today's `Handover`. `Handover` ends the leaver's shares
    and revokes their links (`handover.go:147-154`), which would remove the very access the
    rule keeps.
  - The new path moves person and group shares and organisation links with the items and
    makes the account holder their owner and creator, so the org's members they were
    shared with keep their access. It changes no role and widens nothing.
  - Guest links do not survive. Directory deactivation already revokes every guest link
    the account made, and reactivation revives none (root `AGENTS.md`). A guest link also
    stops with its creator (`guest.go:155-182`). KyIdentity's user deletion queues
    `user.deleted` directly (KyIdentity `internal/store/store.go:1123`), so a leaver's guest
    links may still be live when it arrives. The new path therefore revokes them in its
    own transaction, as deactivation would, and never revives one. An item that moves for a
    person, group or organisation-link share loses its guest links too, and the holder can make a
    new one.
  - The holder sees the folder and can stop any of the shares or links.
- **Everything else goes to the trash** of the leaver's workspace and is purged 90 days
  later, whatever the drive's trash age. D11 still wins: the 90 days do not run while the
  org is not `active`, and on resume the clock waits for `retention_after`, like every other
  purge. Nobody opens the files in the meantime. Staff can still
  run today's org-class `Handover` on the leaver's workspace when the account holder asks
  (see Admin surface). That moves the whole tree, trash included, without opening an item.
- A deactivated (not deleted) member's files do not move, since KyIdentity can re-enable
  them. Deactivation still revokes their credentials and guest links, as today.
- If the org has no account holder, nothing moves, and the files stay as a leaver's
  workspace (`handover.go:37`), counted in the org's pool, until the org is deleted.
- **Needs KyIdentity:** `/api/suite/orgs` returns no account holder today (T "Suite
  directory": `id`, `status`, `revision`, `enabled`, `quotas`). KyDrive needs the holder's
  `sub`. This is a new ask of KyIdentity's step 3. The holder can never be the leaver,
  because KyIdentity refuses to delete the account holder (T `409 account_holder`).

### Export

- Owner decision (question 1): members export their own files while the org is active. The
  deletion window is kept short, and KyDrive has no staff export.
- **While active**, any member downloads what they can read: files, and folders as zips
  (`drive_handlers.go:80`, capped by `KYDRIVE_MAX_ZIP_SIZE`, `config.go:332`). Unverified:
  whether a workspace's root can be zipped in one request.
- **Once deletion is scheduled**, the org is suspended and KyDrive serves it nothing. T's
  export-only session "issues no codes or tokens and opens no suite or forward session" (T
  "Views"), so it cannot reach KyDrive. T's staff export, offered when an account holder
  cannot sign in, covers KyIdentity's data only.
- **So the warning must come before scheduling.** A banner shown after scheduling arrives
  too late, because KyDrive already refuses every credential by then. Deletion is scheduled
  by the billing service (T `POST /orgs/{id}/schedule-deletion`), so the billing service
  must warn first. Its cancellation confirmation, and the notice it mails the account
  holder and members before it schedules deletion, must say to download files from KyDrive
  first. The billing service is not built yet, so this is a requirement for its design. A
  suspension for an unpaid bill also blocks downloads, so that notice must say the same
  thing before it suspends.

## Threat model

| Attacker | Goal | Defence |
|---|---|---|
| Member of org A | Read or change org B's files, folders, versions, shares or trash by putting B's IDs in `/api/drive`, `/dav` or `/mcp` | Rank (`rankSQL`, `itemRankSQL`) includes the org, so B's items rank 0 for A. The answer is the same as for a random ID. `TestCrossOrgMatrix` proves it on every route and tool, with every credential type. |
| Member of org A | Discover B's people or groups, or share to them | The people picker, principal lookup and grant are filtered to A's org. Same-org triggers on `drive_shares`, `drive_grants` and `group_members`. |
| Member of org A, holding a forwarded organisation link from B | Get B's file | `OpenLink` admits only B's members. A gets the unknown-token answer. A trigger on `drive_link_users` refuses the row anyway (D6). |
| Anyone with a guest link | Read the shared item | Intended: guest links are the deliberate public path. They expire (`guest.go:69`) and can have a password. They stop with the creator's access or org (`guest.go:155-182`). Unchanged. |
| Member of org A | Infer that B has a workspace or file name | Workspace names are free per org (`nameFree`). Search scores A's refs only. The vocabulary expands only words in A's refs. |
| Member of org A | Get B's blob, by copy, by move, or by guessing | Blob IDs are random. No route takes a blob ID. Copy and move need ranks in both workspaces. D8's trigger refuses a version naming another org's blob. |
| Malicious or buggy relying party, or a request field | Set or swap the org | The org comes only from the verified ID token, the `syncauth`-verified webhook or the suite directory (D12). The one exception, the staff org for local administrators, comes from operator configuration, not a request. `users.org_id` is set once and is immutable (trigger). A known `sub` with another org is refused (point 3). |
| Holder of a stolen KyPost pair secret, or a buggy KyPost | Act as org B's user for org A, or name a user in the wrong org | Every body carries `org_id`, and `kypostAccount` refuses a mismatch. Peer contexts can do only three narrow things (`access.go:85-103`). A stolen secret still reaches every user whose `sub` and org it can name: residual, as in T. |
| User of a suspended org | Keep working through WebDAV, the desktop client, an agent or a service token | `activePrincipal` on every credential path. Editor sessions are revoked. Within one poll (about 60 s). |
| Customer of org A | Fill the shared disk | The org pool in `fits`, failing closed. The resumable upload cap and the file size limit bound staging (`uploads.go:100`, `config.go:328`). |
| Customer of org A | Slow other orgs down: one database, one Euro-Office, one indexer | Per-user and per-IP rate limits, shared across replicas (R4). Replicas spread requests; the database, Euro-Office and the elected indexer stay shared. Residual (question 6). |
| A replica with other shared settings or another key | Serve with another issuer, another KyPost secret, or a key that cannot read the blobs | The shared-configuration fingerprint refuses its start and names the fields, never the values (R3). Multi mode refuses a key minted in the data directory (D13). |
| Holder of one replica's data directory | Decrypt customer files | In multi mode the directory holds no key and no blob (D13). |
| Reader of the shared file storage | Read customer files | Every blob, view copy, thumbnail, text cache and staging segment is sealed under its org's key and bound to its org and blob ID (D14). The org keys are in the database, wrapped under the deployment key, and neither is stored there. |
| A bug, or a swapped ID, that hands one org's blob or cache file to another | Read it | It opens only under its own org's key, and its binding names the org (D14). The org checks in Isolation come first; this is the cryptographic second layer. |
| Anyone replaying to a second replica | Reuse a KyPost event, a device signature, a CAPTCHA or a thumbnail ticket, or reset a sign-in window | Replay and limit state is in PostgreSQL, one unique key or atomic upsert per item (R4). The two-replica test sends each replay to the other replica (step 6). |
| Two replicas racing a job | Purge twice, or let two indexers write | One elected worker runs every job (R3), and every job is idempotent. The purge already is (Lifecycle, `deleted`). |
| Staff, or a stolen staff account | Read customer content | No admin route opens an item, and administration grants no rank (`access.go:16`). Handover stays within one org and is audited in that org's log. Staff do see the org directory and workspace names (residual, as in T). |
| Operator | Anything | Trusted with the database, the deployment key and the host. The server reads plaintext (root `AGENTS.md`), except inside an end-to-end vault (D15). Otherwise residual, unchanged. |
| Deleted customer | Have their data kept | The purge removes rows, blobs and caches, deletes the org's index rows and per-org vocabulary by org ID (multi mode, step 4), and destroys the org key, so anything it missed is unreadable (D14). With the index in PostgreSQL that is all one transaction; with a per-replica index, a replica's rows can outlive the key until it next sees the org deleted (residual). A restore suspends every org until a full poll, and purges again what KyIdentity reports deleted. Backups keep the wrapped key and copies until their retention ends (residual, question 5). |

## Phased build plan

Each PR is shippable on its own, with single-tenant behaviour unchanged. `KYDRIVE_TENANCY=multi`
is refused at startup until PR 6. Multi-tenant code runs only in tests before then.

Amended 2026-11-22 (D2, D13). The build order is 1 to 4b, then R1 to R4, then K (per-org keys, amended 2026-11-22), then 5 and 6. Every
R step is required before 6 can enable multi mode; none is optional. The R steps keep their
own labels, so the existing references to steps 5 and 6 still hold. SQLite stays unchanged
byte for byte through every R step: a test compares its schema (`sqlite_master.sql`) with
the release before. No R step starts before the owner approves this amendment.

1. **This spec.** Docs only.
2. **Mode, org registry and columns.**
   - Adds `KYDRIVE_TENANCY`, recorded at first start, and `drive_orgs` with the default org.
   - One appended drive schema entry: `org_id` on every **O** table, with the default for
     existing rows, and every trigger above.
   - The table classification lists.
   - Tests: an upgrade test from a database made by the previous release, in which every
     existing store test passes and the same queries return the same rows.
     `TestEveryTableIsClassified` and `TestOrgOwnedTablesHaveOrgID`. A trigger test per
     same-org rule. The full `go test -race ./...`.
3. **Scoping**, in two PRs.
   - 3a, the store: the org predicate in `rankSQL` and `itemRankSQL`, `activePrincipal` on
     every credential path, `org_id` on every insert, and the org filter on every member
     lookup in the Isolation table. Organisation links scoped (D6). A test-only
     constructor builds several orgs. Tests: `TestOrgSQLNamesOrgID` and store-level
     cross-org tests, including one with the triggers dropped.
   - 3b, administration: the admin route classes and `X-Ky-Org`, the staff-org rule for
     `role='admin'` (D9), the refused routes, and the admin directory, audit, leavers and
     handover bound to the selected org. Test: `TestCrossOrgMatrix` over every route.
     As built: the matrix covers every `/api/drive` route with an ID in its path, as an
     org A session and as staff acting in org A. It does not call `/dav`, `/mcp` or the
     other credential types; those reach the same rank and `activePrincipal` checks that
     the 3a store tests cover, which is an argument, not a test. `KYDRIVE_STAFF_ORG` is not read yet: configuration still
     refuses multi-tenant mode, so the staff org reaches the store only from tests until
     step 6.
   - Single-tenant runs the whole existing suite unchanged after each.
4. **Identity binding** (needs KyIdentity step 3).
   - `org_id` from the ID token, the step-up token and the logout token.
   - `org_id` in the `syncauth` webhook receiver, which #154 already moved to `syncauth`
     and SCIM bodies.
   - The `/api/suite/orgs` poller, through a `ky-primitives/suitedir` `Orgs` method (T
     PR 3).
   - Suite groups per org. Inbound SCIM off in multi-tenant mode. Forced auto-provisioning.
   - KyPost `org_id`, which lands with a matching KyPost PR. `docs/KYPOST.md` changes in
     both.
   - Tests: every point of the downstream contract, at every entry point, with the claim
     absent and mismatched. Single-tenant golden callback and webhook behaviour is
     unchanged.
   - As built, in two PRs. 4a: the tokens, the webhook, the org poller, forced
     provisioning, the unique subject index and the installer refusal. 4b: suite groups per
     org, which needs a ky-primitives `suitedir.Groups` that takes an org (until then a
     multi-tenant install does not read suite groups), and KyPost `org_id`. 4a departs from
     the text above in four places:
     - A back-channel logout by `sid` ends that login's sessions whatever org the token
       names. Filtering by org would only let a mismatched token end less, and a logout only
       takes access away. A subject-wide logout checks the org, logs a mismatch as an alert
       and still applies, as above.
     - The unique subject index is created with `IF NOT EXISTS` each time a multi-tenant
       store opens (`recordTenancy`), not by a versioned `initMultiTenant`. A single-tenant
       store never runs it.
     - A webhook from another org answers 400, so KyIdentity does not retry it. A webhook for
       an org not yet registered answers 500, so it is retried until the poll lists the org.
       `user.mfa_reset` applies whatever the org.
     - The poll mirrors status, `enabled` and `storage_bytes`, which `activePrincipal`
       already enforces. The status transaction's audit row (`tenancy.org_status`) and
       editor revocation are step 5's.
   - 4b as built: suite groups per org through `suitedir.OrgGroups` (ky-primitives #73),
     over `ActiveOrgs`, and KyPost `org_id` (KyPost-Server `docs/KYDRIVE.md` already sends
     it). One departure: a member from another org is audited on every pass that lists
     them, not once, since KyIdentity should never list one.
- **R1. PostgreSQL drive store.**
  - `drive.Open` on both drivers (today SQLite only, `internal/store/sqlstore.go:54-60`).
    The drive store's 312 statements use `?` and need the main store's rebind. Its SQLite-only
    SQL needs porting: `julianday` and `strftime` (3 each), one `INSERT OR`, one `PRAGMA`,
    and the trigger DDL, whose 18 value comparisons with `IS NOT` (`tenancy.go`) become
    `IS DISTINCT FROM`.
  - `fits` locks the row its quota check reads (Quotas), since PostgreSQL does not serialise
    writers. A test runs two concurrent uploads with room for one on PostgreSQL, against
    the per-workspace quota.
  - A PostgreSQL drive schema with every table. `org_id` on every **O** table is a foreign key
    to `drive_orgs(id)`. Schema 27's and 28's triggers and `recordTenancy`'s unique subject
    index exist under the same names.
  - The catalogue tests read both backends (Tests). Every drive, tenancy and cross-org test,
    `TestCrossOrgMatrix` included, runs on both, against PostgreSQL in CI.
  - `migrate-postgres` for a single-tenant SQLite install, by the replica pattern: one ordered
    table list (`migrationTables`), in foreign-key order, for migration and the backup
    snapshot; sequences advanced; a restore drill that inserts into every table afterwards.
  - Multi mode stays refused.
  - R1 ships in two PRs: R1a, the drive store on PostgreSQL, then R1b, `migrate-postgres`.
  - R1a as built:
    - `internal/qmark` opens PostgreSQL through pgx, renumbering `?` and `?NNN` and binding a
      Go bool to an integer parameter as 1 or 0. The 312 statements are unchanged.
    - The PostgreSQL schema is created whole at version 28 (`schema_postgres.go`). The
      triggers come from the same generator as SQLite's. A few SQLite functions
      (`julianday`, `unixepoch()`, two-argument `max` and `min`, `printf('%020d')`) are
      PostgreSQL functions there.
    - Departure from the Quotas section: `fits` takes no row lock. Every PostgreSQL transaction
      and statement is serializable (`default_transaction_isolation`, set by `qmark.Open`),
      the main store's included. Every drive and main-store transaction reruns on a
      serialization failure (`qmark.Retry`). This covers the quota check, a credential created
      while a password change or logout commits, and every other read-then-write the one
      SQLite writer protected. A single statement outside a transaction is not rerun.
    - A PostgreSQL start holds a database-wide advisory lock while it migrates and opens the
      store, so processes starting together create the schema once. Step 6 still owns the
      bootstrap administrator and its password file, which are written after the store opens.
    - A name retry inside a transaction runs in a savepoint. Change rows take their seq under a
      per-workspace transaction lock, so a cursor never passes a row that commits later.
    - Not ported until their own steps: content search (R2) and backups (R3) stay SQLite only,
      and their tests skip on PostgreSQL.
  - R1b as built:
    - `migrate-postgres` runs with the SQLite install's settings and reads the target only
      from `KY_MIGRATE_POSTGRES_DSN`. It snapshots the database, upgrades the snapshot,
      creates the schema by opening a store on the target, and copies every table in
      `migrationTables` order in one transaction, comparing row counts and summed row hashes
      before it commits. `-check` rolls the copy back.
    - The rows a store open writes on an empty database (`drive_orgs`, `drive_key`,
      `server_settings`) are replaced by the source's. Change rows keep their seq, and every
      identity and serial sequence is set past the copied rows.
    - The restore drill is `TestMigrateToPostgresKeepsEveryRow`: a source with a row in
      every table, then a new audit row and change row on the copy. Only `audit_records` and
      `drive_changes` take database-made IDs; every other table's keys come from the code.
      The backup snapshot's use of `migrationTables` is R3's.
    - A SQLite server now holds a lock on its database file while it runs (`LockSQLite`), as
      the replica pattern's does, so a migration never copies a database being written.

- **R2. Shared file storage and search.**
  - One storage root outside the data directory for `blobs/`, `staging/`, `view/`, `thumbs/`
    and `text/`. Object storage or a written shared-volume contract is question 13. Either way
    it must provide write-once objects named by random UUID, an atomic publish (rename, or a
    conditional put), read-after-write across hosts, a listing for the orphan sweeps, and
    deletion only after a committed transaction or by the elected worker.
  - The search index tables and the sealed vocabulary move to PostgreSQL (Inventory, Search
    index). Question 14.
  - A single-tenant SQLite install keeps every path under `<data>`, unchanged.
  - Tests: one contract suite run against each storage implementation. Two processes write,
    read and sweep the same root.
- **R3. Replica plumbing**, porting the replica pattern's "Product rollout contract" (a) to (e).
  - `/livez`, and `/readyz` (a database ping, cached for one second, 503 while draining), with
    its drain order on SIGTERM. Today there is `/healthz` (`drive_handlers.go:93`) and a 5 s
    shutdown (`main.go:157`).
  - The shared-configuration fingerprint (`replica_config_fingerprint`, `accept-replica-config`)
    over the base's fields plus KyDrive's own: KySignOn, KyPost, Euro-Office, the storage
    root, the tenancy mode and the staff org.
  - Keys from outside the data directory (D13). Multi mode refuses a minted key.
  - One elected worker (`scheduled-worker`, a session advisory lock) runs every job
    `cmd/server` starts today (`main.go:244-258`): editor revocations, retention and the purge,
    suite groups, the org poll, blob sealing, expiry cleanup, the search indexer and the backup
    schedule. Each replica still resolves the KyPost pairing for itself (`StartKyPost`,
    `main.go:242`), since every replica needs the secret. `orgs_since` is written
    monotonically.
  - The `backup-run` lease for scheduled, manual and CLI runs. The PostgreSQL snapshot backup,
    restore through `migrate-postgres`, and `prepareRestoredDatabase` dropping KyDrive's
    session-like tables and the fingerprint.
- **R4. State in the database**: every row of the in-memory table above (WebDAV locks,
  Euro-Office thumbnail tickets, sign-in and per-IP attempt windows, device-signature nonces,
  spent CAPTCHA challenges, the KyPost replay cache), on PostgreSQL only. SQLite keeps the
  in-memory versions. The expiry sweeps run on the elected worker.
- **PostgreSQL follow-ups for steps already built.** Each was built and tested on SQLite
  only, and R1 or R3 picks it up:

  | Step | Needs |
  |---|---|
  | 3a (#158) | Schema 27's columns and triggers on PostgreSQL, with the foreign keys (R1). `rankSQL`, `itemRankSQL`, `activePrincipal`, `sameOrg`, `userOrg` and `workspaceOrg` use only `EXISTS`, joins, `CASE` and `COALESCE`, so they should port as they are once rebound; R1's tests on both backends prove it. The cross-org store tests, the one with triggers dropped included, run on both (R1). `TestOrgSQLNamesOrgID` is a source scan and needs nothing. The upgrade test stays SQLite only, since a PostgreSQL multi-tenant database is always new. |
  | 3b (#159) | Schema 28's staff triggers on PostgreSQL (R1). `TestCrossOrgMatrix` on both (R1). |
  | 4a (#160) | `recordTenancy`'s partial unique index on PostgreSQL, and `ApplyOrgs` and `OrgsSince` there (R1). The org poll under the elected worker in place of `orgsMu`, with a monotonic `orgs_since` (R3). The sign-in attempt limits it relies on move to R4. |
  | 4b | The suite-group reconcile under the elected worker (R3), and its tests on both backends (R1). |

- **K. Per-org data keys** (D14, multi-tenant only). It merges after R4 and before 5, in the
  build order above. It depends only on R2 (where blobs live), so work on it may start once R2
  merges.
  - `drive_org_keys`, the wrap under the deployment key, and the `kydrive-org` stanza for
    blobs, view copies, staging, thumbnails and text. Name and search keys per org, and the
    replica key cache.
  - The org-wide purge is step 5's, so K adds no purge. Step 5 builds its key step (Lifecycle,
    `deleted` step 5) and the multi-mode restore reset with it. K's own deliverable is the
    sealing, so K ships and is tested on its own.
  - Tests:
    - A blob, cache file and name sealed for org A fail to open under org B's key, or with
      the stanza's org ID changed.
    - A wrapped key copied onto another org's row fails to open.
    - Multi mode refuses a `kydrive` stanza, and single-tenant mode refuses a `kydrive-org`
      one.
    - Single-tenant sealing is byte-compatible with the release before (the upgrade tests),
      and an upgraded single-tenant install has no `drive_org_keys` row.
    - The same name in two orgs gets two `name_hmac` values.
    - Deleting an org's `drive_org_keys` row makes its blobs, caches and names unopenable,
      on every replica once the cache is evicted.
5. **Quotas and lifecycle.**
   - The org pool in `fits`, failing closed.
   - Status enforcement, the retention pause, the editor revocation at suspension, the
     purge job, and the leaver rule (shared files to the account holder, the rest to the
     trash for 90 days).
   - The capacity measurement (question 6), before PR 6 can enable the mode.
   - Amended 2026-11-22: built after R1 to R4, on both backends. The org pool check locks the
     org's row on PostgreSQL (Quotas). The purge and the leaver job run on the elected worker.
   - Amended again 2026-11-22: built after K. The purge's step 4 deletes the index by org ID
     and its step 5 destroys the org key (D14). The multi-mode restore reset suspends every
     org until a full poll. Its tests:
     - after a purge, a blob left on the storage on purpose cannot be opened;
     - the org's index rows and vocabulary are gone, with the key already destroyed;
     - a restore serves no org before the first full poll, and purges again an org the poll
       reports `deleted`.
   - Tests: the quota on every path in the Quotas table, plus the source scan. Two parallel
     uploads with room for one. `TestSuspendedOrgServesNothing` for every credential type.
     Resume restores long-lived credentials. The purge leaves no row, blob, thumbnail, text
     entry, staging directory or index ref for the org, which is checked against the
     classification. The purge is killed halfway and rerun. A leaver's shared files go to the
     holder with every person and group share and organisation link still working for the
     same people, including access inherited from a shared parent folder, and their guest
     links revoked. A file shared only by guest link goes to the trash with the rest, and
     the trash copy is purged at 90 days. An org suspended during those 90
     days purges nothing until it is active again and past `retention_after`. A member
     deactivated before deletion has the same files moved as one deleted while active. A member
     who drops out of a group list, and a poll that fails or is truncated, move and trash
     nothing. An org that goes straight from `active` to `deleted` has its editor sessions revoked before any row is deleted. A write into a deleted org is refused by the trigger.
6. **Enable multi-tenant mode.** Startup accepts `multi` on an empty database with
   `KYDRIVE_STAFF_ORG`, and seeds that org's row before bootstrap (Local accounts). It also
   requires a replica-capable install (D13): the PostgreSQL driver, the shared storage
   configured and reachable, both keys outside the data directory, and the R3 and R4
   mechanisms in place. Otherwise it refuses to start and names each missing piece.
   - First start is serialised. Replicas usually start together on the empty database, so
     the steps that record `tenancy_mode` and `staff_org`, seed the staff org's row and create
     the bootstrap administrator run in one transaction under a database-wide advisory lock.
     The replica pattern already serialises migrations and initial admin creation this way.
     A replica that gets the lock second finds the records and the account, checks that its
     own mode and staff org match them (refusing to start if they differ), and writes no
     password file. The replica that created the account logs that it wrote
     `initial-admin-password`, so the operator knows which data directory holds it. The admin UI gains the org selector and staff audit views. Operator
   docs: `INSTALL.md`, `docs/DEPLOYMENT.md`, `docs/PERMISSIONS.md`, and
   `docs/ASSURANCE-CASE.md`, whose trust boundaries change here.
   - Tests: a **two-replica test**: two server processes, one PostgreSQL database and the
     shared storage. It signs in on one replica and uses the session on the other, uploads on
     one and downloads on the other, and checks that a WebDAV lock taken on one refuses a
     write on the other. A KyPost event, a device signature and a CAPTCHA spent on one
     replica are each refused on the other, and a sign-in window filled on one holds on the
     other. When the elected worker is killed, the other takes its jobs within a minute. A
     replica with another shared setting refuses to start, and SIGTERM drains one replica
     while the other keeps serving. Also a start refused for each missing piece of D13. The two
     replicas also start at once on an empty database, and the test expects one mode record, one
     staff org row, one bootstrap administrator and one password file.
   - The browser flow for staff org selection (`UI-VERIFICATION.md`). An end-to-end
     run against a KyIdentity in multi-tenant mode (unverified: whether one can run in CI
     before KyIdentity PR 6).

## Residual risks (accepted)

- **One database, one Euro-Office and one elected worker for every org.** Replicas spread
  requests, not database writes, editor sessions or indexing, so a busy org can still slow the
  others. Capacity is unmeasured (question 6).
- **Application replicas are not database high availability.** Every replica uses one
  PostgreSQL database. When it is down, every replica is unready. Its replication and failover
  are the operator's (the replica pattern).
- **A capsule restore rolls back every org.** There is no per-org restore.
- **No export after deletion is scheduled.** A member who did not download their files
  before then cannot get them (question 1). The only warning is the billing service's,
  before it schedules deletion (Export).
- **Backups outlive deletion.** A deleted org's blobs and rows stay in capsules and Restic
  snapshots until backup retention drops them. So does its wrapped key: anyone with a capsule
  and the custodian shares can still read the org. Per-org keys make everything outside the
  backups unreadable (D14), not the backups. Restic bulk pruning is opt-in and off by
  default (root `AGENTS.md`).
- **Stale status.** If `/api/suite/orgs` cannot be read, KyDrive keeps the last status it
  saw, and alerts the operator after 10 minutes. A suspension is then late until the next
  successful poll (question 4).
- **Shared pair secret.** A stolen KyPost pair secret reaches every org's users whose
  subjects it can name, though only the three peer actions.
- **Staff see directories and workspace names**, never file names or content.
- **The search index** (a file on SQLite, tables on PostgreSQL) shows word-frequency and
  co-occurrence patterns to whoever reads it, as it does across users today (root
  `AGENTS.md`). In multi mode its term hashes are keyed per org (D14), so the patterns stay
  within each org and cannot be linked across orgs.
- **Euro-Office and the server read plaintext**, as today, outside end-to-end vaults (D15).

## Questions, settled by the owner on 2026-11-20

1. **Export while deletion is scheduled.** Members export their own files while the org is
   active. The deletion window is short, and there is no staff export (Export).
2. **How KyDrive knows the staff org.** `KYDRIVE_STAFF_ORG`, recorded at first start and
   immutable like the mode.
3. **Linked computers when an org is deleted.** Revoked without a wipe: the customer keeps
   their own copy.
4. **`/api/suite/orgs` unreachable.** Keep the last status, and alert the operator after 10
   minutes. Refusing credentials in every org would turn a KyIdentity outage into a KyDrive
   outage for every customer.
5. **Deleted orgs in backups.** Accepted. The hosted install must run bulk retention so
   copies age out. Amended 2026-11-22: per-org keys destroyed at purge (D14, step K) make
   every copy outside the backups unreadable. The backups themselves keep the wrapped key until
   their retention ends, because the KEK is the deployment key, which every capsule carries.
6. **Capacity.** Measure a target (orgs, stored bytes, writes a minute, concurrent editor
   sessions) before launch. Amended 2026-11-22: measure on the replica deployment of step 6.
   D2 is no longer the fallback.
7. **Staging bytes outside the quota.** Accepted, given the bounds in Quotas.
8. **What an organisation link is called on the hosted service.** "Household link" (2026-11-20). Reopened by the 2026-11-23 plans: question 16.
9. **Webhook compatibility.** Checked: the two sides do not interoperate (Signed webhook).
   Fixed ahead of PR 4 by #154, in both modes.
10. **Suite group poll cost.** Measure first, as part of question 6, before asking
    KyIdentity for one request across orgs.
11. **Per-workspace retention on the hosted service.** Stays refused (D10). Revisit only if
    customers ask, Business teams included.
12. **A member's My files after they leave** (every plan). Shared files move to the account
    holder with their person and group shares and organisation links kept. Guest links are
    revoked, and a file shared only with guests counts as unshared. The rest goes to the
    trash and is purged after 90 days (A member leaves).
    This needs the account holder's `sub` from `/api/suite/orgs`, a new ask of KyIdentity.

## Questions opened by the 2026-11-22 amendment

13. **Shared file storage (R2).** Either object storage (S3-compatible: conditional puts, no
    rename) or a written shared-volume contract (for example NFS or CephFS: atomic rename
    within the volume, durable fsync, close-to-open consistency). Recommendation: object
    storage.
    - **Cost**, the owner's input of 2026-11-23: object storage costs about $0.007/GB a month
      (Backblaze B2), against about EUR 0.057 (Hetzner volumes) or $0.10 (DigitalOcean) for
      block storage, 8 to 15 times more. Storage is the main cost per account.
    - **Fit:** blobs are already write-once, so a put fits. A shared volume's guarantees vary
      by filesystem and mount options, and nothing in KyDrive can check them at startup.
    - **Unverified:** the latency of one put per staging segment (one request body, up to
      64 MiB, `drive_uploads.go:15`).
    - The owner has not chosen yet.
14. **The search index on replicas (R2).** Either PostgreSQL tables written by the elected
    indexer, or a local index on each replica, rebuilt from the database. Recommendation:
    PostgreSQL. A local index on each replica indexes every file once per replica, and reports
    different `pending` counts on different replicas.

## Questions opened by the 2026-11-23 plans

15. **May a member create a shared workspace on the hosted service?** Today D10 allows only
    suite group workspaces there. A Business team, or a family, then gets a shared folder only
    from a KyIdentity group that staff or the billing portal make. Recommendation: let any
    member create one in their own org and manage it. The org's pooled quota (D7) still bounds
    it, and no staff curation is involved. Default until answered: D10 as written.
16. **What an organisation link is called.** Recommendation: by the org's kind from
    `/api/suite/orgs`: "Team link" for Business, "Household link" for Couple and Family, and no
    organisation links on a Personal org of one, where the link would reach only its owner.
    That needs KyIdentity's `business` kind in the poll (D16). The alternative is one neutral
    name for every plan, such as "Members link".
