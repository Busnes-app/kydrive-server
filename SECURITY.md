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
- Deactivating an identity in KyIdentity must remove its access; group-owned content remains after a member leaves.

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

