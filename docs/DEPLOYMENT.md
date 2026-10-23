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

To build from source instead, add the build overlay to every command, which tags the build `kydrive-server:local` whatever `KY_IMAGE` says: `docker compose -f docker-compose.yml -f docker-compose.build.yml build`, then `... up -d` with the same two files.

`bulk-init` is safe to rerun: when the repository already opens with this deployment's key it reports that and changes nothing. A repository at that location that the key cannot open is refused (`bulk repository exists but this deployment's key does not open it`); point `KYDRIVE_BULK_BACKUP_REPOSITORY` at the right repository rather than reinitializing.

### Upgrading

- Unreleased (after 0.4.2): Compose defaults to the published image instead of `kydrive-server:local`. Installs that build from source keep working with the build overlay. An install that ran a separately built `kydrive-server:local` without the overlay, and has no `KY_IMAGE` in `.env`, now pulls the release image: set `KY_IMAGE=kydrive-server:local` to keep the local build.
- Unreleased (after 0.4.2): the Compose default moves from `:0.4.2` to `:stable`, so `docker compose pull` takes each new release, daily dependency releases included. An install that wants to stay on a version sets `KY_IMAGE` to it (or pins the digest).
- To move to a new release on `:stable`, check out its tag, then `docker compose pull` and `docker compose up -d`. With a pinned `KY_IMAGE`, set it to the new version or digest first.

The root Compose package serves KyDrive only. It binds HTTP to loopback for the existing HTTPS proxy. Euro-Office runs on Docker with `docker-compose.office.yml` (see [Docker setup](EURO-DOCKER.md)), or in Kubernetes. Use an explicit Docker-network bind address only when the proxy requires one. SQLite's WAL and durable flushes require a local filesystem with working file locks/fsync, not an SMB-mounted database. A NAS data share is not an independent backup; mount bulk storage from a separate failure domain. Trash and versions count toward quota until purged (see Retention and permanent delete). Restic snapshot pruning (Recovery screen, Document retention) is opt-in, off by default, never forgets the newest KY_BACKUP_KEEP deposit snapshots. KyRecovery-held capsules older than the policy may name forgotten snapshots: set the policy to outlive KyRecovery's retention. An append-only rest-server or Object Lock repository refuses prune by design.

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
