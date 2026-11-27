# Assurance case

[Product overview](../README.md) · [Permissions](PERMISSIONS.md) · [Recovery and encryption](RECOVERY-AND-ENCRYPTION.md) · [Verifying releases](VERIFYING-RELEASES.md)

This is the argument that KyDrive meets its security requirements: what it protects, from whom,
where the trust boundaries are, and why the design and the code hold at each one. The detailed
contracts live in the AGENTS.md files of each package and in the topic guides; this page ties them
together and points at the evidence. Where a claim names code, the path is relative to the
repository root.

## What KyDrive must protect

KyDrive is the suite's file drive: personal and shared workspaces, sharing, versions, WebDAV, an
MCP endpoint for AI agents, desktop sync and Euro-Office editing. Its security requirements:

1. **Only the right person or client gets in.** Passwords, second factors, sessions, app
   passwords, device tokens and agent tokens cannot be guessed, replayed or used outside the
   surface they were issued for.
2. **Every file operation is authorized against current grants.** A user, service, device, agent or
   editor session reaches only the workspaces and items its grants allow at the moment of the
   request; administration does not bypass content grants.
3. **Stored files never run in the app's origin.** User content is served so that a browser will
   not execute it against KyDrive.
4. **File contents and names at rest stay secret** from someone who copies the data directory,
   when the deployment key is kept outside it.
5. **Directory changes take effect.** Disabling or deleting an account in KyIdentity ends its
   sessions, and resetting its second factor ends its sessions and credentials; old or forged directory events change nothing.
6. **Recovery restores a complete, consistent drive** from sealed metadata plus the bound bulk
   snapshot, and restored credentials are invalidated.

## Threat model

| Attacker | Can | Wants |
|---|---|---|
| Anonymous internet client | Send any request through the TLS proxy | Take over an account, guess passwords, exhaust resources |
| Signed-in user | Use their own session, upload any file | Read or change workspaces they hold no grant on, reach admin functions |
| Hostile web page in a user's browser | Make the browser send requests | Act through the user's session, or obtain an agent token |
| Uploaded file content | Be opened in the browser, previewed, or read by an AI agent | Run script in KyDrive's origin, or steer an agent |
| Holder of a leaked app password, device or agent token | Call the surface it was issued for | Use it on another surface or beyond its scope |
| Forged directory or editor traffic | Send webhook, SCIM or editor callback requests | Create or elevate accounts, inject a document version |
| Someone with a copy of the data directory or a backup | Read the database and blob files | Read files and names, recover credentials |

Out of scope: an attacker who runs code on the host, or who holds the data directory **and** the
deployment key (they are the server); a malicious administrator; and the Euro-Office service
itself, which reads plaintext by design. KyDrive is not end-to-end encrypted
([Encryption at rest](RECOVERY-AND-ENCRYPTION.md#encryption-at-rest)).

## Trust boundaries

1. **Browser → TLS proxy → KyDrive.** KyDrive serves HTTP behind a proxy. `auth.ClientIP` honours
   `X-Forwarded-For` only from peers in `KY_TRUSTED_PROXIES` and ignores `X-Real-IP`; rate limits
   and session binding both use it (`internal/auth`).
2. **Credential surfaces.** Session cookies authenticate the web app; HTTP Basic app passwords only
   `/dav/`; device tokens (`kyd_`) only `/api/drive/*` and `/dav/`; agent tokens (`kya_`) only
   `/mcp`. A device that enrolled a P-256 key when it linked must sign every request (method, URI,
   state-changing headers, body digest, date, nonce), so its token alone authorises nothing and a
   captured request cannot be replayed (`internal/api/devicesig.go`). A pinning app learns the
   server's Ed25519 identity key over its pinned link and repins after a certificate renewal only
   to a leaf that key signed for this host (`GET /api/devices/tls-pin`, `internal/api/tls_pin.go`),
   so an interceptor holding a certificate the device trusts cannot take over the pin. `/mcp` refuses any request carrying an `Origin` header, so a web page cannot use it
   (`internal/api/mcp.go`).
3. **KyIdentity → KyDrive.** OIDC sign-in with PKCE `S256` and verified ID tokens; SCIM with a
   bearer token; a directory webhook that must carry a valid `syncauth` HMAC-SHA256 signature
   over its timestamp, event type, event ID and body, with the timestamp within five minutes,
   before anything is parsed further, and that applies an event only when its revision is newer; back-channel logout tokens verified
   like ID tokens plus the logout-token claims (`internal/sso`, `internal/scim`).
   Outbound, the app rail asks KyIdentity `/api/suite/apps` with the KyDrive client credentials
   (`ky-primitives/suitedir`: HTTPS or loopback issuer, redirects refused, body capped); entries need an HTTPS
   URL, and the browser renders them as plain links, never markup (`internal/api/suite_apps.go`).
4. **KyDrive ↔ Euro-Office.** Editor configuration and callbacks are HS256 JWTs under a secret of
   at least 32 bytes; output is fetched only from the configured editor origin, without following
   redirects (`internal/editor/client.go`).
5. **KyDrive → KyRecovery and the bulk repository.** Capsules are sealed with
   `ky-primitives/recoveryclient`; a private-network KyRecovery URL is refused unless
   `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` is set (`internal/api/backup_handlers.go`). Bulk-repository
   credentials reach only the restic child process.
6. **KyDrive → its data directory.** Blobs and file and folder names are sealed
   under keys derived from the deployment key before they are written.
7. **Other suite apps → KyDrive.** In the browser, the picker (`/pick`) serves only an origin that
   is exactly the `https` origin of an app in the signed-in person's KyIdentity suite list, sends
   nothing before the person's confirming click and posts only to that origin
   (`internal/api/suite_pick.go`, [SUITE.md](SUITE.md)). Server to server, KyPost's signed routes
   (syncauth v1 under the pair secret, replay guard) act through a peer context that holds no
   rank and admits only the operations listed in `internal/drive/access.go` ([KYPOST.md](KYPOST.md)).

## Secure design principles applied

- **Least privilege.** Each token type works on one surface. Agent tokens are read-only by
  default, may be limited to one folder, and can only trash, never purge. The container drops all
  capabilities, forbids privilege escalation and runs read-only as uid 1000 (`Dockerfile`,
  `docker-compose.yml`).
- **Fail-safe defaults.** Privileged routes are registered wrapped in `s.requireAdmin` in one place;
  CORS allows only the exact `KY_APP_URL` origin and never `*`. Subcommands refuse a missing key or
  database rather than create one.
- **Complete mediation.** Current user status and group memberships are read on every operation;
  WebDAV builds one file system per request and lets the drive store authorize each call
  (`internal/dav`).
- **Economy of mechanism.** One HKDF-SHA256 key derivation and one AES-256-GCM helper
  (`internal/crypto`); OIDC and OAuth come from `coreos/go-oidc` and `golang.org/x/oauth2`, SCIM
  parsing from `elimity-com/scim`, passwords from `ky-primitives/password`.
- **Separation of privilege.** Two-factor sign-in is enforced by KyIdentity, not KyDrive; administration of
  workspaces does not grant read access to their content.
- **Open design.** Only published algorithms: AES-256-GCM, HKDF-SHA256, HMAC-SHA256, SHA-256, Ed25519,
  Argon2id through `ky-primitives/password`, OIDC with PKCE.
- **Psychological acceptability.** Startup and the Recovery screen warn when the deployment key
  lives in the data directory; the agent token page shows the access it grants.

## Common weaknesses and how they are countered

| Weakness | Counter | Where |
|---|---|---|
| Broken authentication, credential stuffing | Passwords of at least 12 characters, hashed by `ky-primitives/password`; per-client and per-account limits (10 per 15 minutes for one username from one client, an IPv6 one by its /64, answered like a wrong password so nobody can lock the owner out from elsewhere); one answer for unknown, inactive and wrong-password attempts | `internal/auth/policy.go`, `internal/api/auth_handlers.go` |
| Rate-limit allowlist abused or mistyped | Office networks (`KY_RATE_LIMIT_EXEMPT_NETWORKS` plus the admin setting) skip only the per-address sign-in, step-up, password-change and WebDAV app-password windows; per-account windows, the CAPTCHA and every other limit still run. The match uses `ClientIP`, so an untrusted peer's `X-Forwarded-For` claims nothing. Ranges broader than /16 or /48 and loopback are refused; only administrators change the list and each change is audited. Listed networks share one office budget per limit (200 a minute), then meet the per-address window, so office traffic cannot mint enough account keys to evict live account windows; per-account windows are reserved before verification | `internal/api/settings_handlers.go`, `internal/api/exempt_networks_test.go`, `ky-primitives/netallow` |
| Undersized signing and verification keys | Session and webhook HMAC secrets need 16 bytes, the editor JWT secret 32, the deployment key exactly 32; OIDC JWKS RSA keys need 2048 bits; `GODEBUG=fips140=on` limits outbound TLS chains to RSA-2048+/P-256+ | `internal/config/key_strength_test.go`, `internal/sso/strong_crypto_test.go`, [STRONG-CRYPTO.md](STRONG-CRYPTO.md) |
| Restricted export login used for ordinary access | A verified `export_only` ID-token claim issues a distinct, one-hour session only for an existing same-org active member in a fresh, enabled `deletion_scheduled` org. Authentication permits only personal export, self and logout; the archive selects only My files, with trash and every retained version, without a client-selected org/workspace. Ordinary credential and content checks still require active orgs. Logout, erasure, expiry or a lifecycle change stop the export | `internal/auth/session.go`, `internal/api/export.go`, `internal/drive/export.go`, `TestExportOnlySessionAndPersonalArchive`, `TestExportOnlyAdmissionAndLifecycle`, `TestSingleTenantIgnoresExportOnlyClaim` |
| Session theft from storage | Only the SHA-256 of the session token is stored; cookies are `HttpOnly`, `SameSite=Lax`, `__Host-` prefixed with `Secure` | `internal/auth/session.go`, `internal/store/sqlstore.go` |
| CSRF | Token is `HMAC-SHA256(session secret, "ky-csrf:" + session token)`, recomputed on each check, so a planted cookie is worthless; `TestAgentTokenWritesRefuseForgedCrossSiteRequests` | `internal/auth/session.go` (`ValidateCSRF`), `internal/api/browser_origin_test.go` |
| Cross-origin data theft | CORS reflects only the configured origin; `TestCORSNeverGrantsAnotherOrigin` walks the source | `internal/api/server.go`, `internal/api/browser_origin_test.go` |
| Broken access control | Admin routes wrapped in `requireAdmin` and pinned by `TestPrivilegedEndpointsRequireAdmin`; drive operations authorize against live grants | `internal/api/server.go`, `internal/api/authz_test.go`, `internal/drive` |
| Stored XSS through uploaded files | Downloads are `attachment`; WebDAV `GET` adds `Content-Security-Policy: sandbox; default-src 'none'`; previews carry a sandbox CSP; the app CSP has `script-src 'self'` and `frame-ancestors 'none'`; no `dangerouslySetInnerHTML` in `web/src` | `internal/api/drive_handlers.go`, `internal/dav/serve.go`, `internal/api/drive_preview.go`, `internal/api/server.go` |
| Path traversal | Disk paths are generated blob IDs, never user filenames | `internal/drive` |
| SQL injection | `database/sql` with parameters; formatted statements are the PostgreSQL migration lock, built from a constant, and `migrate-postgres`, whose table and column names come from `migrationTables` and the database catalogue and are quoted | `internal/store/migrations/migrations.go`, `internal/store/migrate_postgres.go` |
| Session outliving its identity-provider login | Back-channel logout token verified (signature, issuer, audience, expiry, `iat`, logout event, no nonce, `logout+jwt` when typed) before ending every session of its `sid`, or of its subject issued before it; the `sid` or the subject's cutoff is fenced so an in-flight callback cannot revive it; editor sessions record the login that opened them and are revoked with it even after its browser session expired; SSO sessions, MFA challenges and editor sessions from before the upgrade, which carry no such binding, are ended once by the migration | `internal/sso/backchannel.go`, `internal/store/sqlstore.go`, `internal/drive/sessions.go`, `TestVerifyLogoutToken`, `TestBackchannelLogoutEndsTheSSOLogin`, `TestBackchannelLogoutRevokesEditorsOfPrunedSessions`, `TestBackchannelMigrationEndsUnboundSSOLogins`, `TestLoginSchemaRevokesUnboundDirectoryEditors` |
| Forged directory events | `syncauth` HMAC-SHA256 over timestamp, event type, event ID and body, checked in constant time before parsing; five-minute window; events no newer than the account's revision ignored; payload roles ignored | `internal/sso/kysignon.go`, ky-primitives `syncauth` |
| Forged editor callbacks, SSRF through the editor | Signed JWT callbacks; output fetched only from the editor origin with redirects refused | `internal/editor` |
| SSRF through the recovery URL | `recoveryclient.ValidateURL` refuses private destinations unless explicitly allowed | `internal/api/backup_handlers.go` |
| Replica startup creates incompatible local keys | Multi-mode config requires the retained environment deployment key and an existing private external device seed; resolved ancestry and local-copy checks precede key loading. Missing/corrupt keys are never minted or replaced. Multi startup remains refused pending other gates; single mode is unchanged | `internal/config/replica_keys.go`, `internal/config/replica_keys_test.go` |
| Secrets and files at rest | Blobs: age v1 streaming encryption (random file key, 64 KiB authenticated chunks) with the file key wrapped under HKDF of the deployment key, bound to the blob ID (KYB2; KYB1 read until migrated); names AES-GCM with the row as AAD | `internal/drive/sealed.go`, `internal/drive/names.go` |
| Vulnerable dependencies | Dependabot; `npm audit --audit-level=high` in CI; OSV ignore list with reasons; dependency releases ship daily | `.github/`, `osv-scanner.toml` |
| Supply-chain tampering | Actions and base images pinned by digest; release images carry build provenance verified before `:stable` moves; the binary rebuilds to the same bytes | [VERIFYING-RELEASES.md](VERIFYING-RELEASES.md) |

## Residual risk

- **The deployment key defaults to `data/encryption.key`.** Encryption at rest protects a copied
  data directory only when the key is moved out (`KY_ENCRYPTION_KEY`); startup and the Recovery
  screen warn.
- **Not end-to-end.** The server, its operator and Euro-Office read plaintext. Workspace, group and
  service-token names are stored in plaintext; backups taken before name encryption hold plaintext
  names; the content search index reveals word-frequency and shared-word patterns.
- **Rate limits, proof-of-work spent salts and device nonces are process-local** (single-instance
  server); a restart forgets nonces, which the five-minute date window bounds.
- **Linking is trust on first use:** whoever redeems the two-minute code enrols the key. Signing stops a stolen token, not an interceptor reading or
  changing file bytes in transit. The key signs without the user present, so background sync
  works. TLS leaf pinning with signed rollover is server-side only: no released client pins yet,
  and the QR link does not carry a pin, so the first link is still trust on first use.
- **`KYDRIVE_TLS_PIN_PROBE=true` signs the leaf a probe of `KY_APP_URL` reaches** (opt-in, off by
  default, for a front with no certificate to mount, such as Cloudflare Tunnel; owner decision
  2026-11-16). Someone who controls the server's own outbound path and holds a hostname-valid
  certificate both the server and the devices trust then gets that certificate's key signed, and
  pinning devices accept it. Without it the server signs only `KYDRIVE_TLS_CERT_FILE`, or nothing
  (503).
- **The TLS pin identity key lives in the data directory by default** and in every backup. Anyone
  who copies it can sign a pin for their own leaf; they still need a certificate the device's
  system trust accepts and a position on the network.
- **AI agents may follow instructions in file contents.** The README says to give write access only
  when everyone who can message the agent is trusted.
- **No govulncheck in CI.** Known Go vulnerabilities are caught by Dependabot alerts, not by a
  reachability scan on each pull request.
- **No independent security audit** has been done.

## Evidence

- **Tests:** the security tests named above, the whole Go suite under the race detector, Vitest and
  Playwright browser tests, and the deploy rendering tests, on every push to `master` and pull request (`race`, `fips` and
  `checks` jobs, gated by `test`). Restic-backed tests run in CI, which installs restic.
- **Static analysis:** `go vet` and `tsc -b` (strict TypeScript) on every push to `master` and pull request,
  and GitHub CodeQL (default setup, configured in the repository settings rather than a workflow
  file) for Go, JavaScript/TypeScript, Python and Actions.
- **Dynamic analysis:** the race detector on the full suite, the binary smoke test
  (`scripts/smoke-test.sh`) and Playwright against the built server.
- **Releases:** attested images and a repeatable binary ([VERIFYING-RELEASES.md](VERIFYING-RELEASES.md)).

This case is updated when a trust boundary or a security requirement changes. A pull request that
moves one must update this page in the same change.


## Replica configuration groundwork

Multi-mode PostgreSQL startup checks shared configuration before bootstrap (`cmd/server/replica.go`).
`internal/config/replica.go` and `internal/editor/replica.go` store field-separated keyed checks,
so copying the database alone cannot guess low-entropy configuration values. Independent
first starts and acceptance serialize on the settings row; acceptance and its names-only
audit commit together (`internal/store/replica.go`). Malformed records and immutable key or
tenancy identity changes fail closed. Bulk settings omit the record and restore reset drops it.
`TestReplicaConfigAtomic` exercises real independent PostgreSQL pools, mismatched first
starts, old-replica refusal, upgrade fields and audit rollback. Single-mode tests require no
coordination state. This groundwork does not establish replica/hosted readiness; provider durability, object-store restore, failover/load and full acceptance remain required.


The R3 session-lock boundary (`internal/store/replica_lock.go`) keeps named ownership
on a reserved PostgreSQL session, never a pooled connection after return. A one-second
probe cancels work on session loss; voluntary release waits for the callback to finish.
Real independent-pool tests cover contention, distinct names, error/panic cleanup,
backend termination, cancellation and takeover. The method refuses single mode. `cmd/server`
`startJobs` runs every background job only on the `scheduled-worker` holder, and
`backup.OneRun` puts scheduled, manual and CLI deposits under `backup-run`; two-replica
tests on one PostgreSQL database (`TestOneReplicaRunsTheJobs`,
`TestDepositWaitsForOtherReplicasBackup`, `TestManualDepositWaitsForOtherReplicasBackup`)
cover exclusion, takeover after backend termination and refusal. This is still not
exactly-once execution or fencing of an external write already in flight, and startup
qualification remains required.


## Hosted object and index boundary

Test-build hosted storage selects `cells/<home_region>/orgs/<org_id>/` from the immutable
directory home and registered org. Blob, cache and segment sealing uses that org's data key.
Both desktop identity endpoints report exact issuer/subject and the stored org; usernames
and local IDs never substitute for the OIDC tuple.

Each hosted index is one immutable AES-256-GCM snapshot under an org-derived key. AAD binds
org, object UUID and pointer version, so moving or replaying ciphertext at a different
committed version fails authentication. Queue generations and a serving-state check are in
the pointer CAS transaction. Readers own separate in-memory SQLite connections; host disk
contains sealed drafts/ciphertext only. The queue and pointer contain opaque IDs, not terms.
Erasure fences publishers, removes their rows and destroys the key; repeated recursive
prefix cleanup catches late writes and failed deletions. Accessible object versions and delete markers are erased through S3 version APIs;
provider-managed backup copies remain outside application authority and require provider policy.

`TestHosted*` uses CI's signature-checking S3 fixture and PostgreSQL for replica reads,
cell/org separation, queue/CAS races, replay refusal, failed publishes and erasure. These
checks do not qualify a production provider, load, desktop packaging or hosted Office.
