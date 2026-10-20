# kydrive standalone installation

## Before you start

These steps are for a **new, independent installation** from this repository. Use the
README’s upgrade/restore procedure for an existing instance. Keep its volumes, keys,
configuration and Compose overlays; do not replace its environment files with examples.

Use Linux with Docker Engine and Docker Compose 2.24 or newer, Git, and permission to use Docker.
Choose a release/revision and use its matching Compose files and documentation. Image tags
can move: follow the README/Compose attestation and digest-pinning procedure before relying
on a published image. Source builds use the separate build overlay where provided.

Clone this repository, enter its directory, then run the product steps below:

```sh
git clone https://github.com/Busnes-app/kydrive-server.git
cd kydrive-server
```

Each service needs its own directory and persistent storage. Several products use host
port 8080 by default; install them on separate hosts or configure distinct host ports and
matching public origins. For remote users, provide a TLS reverse proxy and a hostname you
control. Trust only the proxy’s actual peer address, and forward the original scheme/client
address according to the product’s proxy guide.

## Install a separate drive instance

The drive is a separate service, but everyday users need **KyIdentity OIDC and SCIM
provisioning**. The local bootstrap administrator is for managing the service. Euro-Office
is optional for document editing, and desktop sync needs an M7-compatible server.

```sh
[ -e .env ] || (umask 077; cp .env.example .env)
[ -e .env.runtime ] || (umask 077; cp .env.runtime.example .env.runtime)
chmod 600 .env .env.runtime
```

In `.env`, choose `KY_UID`/`KY_GID`, data/backup paths, host port and `KY_BULK_PATH`.
Create all mount directories owned by that UID/GID. The bulk repository must be an
independently mounted destination for production recovery. SQLite requires local file
locking/fsync; do not put its database on SMB.

In `.env.runtime`, set the real HTTPS `KY_APP_URL`, a generated durable
`KY_SESSION_SECRET`, and your KyIdentity issuer/client secret and matching SCIM token.
Register callback `<drive origin>/api/sso/kysignon/callback`, keep
`KY_SSO_AUTO_PROVISION=false`, and follow [identity and permissions](docs/PERMISSIONS.md#identity-and-permissions)
for subjects, groups and administrator grants. Configure a TLS proxy with exact trusted
peers (`KY_TRUSTED_PROXIES` in `.env.runtime`) as described in the README. Configure the separate editor and its JWT secret only
if editing is needed; otherwise clear `KYDRIVE_EDITOR_URL` and `KYDRIVE_EDITOR_SECRET`
from the example. Example origins are not working services.

```sh
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose exec app cat /app/data/initial-admin-password
docker compose exec app /app/kydrive-server bulk-init
curl -fsS http://127.0.0.1:8080/healthz
```

Use the selected host port if changed. Read the generated `admin` password privately,
replace it at first login and remove the bootstrap file. Provision an everyday user,
assign a workspace, then test upload/download and offboarding. An admin login alone does
not verify staff access. `bulk-init` refuses an existing repository opened by another key;
preserve it and find the correct key instead of reinitializing.

The capsule seals metadata/keys and references bulk snapshots; it is not a copy of every
file. Complete both parts of [recovery](docs/RESTORE.md). For source builds select
`docker-compose.yml` plus `docker-compose.build.yml` on **every** Compose command, build,
and start the same overlay selection. See [NAS package](docs/DEPLOYMENT.md#nas-package),
[pilot evidence](docs/PILOT.md) and [acceptance limits](docs/ACCEPTANCE.md).

## Keep it recoverable

Preserve the database **and deployment keys**, not just the image. Configure recovery using
the linked restore runbook, retain an independent copy, and complete a scratch restore with
custodian cards before treating recovery as verified. A synthetic drill uses a throwaway
key and does not prove that the real cards are available or correct.

For upgrades, back up first, read the chosen release’s migration notes, and reuse the same
Compose project, volume mounts and overlay selection for every command. `docker compose
pull` followed by `docker compose up -d` updates a published-image installation; a digest pin
must be deliberately changed after verification. A source install must keep its build
overlay and rebuild. `docker compose down -v` deletes managed volumes: it is not an upgrade.

## What verification means

The startup checks above prove only the checks they perform. Use the README’s development
section and the repository CI workflow for source/build tests; a passing health response or
screenshot is not a substitute. Record the revision, commands, environment and skips when
reporting results. Design plans and dated evidence remain records of their stated scope.
