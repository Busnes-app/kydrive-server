# Deployment

[Product overview](../README.md) · [Standalone installation](../INSTALL.md)

Commands and unlinked file paths below are relative to the repository root.

## NAS package

```sh
cp .env.example .env
cp .env.runtime.example .env.runtime
chmod 600 .env .env.runtime
# Edit origins and credentials; choose persistent paths and an independent bulk mount.
# Create each mount with ownership matching KY_UID:KY_GID (defaults 1000:1000).
docker compose pull
docker compose up -d
docker compose exec app /app/kydrive-server bulk-init
```

For a quick start, Compose runs `ghcr.io/busnes-app/kydrive-server:stable` (`KY_IMAGE` in `.env`), which follows the newest release. Each GitHub release `vX.Y.Z` publishes `:X.Y.Z` once, and so does each daily dependency-update release `vX.Y.Z.N` (`:X.Y.Z.N`); the newest non-prerelease then moves `:stable`. There is no `v` and no `latest`. To hold a version, set `KY_IMAGE` to its `:X.Y.Z[.N]` tag. The release workflow refuses to push a version tag that already exists and master CI writes only commit tags, but the registry itself does not lock tags, so anyone with write access to the package could still move one.

For production, pin the digest: set `KY_IMAGE=ghcr.io/busnes-app/kydrive-server@sha256:<digest>`, taking the digest from the release job's summary (`Published ...@sha256:<digest> as :X.Y.Z`) or from `docker buildx imagetools inspect ghcr.io/busnes-app/kydrive-server:X.Y.Z`. Verify it before first use:

```sh
gh attestation verify oci://ghcr.io/busnes-app/kydrive-server@sha256:<digest> --repo Busnes-app/kydrive-server \
  --cert-identity https://github.com/Busnes-app/kydrive-server/.github/workflows/release-image.yml@refs/tags/v<version>
```

[Verifying releases](VERIFYING-RELEASES.md) also checks that the attestation names the commit the release tag points at, and rebuilds the binary to compare hashes.

To build from source instead, add the build overlay to every command, which tags the build `kydrive-server:local` whatever `KY_IMAGE` says: `docker compose -f docker-compose.yml -f docker-compose.build.yml build`, then `... up -d` with the same two files.

`bulk-init` is safe to rerun: when the repository already opens with this deployment's key it reports that and changes nothing. A repository at that location that the key cannot open is refused (`bulk repository exists but this deployment's key does not open it`); point `KYDRIVE_BULK_BACKUP_REPOSITORY` at the right repository rather than reinitializing.

### Upgrading

- After 0.6.0: local two-factor sign-in (authenticator codes and recovery codes) is removed; two-factor is now provided by KyIdentity sign-in. Local password accounts, such as the recovery administrator, sign in with their password alone, so nobody is locked out by losing a factor. TOTP secrets and recovery codes stored by earlier releases stay in the database, unused; no column or data is dropped.
- 0.4.3: Compose defaults to the published image instead of `kydrive-server:local`. Installs that build from source keep working with the build overlay. An install that ran a separately built `kydrive-server:local` without the overlay, and has no `KY_IMAGE` in `.env`, now pulls the release image: set `KY_IMAGE=kydrive-server:local` to keep the local build.
- 0.4.3: the Compose default moves from `:0.4.2` to `:stable`, so `docker compose pull` takes each new release, daily dependency releases included. An install that wants to stay on a version sets `KY_IMAGE` to it (or pins the digest).
- To move to a new release on `:stable`, check out its tag, then `docker compose pull` and `docker compose up -d`. With a pinned `KY_IMAGE`, set it to the new version or digest first.

The root Compose package serves KyDrive only. It binds HTTP to loopback for the existing HTTPS proxy. Euro-Office runs on Docker with `docker-compose.office.yml` (see [Docker setup](EURO-DOCKER.md)), or in Kubernetes. Use an explicit Docker-network bind address only when the proxy requires one. SQLite's WAL and durable flushes require a local filesystem with working file locks/fsync, not an SMB-mounted database. A NAS data share is not an independent backup; mount bulk storage from a separate failure domain. Trash and versions count toward quota until purged (see Retention and permanent delete). Restic snapshot pruning (Recovery screen, Document retention) is opt-in, off by default, never forgets the newest KY_BACKUP_KEEP deposit snapshots. KyRecovery-held capsules older than the policy may name forgotten snapshots: set the policy to outlive KyRecovery's retention. An append-only rest-server or Object Lock repository refuses prune by design.

### Moving to PostgreSQL

`migrate-postgres` copies a single-tenant SQLite install into an empty PostgreSQL database, in the tenancy the database records (a restored multi-tenant capsule becomes a multi-tenant database: RESTORE.md). **Not for production yet:** on single-tenant PostgreSQL, KyDrive's own backups and content search do not run until later releases. Move a test install, or one you back up yourself (`pg_dump` plus the data directory).

1. Back up, then stop the server and keep its volumes: `docker compose stop app`. The command refuses while a server holds the SQLite database. Servers from this release on hold a lock on it; stop older ones yourself.
2. Create an empty PostgreSQL database owned by the account KyDrive will use. Put its DSN in a private file, never on a command line: `(umask 077; echo 'KY_MIGRATE_POSTGRES_DSN=postgres://kydrive:<password>@<host>:5432/kydrive?sslmode=verify-full' > migration.env)`. Use `sslmode=verify-full` for a database on another host.
3. Check, then copy. Both run with the SQLite install's own settings and data volume:

   ```sh
   set -a; . ./migration.env; set +a
   docker compose run --rm --no-deps -e KY_MIGRATE_POSTGRES_DSN app /app/kydrive-server migrate-postgres -check
   docker compose run --rm --no-deps -e KY_MIGRATE_POSTGRES_DSN app /app/kydrive-server migrate-postgres
   ```

   The command copies a private snapshot of the database, brought to this release's schema, in one transaction. It then compares every table's row count and content with the snapshot before it commits; `-check` does all of this, then rolls the copied rows back. It leaves KyDrive's schema in the database, with the few rows a first start writes (such as the key check and the tenancy setting); the real run replaces those. Drop the database if you abandon the move. It prints the rows copied per table. It refuses, keeping no rows, a target that holds data or tables KyDrive does not know, a source table it does not know, a different encryption key, and a value PostgreSQL would read differently (a boolean that is not 0 or 1, a malformed time). Times keep microseconds. The snapshot sits in a private `.postgres-migration-*` directory beside the database and is removed when the command ends; after a killed run, remove it by hand.
4. Switch the install over: add a Compose overlay that sets `KY_DB_DRIVER: postgres` and `KY_DB_DSN: ${KY_DB_DSN}` for `app` (the base file pins SQLite), and put `KY_DB_DSN` in `.env`. Keep the same encryption key (`KY_ENCRYPTION_KEY` or `data/encryption.key`) and the data volume: files, the identity key and the search index stay there. Delete `migration.env`, then `docker compose up -d`.

The command never writes the SQLite database or your settings. Until PostgreSQL has taken writes, going back is starting the SQLite install again; after that, going back loses those writes.

### Office networks and sign-in limits

KyDrive limits sign-in failures per client address: 20 sign-ins a minute, 10 password changes a minute and 20 failed WebDAV app-password sign-ins a minute. Staff in one office usually share one public NAT address, so a busy morning can trip those limits for everyone there. List the office's networks, comma-separated (`203.0.113.0/24,2001:db8:1::/48`), in `KY_RATE_LIMIT_EXEMPT_NETWORKS`, or one per line under **Server settings → Office networks**; the two lists are combined. Requests from a listed network skip those per-address limits only, and only up to a shared office budget: all listed networks together get 200 sign-ins, 200 password changes and 200 failed WebDAV sign-ins a minute past the per-address windows, after which each office address meets its ordinary per-address window again. These still apply to them: the per-account windows (10 guesses at one account per 15 minutes, 5 password changes per account a minute), the CAPTCHA, step-up, KyIdentity sign-in with its MFA, and every per-address limit on agent tokens, device tokens, desktop linking, organisation links and guest links.

The list is empty by default. Ranges broader than /16 (IPv4) or /48 (IPv6), loopback, invalid entries and more than 256 networks are refused: by the variable at startup, and with 400 when saved in the UI. A saved list that no longer parses at startup is dropped with a log line; the variable's list stays. The match uses the client address resolved through `KY_TRUSTED_PROXIES`, so a forged `X-Forwarded-For` cannot claim a listed network. Changes are audited as **Office networks changed**, and the screen counts the requests that skipped the limit since the last change or restart.

List only networks you control. Everyone behind a listed address loses the per-address limit, so never list guest Wi-Fi, a shared or public network, a VPN provider's exit, or the reverse proxy's own address (with an incomplete `KY_TRUSTED_PROXIES`, every client appears to come from the proxy, and listing it would exempt the whole internet). A compromised machine on a listed network can still guess at most 10 passwords per account per 15 minutes, but across many accounts.

## Kubernetes rendering

`deploy/render.py` renders Euro-Office's single-instance deployment, persistence, service and TLS ingress. It performs no installation. Optionally render the drive profile with a digest-pinned drive image and an already provisioned independent bulk PVC.

```sh
python deploy/render.py --drive-url https://kydrive.example.test \
  --editor-url https://euro-office.example.test --editor-secret-file /private/editor-jwt \
  --storage-class YOUR_CLASS --output /private/kydrive-rendered
```

The secret file must have mode 0600. Output contains a Secret and is protected at 0700/0600; never commit it. Supply TLS Secrets through existing ingress/certificate tooling. For a Kubernetes drive, pass `--kubernetes-drive --drive-image REGISTRY/IMAGE@sha256:DIGEST --bulk-pvc EXISTING_INDEPENDENT_CLAIM` and create `drive-integrations` with OIDC/SCIM/bootstrap configuration. Read generated `INSTALL.txt` before applying.

The optional [`docker-compose.office.yml` production editor service](EURO-DOCKER.md) packages the same pinned image for Docker, with private JWT input, persistent bundled services and explicit resource bounds. Kubernetes remains supported.

Private-IP fetching stays disabled in the editor package (`ALLOW_PRIVATE_IP_ADDRESS=false`, `ALLOW_META_IP_ADDRESS=false`). If NAS DNS resolves privately, as on a LAN-only drive, Euro-Office cannot fetch `KYDRIVE_EDITOR_DRIVE_URL` until `ALLOW_PRIVATE_IP_ADDRESS=true`. That lifts the editor's private-address block for every fetch, so first constrain editor egress to the intended drive/proxy endpoints and record the explicit exception before enabling it. This delivery does not choose actual ingress/TLS or NAS filesystem settings without testing that infrastructure.


## Shared replica configuration groundwork

Multi-tenant startup is still refused. This R3 slice implements the shared configuration
check and `accept-replica-config` command for that future mode; it does not enable replicas,
change single-mode behavior, or qualify a hosted service. Actual shared S3 configuration
and its storage-root fingerprint must land before the startup gate opens. There is no
shared disk fallback and no new replica-mode flag.

Before bootstrap, a multi-mode PostgreSQL process compares its effective configuration
(including installer-supplied settings and validated Euro-Office defaults) with the private
`replica_config_fingerprint` row. It records HMAC-SHA256 checks under the retained deployment
key, with a product and field label; raw values never enter the record. Fields cover the
session/cookie, CAPTCHA, provisioning and KyIdentity settings, KyPost pairing, Euro-Office
origins/secret, limits/search, backup schedule/private-recovery policy and tenancy/staff
identity. Device identity and KyPost checks cover key/secret bytes, not host-specific file
paths. `SESSION_TTL` is an internal fixed field, not an environment option.

Data directory, database DSN/pools, bind address, trusted proxies, local backup directories,
timeout tuning and certificate-file paths are host-local. Each replica keeps its own data
directory. Unused inherited generic OIDC/SAML fields do not configure a working integration.
Dynamic KyIdentity peer answers and admin settings already come from shared authorities.
When a future release adds a shared configuration field, its first successful check adds it;
older binaries leave unknown fields intact. This is not a schema downgrade guarantee.

A mismatch reports only field names and refuses startup. For a deliberate mutable change,
stop every replica, install the same configuration on every replica, then run
`/app/kydrive-server accept-replica-config` once with that environment and retained keys
before restarting. It loads existing configuration and records changed field names in
`admin.replica_config_accepted` in the same transaction as the update. It cannot prove the
other stopped hosts were updated; that remains the operator's responsibility. Deployment
key, device identity key, tenancy mode and staff org are immutable here. A corrupt record
also fails closed: restore a valid record rather than accepting over an unknown key check.
The command currently reaches the same closed multi-mode loader gate as startup.

The private row is omitted from all bulk settings responses (including administrators).
Restore reset removes it atomically with restored grants, so a restored database must
establish its configuration again before serving. This does not implement PostgreSQL
backup/restore or remove any other R3/R4/hosted qualification gate.


In multi mode on PostgreSQL, replicas coordinate through two session locks. The replica
holding `scheduled-worker` runs every background job (editor revocations, retention and
purge, suite groups, the org poll, blob sealing, expired-row cleanup, the search indexer and
the backup schedule); the others retry every 10 seconds, and a new holder waits 3 seconds
before starting so a worker that lost its session has stopped its jobs. Scheduled, manual
and CLI (`deposit`) backups each take `backup-run`; a second one anywhere is refused as
already in progress (409 on the admin route). A lock lives on a reserved session, so use
direct database connections or session pooling, never transaction pooling, and allow at
least three connections per replica (`KY_DB_MAX_OPEN_CONNS`; multi mode raises a lower
value to 3). Neither lock is an exactly-once or stale-writer fence: jobs are idempotent, and
a deposit already sealing or uploading when its session dies finishes. Multi-mode startup
and acceptance gates all remain.
