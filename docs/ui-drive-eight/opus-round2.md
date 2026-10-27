# KyDrive UI Council: Opus round 2 (stage 01-after)

Reviewer: Claude Opus 5.5 (`claude-opus-5-5`), owner progression reviewer. Rubric `ky-suite-v2`, schema `ky-ui-council-scores/2`. Authority: `authority.json` (ky-ui `639e5882`, 5 local objects verified). Product source `7dd862e4fab842c234e828754c101a56ebab322f`; evidence `bc34c4669bcc15d51f0384a0934a91df66ad4802`. Static screenshot review only: no builds, tests, services, browser, recapture or image-pair recomputation (owner instruction after a host hard lock).

## Result

**Overall covered set (40 unique stage01 originals): sum 314.6, mean 1573/200 = 7.865, which rounds half-up to 7.9. Below the 8.0 progression line.** This summarises the covered set only. It is not a ranking and not whole-product acceptance.

| Cohort (covered set, not a ranking) | n | Sum | Exact mean | Half-up 0.1 |
|---|---|---|---|---|
| drive (files, folder, new-folder, preview, share, versions, trash) | 28 | 219.8 | 157/20 = 7.85 | 7.9 |
| auth (login) | 4 | 31.6 | 79/10 = 7.9 | 7.9 |
| settings (appearance, account) | 8 | 63.2 | 79/10 = 7.9 | 7.9 |

| Surface | Cohort | n | Sum | Mean | Half-up |
|---|---|---|---|---|---|
| login | auth | 4 | 31.6 | 7.9 | 7.9 |
| files | drive | 4 | 31.2 | 7.8 | 7.8 |
| folder | drive | 4 | 31.6 | 7.9 | 7.9 |
| new-folder | drive | 4 | 33.6 | 8.4 | 8.4 |
| preview | drive | 4 | 30 | 7.5 | 7.5 |
| share | drive | 4 | 29.8 | 7.45 | 7.5 |
| versions | drive | 4 | 32 | 8 | 8.0 |
| trash | drive | 4 | 31.6 | 7.9 | 7.9 |
| appearance | settings | 4 | 32 | 8 | 8.0 |
| account | settings | 4 | 31.2 | 7.8 | 7.8 |

Verdict: `satisfied: false`, `genuinely_satisfied: false`. Opus covered-set overall is 7.865 (sum 314.6/40), which rounds half-up to 7.9, below 8.0. The refinement is real: the 14 product-changed files rose by a mean of 1.47. What remains is small: the Share dialog's missing primary and squeezed phone close, the phone toolbar indent, and the nested Preview frame. Each cohort rounds to 7.9. 9/10 is not achieved and all behaviour gates are untested.

Progression (parent's decision): the gate uses only this Opus overall, half-up to 0.1, against 8.0. Value: **7.9, does not meet 8.0.** It is never averaged with Sol, history or brand scores, and no single cohort can veto it. 9/10 is not achieved.

## Per-file scores

Scores are H/T/D/C/A/Co = hierarchy, typography, density_layout, color, visible_affordances, coherence. Confidence is high for every file. Full observations are in the JSON.

| File | Surface | State | H | T | D | C | A | Co | Total | Ky fit |
|---|---|---|---|---|---|---|---|---|---|---|
| `kydrive-01-after-login-desktop-light.png` | Sign-in page (signed out) | populated | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.5 |
| `kydrive-01-after-files-desktop-light.png` | My files root list with search, quota, Trash/ref | sparse | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.6 |
| `kydrive-01-after-folder-desktop-light.png` | Empty nested folder with breadcrumb My files > P | empty | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.6 |
| `kydrive-01-after-new-folder-desktop-light.png` | New folder modal dialog over dimmed/blurred My f | sparse | 9 | 8 | 8 | 8 | 9 | 8 | 8.4 | 8.5 |
| `kydrive-01-after-preview-desktop-light.png` | Quick look preview dialog for Meeting notes.txt  | sparse | 8 | 8 | 7 | 8 | 8 | 7 | 7.7 | 8.5 |
| `kydrive-01-after-share-desktop-light.png` | Share dialog for Meeting notes.txt | empty | 7 | 8 | 8 | 8 | 7 | 7 | 7.5 | 8.5 |
| `kydrive-01-after-versions-desktop-light.png` | Version history dialog for Meeting notes.txt | sparse | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.5 |
| `kydrive-01-after-trash-desktop-light.png` | Trash view for My files with retention note, Emp | sparse | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.6 |
| `kydrive-01-after-appearance-desktop-light.png` | Appearance theme-preview card picker | populated | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.6 |
| `kydrive-01-after-account-desktop-light.png` | Account page | empty | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.6 |
| `kydrive-01-after-login-mobile-light.png` | Sign-in page (signed out) | populated | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.5 |
| `kydrive-01-after-files-mobile-light.png` | My files root list with search, quota, Trash/ref | sparse | 8 | 8 | 7 | 8 | 8 | 7 | 7.7 | 8.6 |
| `kydrive-01-after-folder-mobile-light.png` | Empty nested folder with breadcrumb My files > P | empty | 8 | 8 | 7 | 8 | 8 | 8 | 7.8 | 8.6 |
| `kydrive-01-after-new-folder-mobile-light.png` | New folder modal dialog over dimmed/blurred My f | sparse | 9 | 8 | 8 | 8 | 9 | 8 | 8.4 | 8.5 |
| `kydrive-01-after-preview-mobile-light.png` | Quick look preview dialog for Meeting notes.txt  | sparse | 7 | 8 | 7 | 8 | 7 | 7 | 7.3 | 8.5 |
| `kydrive-01-after-share-mobile-light.png` | Share dialog for Meeting notes.txt | empty | 7 | 8 | 8 | 8 | 6 | 7 | 7.4 | 8.3 |
| `kydrive-01-after-versions-mobile-light.png` | Version history dialog for Meeting notes.txt | sparse | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.5 |
| `kydrive-01-after-trash-mobile-light.png` | Trash view for My files with retention note, Emp | sparse | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.6 |
| `kydrive-01-after-appearance-mobile-light.png` | Appearance theme-preview card picker | populated | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.6 |
| `kydrive-01-after-account-mobile-light.png` | Account page | empty | 8 | 8 | 7 | 8 | 8 | 7 | 7.7 | 8.6 |
| `kydrive-01-after-login-desktop-dark.png` | Sign-in page (signed out) | populated | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.5 |
| `kydrive-01-after-files-desktop-dark.png` | My files root list with search, quota, Trash/ref | sparse | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.6 |
| `kydrive-01-after-folder-desktop-dark.png` | Empty nested folder with breadcrumb My files > P | empty | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.6 |
| `kydrive-01-after-new-folder-desktop-dark.png` | New folder modal dialog over dimmed/blurred My f | sparse | 9 | 8 | 8 | 8 | 9 | 8 | 8.4 | 8.5 |
| `kydrive-01-after-preview-desktop-dark.png` | Quick look preview dialog for Meeting notes.txt  | sparse | 8 | 8 | 7 | 8 | 8 | 7 | 7.7 | 8.5 |
| `kydrive-01-after-share-desktop-dark.png` | Share dialog for Meeting notes.txt | empty | 7 | 8 | 8 | 8 | 7 | 7 | 7.5 | 8.5 |
| `kydrive-01-after-versions-desktop-dark.png` | Version history dialog for Meeting notes.txt | sparse | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.5 |
| `kydrive-01-after-trash-desktop-dark.png` | Trash view for My files with retention note, Emp | sparse | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.6 |
| `kydrive-01-after-appearance-desktop-dark.png` | Appearance theme-preview card picker | populated | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.6 |
| `kydrive-01-after-account-desktop-dark.png` | Account page | empty | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.6 |
| `kydrive-01-after-login-mobile-dark.png` | Sign-in page (signed out) | populated | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.5 |
| `kydrive-01-after-files-mobile-dark.png` | My files root list with search, quota, Trash/ref | sparse | 8 | 8 | 7 | 8 | 8 | 7 | 7.7 | 8.6 |
| `kydrive-01-after-folder-mobile-dark.png` | Empty nested folder with breadcrumb My files > P | empty | 8 | 8 | 7 | 8 | 8 | 8 | 7.8 | 8.6 |
| `kydrive-01-after-new-folder-mobile-dark.png` | New folder modal dialog over dimmed/blurred My f | sparse | 9 | 8 | 8 | 8 | 9 | 8 | 8.4 | 8.5 |
| `kydrive-01-after-preview-mobile-dark.png` | Quick look preview dialog for Meeting notes.txt  | sparse | 7 | 8 | 7 | 8 | 7 | 7 | 7.3 | 8.5 |
| `kydrive-01-after-share-mobile-dark.png` | Share dialog for Meeting notes.txt | empty | 7 | 8 | 8 | 8 | 6 | 7 | 7.4 | 8.3 |
| `kydrive-01-after-versions-mobile-dark.png` | Version history dialog for Meeting notes.txt | sparse | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.5 |
| `kydrive-01-after-trash-mobile-dark.png` | Trash view for My files with retention note, Emp | sparse | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 8.6 |
| `kydrive-01-after-appearance-mobile-dark.png` | Appearance theme-preview card picker | populated | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 8.6 |
| `kydrive-01-after-account-mobile-dark.png` | Account page | empty | 8 | 8 | 7 | 8 | 8 | 7 | 7.7 | 8.6 |

## Ky brand fit (separate, never in quality or progression)

Mean of per-file means: 8.54, half-up 8.5. By cohort: drive 8.5, auth 8.5, settings 8.6. Weights are equal over non-N/A dimensions. Shell is N/A on 20 files: the signed-out login and the dialogs, where the scrim blurs the shell. suite_cohesion is scored against the 4 verified KyIdentity brand references, which are context only and get no quality score. Shell, nav rail, account region and Appearance cards match KyIdentity closely. In light theme, the selected row on Appearance and Account is near-white instead of the tinted fill used on My files.

## Findings

### Material

- **R2-M1**: Share dialog close button is squeezed to about 24px wide beside the two-line title at 390px (other dialogs keep a square ~36px close). Unchanged from my round1 O4. Files: `screenshots/kydrive-01-after-share-mobile-light.png`, `screenshots/kydrive-01-after-share-mobile-dark.png`. Smallest fix: Give the Dialog header close button flex-shrink: 0 and a fixed square size.
- **R2-M2**: With Done now secondary, the Share dialog has no filled action and no visible way to add the person typed in 'Find a person'; Create link and Done are equal-weight outlines. Files: `screenshots/kydrive-01-after-share-desktop-light.png`, `screenshots/kydrive-01-after-share-desktop-dark.png`, `screenshots/kydrive-01-after-share-mobile-light.png`, `screenshots/kydrive-01-after-share-mobile-dark.png`. Smallest fix: If adding happens on picking a result, say so in the field's helper text; otherwise add one 'Share' button after Access as the dialog's single filled action (disabled until a person is chosen). Keep Done secondary.
- **R2-M3**: At 390px the wrapped Trash/refresh/New toolbar starts about 16–20px right of the panel heading/breadcrumb above it (also present in stage00). Files: `screenshots/kydrive-01-after-files-mobile-light.png`, `screenshots/kydrive-01-after-files-mobile-dark.png`, `screenshots/kydrive-01-after-folder-mobile-light.png`, `screenshots/kydrive-01-after-folder-mobile-dark.png`. Smallest fix: Drop the toolbar's left offset when it wraps below the panel title so both share the panel padding edge.
- **R2-M4**: 8 role labels are wrong: the 4 KyIdentity brand references say 'historical round1 original', and 4 stage00 mobile-dark files say 'current round1 scoring original'. Hashes and file membership are correct. Files: `artifacts-v2.json`. Smallest fix: Relabel those 8 roles (brand-only; historical stage00). No image changes needed.

### Optional

- **R2-O1** (preview, all 4): Bordered monospace frame nested in a tinted body inside the dialog; dialog stays near full height for 3 lines; on phone Close moves to the action row and 'Open in new tab' is indented from the title. Smallest fix: Render text on the dialog body without its own border; size to content with a max-height; keep Close in the title row.
- **R2-O2** (trash, all 4): Empty trash uses a tinted danger fill while the row's Delete permanently is a plain danger outline (present in stage00 too). Smallest fix: Use one quiet danger-outline style for both.
- **R2-O3** (files root, all 4): Root list panel header repeats 'My files' under the identical h1 (root breadcrumb deliberately retained). Smallest fix: Accept as the breadcrumb root, or render the root crumb muted so it reads as a path rather than a second title.
- **R2-O4** (`screenshots/kydrive-01-after-appearance-desktop-light.png`, `screenshots/kydrive-01-after-account-desktop-light.png`, `screenshots/kydrive-01-after-appearance-mobile-light.png`, `screenshots/kydrive-01-after-account-mobile-light.png`): Light theme selected nav row is near-white (#FFFFFD/#FFFDF8 sampled) on Appearance/Account but tinted #FBF0EC on My files, which matches KyIdentity. Cause unproven (product style vs pointer hover left by capture navigation). Unchanged from my round1 O6. Smallest fix: Verify without pointer hover in the next batch; if real, apply the same current-row background to every nav item.
- **R2-O5** (account, all 4): 'Off' status pill runs into the following sentence; choice cards sit inside section panels (card-in-card). Smallest fix: Put the status pill on its own line or after the heading; flatten choice cards to bordered rows or drop the outer panel border.
- **R2-O6** (login, all 4): Security-check status is a bordered box inside the bordered card; on dark, the stamp's tile matches the page so only the B glyph shows. Unchanged from my round1 O2. Smallest fix: Show the verified status as inline text+icon; give the dark stamp a visible tile edge.
- **R2-O7** (files/folder/trash (12)): Quota meter fill is still not visible at 350 B of 10 GiB, so the bar reads as an empty rule. Smallest fix: Minimum visible fill, or text only.
- **R2-O8** (appearance/account dark (4)): Unselected radios render as filled grey dots in dark theme. Smallest fix: Outlined unfilled ring for unselected radios.
- **R2-O9** (desktop shell views (10)): Account region shows 'Administrator' as both name and role. Smallest fix: Show username (admin) with the role, or omit the role when it equals the display name.

## Freeze and blind exposure

- I froze the complete files array (quality and brand vectors, observations, totals, derived fields) at **2026-10-27T20:38:10+00:00**. SHA-256 `014f111dbd7f19746f1926f8407d825fe6f08140ea09c0e14391be9b41b3b16c`, computed over `json.dumps(files, sort_keys=True, separators=(',',':'), ensure_ascii=False).encode('utf-8')`. The published files array still matches that hash.
- Before the freeze I read the AGENTS chain, the skills, the pinned authority, the blind inputs (capture-provenance includes the capture agent's per-image notes, which carry no scores), the 40 stage01 originals and the 4 brand references, each viewed individually at native size, plus 3 pixel samples.
- After the freeze I read only `opus-round1.json` and the 10 stage00 originals listed in `drift.stage00_viewed_after_freeze`.
- Never read: `opus-round1.md`, Sol or other peer reports, round1 audits, restart-recovery, delivery, COUNCIL, CSV/XLSX, galleries, PRs and hand-offs.

## Drift against my own round 1 (frozen scores unchanged, no adjusted means)

Round 1: 7.2325 (half-up 7.2). Round 2: 7.865 (half-up 7.9). Mean file delta: +0.6325. Product-changed files (n=14): +1.4714. All other files (n=26): +0.1807. Files with |delta| ≥ 0.5: 16.

| File | Prior | Now | Δ | Cause | Explanation |
|---|---|---|---|---|---|
| `kydrive-01-after-files-desktop-light.png` | 6.6 | 7.9 | 1.3 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, Upload files moved to the page header, and the folder icon is neutral. |
| `kydrive-01-after-folder-desktop-light.png` | 6.5 | 8.0 | 1.5 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, the h1 names the current folder/Trash, Upload files moved to the page header with the empty-state upload now secondary. |
| `kydrive-01-after-trash-desktop-light.png` | 6.6 | 7.9 | 1.3 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, the h1 names the current folder/Trash, Upload files moved to the page header, and the Trash panel header is no longer clipped. |
| `kydrive-01-after-account-desktop-light.png` | 6.4 | 7.9 | 1.5 | product | Actual product change visible in stage00: page column now left-aligned at the shared x=280 edge, disabled Create app password is neutral grey instead of a faded primary, and the dashed 'In use' rule is gone. |
| `kydrive-01-after-files-mobile-light.png` | 6.1 | 7.7 | 1.6 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, Upload files moved to the page header, and the folder icon is neutral. |
| `kydrive-01-after-folder-mobile-light.png` | 6.1 | 7.8 | 1.7 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, the h1 names the current folder/Trash, Upload files moved to the page header with the empty-state upload now secondary. |
| `kydrive-01-after-trash-mobile-light.png` | 6.5 | 7.9 | 1.4 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, the h1 names the current folder/Trash, Upload files moved to the page header, and the Trash panel header is no longer clipped. |
| `kydrive-01-after-account-mobile-light.png` | 7.2 | 7.7 | 0.5 | reviewer variance | Stage00 and stage01 phone viewports look the same on visual inspection (pixel diff not recomputed by instruction); the +0.5 is my own scoring variance, not product gain: hierarchy, color and affordances each read one point higher this round. |
| `kydrive-01-after-files-desktop-dark.png` | 6.6 | 7.9 | 1.3 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, Upload files moved to the page header, and the folder icon is neutral. |
| `kydrive-01-after-folder-desktop-dark.png` | 6.5 | 8.0 | 1.5 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, the h1 names the current folder/Trash, Upload files moved to the page header with the empty-state upload now secondary. |
| `kydrive-01-after-trash-desktop-dark.png` | 6.6 | 7.9 | 1.3 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, the h1 names the current folder/Trash, Upload files moved to the page header, and the Trash panel header is no longer clipped. |
| `kydrive-01-after-account-desktop-dark.png` | 6.4 | 7.9 | 1.5 | product | Actual product change visible in stage00: page column now left-aligned at the shared x=280 edge, disabled Create app password is neutral grey instead of a faded primary, and the dashed 'In use' rule is gone. |
| `kydrive-01-after-files-mobile-dark.png` | 6.1 | 7.7 | 1.6 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, Upload files moved to the page header, and the folder icon is neutral. |
| `kydrive-01-after-folder-mobile-dark.png` | 6.1 | 7.8 | 1.7 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, the h1 names the current folder/Trash, Upload files moved to the page header with the empty-state upload now secondary. |
| `kydrive-01-after-trash-mobile-dark.png` | 6.5 | 7.9 | 1.4 | product | Actual product change visible in the matched stage00 original: the three administrator accordions that preceded search/list now sit in one collapsed Administration disclosure after the list, the h1 names the current folder/Trash, Upload files moved to the page header, and the Trash panel header is no longer clipped. |
| `kydrive-01-after-account-mobile-dark.png` | 7.2 | 7.7 | 0.5 | reviewer variance | Stage00 and stage01 phone viewports look the same on visual inspection (pixel diff not recomputed by instruction); the +0.5 is my own scoring variance, not product gain: hierarchy, color and affordances each read one point higher this round. |

The 24 deltas under 0.5 are labelled per file in the JSON. Most are my own re-scoring variance on screens that look unchanged, and I do not count them as product gain: new-folder +0.4, login +0.2, Appearance +0.0 to +0.3, preview +0.0 to +0.2. Two have minor product causes: versions +0.1 (neutral Current badge) and share −0.2/+0.2 (Done made secondary). Without the variance terms the overall would be slightly lower. The frozen value still stands.

## Gates and tests

Keyboard, focus, contrast, states, responsive and role/security/integration are all **untested**. Screenshots cannot pass a gate. I ran no tests. Parent reported 100 frontend and 50 browser passes (2 skipped) before the reboot; temp logs are lost, the full Go suite was interrupted, and fresh hosted CI is pending. Not independently verified.

## Inputs, conditions and coverage

- Capture: Playwright 1.64.1 / Chromium 156.0.8078.4 headless; DPR 1; 1280×900 and 390×844; OS light/dark, no saved choice; en-US, America/New_York; normal service workers; initial viewport only; one invocation, 0 recaptures. The T3 native browser was used only for preparatory navigation.
- Binary: `6e0559e1…edec`, vcs.revision 7dd862e, `vcs.modified=true`. Per the provenance record, all 390 product pins and the served index matched. This is not a hermetic-build claim, and I did not re-verify it.
- Account: the actual local recovery administrator in every authenticated picture; login is signed out.
- Image inventory: 84 images. 40 stage01 are quality-scored, with hashes verified now. 40 stage00 are historical and unscored; 10 were viewed after the freeze for drift. 4 KyIdentity references are brand-only. `artifacts-v2.json` mislabels 8 roles (R2-M4).
- Exact duplicates: 0. Near duplicates: 0 across 380 recorded RGBA comparisons (not rerun).
- Unavailable and unscored, not inferred absent: Ordinary OIDC account shell and task workflows (no local staff provisioning); Group-granted shared workspace and populated Shared with me; Euro-Office editing and save; Live SSO, guest links, linked computers and recovery integrations; Admin Settings & DB, Recovery and expanded Administration contents (not captured); Below-fold content on phone captures and desktop Account.
- Input hashes: see `input_hashes` in the JSON (15 inputs, including authority objects and `opus-round1.json`).
- Gemini: unused (advisory only after both primaries are satisfied; quota unavailable). MySlop: not posted (known 401).

