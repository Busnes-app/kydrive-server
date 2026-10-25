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

