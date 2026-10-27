# Stage03 bounded capture checks

**Source:** `24f667e61ca2802dabf4cbdf8a71117512203d6a`, branch `ui/drive-eight`. One initial batch; no confirmation/overwrite; no score or acceptance decision.

**Observation for parent:** immediate native phone keyboard DOM probe after Tab reaches Appearance while most of its focused button remains clipped (x381.39–507.69 at390px, scrollLeft0); next Recovery focus scrolls row. Both light/dark showed this immediate focus state. The probe had no explicit focus-scroll settling delay or screenshot, so in-flight browser scrolling is not excluded; a persistent clipping defect is unconfirmed. No product edit or extra capture authorized here.

## Fixture and process

- Retained R3 instance at `http://127.0.0.1:5878`; user/workspace/file/folder IDs and file timestamps unchanged. Authenticated read-only identity/listings/live bytes/versions/shares/links exactly match R3. Three live files + one trashed, all revision1; Project materials empty. Post-capture SQLite read-only verification confirms durable IDs/revisions/digests/sizes/timestamps and zero share/link/grant/editor/credential issuance. Original key/config/data retained.
- Verified PID65645, `/proc` executable, start_ticks159822 and binary hash before SIGTERM. Exit observed. Preserved old binary/record at private distinct paths. New PID154767, start_ticks341610, binary `/tmp/kydrive-r3-recreated-sgjfh9md/server-r4`; owned record `/tmp/kydrive-r3-recreated-sgjfh9md/owned-process-r4.json`.
- Current private credentials: `/tmp/kydrive-r3-recreated-sgjfh9md/credentials.json` (0600); fixture directory0700. Never copy values. Server left running for parent. Runtime limited to GOMAXPROCS2 with same minimal recorded fixture environment; no deployment secrets inherited.

## Checks and boundaries

- Sequential `npm --prefix web run build` then `GOMAXPROCS=2 go build -p 1 -trimpath`; existing dependencies and persistent caches. Both pass; Vite>500kB chunk warning retained in private build log. Go1.27.3 metadata pins source and `vcs.modified=false`. Served index equals dist. All 835 preexisting tracked files and 390 product-source files match before/after; dist unchanged during capture.
- Native T3 incognito login/type/click, OS light/dark and1280×900/390×844; DPR2 is separate probe evidence. Blank native tab opened before builds only to establish availability; app navigation/probes after both builds. Native tab closed and preview status empty before formal Playwright.
- Same single header navigation container, identity name/role visible at both sizes/schemes; phone scroll hint visible. Destinations keyboard-focusable, with the immediate Appearance clipping observation and timing limit above.
- Header preference absent; Account→Files available. UI toggle OFF persisted after reload and API read; original ON restored and verified after reload. No editors opened.
- Share person aria-describedby points to help; help directly below input before Access on phone. Reader/editor/manager wording verified using actual keyboard select; no person query, share or link creation. Close40×40, flex-shrink0. Escape and Close dismissal observed.
- Preview exact Meeting notes.txt; every header control40px high, Close40×40, pre border0, no inner/document horizontal overflow in all four native probes; Escape and Close dismiss. Phone Close wraps onto its own row (retained product layout).
- Desktop search spans x280–1240; table outer panel same edges (table x281–1239 inside1px border). Heading and Upload top36px. Folder/Trash titles correct. Trash Back to files precedes Empty trash; Restore/Delete permanently40px high. No destructive action activated.
- Both destructive buttons use `btn-danger-quiet`, idle transparent background/45% danger border; hover12% danger fill/full danger border in light/dark and both widths. Old stage02 desktop-light pink fill is consistent with hover-transition residue, not a distinct style. Its old manifest lacks hover data, so exact old instant cannot be proven.
- Capture-state correction: stage03 moves pointer0,0 BEFORE500ms settling; stage02 moved it AFTER. Hover-color differences are not credited as product improvement. All40 stage03 hovered-button lists empty.
- Formal exact conditions:1280×900/390×844, OSlight/dark, DPR1, en-US, America/New_York, normal service worker controller on every image, no saved ky_theme, initial view, no scroll/capture overwrite.40/40 complete, no pageerrors/HTTP failures, horizontal overflow0. Formal driver does not log every console warning or request cancellation.
- Native diagnostics: no console entries; two canceled preview HEAD requests (`net::ERR_ABORTED`) during dismissal. Native selector/keyboard probes were corrected after two null read errors. The private post-capture metadata checker initially compared raw blank mtime with API-fallback mtime; corrected to the API created-time fallback and passed without data changes. A snapshot while login was pending exposed the disposable password in tool history; no value copied into repository evidence or formal PNGs. Current private credential file remains for parent.
- No full suite, install/audit, Go tests/race/crypto, Docker, concurrent compiles or simultaneous native/formal browser. No database writes/fabrication, API mocks, auth/CSRF/CSP bypass, new content/tokens/grants. No ordinary/shared/SSO/editor/production/accessibility acceptance claim.
- DOX pass completed; AGENTS intentionally unchanged: existing evidence contracts apply and explicit edit prohibition. Parent owns correction/index decisions. MySlop skill read, known401/no-retry/no-post honored; no board request.

## Original-by-original inspection

Each original individually opened with `view_image(detail=original)` at native dimensions. No capture defect observed. Background blur/dimming, preview phone control wrap and viewport continuation are product behavior.

- `screenshots/kydrive-03-after-login-desktop-light.png` — 1280×900; PASS capture; Signed out; shipped placeholders, verified security check, Match system. No filled current credentials in formal image.
- `screenshots/kydrive-03-after-files-desktop-light.png` — 1280×900; PASS capture; Three live revision-1 files plus Project materials; name/role in shell; no header preference; Upload and full-width search; Administration collapsed.
- `screenshots/kydrive-03-after-folder-desktop-light.png` — 1280×900; PASS capture; Project materials title and breadcrumb, empty folder; no content added.
- `screenshots/kydrive-03-after-new-folder-desktop-light.png` — 1280×900; PASS capture; Working drafts entered; unsubmitted form; fixed Close; blur/dimming is product behavior.
- `screenshots/kydrive-03-after-preview-desktop-light.png` — 1280×900; PASS capture; Exact Meeting notes.txt plain text; no inner border; header controls visible, phone Close wraps to its own row; no overflow.
- `screenshots/kydrive-03-after-share-desktop-light.png` — 1280×900; PASS capture; Empty unsubmitted form, Reader; help below person input before Access on phone, square Close; no shares/links.
- `screenshots/kydrive-03-after-versions-desktop-light.png` — 1280×900; PASS capture; One current version; original creation timestamp, relative age naturally advanced; no version action.
- `screenshots/kydrive-03-after-trash-desktop-light.png` — 1280×900; PASS capture; Only Archive note.txt; Back to files before Empty trash; idle danger-quiet controls; no restore/delete activated.
- `screenshots/kydrive-03-after-appearance-desktop-light.png` — 1280×900; PASS capture; Match system selected; theme-preview cards; phone list continues below viewport; no theme saved.
- `screenshots/kydrive-03-after-account-desktop-light.png` — 1280×900; PASS capture; Account Files preference visibly ON; local administrator, MFA Off; no credential issuance; continues below viewport.
- `screenshots/kydrive-03-after-login-mobile-light.png` — 390×844; PASS capture; Signed out; shipped placeholders, verified security check, Match system. No filled current credentials in formal image.
- `screenshots/kydrive-03-after-files-mobile-light.png` — 390×844; PASS capture; Three live revision-1 files plus Project materials; name/role in shell; no header preference; Upload and full-width search; Administration collapsed.
- `screenshots/kydrive-03-after-folder-mobile-light.png` — 390×844; PASS capture; Project materials title and breadcrumb, empty folder; no content added.
- `screenshots/kydrive-03-after-new-folder-mobile-light.png` — 390×844; PASS capture; Working drafts entered; unsubmitted form; fixed Close; blur/dimming is product behavior.
- `screenshots/kydrive-03-after-preview-mobile-light.png` — 390×844; PASS capture; Exact Meeting notes.txt plain text; no inner border; header controls visible, phone Close wraps to its own row; no overflow.
- `screenshots/kydrive-03-after-share-mobile-light.png` — 390×844; PASS capture; Empty unsubmitted form, Reader; help below person input before Access on phone, square Close; no shares/links.
- `screenshots/kydrive-03-after-versions-mobile-light.png` — 390×844; PASS capture; One current version; original creation timestamp, relative age naturally advanced; no version action.
- `screenshots/kydrive-03-after-trash-mobile-light.png` — 390×844; PASS capture; Only Archive note.txt; Back to files before Empty trash; idle danger-quiet controls; no restore/delete activated.
- `screenshots/kydrive-03-after-appearance-mobile-light.png` — 390×844; PASS capture; Match system selected; theme-preview cards; phone list continues below viewport; no theme saved.
- `screenshots/kydrive-03-after-account-mobile-light.png` — 390×844; PASS capture; Account Files preference visibly ON; local administrator, MFA Off; no credential issuance; continues below viewport.
- `screenshots/kydrive-03-after-login-desktop-dark.png` — 1280×900; PASS capture; Signed out; shipped placeholders, verified security check, Match system. No filled current credentials in formal image.
- `screenshots/kydrive-03-after-files-desktop-dark.png` — 1280×900; PASS capture; Three live revision-1 files plus Project materials; name/role in shell; no header preference; Upload and full-width search; Administration collapsed.
- `screenshots/kydrive-03-after-folder-desktop-dark.png` — 1280×900; PASS capture; Project materials title and breadcrumb, empty folder; no content added.
- `screenshots/kydrive-03-after-new-folder-desktop-dark.png` — 1280×900; PASS capture; Working drafts entered; unsubmitted form; fixed Close; blur/dimming is product behavior.
- `screenshots/kydrive-03-after-preview-desktop-dark.png` — 1280×900; PASS capture; Exact Meeting notes.txt plain text; no inner border; header controls visible, phone Close wraps to its own row; no overflow.
- `screenshots/kydrive-03-after-share-desktop-dark.png` — 1280×900; PASS capture; Empty unsubmitted form, Reader; help below person input before Access on phone, square Close; no shares/links.
- `screenshots/kydrive-03-after-versions-desktop-dark.png` — 1280×900; PASS capture; One current version; original creation timestamp, relative age naturally advanced; no version action.
- `screenshots/kydrive-03-after-trash-desktop-dark.png` — 1280×900; PASS capture; Only Archive note.txt; Back to files before Empty trash; idle danger-quiet controls; no restore/delete activated.
- `screenshots/kydrive-03-after-appearance-desktop-dark.png` — 1280×900; PASS capture; Match system selected; theme-preview cards; phone list continues below viewport; no theme saved.
- `screenshots/kydrive-03-after-account-desktop-dark.png` — 1280×900; PASS capture; Account Files preference visibly ON; local administrator, MFA Off; no credential issuance; continues below viewport.
- `screenshots/kydrive-03-after-login-mobile-dark.png` — 390×844; PASS capture; Signed out; shipped placeholders, verified security check, Match system. No filled current credentials in formal image.
- `screenshots/kydrive-03-after-files-mobile-dark.png` — 390×844; PASS capture; Three live revision-1 files plus Project materials; name/role in shell; no header preference; Upload and full-width search; Administration collapsed.
- `screenshots/kydrive-03-after-folder-mobile-dark.png` — 390×844; PASS capture; Project materials title and breadcrumb, empty folder; no content added.
- `screenshots/kydrive-03-after-new-folder-mobile-dark.png` — 390×844; PASS capture; Working drafts entered; unsubmitted form; fixed Close; blur/dimming is product behavior.
- `screenshots/kydrive-03-after-preview-mobile-dark.png` — 390×844; PASS capture; Exact Meeting notes.txt plain text; no inner border; header controls visible, phone Close wraps to its own row; no overflow.
- `screenshots/kydrive-03-after-share-mobile-dark.png` — 390×844; PASS capture; Empty unsubmitted form, Reader; help below person input before Access on phone, square Close; no shares/links.
- `screenshots/kydrive-03-after-versions-mobile-dark.png` — 390×844; PASS capture; One current version; original creation timestamp, relative age naturally advanced; no version action.
- `screenshots/kydrive-03-after-trash-mobile-dark.png` — 390×844; PASS capture; Only Archive note.txt; Back to files before Empty trash; idle danger-quiet controls; no restore/delete activated.
- `screenshots/kydrive-03-after-appearance-mobile-dark.png` — 390×844; PASS capture; Match system selected; theme-preview cards; phone list continues below viewport; no theme saved.
- `screenshots/kydrive-03-after-account-mobile-dark.png` — 390×844; PASS capture; Account Files preference visibly ON; local administrator, MFA Off; no credential issuance; continues below viewport.

## Hash/duplicate audit

All40 file SHA-256/bytes/dimensions match manifest; 40 unique PNG hashes and 40 unique decoded RGBA hashes.380 same-size comparisons sequentially using Pillow/NumPy; at most2 original arrays plus delta. Near duplicate threshold: changed pixels<0.1% AND maximum channel delta≤8. Near duplicates: 0.

## Complete new repository file list

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

No preexisting tracked file was changed; earlier stages/drivers/manifests/raw reports remain byte-identical. Parent-owned untracked `round3-spread-audit.md` appeared during work and was neither read nor modified.
