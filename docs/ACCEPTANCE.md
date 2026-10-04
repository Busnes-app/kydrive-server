**Repo:** KyDrive-server
**Worktree:** /home/yoshi/git/busnes.app/KyDrive-server (branch work/drive)

# Local acceptance — 2026-10-03

The user authorized build and test locally. No NAS, Kubernetes, DNS, live directory or recovery provisioning was performed. This is a working local implementation across seven phases; production acceptance remains open.

| Phase | Local result | Remaining production gate |
| --- | --- | --- |
| 1 — contracts and editor | General drive contracts implemented; actual Euro-Office 9.3.4 Build 37 opens DOCX/XLSX/PPTX. AGPL license and Euro-owned source/build repositories inspected. | Bind the pinned image to an independently verified source build; approve provenance. |
| 2 — storage and access | Immutable versions, folders, workspace group grants, quotas, trash, audit and scoped service credentials. Concurrent revision conflicts, interrupted/oversized uploads, role boundaries and retained ownership tested. | Validate actual NAS filesystem, permissions, capacity and power-loss behavior; genuine ENOSPC was not injected. |
| 3 — identity | Local HTTP SCIM lifecycle and KyIdentity-compatible externalId filters tested. Deactivating a second editor revokes its active session without deleting shared files. OIDC signature/issuer/audience/nonce/PKCE checks use the inherited verified implementation. | Connect actual KyIdentity OIDC and outbound provisioning; verify real account/group lifecycle end to end. |
| 4 — editing | Two distinct users collaboratively edited DOCX; saved changes reopened. XLSX cell and PPTX title edits saved through signed callbacks and reopened. Forgery, expiry, replay, replacement conflict and fetch-origin restrictions tested. | Validate HTTPS in both directions, real network interruptions and editor restart recovery. |
| 5 — interface | Drive browsing, upload/download, versions, trash, group permissions, quota, operations and audit are available. People/groups link to KyIdentity. Native browser checked desktop/mobile Busnes themes. | Integrated suite navigation and actual onboarding/offboarding workflow. The local UI does not embed KyIdentity administration. |
| 6 — recovery | recoveryclient seals metadata and integration secrets; an exact encrypted Restic snapshot binds bulk blobs. Final Docker drill returned HTTP 200, all seven checks passed, every referenced blob restored. | Pair real KyRecovery, choose an independent bulk destination, test custodian recovery and representative-volume restore time. |
| 7 — packaging | Non-root linux/amd64 Docker image built; NAS Compose validated; reviewed renderer emits Euro Kubernetes resources and optional single-instance drive profile. Health and restart preserve the local corpus. | Publish a reviewed image; supply actual DNS/TLS/storage/secrets; NAS deployment, cluster installation, upgrade and rollback rehearsal. No suite-wide one-prompt installer is delivered. |

## Artifacts

- KyDrive local image: `kydrive-server:local`, image ID `sha256:757588c3450b29c160255d48888eda95e45125fb85fec768589ecee9c9277696`, amd64, UID/GID 1000:1000. This local image ID is not a published registry reference.
- Tested Euro artifact: `ghcr.io/euro-office/documentserver@sha256:beca380debb9b4eadb7e4c662d112a325c5df0f2179453a4affd8e6ba5d00562` (amd64). Runtime reports 9.3.4 Build 37. The image lacks a source-revision label; source inspection does not prove digest-to-source correspondence.
- Published source release inspected: [v9.3.4-hotfix.1](https://github.com/Euro-Office/DocumentServer/releases/tag/v9.3.4-hotfix.1), commit `4ba0c0f202ae484eb202f24c59ddf5f4a7004161`. [Build workflow](https://github.com/Euro-Office/DocumentServer/blob/v9.3.4-hotfix.1/.github/workflows/build.yml) and Euro-owned submodules describe the declared release chain. Inherited ONLYOFFICE strings remain in Euro; no ONLYOFFICE product was deployed.
- Source starts from server-base commit `9bea11f3e7a88b7a17d783c6a01fd56ec2c0e9a6`; original server-base and KyIdentity worktrees remain unchanged.

## Verification

Passed: `go test -race ./...` with Restic available; `go vet ./...`; `go mod verify`; frontend seven tests, TypeScript/production build and npm audit (zero vulnerabilities); renderer unittest; shell smoke checks; amd64 Docker build and Compose validation. The final rebuilt frontend matches the container's `index-D9sh4Vj8.js` bundle.

Native T3 browser exercised actual Euro editors rather than simulated save callbacks. DOCX persisted both participants' text at revision 4; XLSX A1 persisted “KyDrive spreadsheet round trip” at revision 2; PPTX title persisted “KyDrive slide round trip” at revision 2. Disabling the second user changed its active editor to view mode and completed the durable drop queue; documents survived.

The final container restore drill sealed five files including integration configuration, unpacked into a restricted scratch directory, checked SQLite integrity and restored all referenced immutable blobs from Restic. Duration was 563 ms for a tiny three-document fixture; this is not a production restore benchmark. Drills use a throwaway recovery key, not real custodian cards or the live recovery service.

Local SCIM identities received test-only password hashes directly in the disposable database so two browser users could exercise collaboration. This does not demonstrate live KyIdentity login or password provisioning. Tests also cover missing bulk blobs, incorrect Restic key, cross-workspace service access, callback forgery, stale saves and atomic membership rollback.

## Boundaries

Single writer/instance; no HA. No desktop sync, WebDAV, SMB, advanced search, automatic blob collection or Restic pruning. Retained versions and trash consume quota. Standard Euro-Office editing exposes plaintext to the editor service; this is not CryptPad-style end-to-end encryption. Its error logs may include scoped document URLs; treat editor logs as sensitive and configure access/retention before production.

See [README](../README.md), [restore runbook](RESTORE.md), [UI verification](../UI-VERIFICATION.md) and [handoff](../STATUS.md). Local completion does not close the production gates above.
