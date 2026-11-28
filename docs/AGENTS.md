# KyDrive operator documentation

## Purpose
Own operator documentation, restore instructions, security references, integration contracts, and the mdBook user manual.

## Ownership
`EURO-DOCKER.md` owns the production Docker editor overlay, private JWT input (including the SELinux `container_file_t` label), named-volume persistence and proxy/acceptance runbook.
`INSTALLER-SETUP.md` owns the container-local setup schema, runtime activation and adapter limitations.
`RESTORE.md` is the recovery runbook. `VAULT-INTEGRATION.md` owns the vault control-plane runbook and readiness boundary. `superpowers/plans/` holds dated implementation plans and `superpowers/specs/` dated designs and options studies (`2026-11-15-content-search-options.md`: prefix and phrase search, shared items and PDFs without Euro-Office, awaiting the owner's choice; implementing an option updates it; `2026-11-20-tenancy-design.md`: multi-tenant (SaaS) mode, KyDrive's half of KyIdentity's downstream tenancy contract, approved with the owner's answers and current as-built progress; each build PR updates it; `2026-11-28-kydrive-pq-status.md`: which vault and CryptPad parts are post-quantum, hybrid or classical, and the marketing claims allowed; a change to vault, CryptPad or TLS crypto updates it); its roadmap file records milestone decisions. `DESKTOP-ACCESS.md` is the user guide for desktop WebDAV clients (rclone, Cyberduck) and `KyDrive.cyberduckprofile` its connection profile. `DEPLOYMENT.md` owns deployment configuration; `PERMISSIONS.md`, `CLIENTS-AND-EDITOR.md`, `RECOVERY-AND-ENCRYPTION.md` and `DEVELOPMENT.md` own the detailed topics moved from the README. `EURO-PROVENANCE.md` owns Euro-Office image provenance. `VERIFYING-RELEASES.md` owns release-image verification and the binary rebuild; update it when `release-image.yml`, the Dockerfile's builds or the image layout change. `STRONG-CRYPTO.md` owns the key-size table, the strong-key profile and its out-of-scope list; `check-strong-crypto-compose.py` proves the overlay changes nothing else across every Compose chain (add a new overlay to its list). `ASSURANCE-CASE.md` owns the threat model, trust boundaries and weakness → counter → code table; a change that moves a trust boundary, a security requirement or a listed code path updates it in the same change. `KYPOST.md` (contracts C, I2, J, K: KyPost's signed routes and Email a link) and `SUITE.md` (contract I: the picker) are KyDrive's durable copies of the suite integration contracts; both sides change together. Root README is the concise overview and navigation.

## Local Contracts
- Document exact verified behavior; local fixtures do not establish live suite integration or production readiness.
- Keep credentials, opened capsules and custodian shares out of documentation and screenshots.
- Recovery instructions must restore the sealed integration configuration and the exact bound bulk snapshot; never treat metadata alone as a complete restore.

## Verification
- Check referenced paths exist. Restore checks are in `internal/backup`.
- Run `mdbook build docs`, `python3 docs/check-links.py`, and `python3 docs/check-strong-crypto-compose.py`.

## Child DOX Index

- `src/` — mdBook user manual sources (`book.toml`, `src/SUMMARY.md`).

## mdBook manual

- `book.toml` and `src/` own the user manual; `src/SUMMARY.md` lists every chapter.
- Describe implemented behavior and preserve release/readiness limits. Root README and referenced runbooks remain authoritative for exact commands and policy.
- Keep setup, user tasks, troubleshooting, security references and contribution guidance current when product behavior changes.
- `book/` is generated and ignored. `.github/workflows/docs.yml` builds/checks the book and uploads an artifact; public-site publication is separate.
- Verify with `mdbook build docs` and `python3 docs/check-links.py` from the repository root. The checker validates generated local links/anchors, not external URLs.
