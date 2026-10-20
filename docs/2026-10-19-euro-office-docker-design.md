# Euro-Office production Docker packaging — 2026-10-19

## Decision and verified gap

The user selected production Euro-Office on Docker as well as Kubernetes. This supersedes the earlier Kubernetes-only placement in the workspace `AGENTS.md` and `DRIVE_IMPLEMENTATION_PLAN.md`; those owning contracts must be updated with the implementation. The existing Kubernetes package stays supported. Production provenance, HTTPS, editing, revocation, restart/rollback and recovery acceptance gates remain applicable to both runtimes.

The gap is real: [README.md](../README.md) explicitly says no Euro-Office Compose service ships. [deploy/render.py](../deploy/render.py) already provides a tested image, persistence paths, JWT settings and Kubernetes packaging. Reuse that distribution and contract rather than introducing a second editor build.

## Primary-source findings

- Use the existing amd64 image `ghcr.io/euro-office/documentserver@sha256:beca380debb9b4eadb7e4c662d112a325c5df0f2179453a4affd8e6ba5d00562`. [The artifact record](EURO-PROVENANCE.md) links that digest to Euro-Office's release workflow at source commit `4ba0c0f202ae484eb202f24c59ddf5f4a7004161`. Workflow correspondence is established; independently signed GitHub provenance and a reproducible rebuild are not.
- The [pinned standalone Dockerfile](https://github.com/Euro-Office/DocumentServer/blob/4ba0c0f202ae484eb202f24c59ddf5f4a7004161/build/.docker/standalone.bake.Dockerfile) bundles PostgreSQL, RabbitMQ, Redis, Nginx and supervisord. It starts `/entrypoint.sh` as root. Keep those service databases and the editor cache in separate persistent mounts, matching the existing Kubernetes renderer.
- The [pinned entrypoint](https://github.com/Euro-Office/DocumentServer/blob/4ba0c0f202ae484eb202f24c59ddf5f4a7004161/build/scripts/standalone/entrypoint.sh#L95-L114) reads `/var/www/euro-office/Data/.private/jwt_secret` when `JWT_SECRET` is unset. It assigns `JWT_SECRET_FILE` internally, so setting an environment variable of that name does not select a different file. Mounting the protected input directly at the native path avoids exposing the JWT through Compose interpolation or container `Config.Env`. An empty/missing secret otherwise causes the vendor to generate a different key; explicitly refuse this case before invoking the vendor entrypoint.
- [Docker volume documentation](https://docs.docker.com/engine/storage/volumes/#mounting-a-volume-over-existing-data) specifies that an empty named volume is populated with the image directory's existing contents. Five separate named volumes therefore provide native initial seeding; omit `nocopy`. Bind mounts do not provide this behavior. [Engine implementation](https://github.com/moby/moby/blob/master/daemon/create_unix.go) calls `populateVolumes` during container creation. Existing volumes remain in place when containers are replaced. This is the shortest packaging approach; it does not establish crash-atomic copy-up or accept manually prefilled volumes.
- [Compose secrets documentation](https://docs.docker.com/reference/compose-file/services/#secrets) allows an absolute `target` path. File-backed local Compose secrets are bind-mounted; `uid`, `gid` and `mode` cannot enforce host-file permissions. Require the source file to have mode 0600 and validate the mounted regular file before vendor startup.
- [The existing renderer](../deploy/render.py) reserves 2 GiB RAM and limits it to 4 GiB. It defaults private-IP and metadata-IP fetching off and supplies HTTPS ingress with 3600-second read/send timeouts and a 129 MiB body limit. [README.md](../README.md) explains that a private-address exception requires separately constrained egress. These remain deployment inputs, not a reason to disable TLS verification.

Local image inspection, without starting a container, verified that `/entrypoint.sh` is byte-for-byte the cited pinned source: SHA-256 `14fc68d1066f995d69e45223386eb12abae38c9db23f38599539ace9e5ef0a24`. `/usr/bin/curl`, `/usr/bin/wget`, `/usr/bin/stat`, `/usr/bin/wc` and `/bin/bash` exist. Temporary stopped inspection containers were removed. No image build, running editor or integration test was performed for this research.

## Recommended overlay

Add `docker-compose.office.yml`, explicitly selected with the existing base Compose file. No development-only profile is needed: this is production-capable packaging, subject to the gates below. Keep one instance and stable Compose project/volume names; do not add a `container_name` that prevents independent disposable projects.

```yaml
services:
  euro-office:
    image: ghcr.io/euro-office/documentserver@sha256:beca380debb9b4eadb7e4c662d112a325c5df0f2179453a4affd8e6ba5d00562
    platform: linux/amd64
    hostname: euro-office
    restart: unless-stopped
    stop_grace_period: 2m
    mem_reservation: 2g
    mem_limit: 4g
    memswap_limit: 4g
    cpus: 2
    pids_limit: 512
    ports:
      - "${KY_OFFICE_BIND_IP:-127.0.0.1}:${KY_OFFICE_HTTP_PORT:-8082}:80"
    environment:
      JWT_ENABLED: "true"
      ALLOW_PRIVATE_IP_ADDRESS: "${KY_OFFICE_ALLOW_PRIVATE_IP_ADDRESS:-false}"
      ALLOW_META_IP_ADDRESS: "false"
      USE_UNAUTHORIZED_STORAGE: "false"
      NGINX_CLIENT_MAX_BODY_SIZE: 129m
    entrypoint: ["/bin/sh", "-ec"]
    command:
      - |
        key=/var/www/euro-office/Data/.private/jwt_secret
        test -f "$$key" && test ! -L "$$key" || { echo 'Euro-Office JWT file must be regular' >&2; exit 1; }
        test "$$(stat -c %a "$$key")" = 600 || { echo 'Euro-Office JWT file must have mode 0600' >&2; exit 1; }
        value=$$(cat "$$key")
        test "$${#value}" -ge 32 || { echo 'Euro-Office JWT must contain at least 32 bytes' >&2; exit 1; }
        unset value
        exec /entrypoint.sh
    secrets:
      - source: office-jwt
        target: /var/www/euro-office/Data/.private/jwt_secret
    volumes:
      - office-postgresql:/var/lib/postgresql
      - office-rabbitmq:/var/lib/rabbitmq
      - office-redis:/var/lib/redis
      - office-runtime:/var/lib/euro-office
      - office-data:/var/www/euro-office/Data
    healthcheck:
      test: ["CMD-SHELL", "curl --fail --silent --show-error http://127.0.0.1/healthcheck | grep -qx true"]
      interval: 30s
      timeout: 10s
      start_period: 3m
      retries: 5
secrets:
  office-jwt:
    file: ${KY_OFFICE_JWT_FILE:?Set the mode-0600 shared editor JWT file}
volumes:
  office-postgresql:
  office-rabbitmq:
  office-redis:
  office-runtime:
  office-data:
```

The stable hostname matters for the bundled RabbitMQ node's persisted identity. Resource ceilings above CPU/memory are proposed packaging defaults, requiring actual startup qualification; they are not measured capacity. The entrypoint check uses the image's tools and never prints the key. The 32-byte minimum should match the renderer; use a generated printable ASCII/base64url secret so shell byte/character differences and trailing newline stripping do not create inconsistent interpretations. Require exactly the same key bytes in Drive's existing protected `.env.runtime` `KYDRIVE_EDITOR_SECRET`. The Drive process does not currently accept an editor-secret `_FILE` setting; adding one is a separate product configuration change, unnecessary for this overlay.

No extra helper is required for this named-volume variant. If operator-selected host bind paths are a requirement, reuse the renderer's explicit seeding contract instead: the same pinned image, one owned init container, per-directory markers, source ownership/mode preservation, refusal of non-empty unmarked destinations, and resumable staging. Do not silently replace named volumes with empty bind mounts.

## Proxy, networking and acceptance

Publish HTTP on loopback by default. Route the public editor origin through HTTPS Nginx Proxy Manager with WebSockets, 3600-second read/send timeouts, 129 MiB body allowance and forwarded host/protocol headers. Drive's browser editor URL and authenticated file/callback origin must remain HTTPS. The editor-to-Drive route is authenticated HTTPS even when both run on Docker; do not turn `USE_UNAUTHORIZED_STORAGE` on to work around certificates. Install a legitimate trust chain for any private CA.

Containerized proxies should use the suite's shared network override. KyPost remains owner of `kypost-net` per the user's decision; other products/proxies join its external network. Keep the production editor overlay independently usable with a host proxy, and use an explicit network overlay for a proxy container. Do not create another owner for the shared network. Pin the proxy address and configure Drive's trusted-proxy value narrowly. Joining a bridge does not itself constrain editor egress: private-IP fetching remains off until operator firewall/proxy policy constrains the intended HTTPS destinations.

Before claiming production Docker readiness, execute: Compose interpolation/structure checks without printing resolved secrets; empty-volume startup; health; inspection confirming no JWT value in configured container environment; rejection of empty/short/insecure JWT files; persistence after container recreation; DOCX/XLSX/PPTX edit/save/reopen over actual HTTPS; collaboration and live offboarding; editor restart plus rollback rehearsal; and recovery of required persistent state. Keep image provenance and the existing `docs/ACCEPTANCE.md`/`docs/PILOT.md` production gates explicit. A passing health endpoint only establishes process readiness.

## DOX pass for implementation

Update workspace placement preferences and `DRIVE_IMPLEMENTATION_PLAN.md`; product root Compose ownership/secret/network contracts; `deploy/AGENTS.md` only if rendering or a helper moves into its ownership; and README packaging instructions. `docs/AGENTS.md` already owns exact verified behavior and research notes can remain under that scope; its responsibilities are unchanged by this investigation.
