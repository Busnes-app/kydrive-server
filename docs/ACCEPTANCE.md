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

## M2 resumable uploads — 2026-10-15

`scripts/accept-resumable-upload.sh 5` ran against the compiled server on a disposable btrfs data directory under a 6 GiB memory cap. It uploaded 5 GiB of random data in 64 MiB chunks. A chunk throttled and cut at 2 s left the server's received count unchanged. Restarting the server halfway kept the session and count, and the upload continued from there. The published digest and a full download matched the source SHA-256, and staging was empty afterwards. Total wall time was 34 s, including generating the data and completing the upload, which re-sealed the whole file. The browser suite (`uploads.spec.mjs`, 4 viewports) separately covers a reset chunk connection that resumes, a folder-tree upload, search and folder zip download.

## M4 WebDAV — 2026-10-15

`scripts/accept-webdav.sh` ran against the compiled server with an app password created through the API. curl: no-credential challenge, PROPFIND, MKCOL, PUT, overwrite to revision 2, stale `If-Match` refused (412), MOVE, and DELETE leaving the file in the trash at revision 2, all passed. KDE KIO (`kioclient`) copied a file in, read it back and listed it. Not run on this host: GNOME GVfs (`gvfsd-dav` not installed), rclone and litmus (not installed); the script reports each as SKIP. Not yet verified: mounting in Windows Explorer and macOS Finder, a LibreOffice save creating a version, and a deactivated user's mount failing (covered in Go tests: `TestDAVAuthentication`).

## M3 sharing — 2026-10-15

The roadmap's exit criteria are covered by tests against the real store:
- **A folder grant gives exactly that subtree:** `TestFolderShareScope`, `TestGuestLinkScope`.
- **Leaving a shared group revokes an open editor in one revocation pass:** `TestShareRevocationRevokesEditor`, and `TestLinkRevocationRevokesEditor` for links.
- **An expired or revoked link returns 404:** `TestOrganisationLinks`, `TestGuestLinkEnds`, `TestGuestLinkRoutes`.
- **A guest link never reveals sibling files:** `TestGuestLinkScope`, `TestGuestLinkRoutes`.

The browser suite opens a password guest link in a cookie-less Chromium context and downloads the file natively (`guest.spec.mjs`, 4 viewports). The two-user share and organisation-link flows run through the HTTP API with two real accounts (`TestShareRoutes`) rather than in the browser, because the browser harness has one local account.

## M5 previews — 2026-10-16

- **Previews render for the allowlist; HTML and SVG never render:** `TestPreviewServesOnlyTheAllowlist` sniffs PNG, PDF, text and MP4 as previewable and refuses HTML, an HTML file named `.png`, XML-declared SVG, UTF-16 text and binary (415). Markup without an HTML or XML signature, such as a bare SVG, is served as `text/plain` and shown as source.
- **In a real browser:** `previews.spec.mjs` (4 viewports) decodes an image, shows text with its markup inert, frames a PDF and offers only Download for HTML, whose script does not run.
- **A decompression-bomb PNG is refused without a large allocation:** `TestThumbnailRefusesBombs` feeds PNGs whose kilobyte of compressed data claims 100 000 × 100 000, a 17 000 px side, or 8000 × 6000 at 16 bits, a 6000 × 6000 16-bit gray PNG with transparency (288 MB before its fix), a 7000 × 7000 interlaced PNG (199 MB before its fix), real CMYK JPEGs at 8200 × 4096 baseline and 4000 × 3000 progressive (each decoded in full before its fix), and a real 5000 × 5000 progressive JPEG, also behind a fill byte and after 65 empty comments; each is refused with under 4 MiB allocated. Before the fix for review finding P1, the fill-byte variant hid its progressive frame and was decoded in full. With the budget check removed, the 16-bit PNG allocated 384 MB and the test failed.
- **Thumbnails stay encrypted:** `TestThumbnailCacheIsSealed` and `TestPreviewServesOnlyTheAllowlist` find no PNG signature in the cache, a wrong key refused, and the orphan sweep removing a purged blob's thumbnail.
- **PDF under the sandbox:** probed by hand in Chromium 2026-10-16: its viewer renders a PDF served with `CSP: sandbox`, both as the page and in a same-origin iframe. Firefox is unverified (its Playwright browser is not installed here).

## Boundaries

Single writer/instance; no HA. No desktop sync, SMB, full-text search (filename search only). Restic pruning is opt-in, off by default, never forgets the newest KY_BACKUP_KEEP deposit snapshots. KyRecovery-held capsules older than the policy may name forgotten snapshots: set the policy to outlive KyRecovery's retention. An append-only rest-server or Object Lock repository refuses prune by design. Blobs are removed only by manager purge or an opt-in workspace retention policy; trash and versions otherwise stay and consume quota. Standard Euro-Office editing exposes plaintext to the editor service; this is not CryptPad-style end-to-end encryption. Its error logs may include scoped document URLs; treat editor logs as sensitive and configure access/retention before production.

See [README](../README.md), [restore runbook](RESTORE.md), [UI verification](../UI-VERIFICATION.md) and [handoff](../STATUS.md). Local completion does not close the production gates above.
