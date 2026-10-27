# KyDrive round2 first after-capture checks

Completed one bounded capture on 2026-10-27T20:19:05.526Z–2026-10-27T20:19:41.033Z. **40/40 original PNGs individually inspected at native resolution** using `view_image(detail="original")`; no contact sheet, resized derivative or parent gallery substituted for inspection. No capture or product defect observed in the captured initial views. No proposed product fix and **no confirmation recapture used**. No scoring or acceptance decision.

## Frozen source and binary

- Worktree: `/home/yoshi/git/busnes.app/KyDrive-server/.claude/worktrees/drive-eight`.
- Full source SHA: **`7dd862e4fab842c234e828754c101a56ebab322f`**; branch `ui/drive-eight`.
- All **390 tracked web/internal/cmd files** SHA-256 matched that commit before restart and after capture. No tracked diff; no new untracked product files. Product code, tests, AGENTS, capture driver, exporter and other checkouts were not edited.
- Supplied freshly built `.browser/server` copied to a new filename, never rebuilt or overwritten. Copy/current supplied binary SHA-256: `6e0559e16d9b89baf6be8b8a60d9f38065c437751be0277f26311eef8462edec`.
- Go metadata: Go 1.27.3, linux/amd64, trimpath; `vcs.revision=7dd862e4fab842c234e828754c101a56ebab322f`, `vcs.modified=true`. Evidence-only untracked parent files were present; current tracked source and all product files match the pin. This is not a hermetic/repeatable-build claim.
- Embedded/current/served index SHA-256: `f549302a4bd6d0ef600c0bb89b56c7f2a00417fc15736f39bc0039af49b66e73`.
- **439 dist/dist-fonts files** unchanged across capture. Canonical sorted dist-path/hash JSON SHA-256: `a36c60e1f1ee12dbfc1e4b7dc079024316b33eb5e4d1637966a9f70074d08a72`; each file is listed in `source-provenance-r2.json`.
- Served `/sw.js` SHA-256 matches dist: `5de8f359240a0d913eac87fd9a845715fde5338da42a36f30b6a5ea9545f4fbb`.
- Committed `capture-round2.mjs` SHA-256: `362336b2d23f8ba31c1fb1e2257a6af039d585942d143b2c8dfda620e6f3f32f`; package lock: `d535ae94755b431e17ce0916fb3c1726ed28ffd49a7cd5dcb274e8f834d140be`.

## Owned fixture

URL **http://127.0.0.1:5878/** remains running. Original PID 2293191 executable, `/proc` start ticks 26498715 and binary hash matched the original private record before SIGTERM; identity was rechecked immediately before signaling. Only that process was stopped, gracefully; no escalation or other service interaction.

- New PID **2319319**, executable `/tmp/kydrive-drive-eight-ecm8peut/server-round2-7dd862e`, process start ticks **26709047**; revalidated after capture.
- New private record: `/tmp/kydrive-drive-eight-ecm8peut/round2-owned-process.json` (0600). Original owned-process records remain unchanged.
- Current private credentials: `/tmp/kydrive-drive-eight-ecm8peut/credentials.json` (0600); no values published and retired `initial_password` not used.
- Preserved existing private data, backups, encryption key and current credentials. Minimal environment matched the original fixture: PATH, localhost app origin/host/port, SQLite, same KY_DATA_DIR/KY_BACKUP_DIR, privately read current KY_ADMIN_PASSWORD, CAPTCHA provider none, same worktree dist-fonts. No inherited production environment.
- Only real sign-in/session lifecycle and the product's idempotent personal-workspace initialization occurred. No content upload/create/delete, grant/link, MFA, credential issuance, recovery or external integration event was intentionally triggered. New-folder/share forms stayed unsubmitted. Existing Project materials folder, three live version 1 files and trashed Archive note.txt remain visible.
- Parent's separate 5391 regressions were untouched. No extra agents, commits, heavy builds/tests, production or external calls.

## Native browser reconnaissance

Called native `preview_status`, then opened **own separate Incognito tab** `tab_3u_2e419d46-7b07-427f-bf30-1831131b4cba` with `reuseExistingTab=false`. No user/delegate tab reopened. Used native input/click tools for real current administrator sign-in; credentials were passed privately and suppressed from output. Native snapshots inspected the file task, clicked Project materials, inspected its empty state, then clicked Administration and inspected Operations & audit / Create shared workspace / Leavers disclosures. Read-only native condition probe confirmed normal active `/sw.js`, absent saved `ky_theme`, zero document overflow.

Native Chrome 154.0.0.0 was **1280×800 at DPR 2**. That one-off view is outside the scored capture setup. There were no native same-step failures and no failure fallback was needed. The documented Playwright workflow followed reconnaissance to produce exact DPR 1 matched evidence.

## Formal capture and checks

Ran once, using the committed driver without editing:

```sh
node docs/ui-drive-eight/capture-round2.mjs /tmp/kydrive-drive-eight-ecm8peut/round2-owned-process.json
```

The manifest is complete; headless Chromium 156.0.8078.4 / Playwright 1.64.1, fresh context per viewport/OS scheme, DPR 1, en-US, America/New_York, normal allowed service workers, initial viewport only. No auth/session injection, route/response/clock mock, CSP/CSRF bypass or service-worker bypass.

- **40/40 memberships match baseline:** login, files, folder, new-folder, preview, share, versions, trash, Appearance and Account × desktop 1280×900/mobile 390×844 × OS light/dark.
- **40/40** PNG hashes, bytes and decoded dimensions verified; DPR 1 and matching viewport/scheme; `ky_theme` absent; Busnes light/dark follows OS; scroll(0,0); normal SW controller and registration; zero document horizontal overflow.
- **40 unique PNGs and 40 unique decoded RGBA pixel signatures.** Exhaustive **380 same-size RGBA pairs** checked; changed fraction counts pixels with any channel difference. Near-duplicate requires **changed_fraction<0.001 AND max_channel_delta≤8**; **0 near duplicates**. Audit records every pair and raw values.
- **0 page errors / HTTP failures** recorded by the driver. No capture image overwritten or recaptured.
- Inventory v2 contains the 40 after originals, with exactly the v1 canonical row keys, including `duplicate_of` (all null). Per-original inspection notes and hashes are in `capture-provenance-r2.json`.

Individual visual inspection saw expected synthetic text and form states, readable dialog controls, closed row menus and the shipped modal blur/dimming. Mobile Appearance/Account and desktop Account naturally continue below the viewport; these are initial-view captures. Mobile navigation scrolls locally to the current destination. These are observed rendering conditions, not exhaustive usability/accessibility claims.

## Baseline preservation and artifacts

Baseline source **fd472e8035222ae38d8bb4b5c5cbd79c066879a1**, evidence **0354de843b0503b9ff52b51318ab214867b9e0e8**. Original manifest, SHA256SUMS, inventory-v1, near-duplicate-audit-v1, artifacts.json, source provenance, baseline-setup and owned-process publisher records were compared with evidence commit 0354de84 and remain byte-identical. All40 baseline image hashes and all **50 original SHA256SUMS members** pass. Original SHA256SUMS publisher SHA-256 remains `b50345b5cb09f38e6b963e31d9f2facbdc636ea80fba4162d3bffe86ae3543e3`. Baseline files and publisher checksums were not regenerated.

Added `manifest-01-after.json`,40 `screenshots/kydrive-01-after-*.png`, `inventory-v2.json`, `near-duplicate-audit-v2.json`, `source-provenance-r2.json`, `capture-provenance-r2.json` and this report. JSON outputs were prepared completely, then published with same-directory temp-file replacement. Original artifacts.json is unchanged; optional artifacts-v2 was omitted. Parent owns review-inputs, exporter/CSV/XLSX, broader documentation and acceptance.

## Limits and closeout

Actual local **administrator**, not ordinary OIDC user. Ordinary-user shell/workflows, group-granted shared workspaces/populated Shared with me, Euro-Office editing/save, SSO, external guest collaboration, devices and connected recovery remain unavailable/unscored. No roles or services were fabricated. No named themes or saved choice changed. No full product suite, production readiness, comprehensive keyboard/accessibility, permission isolation, integration or visual quality score is claimed; parent owns product tests/full checks and acceptance.

DOX pass: applicable workspace/root/docs/evidence/web/browser contracts re-read. AGENTS.md intentionally unchanged because this task only adds scoped evidence and does not change product contracts; parent owns any broader index/contract updates. No peer scores/reports, COUNCIL, parent gallery or acceptance records were interpreted or used for capture observations. **MySlop not retried/posted**, following the explicit known HTTP 401 instruction; the handoff skill was read.
