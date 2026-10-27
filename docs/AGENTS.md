# KyDrive operator documentation

## Purpose
Own restore instructions, local acceptance evidence and captured product UI assets.

## Ownership
`EURO-DOCKER.md` owns the production Docker editor overlay, private JWT input (including the SELinux `container_file_t` label), named-volume persistence and proxy/acceptance runbook.
`INSTALLER-SETUP.md` owns the container-local setup schema, runtime activation and adapter limitations.
`RESTORE.md` is the recovery runbook. `ACCEPTANCE.md` distinguishes local evidence from production gates. `PILOT.md` records live deployment evidence and remaining gates. `superpowers/plans/` holds dated implementation plans and `superpowers/specs/` dated designs; its roadmap file records milestone decisions. `desktop-sync-options.md` is the desktop sync research note (options, server gaps, recommendation); its "What a Ky client looks like" section is the M7 design. `DESKTOP-ACCESS.md` is the user guide for desktop WebDAV clients (rclone, Cyberduck) and `KyDrive.cyberduckprofile` its connection profile. `DEPLOYMENT.md` owns deployment configuration; `PERMISSIONS.md`, `CLIENTS-AND-EDITOR.md`, `RECOVERY-AND-ENCRYPTION.md` and `DEVELOPMENT.md` own the detailed topics moved from the README. `VERIFYING-RELEASES.md` owns release-image verification and the binary rebuild; update it when `release-image.yml`, the Dockerfile's builds or the image layout change. `STRONG-CRYPTO.md` owns the key-size table, the strong-key profile and its out-of-scope list; `check-strong-crypto-compose.py` proves the overlay changes nothing else across every Compose chain (add a new overlay to its list). `ASSURANCE-CASE.md` owns the threat model, trust boundaries and weakness → counter → code table; a change that moves a trust boundary, a security requirement or a listed code path updates it in the same change. `ui-fleet-2026-10-26/` holds the fleet Appearance staged captures, their manifests, the capture and in-app check driver (`capture.mjs`) and the CSV results; `ui-visual-refinement-2026-10-26/` holds the visual-refinement before/after captures, manifests, checks and their driver. Root README is the concise overview and navigation; root STATUS owns handoff.

## Local Contracts
- Document exact verified behavior; local fixtures do not establish live suite integration or production readiness.
- Keep credentials, opened capsules and custodian shares out of documentation and screenshots.
- Recovery instructions must restore the sealed integration configuration and the exact bound bulk snapshot; never treat metadata alone as a complete restore.

## Work Guidance
- Update acceptance evidence when a gate is actually exercised. Use actual product screenshots with capture conditions.

## Verification
- Check referenced paths exist. Restore checks are in `internal/backup`; native browser captures are described in root `UI-VERIFICATION.md`.

## Child DOX Index
