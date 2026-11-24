# KyDrive operator documentation

## Purpose
Own restore instructions, local acceptance evidence and captured product UI assets.

## Ownership
`EURO-DOCKER.md` owns the production Docker editor overlay, private JWT input (including the SELinux `container_file_t` label), named-volume persistence and proxy/acceptance runbook.
`INSTALLER-SETUP.md` owns the container-local setup schema, runtime activation and adapter limitations.
`RESTORE.md` is the recovery runbook. `ACCEPTANCE.md` distinguishes local evidence from production gates. `PILOT.md` records live deployment evidence and remaining gates. `superpowers/plans/` holds dated implementation plans and `superpowers/specs/` dated designs and options studies (`2026-11-15-content-search-options.md`: prefix and phrase search, shared items and PDFs without Euro-Office, awaiting the owner's choice; implementing an option updates it; `2026-11-20-tenancy-design.md`: multi-tenant (SaaS) mode, KyDrive's half of KyIdentity's downstream tenancy contract, approved with the owner's answers and current as-built progress; each build PR updates it); its roadmap file records milestone decisions. `desktop-sync-options.md` is the desktop sync research note (options, server gaps, recommendation); its "What a Ky client looks like" section is the M7 design. `DESKTOP-ACCESS.md` is the user guide for desktop WebDAV clients (rclone, Cyberduck) and `KyDrive.cyberduckprofile` its connection profile. `DEPLOYMENT.md` owns deployment configuration; `PERMISSIONS.md`, `CLIENTS-AND-EDITOR.md`, `RECOVERY-AND-ENCRYPTION.md` and `DEVELOPMENT.md` own the detailed topics moved from the README. `VERIFYING-RELEASES.md` owns release-image verification and the binary rebuild; update it when `release-image.yml`, the Dockerfile's builds or the image layout change. `STRONG-CRYPTO.md` owns the key-size table, the strong-key profile and its out-of-scope list; `check-strong-crypto-compose.py` proves the overlay changes nothing else across every Compose chain (add a new overlay to its list). `ASSURANCE-CASE.md` owns the threat model, trust boundaries and weakness → counter → code table; a change that moves a trust boundary, a security requirement or a listed code path updates it in the same change. `ui-fleet-2026-10-26/` holds the fleet Appearance staged captures, their manifests, the capture and in-app check driver (`capture.mjs`) and the CSV results; `ui-visual-refinement-2026-10-26/` holds the visual-refinement before/after captures, manifests, checks and their driver. `ui-suite-2026-10-31/screenshots/` holds the suite design pass before (`kydrive-00-before-*`) and after (`kydrive-01-after-*`) captures, driven by the suite's `captures/drivers/kydrive-after.mjs`, and the ky-ui 0.8 status-pill and phone focus-ring captures (`kydrive-05-before-*`, `kydrive-05-after-*`), driven by `captures/drivers/kydrive-round5.mjs`. `ui-app-rail-2026-10-31/` holds the app rail before/after captures (light/dark, 1440 and 390) and their driver `capture.mjs`, which runs a disposable server against a fake KyIdentity suite directory. `ui-search-type-2026-11-15/` holds the search type chip before/after captures (results; after: PDFs chosen, PDFs alone; light/dark, 1440 and 390) and their driver `capture.mjs` (disposable server, content search off). `ui-workspace-delete-2026-11-02/` holds the workspace delete before/after captures (issue #62: Workspace panel, delete dialog, result; light/dark, 1440 and 390) and their driver `capture.mjs`, which seeds one group into a disposable server's database. `KYPOST.md` (contracts C, I2, J, K: KyPost's signed routes and Email a link) and `SUITE.md` (contract I: the picker) are KyDrive's durable copies of the suite integration contracts; both sides change together. `ui-suite-wave2-2026-11-14/` holds the wave 2 suite integration before/after captures (picker, its refusal, a file's menu, Email a link, Settings → Files with both KyPost switches; light/dark, 1440 and 390) and their driver `capture.mjs`, which runs a disposable server against a fake KyIdentity suite directory and an environment KyPost pairing. `ui-office-viewer-2026-11-15/` holds the KyPost viewing copy captures in Euro-Office view mode (light/dark, 1280 and 390) and their capture conditions. Root README is the concise overview and navigation; root STATUS owns handoff.

## Local Contracts
- Document exact verified behavior; local fixtures do not establish live suite integration or production readiness.
- Keep credentials, opened capsules and custodian shares out of documentation and screenshots.
- Recovery instructions must restore the sealed integration configuration and the exact bound bulk snapshot; never treat metadata alone as a complete restore.

## Work Guidance
- Update acceptance evidence when a gate is actually exercised. Use actual product screenshots with capture conditions.

## Verification
- Check referenced paths exist. Restore checks are in `internal/backup`; native browser captures are described in root `UI-VERIFICATION.md`.

## Child DOX Index

- `ui-drive-eight/` — staged synthetic Council captures, independent raw reviews, score provenance and visual progression records; see `ui-drive-eight/AGENTS.md`.

## mdBook manual

- `book.toml` and `src/` own the user manual; `src/SUMMARY.md` lists every chapter.
- Describe implemented behavior and preserve release/readiness limits. Root README and referenced runbooks remain authoritative for exact commands and policy.
- Keep setup, user tasks, troubleshooting, security references and contribution guidance current when product behavior changes.
- `book/` is generated and ignored. `.github/workflows/docs.yml` builds/checks the book and uploads an artifact; public-site publication is separate.
- Verify with `mdbook build docs` and `python3 docs/check-links.py` from the repository root. The checker validates generated local links/anchors, not external URLs.
