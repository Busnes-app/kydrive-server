# Security Policy

KyDrive is a general drive backend for suite applications. Applications use authenticated service APIs for file access; KyIdentity owns people and groups while KyDrive owns workspace permissions.

## Reporting vulnerabilities

Report security vulnerabilities privately through GitHub Security Advisories:
<https://github.com/Busnes-app/kydrive-server/security/advisories>. Do not open a public issue.
Include the affected area, reproduction steps, impact, and affected versions.
We aim to acknowledge reports within two business days and will coordinate a fix
and disclosure.

## Trust boundaries and limitations

- The server stores file contents and metadata; keep the data volume, database, and backups private.
- Access is per-identity and per-workspace. Personal workspaces are private to their account; group workspaces are scoped to the group.
- Service API tokens are credentials for the calling application; scope them to what that application needs and never log them.
- The opt-in vault catalogue uses signed device requests and an independently administered
  checkpoint witness. Trusted clients must pin both member and witness keys. A copied or
  older witness database cannot serve as the latest witnessed state. See
  [Vault integration](docs/VAULT-INTEGRATION.md).
- The separate-origin vault browser is a staging fixture for disposable data. Its
  origin publisher controls the code that handles browser keys. Q13 requires signed
  native packages and a signed extension for user-facing decryption.
- Deactivating an identity in KyIdentity must remove its access; group-owned content remains after a member leaves.
- KyPost ([docs/KYPOST.md](docs/KYPOST.md)). Whoever holds the KyDrive–KyPost pair secret
  (a compromised KyPost) can, for any active everyday account whose KyPost switch is on: add
  new files (within quota, `KYDRIVE_MAX_FILE_SIZE` and 8 live tickets) to that person's
  `Mail attachments` folder, put reader organisation links on files directly in it, and read
  that person's free quota, and read the bytes of a file that person chose for KyPost in the
  KyDrive picker's copy mode within the last 24 hours and may still read. While the
  administrator's guest-link policy is on and the person turned on their second switch (off by
  default), it can also put view-only guest links, within the policy's `max_days`, on files in
  `Mail attachments`: those open for anyone holding the URL. It cannot read any other file,
  nor list, download, move, trash, purge or replace any file, share with people, make any other
  guest link, mint credentials or editor sessions, change either switch, or reach shared
  workspaces or other folders; the peer context in
  `internal/drive/access.go` refuses all of it, so a bug in a route cannot widen it. An
  organisation link it makes opens only for signed-in KyDrive members. Turning the switch off
  stops it for that person at once, unpairing in KyIdentity (or removing the environment
  variables) stops it for everyone within the hourly refresh; rotate the pair secret in
  KyIdentity after a compromise. Upload tickets are 15-minute single-file credentials carried in
  a URL path: keep `/api/suite/upload/` out of reverse-proxy access logs. KyDrive itself logs no
  request URLs.

## Two-factor sign-in

Two-factor sign-in is provided by KyIdentity sign-in; KyDrive has no local two-factor. Local
password accounts (the bootstrap and recovery administrator) sign in with their password.
TOTP secrets and recovery codes stored by earlier releases stay in the database, sealed and
unused.

## Key strength and TLS

[docs/STRONG-CRYPTO.md](docs/STRONG-CRYPTO.md) lists every key KyDrive generates or
accepts, the minimums it enforces, and the optional `GODEBUG=fips140=on` profile that
limits its outbound TLS to strong certificate keys. Startup refuses a `KY_SESSION_SECRET`
or `KY_KYSIGNON_HMAC_SECRET` shorter than 16 bytes; replace only that value. Sign-in
refuses an OIDC provider publishing an RSA key below 2048 bits until the provider rotates it.

## Deployment requirements

- Run behind authenticated HTTPS; never add a default that downgrades to cleartext.
- Keep the data volume, database, and backups private; stop the server before copying the data directory.
- Configure quotas and rate limits for uploads, downloads, and auth endpoints.

## Security-sensitive changes

Changes to authentication, workspace authorization, sharing, service tokens, trash/versioning, quotas, or logging require tests for both the expected path and the relevant
failure or attack path, plus a description of the trust boundary in the pull
request.

## Related documents

- [CONTRIBUTING.md](CONTRIBUTING.md) — contribution and review requirements
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — community expectations
- [README.md](README.md) — what this project is and how to run it
- [docs/STRONG-CRYPTO.md](docs/STRONG-CRYPTO.md) — key-size rules and the strong-key TLS profile

