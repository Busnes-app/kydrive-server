# Euro-Office on Docker

Euro-Office may run on Docker or Kubernetes. This optional Compose service uses the
same digest-pinned amd64 distribution as `deploy/render.py`. Production release
still requires the provenance and integration gates in `EURO-PROVENANCE.md` and
`ACCEPTANCE.md`; packaging alone does not establish readiness.

## Configure and start

Generate a printable random JWT into a protected file on the editor host (never
in argv or source control). Set its absolute path as `KY_OFFICE_JWT_FILE` in `.env`.
It must be a regular file with mode 0600 and at least 32 bytes. Configure exactly
the same value as `KYDRIVE_EDITOR_SECRET` in Drive's protected `.env.runtime`.
The editor reads the mounted file directly; no secret enters Compose interpolation.

Set `KYDRIVE_EDITOR_URL` to the editor's public HTTPS origin and
`KYDRIVE_EDITOR_DRIVE_URL` to Drive's HTTPS origin in `.env.runtime`. Both origins
must resolve and verify from browsers and the opposite service. Private CAs must
be trusted explicitly; never turn TLS verification off.

```sh
docker compose -f docker-compose.yml -f docker-compose.office.yml config --quiet
docker compose -f docker-compose.yml -f docker-compose.office.yml up -d euro-office
```

The editor can run on a different Docker host: copy this overlay and the JWT file
there and run it as a standalone Compose file. Keep the same Compose project name
and volume names on upgrades. Five named volumes preserve bundled PostgreSQL,
RabbitMQ, Redis, editor runtime and editor data, seeded by Docker from the pinned
image at first creation. Do not replace these mounts with empty host directories.
Never use `down --volumes` on an installation whose data must survive.

## HTTPS proxy

Port 8082 publishes on loopback by default. Put Nginx Proxy Manager in front of it,
with WebSockets, 3600-second read/send timeouts and a 129 MiB request-body limit.
For a containerized proxy, attach it and `euro-office` to the KyPost-owned external
`kypost-net` using a local override, retain the default network, and point NPM at
`euro-office:80`. Alternatively bind one LAN address for a proxy on another host.
HTTP between proxy and editor belongs on a protected network; browsers and
Drive/editor file-transfer and callback endpoints use authenticated HTTPS.

`KY_OFFICE_ALLOW_PRIVATE_IP_ADDRESS` defaults false. If Drive resolves to a private
address, first constrain editor egress to the intended Drive HTTPS endpoint using
host network policy or a controlled egress proxy, then explicitly opt in. Metadata
access remains disabled. This flag does not authorize arbitrary internal access.
The editor runs the bundled services as root, as the pinned distribution requires;
its 4 GiB RAM, two CPU and 512 PID ceilings need capacity qualification for the workload.

## Verify and roll back

Run `python3 scripts/accept-office-compose.py` with disk-backed `TMPDIR` under the
workstation resource limits. It owns a fresh project/volumes, checks private JWT
refusal, startup and recreation, then removes that fixture. It does not test Drive
editing or establish the production gates.


Require `/healthcheck` to return `true`, then exercise DOCX/XLSX/PPTX create,
edit/save/reopen, simultaneous editors, permission loss, interrupted save, restart,
and restore with real Drive. Back up all five editor volumes while stopped and
keep Drive's independently backed-up metadata/blobs and shared JWT. Retain the
previous pinned image and stopped-volume backup for rollback; never assume an
older image can read a database migrated by a newer one. No live services are
changed by adding the overlay.

Local acceptance: the disposable Compose check passed private-JWT refusal, editor health, absence of the JWT from container environment, and persistent data through container recreation. Drive editing and production provenance gates remain separate.
