# Container-local installer setup

`/app/kydrive-server apply-setup --capabilities` advertises supported features.
`apply-setup --file /app/data/installer/bundle.json` sends a protected bundle to the
running product's private socket. `--check` inspects without writes; `--restart`
requests graceful shutdown so the workload controller can activate saved configuration.
The installer waits for a successful check with no pending activation. Use Docker's
`always`/`unless-stopped` policy or a Kubernetes controller that restarts the process.

The binary and the new `ky-primitives/applysetup` package must be built together until
that library package has a published release. The development workspace is explicit;
existing released images do not gain this command automatically.

The `bundle-preparation` capability enables secret-free metadata preparation without a
running server. Set `KY_DATA_DIR` to the existing protected runtime-owned data directory,
then send the envelope below on stdin:

```sh
/app/kydrive-server apply-setup --stage --file /app/data/installer/271333e2-c221-4601-b249-563b3d828530/setup.json < metadata.json
/app/kydrive-server apply-setup --stage --check --file /app/data/installer/271333e2-c221-4601-b249-563b3d828530/setup.json < metadata.json
```

The destination must be exactly `<KY_DATA_DIR>/installer/<setup UUID>/setup.json`.
Linux ancestry and directory ownership/modes are checked; private directories are created
only on apply. Strict bounded JSON is canonicalized and published without replacing any
retained file. Identical resume returns `present`; changed or unsafe intent is refused.
Check writes nothing and refuses missing metadata. Output is a fixed
`ky.setup-preparation/1` receipt with `service`, `setupId` and `status` (`created` or
`present`), not a product-readiness report. Stage cannot combine with capabilities/restart.

Credential references must be distinct clean absolute paths without control characters;
they cannot alias the bundle. SSO requires a native `scimTokenFile` and refuses
`directorySecretFile`, which belongs to signed webhook integrations. Recovery pairing
intent is refused before staging. The referenced
files may be absent during metadata staging. Preparation creates no credentials,
database, socket, accounts or SSO settings. Credential generation/import and receiver-owned
SCIM-token transfer to KyIdentity remain separate unfinished installer work. Ordinary
apply/check below requires those files and a running server.

The bounded JSON envelope is `ky.product-setup/1`, service `kydrive`, with a stable
UUID `setupId`. Credentials are absolute file references inside the container, owned
by its runtime user with no group/other access. Bundle and credential symlinks are
refused. Nothing is copied into workstation installation state.

```json
{
  "schema": "ky.product-setup/1",
  "service": "kydrive",
  "setupId": "271333e2-c221-4601-b249-563b3d828530",
  "sso": {
    "issuerUrl": "https://identity.example.test",
    "clientId": "drive",
    "clientSecretFile": "/app/data/installer/oidc.secret",
    "redirectUri": "https://drive.example.test/api/sso/kysignon/callback",
    "scimTokenFile": "/app/data/installer/scim.secret"
  },
  "admins": [{"issuer": "https://identity.example.test", "subject": "owner-subject", "username": "owner-admin"}],
  "everyday": [{"issuer": "https://identity.example.test", "subject": "everyday-subject", "username": "owner"}]
}
```

Set `KY_APP_URL` to the product origin and `KY_SCIM_TOKEN_FILE` to the same protected
SCIM credential before setup. Provisioning must be enabled. Configure the matching
assigned-only client and outbound SCIM connector in KyIdentity separately. Setup refuses
legacy webhook provisioning, automatic SSO provisioning, conflicting authentication
settings and SSO disabled with `KY_SSO_ENABLED=false`. It never changes the local recovery account or adopts an existing identity.

New directory identities have no local password. Explicit administrator and everyday
roles are recorded atomically with ownership and audit rows. Dedicated administrators
can manage shared workspaces, access and recovery, but cannot use personal workspaces,
file/editor workflows or mint service credentials. Existing mixed-use accounts remain
intact and require an explicit migration. SCIM deactivation disables either role and
revokes existing sessions. Re-running setup does not reactivate a disabled account.

Optional `backup` supports `directory`, `keep` and `interval` (0 or at least 15 minutes).
These configure the existing recoveryclient local-copy/scheduler wiring. Recovery pairing
through this bundle is not implemented; a `recovery` field fails before any writes. A
pinned recovery key and the independent Restic bulk destination remain required to back
up actual drive content. This adapter does not claim a backup without those prerequisites.

`GET /healthz` reports `clientIp`, `proxyHeadersTrusted` and `scheme` for the public
forwarded-header check. Only `KY_TRUSTED_PROXIES` peers may supply forwarding metadata;
the last forwarded address/scheme belongs to the nearest proxy. Configure the edge to
overwrite scheme metadata and append the observed client address.

Verification covers real private-socket activation, transactional rollback on collisions,
read-only retries, dedicated administrator denial and public SCIM session revocation.
Focused native preparation tests cover creation/check/resume, conflicting intent,
refused recovery intent without writes, unknown/oversized input, safe receipts and absence of invented credentials/socket.
Production readiness additionally requires signed KyIdentity login, bulk recovery and
editor acceptance; this source adapter does not establish those deployment gates.
