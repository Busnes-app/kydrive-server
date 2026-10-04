# KyDrive

Open-source shared drive for the Ky suite. KyIdentity owns people and groups; KyDrive owns workspace permissions. Euro-Office is the first editor integration. The drive stores arbitrary files and exposes a workspace-scoped service API for other suite applications.

Initial deployment: one `linux/amd64` Docker container on the NAS, Euro-Office in Kubernetes. The same drive image and SQLite/filesystem model also run on a single Kubernetes persistent volume. The requested NAS is `unraid.urlxl.us` / KyYard `hluswcdata01`; its filesystem and deployment have not been validated. This delivery builds and tests locally.

## Local development

Requires Go matching `go.mod`, Node/npm, Docker for editor tests and Restic for bulk recovery checks. Restic is included in the shipped container.

```sh
make build-web
make build
KY_DATA_DIR=./data KY_DB_DRIVER=sqlite KY_APP_NAME=KyDrive ./kydrive-server
```

Choose a random `KY_ADMIN_PASSWORD` in a private environment file before starting. Bootstrap requires password replacement at first login. Use the configured OIDC provider for staff; SCIM never supplies passwords. Production uses `KY_ENV=production`, HTTPS, an explicit durable `KY_SESSION_SECRET` and explicitly configured integrations. Read/write timeouts default to 5m, configurable with `KY_READ_TIMEOUT`/`KY_WRITE_TIMEOUT` between 15s and 15m.

## NAS package

```sh
cp .env.example .env
cp .env.runtime.example .env.runtime
chmod 600 .env .env.runtime
# Edit origins and credentials; choose persistent paths and an independent bulk mount.
# Create each mount with ownership matching KY_UID:KY_GID (defaults 1000:1000).
docker compose -f docker-compose.yml -f docker-compose.build.yml build
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d
docker compose exec app /app/kydrive-server bulk-init
```

The root Compose package serves KyDrive only. It binds HTTP to loopback for the existing HTTPS proxy. Euro-Office runs in Kubernetes. Use an explicit Docker-network bind address only when the proxy requires one. SQLite's WAL and durable flushes require a local filesystem with working file locks/fsync, not an SMB-mounted database. A NAS data share is not an independent backup; mount bulk storage from a separate failure domain. Retained versions and trash count toward quota. Backups do not prune Restic snapshots automatically: retain every snapshot named by a retained capsule.

## Identity and permissions

Register an OIDC application in KyIdentity using callback `<drive origin>/api/sso/kysignon/callback`. The route retains the base's name; set `KY_KYSIGNON_ISSUER` to the actual KyIdentity issuer, with its client ID/secret. Keep `KY_SSO_AUTO_PROVISION=false`. A verified OIDC `sub` must equal the user's SCIM `externalId`; username and email do not link accounts.

Configure a KyIdentity outbound SCIM system at `<drive origin>/scim/v2` with `KY_SCIM_TOKEN`. Enable group delivery and assign the required users/groups. KyDrive supports exact `externalId` reconciliation, user PUT/deactivation, groups PUT/delete and atomic membership replacement. An `externalId` is immutable. Application administrator privilege is separate from directory roles; incoming `roles=admin` cannot elevate it. The break-glass local administrator cannot be modified through SCIM.

Create a workspace in KyDrive and grant a KyIdentity group reader, editor or manager access. Grants inherit throughout the workspace. An administrator can configure quotas/grants and view audits, but needs a group grant to read document content. Workspace files survive deletion of any employee or group. A manager can issue reader/editor service credentials scoped to their workspace, expiring after 90 days and invalidated when that user's membership or account ceases to authorize them.

The UI provides files, folders, upload/download, versions, trash restore, group grants, quotas, editor opening, operational status and audit. The people/group action links to KyIdentity; it does not duplicate the directory or embed another administration application.

## Editor

Set `KYDRIVE_EDITOR_URL` to the browser-visible HTTPS Euro-Office origin and `KYDRIVE_EDITOR_SECRET` to the same JWT secret supplied to Euro-Office. `KYDRIVE_EDITOR_DRIVE_URL` defaults to `KY_APP_URL`; the editor must resolve and reach it for authorized downloads and signed callbacks. Optional `KYDRIVE_EDITOR_INTERNAL_URL` changes server-to-editor transport, while output URLs must still name the configured public origin. No redirects are followed. HTTP origins are accepted only with `KY_ENV=development` for local testing.

DOCX/ODT/TXT use the document editor; XLSX/ODS/CSV use the spreadsheet editor; PPTX/ODP use the presentation editor. Other files remain download-only. Browser configuration, callbacks and commands use HS256 JWT. Download credentials identify one editor session and revision, are stored only as hashes, expire and recheck current membership. Saved output is restricted to the editor origin's `/cache/` paths, capped at 128 MiB and committed as an immutable version. Stale saves fail with a conflict; replayed identical output is acknowledged without another version. Force-save sequences use a persisted revision pointer.

Deactivation, membership loss, permission downgrade, trash and session expiry queue durable revocations. The worker scans every two seconds and retries failed Euro-Office `drop` commands. The administration UI shows pending commands. Revoked users lose online editing and file access; plaintext already delivered to a browser cannot be recalled. Euro-Office processes plaintext. This is not CryptPad-style client-side encryption.

The local image under test is Euro-Office `v9.3.4-hotfix.1`, amd64 digest in `deploy/render.py`. The documented `9.3.1` tag was unavailable. Its runtime carries AGPLv3 and inherited ONLYOFFICE strings. The image has no source-revision provenance label; digest pinning and a matching version do not independently prove its complete build chain. The release tag pins Euro-Office-owned submodules and its workflow publishes to the Euro-Office registry; this verifies declared source/build ownership, not reproducibility of the pulled bytes. See [acceptance evidence](docs/ACCEPTANCE.md) before a production release decision.

## Recovery

KyRecovery is the blind sealed-capsule destination at `https://kyrecovery.urlxl.us/`. Pair through the Recovery screen after the suite ceremony or pin its public key manually; compare fingerprints. Pairing pins the key permanently. `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` is an explicit opt-in for HTTPS private-network destinations, with loopback still refused. A LAN resolver override is provided by `docker-compose.lan-dns.yml`.

`KY_BACKUP_DIR` enables local sealed copies; `KY_BACKUP_KEEP` defaults to seven. `KY_BACKUP_DEPOSIT_INTERVAL` is the default schedule (UI can choose off or at least 15 minutes). A run seals once and sends to all configured capsule destinations. Backups audit pairing/deposits, include the deployment encryption key and require at least one destination. Unpairing keeps the key pin and requires a separate KyRecovery administrator to revoke its token.

`KYDRIVE_BULK_BACKUP_REPOSITORY` names an independent mounted Restic repository outside the data directory. Initialize it once with `bulk-init`. When there are documents, every backup first snapshots SQLite, verifies all referenced immutable blobs and writes an encrypted Restic snapshot of exactly those blobs. The sealed capsule binds its full snapshot ID and all blob hashes/sizes. The Restic password derives from the deployment key escrowed inside the capsule. A missing/unavailable repository or missing source blob fails the backup. Capsules carry metadata rather than putting the whole corpus inside the 384 MiB KyRecovery cap.

See [restore runbook](docs/RESTORE.md). `backup-drill` uses a throwaway custodian kit, restores the sealed database and the exact Restic snapshot into scratch storage, verifies all hashes and deletes scratch plaintext. It does not hold the suite private key or custodian shares.

## Kubernetes rendering

`deploy/render.py` renders Euro-Office's single-instance deployment, persistence, service and TLS ingress. It performs no installation. Optionally render the drive profile with a digest-pinned drive image and an already provisioned independent bulk PVC.

```sh
python deploy/render.py --drive-url https://kydrive.example.test \
  --editor-url https://euro-office.example.test --editor-secret-file /private/editor-jwt \
  --storage-class YOUR_CLASS --output /private/kydrive-rendered
```

The secret file must have mode 0600. Output contains a Secret and is protected at 0700/0600; never commit it. Supply TLS Secrets through existing ingress/certificate tooling. For a Kubernetes drive, pass `--kubernetes-drive --drive-image REGISTRY/IMAGE@sha256:DIGEST --bulk-pvc EXISTING_INDEPENDENT_CLAIM` and create `drive-integrations` with OIDC/SCIM/bootstrap configuration. Read generated `INSTALL.txt` before applying.

Private-IP fetching stays disabled in the editor package. If NAS DNS resolves privately, first constrain editor egress to the intended drive/proxy endpoints and record the explicit exception before enabling it. This delivery does not choose actual ingress/TLS or NAS filesystem settings without testing that infrastructure.

## Verification

```sh
PATH=/path/containing/restic:$PATH go test -race ./...
go vet ./...
cd web && npm test && npm run build
python -m unittest discover -s deploy -p '*_test.py'
docker build --platform linux/amd64 -t kydrive-server:local .
```

The browser shell and actual Euro-Office were exercised through the native T3 collaborative browser. Evidence and limits are in `docs/ACCEPTANCE.md` and `UI-VERIFICATION.md`. Local testing does not establish a live KyIdentity pairing, production TLS, NAS storage semantics or suite-wide one-prompt installation.

## Scope

Single-instance SQLite and filesystem storage. No WebDAV, SMB endpoint, desktop sync, anonymous sharing, full-text search, automatic version deletion or HA. Interrupted unpublished uploads may leave unreferenced blobs; retain them for operator review rather than deleting potentially recoverable output. An acknowledged save refers to durably flushed bytes plus a committed database revision.

KyDrive inherits the MIT-licensed server base. Euro-Office is a separate AGPLv3 service; Restic is BSD-2-Clause. Keep each dependency's license and source obligations with its distribution.

When a directory user is also named `admin`, reserve a distinct name for the local recovery administrator before provisioning: `/app/kydrive-server rename-local-admin -from admin -to recovery-admin`. Run this operator command with the deployment environment and persistent data mounted. It atomically renames only a local administrator, preserves its credentials and privileges, and records an audit row. Directory accounts are never adopted as local administrators.
