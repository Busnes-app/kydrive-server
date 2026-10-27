# R3 bounded after-capture

Completed the single stage02 capture: **40 original PNGs, all individually inspected at native resolution**. No capture defect observed. **One focused mismatch remains:** at 390×844 in both OS schemes, the Trash SVG bounding box starts at x=34 while the Folder path breadcrumb starts at x=33, a +1 CSS-pixel offset. Parent owns the single confirmation decision; no product fix, overwrite or recapture was performed.

This is a **RECREATED fixture after the owner-reported hard lock/reboot**, not the previous instance. The origin remains the same; account/workspace/folder/file IDs, creation/modification timestamps and sessions are new. Relative ages and raster differences caused by those changes must not be attributed to product gains. Content, actual role, declared state and capture conditions match.

## Exact source and access

- Worktree: `/home/yoshi/git/busnes.app/KyDrive-server/.claude/worktrees/drive-eight`.
- Branch: `ui/drive-eight`.
- Source: `e9fadaca6ad1aba9be1ded36895005ae42ba0fe8`.
- URL: **http://127.0.0.1:5878/**; preferred port was free, so no origin difference.
- New private fixture: `/tmp/kydrive-r3-recreated-sgjfh9md` (0700).
- Credentials: `/tmp/kydrive-r3-recreated-sgjfh9md/credentials.json` (0600). Read current `username`/`password` privately; `initial_password` is retired. No credential values appear in published evidence.
- Owned process record: `/tmp/kydrive-r3-recreated-sgjfh9md/owned-process.json` (0600).
- Owned PID: **65645**; executable `/tmp/kydrive-r3-recreated-sgjfh9md/server`; process start ticks **159822**.
- Binary SHA-256: `fbe85c5f6613244303b26714f490d38ef85c8325c78621da208a340340297b09`.
- Server remains available to parent. Verify PID/executable/start ticks against the new record before any future signal. No old fixture, PID or password was reused or signaled, and no other thread's service was touched.

## Setup and boundaries

Read current workspace/repository root contracts, `docs/AGENTS.md`, `docs/ui-drive-eight/AGENTS.md`, `web/AGENTS.md`, `web/browser/AGENTS.md`, `baseline-setup.md`, `fixture-files.json`, the committed R3 driver, `docs/DEVELOPMENT.md`, browser setup/server/Playwright configuration and the actual bootstrap/server routes. Raw Council reports/scores, parent audits, COUNCIL, galleries and acceptance material were not consulted. A concurrently appearing untracked `round2-spread-audit.md` was neither read nor edited.

Sequential builds reused existing `web/node_modules` and default persistent toolchain caches:

1. `npm run build`, from `web/`: exit 0. Log: `/tmp/kydrive-r3-recreated-sgjfh9md/web-build.log`. Vendor integrity, TypeScript and Vite passed. Vite warned about chunks above 500 kB; preserved, no source remediation attempted.
2. `GOMAXPROCS=2 go build -p 1 -trimpath -o /tmp/kydrive-r3-recreated-sgjfh9md/server ./cmd/server`: exit 0. Log: `/tmp/kydrive-r3-recreated-sgjfh9md/go-build.log`.

No compilation overlapped either browser. Tool versions: Go 1.27.3, Node 24.22.0, npm 12.2.1. No npm ci/audit, new dependency, task-specific cache, Go/full-browser/crypto/race/Docker test, production access or external integration call was run.

One owned server started with only PATH plus `KY_APP_URL=http://127.0.0.1:5878`, `KY_HOST=127.0.0.1`, `KY_PORT=5878`, `KY_DB_DRIVER=sqlite`, new private `KY_DATA_DIR`/`KY_BACKUP_DIR`, generated private `KY_ADMIN_PASSWORD`, `KY_CAPTCHA_PROVIDER=none` and worktree `KY_EXCALIDRAW_FONTS_DIR`. No inherited deployment secrets. The actual bootstrap created admin/Administrator, role admin, provider local, with mandatory password replacement. The native UI completed that replacement, returned to login and signed in using the replacement password. No account, role or session was fabricated.

Called native T3 `preview_status`, then opened a new owned incognito tab `tab_1_704a69f4-6312-4023-8076-8b58a559f6a9`. Real native type/click/upload actions created `Project materials`, uploaded all four declared synthetic files, moved `Archive note.txt` to Trash and cleared finished receipts. Real UI navigation inspected Appearance and Account. No search result was added; no sharing grant/link, MFA secret, app password, agent/device token, editor or recovery event was issued.

One native selector error occurred while selecting the person role: **"Preview automation select received an invalid selector (27 characters)."** Retrying with the exact current snapshot Access ref succeeded. No repeated same-step failure or failure-driven fallback occurred. Successful native work preceded the documented Playwright capture workflow, which supplies exact DPR 1; the native tab was closed first. Native measured DPR was 2 and those probes are not formal captures.

## Fixture evidence

Real authenticated read-only APIs confirm three live files, each at revision 1, and one trashed file:

- `Quarterly plan.md`: 135 bytes; SHA-256 `270d7c5956b5e361e9a56d72a16ad32aac2041299023232c1352fd987822e392`.
- `Supplier list.csv`: 79 bytes; SHA-256 `d0b78e64b8973f51977b305b05a9fc9be5c0fff0a0c2f61abb91de9f4c440755`.
- `Meeting notes.txt`: 81 bytes; SHA-256 `8a9ae5a2c3148cae6459e7e635c11a9323e78663ac81f717c7dbc47ca2478823`.
- Trashed `Archive note.txt`: 55 bytes; SHA-256 `a91f3f7053ea992ed286bf27bdca8204a1b778a5165f4d051fbac3252a4a4121`.

All four upload inputs matched exact declared bytes/hashes before upload, and listing digests/sizes/states match afterward. All three live downloads independently match bytes/hash; each has exactly one version and no shares/links. The archive was not downloaded from Trash. The empty folder was observed in native UI and all four formal folder views. Total retained storage is 350 B, including the archive. Full current IDs and timestamps are in `capture-provenance-r3.json`.

Account shows **Administrator (admin)**, MFA Off, no app passwords/agent tokens and no linked computers. No ordinary staff account can be provisioned locally through the documented real boundary: staff requires external OIDC and SCIM supplies no passwords. No ordinary account fabrication was used.

## Focused rendered checks

- Shared Dialog Close measured **40×40 CSS pixels**, `flex-shrink:0`, in 1280×900 and 390×844 under both OS light/dark. This is a measured native Share-dialog check of the shared component, not an assertion about every separate preview control.
- Person search has `aria-describedby="share-person-help"`. Its associated text states at least two characters and the existing Add-as-role action. Changing Access from Reader to Editor to Manager updated the description to `Add as reader`, `Add as editor`, `Add as manager`, without searching for or adding a person. Closing/reopening reset the local form to Reader.
- Native Escape dismissed Share once. Native Close succeeded in the other three viewport/scheme checks; no open dialog remained afterward. No share form was submitted.
- Phone toolbar exact-alignment check **does not pass strictly**: SVG box x=34 versus breadcrumb x=33 in both schemes. This records DOM geometry, not an invented score or raster-quality gain. No autonomous correction.

## Formal capture and inspection

Ran the committed `capture-round3.mjs` **once**, 2026-10-27T20:55:27.811Z–20:55:58.787Z:

```sh
node --import /tmp/kydrive-r3-recreated-sgjfh9md/atomic-bootstrap.mjs docs/ui-drive-eight/capture-round3.mjs /tmp/kydrive-r3-recreated-sgjfh9md/owned-process.json
```

The private Node resolve hook changes only the driver's manifest write to adjacent temporary-file+rename, satisfying atomic publication without modifying committed driver bytes or browser/capture behavior. Hook source/hashes are recorded. Capture log: `/tmp/kydrive-r3-recreated-sgjfh9md/capture.log`. No overwrite, second run, confirmation or old-batch recapture.

Exact matrix: login, files, empty folder, unsubmitted new-folder form, text preview, empty Share form, one-version history, populated Trash, Appearance, Account  × 1280×900/390×844  × OS light/dark. Playwright 1.64.1, headless Chromium 156.0.8078.4, DPR 1, en-US, America/New_York, fresh context per size/scheme, normal service workers, no saved `ky_theme`, viewport-only PNGs. Same browser/locale/timezone/SW/full-page settings and all 40 surface/viewport/theme/DPR/saved-theme tuples match stage01. Mobile denotes desktop Chromium at phone CSS dimensions.

Each of the 40 originals was individually opened with `view_image` at `detail=original`; no contact sheet, resizing or substituted image was used. Per-image dimensions, hashes, inspection result and concrete surface notes are in `capture-provenance-r3.json`. Expected product dimming/blur around dialogs remains. Account and phone Appearance/folder content continue below the viewport; horizontally scrolling phone navigation remains product behavior. No blank, wrong-surface, broken-content or visible capture error observed. Login shows shipped placeholders, not saved credentials.

Manifest verification: 40/40 dimensions/hash/byte counts, 40 unique surface combinations, DPR 1 and no saved theme throughout, active `/sw.js` controller in all 40, correct Busnes scheme, and zero document horizontal overflow. Recorded page errors and HTTP responses≥400: **0**. The committed driver does not subscribe to all console/requestfailed events, so no exhaustive console/network claim is made.

After the browser closed, one sequential Python/Pillow/NumPy process compared all **380 same-size RGBA pairs**, holding at most two originals and one delta. Threshold: changed fraction<0.001 AND maximum channel delta≤8. **40 unique PNG hashes, 40 unique decoded-pixel hashes, 0 exact or near duplicates.** No old batch was compared or recaptured.

## Provenance and outputs

Strict expectedHEAD and tracked `git diff HEAD` checks pass before and after. All 390 tracked `web`/`internal`/`cmd` files match committed blob hashes and pre/post SHA-256; all 230 embedded dist files match pre/post build hashes. Served index equals built index. Binary Go metadata reports exact e9fad source revision and `vcs.modified=false`; binary hash matches the owned record and capture manifest. All preexisting tracked files, including baseline/R2 images, manifests, evidence and raw reports, retain their initial hashes. The raw reports were hashed for immutability only, not read for content. Untracked evidence belonging to parent/auditor was left untouched.

Only these 46 repository outputs are owned by this task, all under `docs/ui-drive-eight/`:

- 40 `screenshots/kydrive-02-after-*.png`.
- `manifest-02-after.json` — committed driver's frozen capture conditions and 40 per-image records.
- `inventory-v3.json` — canonical v2 row keys, including `duplicate_of`.
- `near-duplicate-audit-v3.json` —380 comparisons, exact pixel signatures and duplicate results.
- `source-provenance-r3.json` — source/build/dist/binary hashes and unchanged prior tracked hashes.
- `capture-provenance-r3.json` — recreated identity/state, native measurements, individual inspections, conditions, limits and artifact hashes.
- `round3-capture-checks.md` — this report.

All JSON outputs were published by adjacent temporary-file replacement. Private fixtures, logs, build outputs/binary, upload inputs, credentials, process record and bounded evidence utilities remain under the new 0700 directory; only ignored normal frontend build output was rebuilt in the worktree. No product/test/AGENTS/exporter edits or commits; no subagents.

## Limits and parent hand-back

This covers the actual local administrator only. Ordinary-user authorization/workflows, group-granted shared workspaces, populated Shared with me, live SSO, external collaboration, connected editor/save and connected devices/recovery remain unavailable/unscored. Full behavior, accessibility, named-theme persistence and integration gates remain untested. Full required repository checks are parent-owned and deliberately not run under this bounded request; no full-suite or acceptance claim.

DOX closeout rechecked the changed paths against root/docs/ui-drive-eight contracts. AGENTS files intentionally unchanged: evidence-only output changes no durable product contract, and parent owns indexes/reviews. The MySlop handoff skill was read; the owner's explicit known 401/no-retry/no-post instruction controls, so no board request or status mutation was attempted. This report is the direct parent hand-back, with that board limitation disclosed.

Parent needs to assess the recorded 1 px alignment mismatch and own any single confirmation decision, CI and reviews. The new owned fixture is available; formal and native browser tabs are closed. No remediation or recapture is implied by this report.
