**Repo:** KyDrive-server
**Worktree:** /home/yoshi/git/busnes.app/KyDrive-server (branch work/drive)

# Local delivery complete — 2026-10-03

Built and tested locally across all seven implementation areas: drive/storage and authorization, SCIM/OIDC wiring, Euro-Office sessions/callbacks/revocation, browser administration, sealed metadata plus encrypted bulk recovery, and NAS/Kubernetes packaging. Exact evidence and remaining production gates: `docs/ACCEPTANCE.md`. Operator configuration: `README.md`; restore: `docs/RESTORE.md`.

Actual Euro-Office DOCX collaboration, XLSX cell editing and PPTX title editing all saved and reopened. Deactivating an active collaborator revoked its editor access while shared files survived. Final container recovery drill restored every referenced blob and sealed the integration secrets. Go race tests/vet, frontend tests/build/audit, renderer tests, smoke checks, Docker and Compose validation passed.

The user explicitly limited this delivery to build/test local. No live NAS/Kubernetes/DNS/KyIdentity/KyRecovery provisioning and no image publication occurred. Target NAS is `unraid.urlxl.us` / KyYard `hluswcdata01`; people/groups belong in KyIdentity, group permissions in KyDrive; recovery target is `https://kyrecovery.urlxl.us/`. Root suite installer remains separate work. Original server-base and KyIdentity worktrees are clean.

## Review state

New repository is on branch `work/drive`, all source files remain untracked pending review; no commit, remote or PR was created. Local image `kydrive-server:local` is amd64/non-root and built from current source. It is not a published deployment digest.

Local demo remains running at `http://localhost:18090`, routed by an ephemeral preview proxy to Docker containers `kydrive-local` and `kydrive-euro-local-test`. Disposable credentials and fixture data are in `/tmp/kydrive-local-test`, with private credential files mode 0600; never copy them into Git, images or the handoff board. `env.json` identifies generated local credentials; these are not production credentials. The browser administrator session is already authenticated. The second local SCIM test user is intentionally inactive after the revocation test.

To stop the demo: `docker stop kydrive-local kydrive-euro-local-test`; inspect the proxy command/process before terminating the PID in `/tmp/kydrive-local-test/proxy.pid`. Do not use the old server.pid: it refers to a stopped initial server. These temporary files are not durable product storage.

## Next production work

Verify source-to-artifact provenance for the pinned Euro image, choose real NAS persistent paths and independent bulk storage, supply TLS/DNS and cluster storage settings, connect KyIdentity OIDC/SCIM and pair KyRecovery. Then validate real offboarding, HTTPS transfers, NAS durability/capacity, large restore, upgrade and rollback. A renderer produces reviewable manifests; it neither applies them nor implements the full suite one-prompt installer.

## Careful

Do not silently grant document access to identity administrators. Do not prune bulk snapshots referenced by retained capsules. A metadata-only restore is incomplete: restore and verify the exact bound Restic snapshot. Keep integration secrets sealed and persistent. Private-network editor fetches require a constrained deployment policy; the renderer does not enable arbitrary private access. Euro provenance and production acceptance remain open despite passing local editing tests.

DOX pass updated root/product/domain/deployment contracts and child indexes. Auth, crypto, devices and testdb contracts were intentionally unchanged because their inherited responsibilities did not change. Root plan records the local-only scope and links the acceptance record. This handoff and the plan are mirrored to myslop.
