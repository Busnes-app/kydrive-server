# KyDrive baseline — 00-before (2026-10-27)

Fresh capture inputs only: no scoring, acceptance, source change, commit, merge, deploy,
other-checkout modification or agent delegation. Historical quality scores and other
apps’ reviewer reports were not consulted. No production access/data or external
service/email/editor connection was used.

## Source and fixture access

- Worktree: `/home/yoshi/git/busnes.app/KyDrive-server/.claude/worktrees/drive-eight`.
- Branch: `ui/drive-eight`; full source: `fd472e8035222ae38d8bb4b5c5cbd79c066879a1`.
- Tracked source diff empty at build, capture and hand-back; only this directory added.
- URL: **http://127.0.0.1:5878/**. One owned Go server, PID **2293191**, left running.
- Private directory: `/tmp/kydrive-drive-eight-ecm8peut` (0700).
- Private credentials: `/tmp/kydrive-drive-eight-ecm8peut/credentials.json` (0600).
  Read `username` and current `password` privately; `initial_password` is retired.
  Never copy values to prompts, docs or screenshots.
- Owned PID record: `/tmp/kydrive-drive-eight-ecm8peut/owned-process.json`; a copy is
  here as `owned-process.json`. Check executable and process start ticks before stopping.
  Database, blobs, key, binary and server log remain inside the owned private directory.

**Ordinary login could not be provisioned through real local boundaries.**
`docs/DEVELOPMENT.md` requires OIDC for staff and says SCIM supplies no passwords;
`cmd/server/main.go` bootstrap/`init-admin` create administrators; no local staff
registration route is registered in `internal/api/server.go`. No identity provider,
role, database identity or session was fabricated. Authenticated captures show the
actual **local administrator**, not ordinary-user navigation/access. The existing
administrator personal workspace behavior was observed without changing its contract.

Real UI created the empty `Project materials` folder, uploaded four synthetic files
listed with contents/hashes in `fixture-files.json`, trashed `Archive note.txt`, and
cleared finished upload receipts. Three files remain live, all version 1. Share forms
and the `Working drafts` new-folder form in captures are unsubmitted. No sharing link,
MFA secret, app password, device credential or group grant was issued.

## Native browser use before reproducible capture

Called `preview_status`, then `preview_open` for a separate incognito tab,
`tab_3r_2e419d46-7b07-427f-bf30-1831131b4cba`. Opening `about:blank` returned invalid
URL; omitting URL succeeded. Navigated to the loopback server, submitted real bootstrap
login, completed mandatory password replacement, then signed in again. A rejected
`[exact=true]` locator was corrected using current snapshot refs before submission.
Native type/click/upload actions prepared all synthetic state. Inspected Appearance at
1280×900 light and 390×844 dark through native resize/appearance controls.

Read-only native probe: Chrome **154.0.0.0**, DPR **2**, no saved `ky_theme`, active
`/sw.js`. Native reconnaissance screenshot, outside the formal DPR 1 batch:
`/home/yoshi/.t3/userdata/browser-artifacts/browser-screenshot-127-0-0-1-mvr3gk53-647fefbf.png`.
The documented Playwright workflow followed reconnaissance to provide exact DPR 1.
No auth, CSP, CSRF or service-worker bypass was used. Although fixture configuration
sets `KY_CAPTCHA_PROVIDER=none` as in the browser harness, the real UI performed its
security check naturally; capture waits for “Security check verified.”

## Build, environment and reproduction

Explicitly changed to the target worktree. Ran `cd web && npm ci`, `npm run build`
(vendor integrity, TypeScript, Vite), then `go build -trimpath -o
/tmp/kydrive-drive-eight-ecm8peut/server ./cmd/server`. Builds passed. Tools: Go **1.27.3**
(pinned toolchain), Node **24.22.0**, npm **12.2.1**. Default persistent npm/Go/Playwright
caches reused, with no clearing. npm reported zero vulnerabilities and upstream React
peer-range warnings, deprecated `sliced@1.0.1`, and blocked optional
`@parcel/watcher@2.6.0` install script. Vite warned about >500 kB chunks; preserved in
`web-build.log`. Existing warnings were recorded; source remediation was out of scope.

Minimal detached server environment: PATH plus `KY_APP_URL=http://127.0.0.1:5878`,
`KY_HOST=127.0.0.1`, `KY_PORT=5878`, `KY_DB_DRIVER=sqlite`,
`KY_DATA_DIR=<private dir>/data`, `KY_BACKUP_DIR=<private dir>/backups`,
`KY_ADMIN_PASSWORD` read privately, `KY_CAPTCHA_PROVIDER=none`, and
`KY_EXCALIDRAW_FONTS_DIR=<worktree>/web/dist-fonts`. No production secrets inherited.

```sh
cd /home/yoshi/git/busnes.app/KyDrive-server/.claude/worktrees/drive-eight
node docs/ui-drive-eight/capture.mjs /tmp/kydrive-drive-eight-ecm8peut/owned-process.json
python3 docs/ui-drive-eight/verify-baseline.py
```

Capture ran once and refuses an existing manifest. **No correction/recapture used.**
If the one permitted correction becomes necessary, preserve the entire original batch,
driver and manifest first and document why. The verifier is safe to rerun without
recapturing. To recreate a fixture, use the same minimal environment, new owned temp
directory/unused loopback port/generated private credentials, then repeat real bootstrap
replacement, folder creation, `fixture-files.json` uploads, archive trash and receipt
clearing. Pass its matching owned-process record to the driver. Dates, relative ages,
IDs and sessions vary naturally: no clock/response mocks or byte-identical claim.

## Frozen evidence and limits

`source-provenance.json` freezes tracked hashes/tool versions; `manifest-00-before.json`
freezes commit, binary/index/lock/driver hashes and exact per-image conditions. Formal
browser: Playwright **1.64.1**, headless Chromium **156.0.8078.4**; fresh context per
size/OS scheme, DPR 1, en-US, America/New_York, no saved theme, normal service workers.
Mobile means desktop Chromium at mobile CSS dimensions, not a physical-device claim.
All app navigation remains at `/`. No named theme choice or product behavior changed.

**40 individually inspected PNGs** in `screenshots/`: login, files, empty folder,
new-folder form, text preview, empty share form, one-version history, populated trash,
Appearance and Account × 1280×900/390×844 × light/dark. No capture defect observed.
Dialog blur/dimming is product behavior; login fields show shipped placeholders.
Mobile files/folder/Appearance/Account and desktop Account continue below the viewport.
`inventory-00-before.csv` records per-image SHA-256, bytes, dimensions and inspection
notes. Verification passed all 40 combinations, hashes/dimensions, DPR/theme conditions,
active SW controllers and zero document overflow; no page errors/HTTP failures recorded.
Duplicate audit: 40 unique PNG hashes and 40 unique decoded-pixel signatures, no exact
duplicates; no perceptual-similarity claim. `SHA256SUMS` freezes all evidence files.

Missing/unscored: ordinary staff workflows, group-granted shared workspaces, populated
Shared with me, live SSO, Euro-Office editing/save, guest collaboration and connected
device/recovery integrations. Admin Settings/Recovery were outside this task batch.
No exhaustive named-theme/persistence, accessibility, keyboard or functional assurance.
No full Go, browser or unit suite run, per the explicit baseline-only brief.

DOX pass: only evidence artifacts changed under root/docs contracts. AGENTS.md files
intentionally unchanged: no product contract changed, and parent owns follow-up
indexes/docs/reviewer delegation/acceptance. Impeccable context ran once and reported
no PRODUCT.md/DESIGN.md; no design authority invented. **No MySlop board post:** known
HTTP 401 with unchanged credential and explicit no-retry instruction; skill was read.
