# KyDrive tenancy: single-tenant and multi-tenant (SaaS), design

Date: 2026-11-20. Status: design approved, with the owner's answers to its questions (2026-11-20). Nothing built.

KyDrive today serves one organisation per install. This note adds a second mode to the same
binary: **multi-tenant**, one shared install for every individual and household on the hosted
service, with data separated by organisation (org). **Single-tenant** is today's behaviour,
and it does not change for existing installs.

Owner decisions (fixed, not reopened here):

1. One binary, both modes. Single-tenant stays unchanged on upgrade. Multi-tenant is a mode.
2. Organisations under one sign-in: one KyIdentity issuer for every customer. Each customer
   (a person or a household) is an org, and tokens carry an opaque `org_id`. KyDrive runs as
   one shared multi-tenant install. Per-customer realms were rejected.
3. One org per account. There is no org switcher.
4. No administrators inside customer orgs. Only our staff are administrators, and customers
   are members. Single-tenant installs keep their own administrators.
5. Businesses don't use the shared service. Each gets its own single-tenant install, set up
   by hand.
6. Stripe lives only in a separate billing service, built later. KyDrive has no billing code.

Line references are to KyDrive `origin/master` at `cb492e0`. **T** is KyIdentity's tenancy
spec, `docs/superpowers/specs/2026-11-19-tenancy-design.md` at `origin/master` `2e304a4`.
"Unverified" marks a claim this note did not check in code or by running it. A bare file
name is the only file of that name in the repository, except `store.go`, `devices.go`,
`guest.go` and `handover.go`, which mean the `internal/drive` file unless a path is given.
`AGENTS.md` alone is the root one.

KyIdentity's step 3 (`org_id` in tokens, userinfo and logout tokens, signed webhooks and
`/api/suite/orgs`, T "Phased build plan") has not landed. Nothing below can be built past
PR 3 until it does.

## Summary

| | Single-tenant (self-hosted, business platforms) | Multi-tenant (hosted SaaS) |
|---|---|---|
| Mode | `KYDRIVE_TENANCY=single` (default) | `KYDRIVE_TENANCY=multi`, fresh database only |
| Database | SQLite, one writer (root `AGENTS.md`, "One SQLite writer") | the same: SQLite, one writer, one instance (D2) |
| Orgs | one default org, never named in tokens (T D4) | one per customer, mirrored from `GET /api/suite/orgs` (D3) |
| Accounts | SCIM from KyIdentity, sign-in links by `sub` (`docs/PERMISSIONS.md:9,13`) | created at first sign-in from the verified token, kept current by the signed webhook (D5) |
| Administrators | local recovery admin and directory accounts given the role (`docs/PERMISSIONS.md:32`) | staff accounts only (D9) |
| Shared workspaces | created by administrators or from suite groups | from suite groups only (D10) |
| Organisation links | anyone signed in to the install | anyone signed in to **the link's org** (D6) |
| Guest links | unchanged | unchanged: the only deliberate cross-org path (D6) |
| Quota | per workspace (`drive_workspaces.quota`, `store.go:98`) | one pool per org, `storage_bytes` from `/api/suite/orgs` (D7) |
| Blob layout | `<data>/blobs/<uuid>` (`drive_handlers.go:362`) | unchanged (D8) |

## Decisions

| # | Decision | Why |
|---|---|---|
| D1 | `KYDRIVE_TENANCY=single` (default) or `multi`, recorded in `server_settings` at first start. A later start with the other value refuses to start. `multi` is accepted only on an empty database. Converting a single-tenant install is out of scope. | Install-time, as for KyIdentity (T D1). An empty database avoids rewriting existing rows, and lets the multi-only DDL (see Triggers, "Mode-only DDL") be created on empty tables. |
| D2 | Multi-tenant stays on SQLite, with one writer and one instance. No PostgreSQL. | KyDrive's production storage is SQLite only (root `AGENTS.md` "One SQLite writer"; `internal/store/AGENTS.md` "Production drive storage is SQLite-only"). Blobs live on one local filesystem. A second backend is a separate project. Capacity is question 6. |
| D3 | KyDrive keeps a local org registry, `drive_orgs(id, status, revision, enabled, storage_bytes, purged, seeded, retention_after)`, filled from `GET /api/suite/orgs` (T "Suite directory"), with one exception: the staff org's row, seeded from `KYDRIVE_STAFF_ORG` and never overwritten by the poll (Local accounts). Polled about every 60 s with `since=<revision>`. Single-tenant has one row, the default org, with the fixed ID T uses (`00000000-0000-0000-0000-000000000001`), `active`, unlimited. A multi-tenant database has no default org row (see Triggers). | The org's status and quota must be readable inside the same SQL statement that authorizes a request. The default org gives single-tenant one code path. |
| D4 | Every org-owned table gets `org_id TEXT NOT NULL`. SQLite triggers make it immutable, require it to exist in `drive_orgs`, and refuse a row that links two orgs (shares, grants, group members, link openers, personal-workspace owners). Isolation is enforced in the application, and the triggers are a second layer. | T D3 makes the same choice. KyDrive has one SQL dialect, and every access decision already goes through `rankSQL` and `itemRankSQL` (`access.go:17,33`), so adding one org predicate there covers most paths. The triggers catch a path that forgets it. |
| D5 | In multi-tenant mode, inbound SCIM is off (`/scim/v2` answers 404). A local account is created at first sign-in from the verified ID token's `sub` and `org_id`, so auto-provisioning is forced on. The signed webhook keeps accounts current. Groups come only from suite groups. | KyIdentity turns generic outbound SCIM targets off in multi-tenant v1 (T "Per-org features"), and SCIM bodies carry no org. A household has nobody to pre-provision accounts by hand. |
| D6 | Organisation links (`scope='org'`) become org-scoped: only an active member of the link's org can open one. Guest links (`scope='guest'`) stay as they are: they are the only deliberate way for content to leave an org. | Today an organisation link gives "any active signed-in user" its role (`links.go:14-16`). On a shared install that means every customer. |
| D7 | One storage pool per org: the sum of every workspace's version bytes and viewing copies must stay within `quotas.storage_bytes` from `/api/suite/orgs`. It is checked in `fits` (`store.go:682`), the one function every byte-adding path already calls. Per-workspace `quota` is not enforced in multi-tenant mode. A missing or unreadable quota refuses writes. | Household plans are one allowance shared by its people. Every path that adds bytes already ends in `fits` (see Quotas). Failing closed means a polling bug cannot hand out unlimited storage. |
| D8 | Blob storage stays flat (`<data>/blobs/<uuid>`). A trigger on `drive_versions` refuses a version that names a blob already named by a version in another org. | Blob IDs are fresh random UUIDs (`blobs.go:21`), with no content addressing, so there is no dedup across orgs. A blob is only reachable through a version row. The one path that shares a blob, copy (`move.go:131-132`), already needs ranks in both workspaces. A per-org directory would add an org argument to every `BlobPath` call and protect nothing the trigger doesn't. |
| D9 | In multi-tenant mode, the store refuses `role='admin'` for any account outside the staff org, which is `KYDRIVE_STAFF_ORG` recorded at first start (question 2). Admin routes are classed as `platform`, `org` or `refused` (see Admin surface). | Owner decision 4, and T D8. |
| D10 | In multi-tenant mode, shared workspaces exist only as suite group workspaces (`suitegroups.go:57`). Creating, renaming, deleting and re-quotaing a shared workspace through the admin API answers 404. | Customers have no administrator to create workspaces. Suite groups give households shared folders, and staff should not curate customer folders. |
| D11 | An org whose `drive_orgs.status` is not `active`, or whose `enabled` is false, is served nothing, by any credential. Its data is kept, and retention, version pruning and workspace purge pause for it. Only `deleted` triggers a purge. | T downstream contract point 5. A trash age must not delete a suspended customer's files while they settle a bill. |
| D12 | The org comes from exactly three places: the verified ID token (`org_id`), a `syncauth`-verified webhook body (`org_id`), and a suite directory answer. One named exception: the local bootstrap and recovery administrators take the staff org from operator configuration (`KYDRIVE_STAFF_ORG`), since no token exists when they are created. Every KyDrive-issued credential (session, app password, agent token, device token, service token, guest link, upload ticket, editor token) takes its org from its user or workspace row, never from the request. | T downstream contract point 4. |

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

### In-memory state

| State | Defined | Class |
|---|---|---|
| WebDAV lock table | `dav/locks.go:22-33` | Keyed by workspace ID, not by display name, so two orgs' `/My files/a.txt` never meet |
| KyPost replay cache | `kypost.go:105` | P, keyed by event ID |
| Rate-limit buckets, PoW salts | `server.go:304-326`, `internal/auth/AGENTS.md` | P, keyed by IP, account and route |
| Search vocabulary | `search/vocab.go` | X, above |

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
   | Workspace name free | `workspaces.go:17-29` | Install-wide | Per org. Otherwise two households' suite groups called "Family" become "Family (2)", which reveals that another org has one. |
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
   ID.

A cross-org ID then fails at the same point, and for the same reason, as a random one.
Today a missing item is usually `ErrDenied`, which is 403 (`move.go:38-39`,
`drive_handlers.go:111-113`), and sometimes `ErrNotFound`, which is 404 (`links.go:131`).
The test below compares the two answers instead of assuming 404.

### Triggers (second layer)

Created by one drive schema entry (appended to `schema.go:15`, append-only):

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

- `TestEveryTableIsClassified` reads `sqlite_master` and requires each table to appear in
  exactly one of `orgOwnedTables`, `derivedTables` (each with its parent FK) or
  `platformTables`. A new table with no class fails the build (as in T).
- `TestOrgOwnedTablesHaveOrgID` requires `org_id NOT NULL` and both triggers on every **O**
  table. `nullableOrgID = {"audit_records", "drive_events"}` are the only exceptions.
- `TestOrgSQLNamesOrgID` is a go/ast scan of SQL string literals in `internal/drive`,
  `internal/store` and `internal/api`. A literal that names an **O** or **D** table must
  also name `org_id`, call `rankSQL`, `itemRankSQL` or `activePrincipal`, or be on an
  allowlist (pre-org lookups by token hash, platform workers) with a reason.
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
- The owner chose to fix this ahead of PR 4, in both modes: a separate PR makes the receiver
  verify with `syncauth.Verify` and read the SCIM resource, as KyMessage's receiver does.
- Multi-tenant (PR 4) then adds `org_id`, which T authenticates through the signature (T
  "Outbound sync").
  - No `org_id`: 400.
  - A known `sub` with another org: refused and audited (point 3).
  - `user.created` for an unknown `sub`: creates the account in that org.
  - Event ordering is as the fix PR leaves it (`sso` `AGENTS.md`).

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

UI copy: in multi-tenant mode an organisation link is called a **Household link** (owner,
question 8). "Shared link" was considered and rejected: guest links are shared links too, so
it would suggest the link reaches people outside the household.

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
  SQLite's one writer serialises check and insert, so two uploads cannot both take the last
  bytes. The org check uses the org's net change, not the bytes written to the target
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
    are the household's group names.
- **Handover** is the one content-moving staff action. It runs only within the selected org
  and is audited in that org's log. A leaver's files are normally handled automatically (see
  "A member leaves").

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

A purge job runs from the hourly retention pass (`drive_retention.go:62`). It is idempotent:
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
   rebuild is requested, so the org's words leave the sealed vocabulary.
5. `drive_orgs` keeps the tombstone (`status='deleted'`, `purged=1`), and a platform audit
   row records the org ID and row counts per table, with no names.

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

Owner decision (question 12). When KyIdentity deletes a member's account (a `user.deleted`
webhook, or the member gone from the suite directory), KyDrive acts on that member's My
files at once, inside the org:

- **Shared files move to the account holder.** An item is shared if it, or a folder above
  it, has a live share to a person or group, a Household link or a guest link. Each shared
  item moves with its subtree into one "Handover from <owner> <date>" folder in the account
  holder's My files, as `Handover` does today (`handover.go:83-85`). Unverified: whether
  existing shares and links on a moved item keep working after the move.
- **Everything else goes to the trash** and is purged 90 days later, whatever the drive's
  trash age. Until then staff can restore it on request.
- A deactivated (not deleted) member is left alone, since KyIdentity can re-enable them.
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
  cannot sign in, covers KyIdentity's data only. The deletion banner and notices must say
  files have to be downloaded before deletion is scheduled.

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
| Customer of org A | Slow other orgs down: one SQLite writer, one Euro-Office, one indexer | Existing per-user and per-IP rate limits only. Residual (question 6). |
| Staff, or a stolen staff account | Read customer content | No admin route opens an item, and administration grants no rank (`access.go:16`). Handover stays within one org and is audited in that org's log. Staff do see the org directory and workspace names (residual, as in T). |
| Operator | Anything | Trusted with the database, the deployment key and the host. The server reads plaintext (root `AGENTS.md`). Residual, unchanged. |
| Deleted customer | Have their data kept | The purge removes rows, blobs, caches and index refs, and rebuilds the vocabulary. Backups keep copies until their retention ends (residual, question 5). |

## Phased build plan

Each PR is shippable on its own, with single-tenant behaviour unchanged. `KYDRIVE_TENANCY=multi`
is refused at startup until PR 6. Multi-tenant code runs only in tests before then.

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
3. **Scoping.** The org predicate in `rankSQL` and `itemRankSQL`, `activePrincipal`, and the
   org filter on every lookup in the Isolation table. Organisation links scoped (D6). The
   admin route classes and `X-Ky-Org`, the staff-org rule for `role='admin'` (D9), and the
   refused routes. A test-only constructor builds several orgs.
   - Tests: `TestOrgSQLNamesOrgID` and `TestCrossOrgMatrix`. Single-tenant runs the whole
     existing suite unchanged.
4. **Identity binding** (needs KyIdentity step 3).
   - `org_id` from the ID token, the step-up token and the logout token.
   - `org_id` in the `syncauth` webhook receiver, which the separate fix PR has already
     moved to `syncauth` and SCIM bodies.
   - The `/api/suite/orgs` poller, through a `ky-primitives/suitedir` `Orgs` method (T
     PR 3).
   - Suite groups per org. Inbound SCIM off in multi-tenant mode. Forced auto-provisioning.
   - KyPost `org_id`, which lands with a matching KyPost PR. `docs/KYPOST.md` changes in
     both.
   - Tests: every point of the downstream contract, at every entry point, with the claim
     absent and mismatched. Single-tenant golden callback and webhook behaviour is
     unchanged.
5. **Quotas and lifecycle.**
   - The org pool in `fits`, failing closed.
   - Status enforcement, the retention pause, the editor revocation at suspension, the
     purge job, and the leaver rule (shared files to the account holder, the rest to the
     trash for 90 days).
   - The capacity measurement (question 6), before PR 6 can enable the mode.
   - Tests: the quota on every path in the Quotas table, plus the source scan. Two parallel
     uploads with room for one. `TestSuspendedOrgServesNothing` for every credential type.
     Resume restores long-lived credentials. The purge leaves no row, blob, thumbnail, text
     entry, staging directory or index ref for the org, which is checked against the
     classification. The purge is killed halfway and rerun. A leaver's shared and unshared
     files go to the holder and the trash, and the trash copy is purged at 90 days. An org that goes straight from `active` to `deleted` has its editor sessions revoked before any row is deleted. A write into a deleted org is refused by the trigger.
6. **Enable multi-tenant mode.** Startup accepts `multi` on an empty database with
   `KYDRIVE_STAFF_ORG`, and seeds that org's row before bootstrap (Local accounts). The admin UI gains the org selector and staff audit views. Operator
   docs: `INSTALL.md`, `docs/DEPLOYMENT.md`, `docs/PERMISSIONS.md`, and
   `docs/ASSURANCE-CASE.md`, whose trust boundaries change here.
   - Tests: the browser flow for staff org selection (`UI-VERIFICATION.md`). An end-to-end
     run against a KyIdentity in multi-tenant mode (unverified: whether one can run in CI
     before KyIdentity PR 6).

## Residual risks (accepted)

- **One writer, one disk, one editor for every org.** A busy org slows every other org's
  writes, editor sessions and indexing. Capacity is unmeasured (question 6).
- **A capsule restore rolls back every org.** There is no per-org restore.
- **No export after deletion is scheduled.** A member who did not download their files
  before then cannot get them (question 1).
- **Backups outlive deletion.** A deleted org's blobs and rows stay in capsules and Restic
  snapshots until backup retention drops them. Restic bulk pruning is opt-in and off by
  default (root `AGENTS.md`).
- **Stale status.** If `/api/suite/orgs` cannot be read, KyDrive keeps the last status it
  saw, and alerts the operator after 10 minutes. A suspension is then late until the next
  successful poll (question 4).
- **Shared pair secret.** A stolen KyPost pair secret reaches every org's users whose
  subjects it can name, though only the three peer actions.
- **Staff see directories and workspace names**, never file names or content.
- **The search index file** shows word-frequency and co-occurrence patterns across orgs to
  whoever reads it, as it does across users today (root `AGENTS.md`).
- **Euro-Office and the server read plaintext**, as today.

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
   copies age out. Per-org keys destroyed at purge (crypto-shredding) stay a later design:
   today's sealing derives every key from one deployment key (`sealed.go:247`).
6. **Capacity.** Measure a target (orgs, stored bytes, writes a minute, concurrent editor
   sessions) before launch. Revisit D2 only if the measurement fails.
7. **Staging bytes outside the quota.** Accepted, given the bounds in Quotas.
8. **What an organisation link is called on the hosted service.** "Household link".
9. **Webhook compatibility.** Checked: the two sides do not interoperate (Signed webhook).
   Fixed ahead of PR 4 in a separate PR, in both modes.
10. **Suite group poll cost.** Measure first, as part of question 6, before asking
    KyIdentity for one request across orgs.
11. **Per-workspace retention on the hosted service.** Stays refused (D10). Revisit only if
    households ask.
12. **A household member's My files after they leave.** Shared files move to the account
    holder, and the rest goes to the trash and is purged after 90 days (A member leaves).
    This needs the account holder's `sub` from `/api/suite/orgs`, a new ask of KyIdentity.
