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
- Production startup requires an explicit, durable `KY_SESSION_SECRET`. The encryption key comes from `KY_ENCRYPTION_KEY` when set, otherwise from the keyfile at `<DataDir>/encryption.key`, which `keyfile.LoadOrCreate` mints on first start; either is a valid production configuration. Any `<DataDir>/encryption.key` on disk sets `Security.KeyInDataDir`, even when `KY_ENCRYPTION_KEY` is set (a leftover or restored file), and so does any entry under the drill directory (`KY_DRILL_DIR`; an interrupted recovery drill leaves its opened capsule, key included, until the next drill sweeps it; must match `backup.DrillRoot`): a copy of the data directory then carries the key to its encrypted blobs, so startup and the Recovery screen warn.

- `KY_BACKUP_DEPOSIT_INTERVAL` is a Go duration (default `24h`), only the default for the schedule the admin screen stores; `0` is off, anything else below `MinDepositInterval` (15m) or negative fails startup. `KY_BACKUP_DIR` (default empty, off) is the sealed local-copy directory and `KY_BACKUP_KEEP` (default 7) how many to retain; below 1 fails startup because the lib refuses it at write time. `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` (default false) admits RFC1918 and CGNAT KyRecovery destinations only.

- `KY_BACKUP_COLLECT_TIMEOUT` (default 2h) bounds one backup collection; `KY_DRILL_TIMEOUT` (default 2h) one drill; both refuse below 1m. `KY_DRILL_DIR` (default `<DataDir>/drill`, must be absolute when set) is the drill scratch root; the residue check above reads it.
- `LoadExisting()` is `LoadFromEnv` for operator commands on an existing deployment: it loads the keyfile with `keyfile.Load` (never mints one) unless `KY_ENCRYPTION_KEY` is set, and refuses a SQLite database file that does not exist, naming the expected path. `cmd/server` uses it for `bulk-init`, `deposit`, `backup-drill`, `export-capsule`, `set-directory-role`, `rename-local-admin` and `handover`; `init-admin` may create a first install.

- `KY_EXCALIDRAW_FONTS_DIR` (default empty, none served) is the directory holding Excalidraw's `Xiaolai/` CJK fonts; set but not a directory fails startup. The Docker image sets it to `/app/excalidraw-fonts`.

- `KY_READ_TIMEOUT` and `KY_WRITE_TIMEOUT` default to 5m and accept 15s–15m, supporting bounded streaming uploads. Production requires an explicit SCIM token when provisioning is enabled; SSO auto-provision defaults off.

## Verification
- `go test -v ./internal/config/...`
- `go test -v ./internal/auth/ -run TestClientIP` (the helper that consumes the allowlist)

## Child DOX Index
None.
