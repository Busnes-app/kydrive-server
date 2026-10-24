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
   sessions; old or forged directory events change nothing.
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
   `/mcp`. `/mcp` refuses any request carrying an `Origin` header, so a web page cannot use it
   (`internal/api/mcp.go`).
3. **KyIdentity → KyDrive.** OIDC sign-in with PKCE `S256` and verified ID tokens; SCIM with a
   bearer token; a directory webhook that must carry a valid HMAC-SHA256 signature and a timestamp
   within five minutes before anything is parsed further (`internal/sso`, `internal/scim`).
4. **KyDrive ↔ Euro-Office.** Editor configuration and callbacks are HS256 JWTs under a secret of
   at least 32 bytes; output is fetched only from the configured editor origin, without following
   redirects (`internal/editor/client.go`).
5. **KyDrive → KyRecovery and the bulk repository.** Capsules are sealed with
   `ky-primitives/recoveryclient`; a private-network KyRecovery URL is refused unless
   `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` is set (`internal/api/backup_handlers.go`). Bulk-repository
   credentials reach only the restic child process.
6. **KyDrive → its data directory.** Blobs, file and folder names and TOTP secrets are sealed
   under keys derived from the deployment key before they are written.

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
- **Separation of privilege.** Local accounts can add TOTP as a second factor; administration of
  workspaces does not grant read access to their content.
- **Open design.** Only published algorithms: AES-256-GCM, HKDF-SHA256, HMAC-SHA256, SHA-256,
  Argon2id through `ky-primitives/password`, TOTP, OIDC with PKCE.
- **Psychological acceptability.** Startup and the Recovery screen warn when the deployment key
  lives in the data directory; the agent token page shows the access it grants.

## Common weaknesses and how they are countered

| Weakness | Counter | Where |
|---|---|---|
| Broken authentication, credential stuffing | Passwords of at least 12 characters, hashed by `ky-primitives/password`; per-client and per-account limits (10 per 15 minutes for one username); one answer for unknown, inactive and wrong-password attempts | `internal/auth/policy.go`, `internal/api/auth_handlers.go` |
| Session theft from storage | Only the SHA-256 of the session token is stored; cookies are `HttpOnly`, `SameSite=Lax`, `__Host-` prefixed with `Secure` | `internal/auth/session.go`, `internal/store/sqlstore.go` |
| CSRF | Token is `HMAC-SHA256(session secret, "ky-csrf:" + session token)`, recomputed on each check, so a planted cookie is worthless; `TestAgentTokenWritesRefuseForgedCrossSiteRequests` | `internal/auth/session.go` (`ValidateCSRF`), `internal/api/browser_origin_test.go` |
| Cross-origin data theft | CORS reflects only the configured origin; `TestCORSNeverGrantsAnotherOrigin` walks the source | `internal/api/server.go`, `internal/api/browser_origin_test.go` |
| Broken access control | Admin routes wrapped in `requireAdmin` and pinned by `TestPrivilegedEndpointsRequireAdmin`; drive operations authorize against live grants | `internal/api/server.go`, `internal/api/authz_test.go`, `internal/drive` |
| Stored XSS through uploaded files | Downloads are `attachment`; WebDAV `GET` adds `Content-Security-Policy: sandbox; default-src 'none'`; previews carry a sandbox CSP; the app CSP has `script-src 'self'` and `frame-ancestors 'none'`; no `dangerouslySetInnerHTML` in `web/src` | `internal/api/drive_handlers.go`, `internal/dav/serve.go`, `internal/api/drive_preview.go`, `internal/api/server.go` |
| Path traversal | Disk paths are generated blob IDs, never user filenames | `internal/drive` |
| SQL injection | `database/sql` with parameters; the only formatted statement is the PostgreSQL migration lock, built from a constant | `internal/store/migrations/migrations.go` |
| Forged directory events | HMAC-SHA256 checked in constant time before parsing; five-minute window; older events ignored; payload `role` ignored | `internal/sso/kysignon.go`, `internal/crypto/crypto.go` |
| Forged editor callbacks, SSRF through the editor | Signed JWT callbacks; output fetched only from the editor origin with redirects refused | `internal/editor` |
| SSRF through the recovery URL | `recoveryclient.ValidateURL` refuses private destinations unless explicitly allowed | `internal/api/backup_handlers.go` |
| Secrets and files at rest | Per-blob HKDF key from a random salt, AES-256-GCM chunks with the header as AAD; names and TOTP secrets AES-GCM with the row as AAD | `internal/drive/sealed.go`, `internal/drive/names.go`, `internal/auth/totp.go` |
| Vulnerable dependencies | Dependabot; `npm audit --audit-level=high` in CI; OSV ignore list with reasons; dependency releases ship daily | `.github/`, `osv-scanner.toml` |
| Supply-chain tampering | Actions and base images pinned by digest; release images carry build provenance verified before `:stable` moves; the binary rebuilds to the same bytes | [VERIFYING-RELEASES.md](VERIFYING-RELEASES.md) |

## Residual risk

- **The deployment key defaults to `data/encryption.key`.** Encryption at rest protects a copied
  data directory only when the key is moved out (`KY_ENCRYPTION_KEY`); startup and the Recovery
  screen warn.
- **Not end-to-end.** The server, its operator and Euro-Office read plaintext. Workspace, group and
  service-token names are stored in plaintext; backups taken before name encryption hold plaintext
  names; the content search index reveals word-frequency and shared-word patterns.
- **Legacy TOTP rows** are not bound to their row until their next use reseals them.
- **Rate limits and proof-of-work spent salts are process-local** (single-instance server).
- **AI agents may follow instructions in file contents.** The README says to give write access only
  when everyone who can message the agent is trusted.
- **No govulncheck in CI.** Known Go vulnerabilities are caught by Dependabot alerts, not by a
  reachability scan on each pull request.
- **No independent security audit** has been done.

## Evidence

- **Tests:** the security tests named above, the whole Go suite under the race detector, Vitest and
  Playwright browser tests, and the deploy rendering tests, on every push and pull request (`test`
  job). Restic-backed tests run in CI, which installs restic.
- **Static analysis:** `go vet` and `tsc -b` (strict TypeScript) on every push and pull request,
  and GitHub CodeQL (default setup, configured in the repository settings rather than a workflow
  file) for Go, JavaScript/TypeScript, Python and Actions.
- **Dynamic analysis:** the race detector on the full suite, the binary smoke test
  (`scripts/smoke-test.sh`) and Playwright against the built server.
- **Releases:** attested images and a repeatable binary ([VERIFYING-RELEASES.md](VERIFYING-RELEASES.md)).

This case is updated when a trust boundary or a security requirement changes. A pull request that
moves one must update this page in the same change.
