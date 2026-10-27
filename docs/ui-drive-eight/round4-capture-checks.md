# Stage03 single confirmation checks

Frozen source `8a23d753ba7aa0f67025efa1eca19bce3df63363`, branch `ui/drive-eight`. One authorized confirmation; no scores or progression/acceptance decision.

**Result:**40/40 matched formal originals complete and individually inspected. Preview Close shares the title row on phone; both links fit the second row. Native phone keyboard focus clipping remains after500ms settling in both themes; no fix or extra capture.

## Residual findings and limits

- **Appearance:** real Tab from My files → Shared with me → Account → Appearance. After an explicit500ms at every stop, row bounds x0–390/y56–104; Appearance x381.390625–507.6875/y60–100; scrollLeft0. Button117.6875px beyond row; focus outline2px + offset2px extends121.6875px beyond. The next Tab to Recovery moves scrollLeft to371. Page/task stays My files throughout. Both OSlight/dark reproduce.
- **Settings & DB:** after Recovery, settled button x252.984375–387.78125 at scrollLeft371 fits, but outer focus ring extends to391.78125,1.78125px beyond the row. Both themes.
- Native light evidence: `/home/yoshi/.t3/userdata/browser-artifacts/browser-screenshot-127-0-0-1-mvr7hxqa-120e767b.png` (780×1688 pixels,390×844 CSS,DPR2). This is probe evidence outside the40 formalDPR1 originals. No dark recapture was needed.
- All six current navigation destinations received keyboard focus. Desktop focused controls/rings fit the header. The first desktop raw probe measured `.app-navs` as zero because desktop uses display:contents; those raw clipping fields do not establish a defect. Its effective clipping ancestor is the240px app-header; dark desktop records that ancestor directly. Exact raw bounds and interpretation are retained in capture-provenance.
- Fixture title Meeting notes.txt fits one line. Verified grid minmax(0,1fr), overflow-wrap:anywhere and no title/Close collision; no synthetic long title, DOM override or extra content introduced. Arbitrary long-title wrapping was not separately runtime-tested here. Parent reports six QuickLook unit tests and syntax check, not rerun here and not a full-suite claim.

## Process, fixture and frozen source

- Old owned serverPID154767, startticks341610, executable server-r4 and SHA256`9bac08cbd4dd3e4c1c91dea608f6dca94dc7925c02da1f93427e558521d4daa9` all matched before SIGTERM; graceful exit observed. Preserved record`/tmp/kydrive-r3-recreated-sgjfh9md/owned-process-r4-initial-preserved.json` (0600) and binary`/tmp/kydrive-r3-recreated-sgjfh9md/server-r4-initial-preserved` (0700); original record/binary retained too.
- New owned serverPID181143, startticks412713, binary`/tmp/kydrive-r3-recreated-sgjfh9md/server-r4-confirm` (0700), SHA256`8f24ccca5e02c1548504658201487145b8b7e751eb9c02bece3572e424c6b6bb`. Record`/tmp/kydrive-r3-recreated-sgjfh9md/owned-process-r4-confirm.json` (0600); server left running at http://127.0.0.1:5878 for parent.
- Same private fixture`/tmp/kydrive-r3-recreated-sgjfh9md` (0700); current`credentials.json` remains0600. Same minimal environment as environment-r4.json plus GOMAXPROCS2, recorded privately in environment-r4-confirm.json. No deployment secrets inherited; credentials/config/key pins unchanged. Never reproduce credential values.
- Same account/workspace/folder/file IDs and original timestamps. Fresh authenticated read-only listings/live download hashes/versions/shares/links equal initialstage03; initial metadata equalsR3. Exactly fixture-files.json: three live files plus one trashed file, allrevision1; Project materials empty. Archive content digest/size/state verified from unchanged durable metadata; no trashed download attempted.
- Before/after SQLite mode=ro checks match four file/version rows, one folder, no child content, one existing local account; no shares/links/grants/service tokens/app passwords/devices/editor records/upload sessions. Account opening preference staysON in UI/API and final SQL. Normal server login/session/audit and idempotent existing personal-workspace lookups are allowed application side effects; no fixture content or credential grants issued.
- HEAD/branch pinned throughout. All885 preexisting tracked files byte-identical before/after, all390 source files pinned to HEAD, dist hashes unchanged during capture, served index equals embedded/dist index, Go binary metadata records vcs.revision=8a23d753ba7aa0f67025efa1eca19bce3df63363 and vcs.modified=false. Full hashes and build metadata are in source-provenance-r4.json.
- Preserved46 initial artifacts exactly match stage03-initial-preservation.json; earlier artifacts/reports/contracts/driver untouched. Only canonical46 new artifacts belong to this confirmation.

## Verification and capture conditions

- Only sequential `npm --prefix web run build`, then `GOMAXPROCS=2 go build -p 1 -trimpath -o /tmp/kydrive-r3-recreated-sgjfh9md/server-r4-confirm ./cmd/server`. Both passed. Existing node_modules and persistent caches reused, no installs/cache override. Vite >500kB chunk warning retained in private frontend log; Go log empty. No compiler/vendor failure.
- Native T3 preview_status first, one owned incognito tab, real type/click authentication; waited for signed-in shell and no password fields before any snapshot. Blank availability tab before builds; actual navigation/probes after both builds. Native1280×900/390×844, OSlight/dark,DPR2. Native tab closed and preview_status unavailable before formal Playwright.
- Focused native checks in allfour conditions: PreviewClose40×40 beside title; links40px high and phone second row; desktop one row; text exact, transparent and border0, no text/body/document overflow; Close and Escape dismiss. Title and Close separate; exact bounds in provenance.
- Identity name/Administrator role retained; phone scroll hint visible; header preference absent. Share help is associated by aria-describedby=share-person-help and placed immediately under person input before Access on phone; Close40px square. No person query or form submit. Trash title/order retained, allfour controls40px high; Empty trash/Delete permanently share btn-danger-quiet, idle transparent/danger45% border in both themes and sizes. Desktop title/Upload top36; search x280–1240 matches outer panel; table x281–1239 is inset by1px border.
- Limited initial checks reused only for unchanged relevant source: preference OFF/reload/ON restore (AppHeader/TwoFactor), editor/manager share wording (ShareDialog), danger hover rules/tokens unchanged. This confirmation freshly checked current rest styles/identity/help/geometry and preferenceON; it did not repeat complete behavior or inaccessible roles. Runtime UI differs from initial only in QuickLook component and its preview grid CSS. The broader390-file inventory also includes three already-frozen owning-doc/browser-regression changes; all were preserved during this confirmation.
- Committed capture-round4.mjs ran ONCE, unmodified, against new owned record. Same10surfaces×2sizes×2OSschemes as initial/R3. Chromium156.0.8078.4/Playwright1.64.1,DPR1,en-US,America/New_York, normal service worker and controller, OS theme with saved ky_theme absent, initial viewport only.40/40 with no pageerror orHTTP≥400, zero horizontal overflow, all hovered-button lists empty; manifest published atomically.
- Pointer0,0 moves BEFORE500ms settling (already initialstage03; R3 moved after delay). Any hover-color correction fromR3 is capture state, not product gain. Same IDs/timestamps are checked as state, not raster equality; relative ages naturally advance.
- No full suites/npmci/audit/install/Go test/race/crypto/Docker/concurrent compiles or simultaneous native/formal browsers. No mocks/auth-storage/CSRF/CSP bypass, account/content/token/link/grant creation or destructive action. Real local recovery administrator only; ordinary/shared/OIDC/editor/connected workflows, full accessibility/behavior and production gates remain unscored.
- Native errors retained: about:blank URL rejected; omitted URL succeeded. Unsupported exact locator and :text-is timeout corrected using snapshot ref. My files retained Trash, so used Back to files before waiting for a live row. No browser fallback or capture retry. Native diagnostics include canceled previewHEAD requests during dismissal, no console entries in final snapshot; bounded snapshot omits older requests. Formal diagnostics do not cover all console warnings/canceled requests.
- DOX pass: applicable chains reread and changed paths reviewed; AGENTS intentionally unchanged because existing stage03 evidence contract covers outputs and owner forbids contract edits. MySlop skill read; known401/no-retry/no-post honored, no board request.

## Individual original inspections

All40 opened separately with native view_image(detail=original). No contact sheet, resizing or recapture. Backdrop blur/dimming and phone page continuation are rendered product behavior. No capture defect observed.

- `screenshots/kydrive-03-after-login-desktop-light.png` — 1280×900; Signed out; shipped username/password placeholders, verified security check and Match system; no current credentials entered before image.
- `screenshots/kydrive-03-after-files-desktop-light.png` — 1280×900; Three live revision-1 files and Project materials; name/role in shell, phone hint, full-width search, Upload and collapsed Administration.
- `screenshots/kydrive-03-after-folder-desktop-light.png` — 1280×900; Project materials heading/breadcrumb and empty folder; no content added.
- `screenshots/kydrive-03-after-new-folder-desktop-light.png` — 1280×900; Working drafts is unsubmitted input only; 40px Close, normal product backdrop blur/dimming; no folder created.
- `screenshots/kydrive-03-after-preview-desktop-light.png` — 1280×900; Exact flat Meeting notes.txt text; Close beside title, all controls40px; desktop one row, phone links together on second row; no clipping or overflow.
- `screenshots/kydrive-03-after-share-desktop-light.png` — 1280×900; Unsubmitted Reader form; person help below input before Access on phone; square Close; no shares/links created.
- `screenshots/kydrive-03-after-versions-desktop-light.png` — 1280×900; One current version with original instant; relative age naturally advanced to46minutes, no version action.
- `screenshots/kydrive-03-after-trash-desktop-light.png` — 1280×900; Only Archive note.txt; Back to files before Empty trash; transparent idle danger buttons; no restore/delete.
- `screenshots/kydrive-03-after-appearance-desktop-light.png` — 1280×900; Match system selected; preview cards; phone continues below viewport, no browser theme saved.
- `screenshots/kydrive-03-after-account-desktop-light.png` — 1280×900; Files preference ON; local recovery administrator/MFA Off; no credentials issued; page continues below viewport.
- `screenshots/kydrive-03-after-login-mobile-light.png` — 390×844; Signed out; shipped username/password placeholders, verified security check and Match system; no current credentials entered before image.
- `screenshots/kydrive-03-after-files-mobile-light.png` — 390×844; Three live revision-1 files and Project materials; name/role in shell, phone hint, full-width search, Upload and collapsed Administration.
- `screenshots/kydrive-03-after-folder-mobile-light.png` — 390×844; Project materials heading/breadcrumb and empty folder; no content added.
- `screenshots/kydrive-03-after-new-folder-mobile-light.png` — 390×844; Working drafts is unsubmitted input only; 40px Close, normal product backdrop blur/dimming; no folder created.
- `screenshots/kydrive-03-after-preview-mobile-light.png` — 390×844; Exact flat Meeting notes.txt text; Close beside title, all controls40px; desktop one row, phone links together on second row; no clipping or overflow.
- `screenshots/kydrive-03-after-share-mobile-light.png` — 390×844; Unsubmitted Reader form; person help below input before Access on phone; square Close; no shares/links created.
- `screenshots/kydrive-03-after-versions-mobile-light.png` — 390×844; One current version with original instant; relative age naturally advanced to46minutes, no version action.
- `screenshots/kydrive-03-after-trash-mobile-light.png` — 390×844; Only Archive note.txt; Back to files before Empty trash; transparent idle danger buttons; no restore/delete.
- `screenshots/kydrive-03-after-appearance-mobile-light.png` — 390×844; Match system selected; preview cards; phone continues below viewport, no browser theme saved.
- `screenshots/kydrive-03-after-account-mobile-light.png` — 390×844; Files preference ON; local recovery administrator/MFA Off; no credentials issued; page continues below viewport.
- `screenshots/kydrive-03-after-login-desktop-dark.png` — 1280×900; Signed out; shipped username/password placeholders, verified security check and Match system; no current credentials entered before image.
- `screenshots/kydrive-03-after-files-desktop-dark.png` — 1280×900; Three live revision-1 files and Project materials; name/role in shell, phone hint, full-width search, Upload and collapsed Administration.
- `screenshots/kydrive-03-after-folder-desktop-dark.png` — 1280×900; Project materials heading/breadcrumb and empty folder; no content added.
- `screenshots/kydrive-03-after-new-folder-desktop-dark.png` — 1280×900; Working drafts is unsubmitted input only; 40px Close, normal product backdrop blur/dimming; no folder created.
- `screenshots/kydrive-03-after-preview-desktop-dark.png` — 1280×900; Exact flat Meeting notes.txt text; Close beside title, all controls40px; desktop one row, phone links together on second row; no clipping or overflow.
- `screenshots/kydrive-03-after-share-desktop-dark.png` — 1280×900; Unsubmitted Reader form; person help below input before Access on phone; square Close; no shares/links created.
- `screenshots/kydrive-03-after-versions-desktop-dark.png` — 1280×900; One current version with original instant; relative age naturally advanced to46minutes, no version action.
- `screenshots/kydrive-03-after-trash-desktop-dark.png` — 1280×900; Only Archive note.txt; Back to files before Empty trash; transparent idle danger buttons; no restore/delete.
- `screenshots/kydrive-03-after-appearance-desktop-dark.png` — 1280×900; Match system selected; preview cards; phone continues below viewport, no browser theme saved.
- `screenshots/kydrive-03-after-account-desktop-dark.png` — 1280×900; Files preference ON; local recovery administrator/MFA Off; no credentials issued; page continues below viewport.
- `screenshots/kydrive-03-after-login-mobile-dark.png` — 390×844; Signed out; shipped username/password placeholders, verified security check and Match system; no current credentials entered before image.
- `screenshots/kydrive-03-after-files-mobile-dark.png` — 390×844; Three live revision-1 files and Project materials; name/role in shell, phone hint, full-width search, Upload and collapsed Administration.
- `screenshots/kydrive-03-after-folder-mobile-dark.png` — 390×844; Project materials heading/breadcrumb and empty folder; no content added.
- `screenshots/kydrive-03-after-new-folder-mobile-dark.png` — 390×844; Working drafts is unsubmitted input only; 40px Close, normal product backdrop blur/dimming; no folder created.
- `screenshots/kydrive-03-after-preview-mobile-dark.png` — 390×844; Exact flat Meeting notes.txt text; Close beside title, all controls40px; desktop one row, phone links together on second row; no clipping or overflow.
- `screenshots/kydrive-03-after-share-mobile-dark.png` — 390×844; Unsubmitted Reader form; person help below input before Access on phone; square Close; no shares/links created.
- `screenshots/kydrive-03-after-versions-mobile-dark.png` — 390×844; One current version with original instant; relative age naturally advanced to46minutes, no version action.
- `screenshots/kydrive-03-after-trash-mobile-dark.png` — 390×844; Only Archive note.txt; Back to files before Empty trash; transparent idle danger buttons; no restore/delete.
- `screenshots/kydrive-03-after-appearance-mobile-dark.png` — 390×844; Match system selected; preview cards; phone continues below viewport, no browser theme saved.
- `screenshots/kydrive-03-after-account-mobile-dark.png` — 390×844; Files preference ON; local recovery administrator/MFA Off; no credentials issued; page continues below viewport.

## Hash and duplicate checks

-40 originals match all manifest SHA256, byte counts and dimensions;40 unique PNG hashes,40 unique decodedRGBA hashes.
-380 same-size comparisons executed sequentially with installed Pillow/NumPy after formal browser closed, at most two image arrays plus delta. Near threshold: changed pixels<0.1% AND maximum channel delta≤8.
- Near duplicates:0; exhaustive results in near-duplicate-audit-v4.json. First private finalization completed380pairs but stopped before publishing on an overly strict source-inventory assertion (five changed inventory paths versus two runtime files). Corrected private metadata assembler and repeated380pairs for publication; two sequential audit attempts, no capture rerun.

## Complete new artifact list

- `docs/ui-drive-eight/screenshots/kydrive-03-after-login-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-files-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-folder-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-new-folder-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-preview-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-share-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-versions-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-trash-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-appearance-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-account-desktop-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-login-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-files-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-folder-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-new-folder-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-preview-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-share-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-versions-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-trash-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-appearance-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-account-mobile-light.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-login-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-files-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-folder-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-new-folder-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-preview-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-share-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-versions-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-trash-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-appearance-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-account-desktop-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-login-mobile-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-files-mobile-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-folder-mobile-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-new-folder-mobile-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-preview-mobile-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-share-mobile-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-versions-mobile-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-trash-mobile-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-appearance-mobile-dark.png`
- `docs/ui-drive-eight/screenshots/kydrive-03-after-account-mobile-dark.png`
- `docs/ui-drive-eight/manifest-03-after.json`
- `docs/ui-drive-eight/inventory-v4.json`
- `docs/ui-drive-eight/near-duplicate-audit-v4.json`
- `docs/ui-drive-eight/source-provenance-r4.json`
- `docs/ui-drive-eight/capture-provenance-r4.json`
- `docs/ui-drive-eight/round4-capture-checks.md`
