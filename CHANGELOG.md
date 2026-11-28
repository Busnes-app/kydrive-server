# Changelog

Interface and behaviour changes, newest first.

## Unreleased

- **Vault recovery key (server).** Signed native devices register a recovery key and an
  encrypted copy of their vault key, fetch their own copy, rotate the recovery key, and replace
  their vault key with a recovery-signed request. Managers re-add a replaced member with a
  `rekey` record. Attempts are rate-limited and audited. Drive schema 36.

- **Native vault protocol.** A signed linked device can read its vault account and
  organization scope. The native client and browser staging fixture share the signed
  history, revision and editor binding implementation.

- **Vault Office co-editing.** Linked staging browsers can share live CryptPad edits
  in DOCX, XLSX and PPTX and save a signed encrypted revision through scoped rooms
  and quota-accounted checkpoints.
- **Vault browser staging.** An explicitly configured editor origin can link a vault-only
  browser, seal its keys locally, share an encrypted vault with a verified member, and
  rotate its key on removal. The proof exercises signed revisions and scoped room sessions.

- **Vault control-plane groundwork.** An opt-in signed-device API commits shared vault
  membership and key rotation. A separately operated HTTPS witness checkpoints the committed
  log without receiving member keys or file data. Signed devices stream ciphertext revisions
  and publish per-member room capabilities. Short-lived relay sessions check their binding
  and device at forwarding time. A standalone staging gateway protects WebSocket, history
  and signed encrypted-blob reads on a private relay network.

- **Hosted storage, cells and desktop identity (test builds).** Device linking and account
  lookup return exact KyIdentity issuer/subject and the registered org alongside legacy local
  IDs. Hosted startup wires `KYDRIVE_S3_*`, requires PostgreSQL and `KY_CELLS`, and stores
  sealed blobs, caches and upload segments under cell/org prefixes. Content search uses
  per-org sealed snapshots on S3, with shared generation-aware queues and CAS publication;
  erasure removes every nested org object and destroys its key. Single-tenant local blob and
  search formats stay unchanged. Release multi-tenant startup remains closed; this enables
  fixture qualification, not production hosted readiness.


- **Per-org data keys (multi-tenant, tenancy step K).** Each org has its own random data key,
  stored only wrapped under a key derived from the deployment key. Blobs, viewing copies,
  upload staging, thumbnails, text caches and file and folder names are sealed under it, so
  one org's bytes and names never open under another's key. The org purge destroys the key,
  so whatever the purge missed, such as a copy the object storage kept, can no longer be
  read. Single-tenant installs keep deployment-key sealing, unchanged byte for byte. Hosted content
  search uses the per-org sealed index described above. A multi-tenant test
  database or capsule made before this change is refused at startup; recreate it.

- **Org purge and Delete everything (multi-tenant, tenancy step 5).** An org KyIdentity
  reports deleted is purged after the next poll: every row and blob it held goes, one
  workspace at a time, and only its tombstone and a platform event with counts stay. A member's
  Delete everything hands each shared item in their My files, with its subtree and shares, to
  the active member with the strongest access to it (then the longest-standing), and deletes
  everything else of theirs, account included; it waits while the org is not active. A restore
  replays erasures, since the erased list is read again from the start.
- **The org storage pool (multi-tenant, tenancy D7).** In multi mode a write that adds bytes
  fits only within the org's unexpired grant from KyIdentity (`POST /api/suite/storage`),
  checked in the write's transaction; the per-workspace quota is not enforced there. Moves,
  handovers and anything that frees bytes always pass, so an org over its pool can still tidy
  up. The elected worker reports each org's drive bytes (versions plus viewing copies) and
  renews grants; a grant lasts at most 15 minutes from when its report was frozen. A stale
  answer (a restored database) stops that org's reports until `storage-seq` advances its
  sequence. Single-tenant quotas are unchanged; multi startup stays refused.
- **Multi-tenant PostgreSQL backups and restore (tenancy R3).** A multi-mode deposit
  snapshots PostgreSQL from one read-only transaction into the capsule's usual SQLite
  member, recording its tenancy; `restore-reset` and `migrate-postgres` keep that tenancy, so
  a capsule restores into an empty PostgreSQL database row for row. Single-tenant SQLite and
  PostgreSQL backups are unchanged; multi startup stays refused.

- **One replica runs the jobs and one backup runs at a time (tenancy R3).** In multi-mode
  PostgreSQL only the `scheduled-worker` lock holder runs background jobs, and a standby
  takes them over when it loses its session; scheduled, manual and CLI deposits take the
  `backup-run` lease, and a second is refused as in progress. The pool keeps at least three
  connections in multi mode. Single mode is unchanged; multi startup stays refused.

- **PostgreSQL session-lock primitive (tenancy R3 groundwork).** Internal replica jobs
  can reserve one session for exclusive named ownership; session loss cancels their
  context and every exit discards the connection. Single-mode calls are refused.
  Scheduled jobs and backup runs have not adopted it yet; multi startup stays disabled.

- **Shared configuration checks (tenancy R3 groundwork).** Multi-mode PostgreSQL replicas
  compare keyed fingerprints before bootstrap; explicit `accept-replica-config` records
  changed field names with its update atomically. Retained deployment/device keys and the
  tenancy/staff identity cannot be rotated this way. Single-mode startup is unchanged.
  Multi startup remains refused; shared-storage wiring and remaining R3/R4 gates are pending.

- **Org polls never rewind their shared cursor (tenancy R3).** A stale concurrent response
  can apply newer org rows but cannot lower `orgs_since`. Cursor reads/comparisons use
  64-bit integers on PostgreSQL as on SQLite. Page rows and cursor still commit together;
  single mode and the closed multi startup gate are unchanged.

- **Replica key loading (tenancy R3/D13).** Multi-mode configuration now requires retained
  external deployment/device keys and refuses local key copies before creating any files.
  Missing, corrupt or public identity seeds are never replaced. Multi startup remains
  disabled; single-tenant key creation and upgrades are unchanged.

- **Hosted members can export My files during scheduled deletion (tenancy groundwork).**
  A verified KyIdentity `export_only` claim opens a separate session of at most one hour,
  for existing members only. Its only product route, `GET /api/export`, downloads a zip of
  their own files, trash and retained versions with a hierarchy manifest. Ordinary access,
  shared-workspace access, editing and credential creation stay refused. Single-tenant
  sign-in and routes are unchanged; multi-tenant startup remains disabled until its other gates pass.

- **`/livez` and `/readyz` (tenancy R3).** `/livez` answers `{"status":"ok"}` without
  touching the database: use it for restarts. `/readyz` answers `{"status":"ready"}` while
  the server isn't stopping and the database answered a ping, and 503 `{"status":"unready"}`
  otherwise. The ping result is cached for one second. On SIGTERM, `/readyz` turns 503 and the
  server keeps serving for 5 more seconds, so a load balancer takes it out before requests
  fail; a stop therefore takes 5 seconds longer. The Compose healthcheck and the Kubernetes
  probes now use them. `/healthz` is unchanged.
- **Back-channel logout tokens act once.** A token must carry a `jti`; one already received
  answers 200 and changes nothing. On a multi-tenant install a logout by session ID ends only
  sessions in the token's org. KyIdentity's tokens already carry a `jti`.
- **PostgreSQL: a write outside a transaction reruns on a serialization failure.** Before, two
  requests updating one row at once, such as two completions of one upload, could fail one with
  a database error instead of its normal answer. SQLite is unchanged.
- **`migrate-postgres` moves a single-tenant SQLite install to PostgreSQL (tenancy R1b).** It
  copies every table into an empty database in one transaction and checks each table's rows
  and content before it commits; `-check` rolls the copy back. The target DSN comes only from
  `KY_MIGRATE_POSTGRES_DSN`. A SQLite server now holds a lock on its database
  (`ky_server.db.lock` beside it) for as long as it runs, so a second server or a migration
  on the same file is refused. Backups and content search still do not run on PostgreSQL
  (R3, R2). Steps: [Deployment](docs/DEPLOYMENT.md#moving-to-postgresql).
- **The drive store opens on PostgreSQL (tenancy R1a).** Before, a PostgreSQL install had no
  drive. Now it gets the full drive schema at first start, with the same triggers as SQLite.
  Every test runs on both backends in CI, apart from content search and backups, which stay
  SQLite only until steps R2 and R3. Single-tenant only: multi-tenant mode still cannot be
  enabled. A SQLite install is unchanged.

- **Suite groups and KyPost requests are bound to their org (tenancy groundwork).** On a
  multi-tenant install KyDrive reads suite groups once per active, enabled org, and each
  group's workspace, local group and members stay in that org: a member from another org is
  skipped and audited, and a workspace name is free per org. A signed KyPost request must name
  the account's `org_id` (else 400 `bad_body`), and an account in another org answers 404
  `no_user` and is audited. A single-tenant install reads suite groups as before and ignores
  `org_id` from KyPost. Multi-tenant mode still cannot be enabled.
- **Menus, dialogs, tabs and search run on Base UI (`@base-ui/react`, pinned, unstyled).**
  The ky-ui look is unchanged; keyboard and focus handling are no longer hand-rolled. Style CSP
  needs nothing new. The main bundle grows by about 57 KB gzipped.
  - Row and New menus flip or shift to stay inside the window. A mouse click opens a menu on
    the menu rather than its first item; Enter, Space and the arrows still start on an item.
  - Dialogs focus their `autoFocus` field (before, the close button took focus), return focus
    to whatever opened them on Escape, and lock the page behind them. A backdrop click still
    does not close one. Quick look and About remain native dialogs.
  - Server settings and workspace views are tabs: arrows move between them, Enter or Space
    opens one, and each panel is labelled by its tab.
  - The search box is a combobox: Down moves into the results, which are options, and Enter
    opens the highlighted one. The type chips are one tab stop that the arrows move along.

- **Sign-in, step-up, logout and the directory webhook bind accounts to their org (tenancy
  groundwork).** On a multi-tenant install, KyIdentity's `org_id` claim and webhook field are
  required. A sign-in for an unknown org polls KyIdentity's org list once, and an org that is
  not active and enabled signs nobody in or up. An account is created at first sign-in in the
  token's org, whatever `KY_SSO_AUTO_PROVISION` says, and a known account arriving with
  another org is refused and audited as `tenancy.org_mismatch`. KyDrive mirrors KyIdentity's
  orgs every 60 seconds, including each org's `storage_bytes` quota. A KyIdentity subject is
  one account there, the installer channel is refused, and suite groups are not read until
  they can be read per org. A single-tenant install ignores the claim, and nothing changes
  for it. Multi-tenant mode still cannot be enabled.
- **Fix: upgrading a database older than drive schema 20 that holds a file and a folder of
  the same name no longer fails at startup.** Since the org groundwork, schema 20's clash
  rename wrote `org_id`, which that schema does not have yet; it now writes the columns of its
  own schema, and the upgrade's later default places the rows in the default org.
- **Audit rows of guests and background workers name their workspace's org.** Guest-link
  downloads and uploads, trash and version retention, and workspace purges are recorded in
  the org of the workspace they touched. A single-tenant install, with one org, is unchanged.

- **Administration is bound to one org (tenancy groundwork).** Every administrator route now
  has a class: install-wide (`platform`), one org (`org`: the admin directory, audit, leavers,
  handover, deleted workspaces and restore), or single-tenant only (creating, renaming,
  deleting, re-quotaing and per-workspace retention of shared workspaces). Drive schema 28
  refuses the administrator role outside the staff org of a multi-tenant database. On a
  multi-tenant install, staff select the org with `X-Ky-Org`, see only that org, and every
  read is audited there; single-tenant ignores the header. The main store's audit rows now
  carry the account's org. Single-tenant behaviour is unchanged.

- **Access is scoped by org (tenancy groundwork).** Every grant now requires the account and
  the workspace to be in the same org, and an account acts only while its org is active and
  enabled. That covers browser sessions, password and KyIdentity sign-in, WebDAV passwords,
  agent, device and service tokens, guest and organisation links, and KyPost viewing copies.
  The people picker, share recipients, group grants, handover targets and organisation links
  stay inside the caller's org, and shared workspace names need only be unique within one
  org. A cross-org ID is refused exactly like one that never existed. A single-tenant install
  has one org that is always active, so its behaviour is unchanged; granting a group ID that
  does not exist is now a 400 instead of a database error.

- **Every row now belongs to an org (tenancy groundwork).** Drive schema 27 adds the org
  registry `drive_orgs` and an `org_id` column on every org-owned table. Upgraded rows go to
  the one default org (`00000000-0000-0000-0000-000000000001`), and SQLite triggers refuse a
  row in an unknown or deleted org, a changed `org_id`, and a row joining two orgs. The first
  start records `tenancy_mode=single` in `server_settings`. `KYDRIVE_TENANCY` accepts only
  `single` (the default); `multi` fails startup until multi-tenant mode ships. Single-tenant
  behaviour is unchanged, and no operator action is needed.

- **A KyIdentity MFA reset signs the account out.** The directory webhook's `user.mfa_reset`
  now ends the account's sessions, open editors and every credential it issued (WebDAV
  passwords, agent and service tokens, linked computers, guest links), audited as
  `sso.mfa_reset`. A login still finishing with a pre-reset ID token is refused too, a first login
  included. The
  account stays active, and the person signs in again. Before, the event was ignored.

- **Directory webhook speaks KyIdentity's suite webhook.** `/api/sso/kysignon/sync` now
  verifies `syncauth` headers (`X-KySignOn-Signature: v1=…` over timestamp, event type, event
  ID and body hash, plus `X-KySignOn-Timestamp`, `-Event-Type` and `-Event-ID`) and reads a
  SCIM user resource. A bare body HMAC in `X-KySignOn-Signature` or `X-Signature-SHA256`, and
  the old `{event, id, username, …, timestamp}` body, now get 401. Pair KyDrive in KyIdentity
  as a `suite_webhook` system with `KY_KYSIGNON_HMAC_SECRET` as its signing secret before
  upgrading if you use this route. Events apply in `meta.version` revision order; migration 11
  renames `users.directory_ts` to `directory_rev` and zeroes it, with no operator action.

- Keep SQLite `v1.62.0` with its upstream-required libc `v1.80.1` and memory `v1.12.1`; defer independent updates of those runtime dependencies.

- Full Go checks use a fresh run and a 20-minute per-package timeout in CI and local Makefile targets. The real-password API suite can exceed Go's ten-minute default under race/FIPS instrumentation. Test assertions and cryptographic settings are unchanged; no production behavior or data migration changes.

- Prepare application version `0.6.1` (from `0.6.0`) for the compatible dependency refresh. Rebuild or install the updated application; this version bump requires no additional data, account, key or configuration migration.

- Refresh compatible dependency versions and lockfiles. Rebuild or install the updated application; no database, configuration, account or key migration is required.

- **Devices must sign.** `POST /api/devices/token` refuses a link without `publicKey` (400,
  the code is not spent). A device linked before signing gets 401
  `{"error":"device_key_required"}` and must be linked again; a revoked one is still told
  `device_revoked`, so it wipes. Upgrade the desktop and Android apps first.
- **Signed TLS pin rollover.** KyDrive keeps a never-rotated Ed25519 identity key
  (`<data dir>/identity.key`, or `KYDRIVE_IDENTITY_KEY_FILE`), sealed in every backup capsule.
  `POST /api/devices/token` and `GET /api/drive/device` add `identity_key` (std base64, 32
  bytes). Public `GET /api/devices/tls-pin` (10 a minute per client, `no-store`) answers
  `{host, pin, issued_at, signature}`: `pin` is `sha256/<base64 SHA-256 of the SPKI>` of the
  leaf `KY_APP_URL` serves, and `signature` is Ed25519 over
  `kydrive-tls-pin/1\n<host>\n<pin>\n<issued_at>` (golden vector
  `internal/api/testdata/tls-pin-v1.json`). 503 without an `https` `KY_APP_URL` or when the
  server cannot read its own certificate through system trust. A pinning client survives a
  certificate renewal without re-linking. Set `KYDRIVE_TLS_CERT_FILE` to the reverse proxy's
  certificate (PEM) so only that key is signed. Behind Cloudflare Tunnel, where there is no
  certificate to mount, `KYDRIVE_TLS_PIN_PROBE=true` signs what a probe of `KY_APP_URL` reaches
  instead (warned at startup). With neither, the route answers 503.
- **Signed device requests.** `POST /api/devices/token` takes an optional `publicKey` (P-256
  SPKI DER, std base64). A device that enrolled one must sign every request with
  `KyDrive-Date`, `KyDrive-Nonce`, `KyDrive-Content-SHA256` and `KyDrive-Signature`
  (`kydrive-request-v1`, `internal/api/devicesig.go`, golden vector
  `internal/api/testdata/device-signing-v1.json`); its `kyd_` token alone then gets 401, and a
  replayed or altered request is refused. Devices linked without a key keep working until the
  clients sign. Schema 26 adds `drive_devices.pubkey`.
- **Open mail attachments read-only (KyPost).** KyPost can send one received Office or PDF
  attachment as a viewing copy: `kypost.drive.view` (`POST /api/suite/kypost/view-tickets`)
  reserves it and a one-time `PUT /api/suite/view/<ticket>` stores it; the person opens it at
  `/view.html` in Euro-Office's view mode (no edit, download or print). It is visible to its
  owner only, never shared, listed, searched or backed up, counts against My files' quota and
  is deleted when the viewer closes or after 24 hours. Status adds `viewer`. Schema 24.
- **Suite groups.** When KySignOn is configured, KyDrive reads KyIdentity's suite groups
  (`GET /api/suite/groups`) every 60 seconds and gives each one a shared workspace, keyed on
  the group's ID: its name follows the group (numbered `Sales (2)` on a clash), and its members
  are exactly the group's listed members with a KyDrive account, as editors; KyDrive
  administrators are never added. A group KyIdentity lists as archived has its workspace
  archived in Deleted workspaces with its files kept; turning it on again restores the same
  workspace. Suite group workspaces are never purged or removed. A failed or refused call
  changes nothing. Schema 25 adds `drive_suite_groups`. See `docs/SUITE.md#suite-groups`.

- **Content searches are rate-limited.** Searches that look inside files are limited to 60 a minute
  and 600 an hour per user, web and MCP together. Over the limit `GET /api/drive/search` answers
  429 `{"code":"rate_limited"}` with `Retry-After`, and MCP `search_files` returns an error.
  Name-only searches are not limited.
- **Prefix and phrase search.** In the search box, `GET /api/drive/search` and MCP
  `search_files`, `conf*` (3 or more letters) matches words starting with them, up to the 100
  found in most of the files the user can read, and `"site report"` matches the words together in that order, across
  line and page breaks, checked in the best 200 matches within half a second. Responses add
  `contents.unverified` (files not checked for the phrase, left out) and
  `contents.expanded_truncated` (MCP: `unverified`, `prefix_capped`); snippets highlight the
  phrase. Names match without the quotes and the `*`. A sealed vocabulary
  (`<KY_DATA_DIR>/search/vocab`) serves the prefixes; after the upgrade it is built in the
  background from the sealed text cache, with no reindex, and whole words work meanwhile. See
  `docs/CLIENTS-AND-EDITOR.md#search`.
- **Search finds what is shared with you.** The search box and `GET /api/drive/search` also
  search the live items in Shared with me, by name and by content: a shared file, or a shared
  folder and everything live inside it. Such a result carries
  `"shared":{"from":"<name>","role":"<role>"}`, its `path` starts at the shared item, and in the
  web app a shared file opens its preview and a shared folder opens in Shared with me. Access is
  checked on every search and again for each shared result before it is returned, so a revoked
  share drops out at once. MCP `search_files` and service tokens are unchanged: own workspaces
  only. No index change. See `docs/CLIENTS-AND-EDITOR.md#search`.
- **Link a phone by QR code.** Settings → Phones & apps → Devices has **Link a phone**: after
  step-up, `POST /api/devices/phone-code` returns `{link, expires_in}`, where `link` is
  `kydrive://link?srv=<KY_APP_URL>&code=<code>`, a code that works once for 120 seconds. KyDrive
  for Android scans it and redeems it with `POST /api/devices/token`
  `grant=qr&code=&name=&platform=android|ios`, getting the same `kyd_` device credential as a
  computer. The dialog names the phone that used the code, with Revoke. A computer's code and a
  phone's code never redeem as each other. The list is now **Devices** and shows phones too; the
  20-device limit counts both.

- **Search by file type.** The search box shows a row of type chips (Folders, Documents,
  Spreadsheets, Presentations, PDFs, Images, Videos, Audio, Archives); the choice is kept in the
  address as `?type=`, so Back works, and a type alone lists everything of that type.
  `GET /api/drive/search` and MCP `search_files` take an optional `type` (unknown: 400
  `bad_type` / a tool error) and accept an empty query with a type. Types come from the name's
  last extension; nothing new is stored. See `docs/CLIENTS-AND-EDITOR.md#search`.
- **Guest links from KyPost (suite contract K).** `kypost.drive.link` takes
  `"audience":"guest"`: a view-and-download guest link on a `Mail attachments` file, only while
  the administrator's guest-link policy is on and the person turned on **Settings → Files →
  Let KyPost create guest links for people outside the organisation** (off by default, shown
  only while the policy is on), for 1 to the policy's `max_days` days; otherwise 409
  `guest_links_off` or 400 `bad_body`. Listed with the file's guest links and audited as made
  through KyPost. `kypost.drive.status` adds `guest_links` and `guest_max_days`. Schema 23 adds
  `drive_peer_guest`. See `docs/KYPOST.md`.
- **Email a link (suite contract J).** A file's menu offers **Email a link** when the person
  may share it and KyPost is in their KyIdentity suite list. It makes an ordinary organisation
  reader link (1, 7, 30 days or never) and opens KyPost's `/compose` deep link in a new tab with
  the file name as subject and the link in the body; nothing is sent until the person sends it.
- **KyPost can fetch a file picked for it (suite contract I2).** New signed route `POST
  /api/suite/kypost/fetch` (`kypost.drive.fetch`, `{sub,issuer,file_id,max_bytes}`) answers
  the raw bytes of a file the person chose in the picker's copy mode within the last 24 hours
  and may still read; anything else is 404 `not_found`, larger than `max_bytes` 413
  `too_large`. This is narrower than the contract's "any file the user may read" so the pair
  secret alone never reads a person's drive. See `docs/KYPOST.md`.
- **KyDrive picker for other suite apps (suite contract I).** `/pick?origin=&mode=link|copy`,
  opened as a popup by a suite app, lets a signed-in everyday person choose up to 20 files they
  may share and, only on the confirming click, sends them to the opener with
  `postMessage` at exactly that origin. The origin must be the `https` origin of an app in the
  person's KyIdentity suite list. Link mode makes reader organisation links (1, 7, 30 days or
  never); copy mode, for the paired KyPost only and with the person's KyPost switch on,
  records the files as picked for 24 hours. New session routes `GET`/`POST /api/drive/pick`;
  schema 22 adds `drive_peer_picks`. See `docs/SUITE.md`.
- **KyPost can save and share files (suite contract C).** When KyPost is paired (by
  KyIdentity, or `KYDRIVE_KYPOST_URL` and `KYDRIVE_KYPOST_SECRET_FILE`), KyPost uploads mail
  attachments into the person's own My files, folder `Mail attachments`, and shares them by
  reader organisation link. New signed routes `POST /api/suite/kypost/upload-tickets`,
  `/links` and `/status` (syncauth v1 under the pair secret) and the ticket upload `PUT
  /api/suite/upload/<ticket>`, the only route that answers CORS for another origin (the paired
  KyPost's, without credentials). Each person chooses under **Settings → Files → Let KyPost
  save and share my files** (default on when KyIdentity paired the apps, off when the
  environment did); **Server settings → System** shows the pairing. KyPost's access is a new
  peer context in `access.go` that can add files to that folder and link them, nothing else.
  Schema 21 adds `drive_peer_settings`, `drive_peer_folders` and `drive_peer_tickets`. See
  `docs/KYPOST.md`.
- **Office networks skip the per-address sign-in limit.** Staff behind one NAT address share
  the per-address windows (20 sign-ins a minute, 10 password changes a minute, 20 failed WebDAV
  app-password sign-ins a minute) and can trip them together. An administrator lists the
  office's networks under **Server settings → Office networks**, or the operator sets
  `KY_RATE_LIMIT_EXEMPT_NETWORKS`; the two lists are combined. Requests from a listed network
  skip those windows (and the step-up's share of the sign-in window) only; per-account windows,
  the CAPTCHA and the limits on agent tokens, device tokens, linking, organisation and guest
  links still apply. Empty by default. Ranges broader than /16 (IPv4) or /48 (IPv6), loopback
  and invalid entries are refused, at startup for the variable and with 400 for the setting.
  The match uses the address resolved through `KY_TRUSTED_PROXIES`. New admin routes GET and
  PUT `/api/settings/rate-limit-networks` (`{networks}`; reply `{networks, environment,
  exempted}`), audited as `admin.rate_limit_networks`. List only networks you control: never
  guest Wi-Fi, a VPN exit or the reverse proxy's address.
- **One shared office budget per limit.** All listed networks together get 200 sign-ins,
  200 password changes and 200 failed WebDAV app-password sign-ins a minute past the
  per-address windows; beyond that their requests meet the ordinary per-address window
  again. One host inside an office therefore cannot make unbounded attempts, nor create
  enough per-account counters to push live ones (and their lockouts) out of the limiter.
  The screen's count includes only requests that actually skipped a per-address window.
- **WebDAV reserves its per-account window before checking an app password.** Concurrent
  wrong guesses at one account could all pass the 10-per-15-minutes check before any was
  counted; now each takes its place first and hands it back on success. Sign-in also hands
  its reservation back when the account lookup fails with a server error.
- **ky-primitives moves to the revision with `netallow`.** It also brings the library's own
  split-file join: capsule `restore` of a database with missing or renumbered parts is still
  refused and writes no database, but the error is now `recoveryclient.ErrSplitParts`
  ("split file parts do not add up") instead of `backup.ErrDatabaseParts`.
- **Guest links are credentials.** `POST /api/drive/{files|folders}/{id}/guest-links` answers
  403 `{"code":"step_up_required"}` without a step-up in the last five minutes (Bearer callers
  included); the Share dialog asks "Confirm it's you" and retries. A password change, reset or
  replacement, and a directory deactivation, revoke the account's guest links; reactivation does
  not bring them back. Creation rechecks the browser session in its transaction, so an in-flight
  request cannot mint a new link after its login was revoked. Organisation links are unchanged.
- **Purges go through the parent.** `DELETE /api/drive/files/{id}`, `DELETE
  /api/drive/folders/{id}` and `DELETE /api/drive/files/{id}/versions/{revision}` need manager
  rank on the item's parent (a file's own share does not count). A share holder purging the
  shared item itself, or its versions, now gets 403 instead of 204; inside a folder shared as
  manager nothing changes.
- **Desktop account ID.** `POST /api/devices/token` also returns `user_id`, the account's
  immutable ID. New `GET /api/drive/device` (device token only) answers `{user_id, user}`.
- **Folder trash precondition.** `PUT /api/drive/folders/{id}/trash` takes an optional `head`, a
  change-feed position: 409 when a change after it names the folder or a live item beneath it
  (or it is below the pruned history). Without `head` the route behaves as before.
