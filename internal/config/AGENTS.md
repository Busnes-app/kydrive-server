# Config

## Purpose
Manages environment and file-based configuration loading, defaults, and type conversions for kydrive-server.

## Ownership
Owns environment variable parsing, configuration validation, default fallbacks, and security key generations.

## Local Contracts
- `KY_SCIM_TOKEN_FILE` reads an existing private target-owned credential through `applysetup.Secret`; conflicting token sources fail. Installer configuration references credentials, never embeds them. Startup merges only absent SSO settings, refusing operator conflicts and automatic provisioning; SCIM must already have the matching durable token.
- `LoadFromEnv() (*Config, error)` must supply safe, valid defaults for all subsystems. A blank variable means its default; an unparseable integer or boolean fails startup naming the variable. `KY_ENV` is `production` or `development` only. Any of `KY_SESSION_SECRET`, `KY_KYSIGNON_SECRET`, `KY_KYSIGNON_HMAC_SECRET`, `KY_SCIM_TOKEN`, `KYDRIVE_EDITOR_SECRET`, `KY_ADMIN_PASSWORD` containing `replace-with` fails startup in every environment; `.env.runtime.example` ships them empty. An unreadable keyfile path or drill directory (other than absent) fails startup.
- Never log plaintext secrets or sensitive tokens.
- `KY_TRUSTED_PROXIES` is a comma-separated list of reverse-proxy IPs or CIDRs, empty by default, parsed once at startup into `[]netip.Prefix`; an unparsable entry fails startup. Only a request whose peer address is in the list may speak for another client through `X-Forwarded-For`. `0.0.0.0/0` and `::/0` are refused at startup; list only the proxy's own address or subnet.
- `KY_RATE_LIMIT_EXEMPT_NETWORKS` (default empty) is validated at startup by `ky-primitives/netallow` and kept in its canonical form in `Security.RateLimitExemptNetworks`; an invalid, too-broad (wider than /16 or /48), loopback or over-256 list fails startup naming the variable. `internal/api` combines it with the admin setting.
- `KY_SESSION_SECRET` and a nonempty `KY_KYSIGNON_HMAC_SECRET` below `crypto.MinHMACKeyBytes` fail startup naming the variable, before any key file is created or read (`key_strength_test.go`).
- Production startup requires an explicit, durable `KY_SESSION_SECRET`. The encryption key comes from `KY_ENCRYPTION_KEY` when set, otherwise from the keyfile at `<DataDir>/encryption.key`, which `keyfile.LoadOrCreate` mints on first start; either is a valid production configuration. Any `<DataDir>/encryption.key` on disk sets `Security.KeyInDataDir`, even when `KY_ENCRYPTION_KEY` is set (a leftover or restored file), and so does any entry under the drill directory (`KY_DRILL_DIR`; an interrupted recovery drill leaves its opened capsule, key included, until the next drill sweeps it; must match `backup.DrillRoot`): a copy of the data directory then carries the key to its encrypted blobs, so startup and the Recovery screen warn.

- `KY_BACKUP_DEPOSIT_INTERVAL` is a Go duration (default `24h`), only the default for the schedule the admin screen stores; `0` is off, anything else below `MinDepositInterval` (15m) or negative fails startup. `KY_BACKUP_DIR` (default empty, off) is the sealed local-copy directory and `KY_BACKUP_KEEP` (default 7) how many to retain; below 1 fails startup because the lib refuses it at write time. `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` (default false) admits RFC1918 and CGNAT KyRecovery destinations only.

- `KYDRIVE_TLS_CERT_FILE` (absolute when set, else refused at load; `Security.TLSCertFile`) names the reverse proxy's PEM certificate; read at each pin request, not at load, so a renewal needs no restart (`TestTLSCertFileMustBeAbsolute`). `KYDRIVE_TLS_PIN_PROBE` (bool, default false; `Security.TLSPinProbe`) lets the pin route sign a probed leaf when no file is set.
- `KYDRIVE_IDENTITY_KEY_FILE` (absolute when set; default `<DataDir>/identity.key`) holds the hex Ed25519 seed in `Security.IdentityKey` that signs TLS pin rollover. `LoadFromEnv` creates it once (`keyfile.LoadOrCreate`, 0600); `LoadExisting` only reads it and leaves nil when absent. A wrong-length or unreadable file fails startup and is never overwritten. Never rotated: linked devices hold its public half. `identity_key_test.go`.
- `KY_BACKUP_COLLECT_TIMEOUT` (default 2h) bounds one backup collection; `KY_DRILL_TIMEOUT` (default 2h) one drill; both refuse below 1m. `KY_DRILL_DIR` (default `<DataDir>/drill`, must be absolute when set) is the drill scratch root; the residue check above reads it.
- `LoadExisting()` is `LoadFromEnv` for operator commands on an existing deployment: it loads the keyfile with `keyfile.Load` (never mints one) unless `KY_ENCRYPTION_KEY` is set, and refuses a SQLite database file that does not exist, naming the expected path. `cmd/server` uses it for `bulk-init`, `deposit`, `backup-drill`, `export-capsule`, `set-directory-role`, `rename-local-admin` and `handover`; `init-admin` may create a first install.

- `KY_EXCALIDRAW_FONTS_DIR` (default empty, none served) is the directory holding Excalidraw's `Xiaolai/` CJK fonts; set but not a directory fails startup. The Docker image sets it to `/app/excalidraw-fonts`.

- `KYDRIVE_KYPOST_URL` (`https://host`, no path, query or credentials; `KyPostURL` also checks KyIdentity's) and `KYDRIVE_KYPOST_SECRET_FILE` (read through `applysetup.Secret`, at least 32 bytes once trimmed) pair KyPost from the environment (`Drive.KyPostURL`, `Drive.KyPostSecret`, never serialised); either alone or a bad value fails startup naming the variable. `kypost_test.go`.
- `KYDRIVE_TENANCY` is `single` (default) or `multi`, validated before any key creation (`Database.Tenancy`, with `KYDRIVE_STAFF_ORG` in `Database.StaffOrg`). Multi mode requires `KY_ENCRYPTION_KEY` and an existing private `KYDRIVE_IDENTITY_KEY_FILE` outside `KY_DATA_DIR` (resolved ancestors included); it never creates either key and refuses local `encryption.key`/`identity.key` copies. Release builds (`gate_release.go`, `//go:build !kye2e`, `multiTenantBuild = false`) always refuse `KYDRIVE_TENANCY=multi` with `KYDRIVE_TENANCY=multi is not available in this release`. Only a `-tags kye2e` suite end-to-end test build (`gate_kye2e.go`, `multiTenantBuild = true`) opens multi-tenant startup, and that build refuses any `KY_KYSIGNON_ISSUER` or installer SSO issuer outside `https://*.test` (`testIssuer`). Single mode retains its existing key creation/loading behavior. The drive store records the mode (`drive.recordTenancy`).
- Hosted storage requires PostgreSQL, `KY_CELLS` (1–32 unique `^[a-z][a-z0-9-]{1,31}$` slugs), and `KYDRIVE_S3_ENDPOINT`, `_BUCKET`, `_ACCESS_KEY`, plus `_SECRET_KEY` or private `_SECRET_KEY_FILE`. `_REGION` defaults to `us-east-1`; `_PATH_STYLE` defaults true. Partial or invalid config fails closed, and conflicting secret sources fail without echoing their bytes. Single mode refuses these hosted settings; `KY_REGION` is retired. `docs/DEPLOYMENT.md` owns operations and the test-build restriction.
- `KYDRIVE_CONTENT_SEARCH` (boolean, default on) runs the content search index worker and content matching (`Drive.ContentSearch`); off leaves file name search alone.
- `KYDRIVE_MAX_FILE_SIZE` (resumable upload size) and `KYDRIVE_MAX_ZIP_SIZE` (folder zip plaintext total) default to `10GiB` and accept a whole number of `MiB`, `GiB` or `TiB` from 1 MiB to 1 TiB; anything else fails startup naming the variable.

- `KY_READ_TIMEOUT` and `KY_WRITE_TIMEOUT` default to 5m and accept 15s–15m, supporting bounded streaming uploads. Production requires an explicit SCIM token when provisioning is enabled; SSO auto-provision defaults off.

- `replica.go` fingerprints effective shared settings with product/field-separated HMAC-SHA256 under the retained deployment key. It includes key material, not secret-file paths; excludes host-local data, bind/pool/proxy/certificate paths and backup directories. `SESSION_TTL` names the fixed in-process setting, not a new environment variable. Unused legacy OIDC/SAML fields are not active integrations. It fingerprints `KY_CELLS` in sorted order and S3 endpoint, bucket, region and addressing mode; credentials are excluded so access-key rotation does not change the storage identity.

- Vault control-plane settings are both present or both absent. An absolute owner-only
  `KYDRIVE_VAULT_PUBLISHER_KEY_FILE` holds raw hybrid signing seeds.
  `KYDRIVE_VAULT_WITNESS_KEY` pins the independent witness public key in standard base64.
  Startup refuses the same key in both roles. Replica checks fingerprint the two public keys.

## Verification
- `go test -v ./internal/config/...`
- `scripts/check-test-build-tag.sh` (keeps `kye2e` inside the gate files, forbids build tags in release paths, and runs `internal/config` under `-tags kye2e`)
- `go test -v ./internal/auth/ -run TestClientIP` (the helper that consumes the allowlist)

## Child DOX Index
None.
