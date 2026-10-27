# KyDrive UI Council — Opus round 3 (stage 02-after)

Reviewer: Claude Opus 5.5 (`claude-opus-5-5`), raw independent report. Rubric `ky-suite-v2`. Product `e9fadac`, evidence `b974b0c`. Recreated fixture after reboot: reconstruction differences are not product gains.

## Result

- Quality, covered set: **311.3/40 = 3113/400 (exact 7.7825) → 7.8 half-up**. Gate ≥8.0: **not met**.
- Brand fit (separate, unrounded per-file means): **1653/200 = 8.265 → 8.3**.
- Independent genuine satisfaction: **false**. Exact quality mean 7.7825 (311.3/40) rounds half-up to 7.8, below 8.0. Fixes for the three prior objections are visible, but Share still lacks a primary, Trash over-emphasises destruction, the desktop search/list widths disagree, the Preview header controls differ in size, and phone nav overflow is uncued. All cohorts are below 8.0. 9/10 is not achieved and every behaviour gate is untested.

Not whole-product acceptance. Behaviour gates are all untested. Ordinary-user, shared-workspace, editor, SSO and connected flows are absent and unscored.

## Freeze

- `files_sha256` `be3a79a44b173c1bfbb57a4091328c73a59122916fec1783d30e789f07d004a9`, 40 rows, frozen 2026-10-27T21:09:35.829603+00:00 before any prior or peer material was read.
- Before freeze I read only the AGENTS chain, skills, pinned authority, the blind inputs, the 40 current originals and the 4 brand references (each opened individually). After freeze I read only my own `opus-round2.json` and the exporter code.

## Cohorts (summaries, not rankings)

| Set | n | Sum | Fraction | Exact decimal | Half-up |
|---|---|---|---|---|---|
| auth | 4 | 31.6 | 79/10 | 7.9 | 7.9 |
| drive | 28 | 216.7 | 2167/280 | 7.739285714285714285714285714 | 7.7 |
| settings | 8 | 63 | 63/8 | 7.875 | 7.9 |
| **overall** | 40 | 311.3 | 3113/400 | 7.7825 | 7.8 |

## Surfaces / device / theme (descriptive)

| Set | n | Sum | Fraction | Exact decimal | Half-up |
|---|---|---|---|---|---|
| login | 4 | 31.6 | 79/10 | 7.9 | 7.9 |
| files | 4 | 30.8 | 77/10 | 7.7 | 7.7 |
| folder | 4 | 31 | 31/4 | 7.75 | 7.8 |
| new-folder | 4 | 33.6 | 42/5 | 8.4 | 8.4 |
| preview | 4 | 30 | 15/2 | 7.5 | 7.5 |
| share | 4 | 29.6 | 37/5 | 7.4 | 7.4 |
| versions | 4 | 32 | 8 | 8 | 8.0 |
| trash | 4 | 29.7 | 297/40 | 7.425 | 7.4 |
| appearance | 4 | 31.8 | 159/20 | 7.95 | 8.0 |
| account | 4 | 31.2 | 39/5 | 7.8 | 7.8 |
| desktop | 20 | 156.2 | 781/100 | 7.81 | 7.8 |
| mobile | 20 | 155.1 | 1551/200 | 7.755 | 7.8 |
| dark | 20 | 155.8 | 779/100 | 7.79 | 7.8 |
| light | 20 | 155.5 | 311/40 | 7.775 | 7.8 |
| desktop-dark | 10 | 78.2 | 391/50 | 7.82 | 7.8 |
| desktop-light | 10 | 78 | 39/5 | 7.8 | 7.8 |
| mobile-dark | 10 | 77.6 | 194/25 | 7.76 | 7.8 |
| mobile-light | 10 | 77.5 | 31/4 | 7.75 | 7.8 |

## Per-file

| File | H | T | D | C | A | Co | Quality | Brand (P,T,S,A,C) | Brand mean |
|---|---|---|---|---|---|---|---|---|---|
| kydrive-02-after-login-desktop-light.png | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 9,8,N/A,9,8 | 17/2 (8.5) |
| kydrive-02-after-files-desktop-light.png | 8 | 8 | 7 | 8 | 7 | 8 | 7.7 | 9,8,8,8,9 | 42/5 (8.4) |
| kydrive-02-after-folder-desktop-light.png | 8 | 8 | 7 | 8 | 8 | 8 | 7.8 | 9,8,8,8,9 | 42/5 (8.4) |
| kydrive-02-after-new-folder-desktop-light.png | 9 | 8 | 8 | 8 | 9 | 8 | 8.4 | 9,9,N/A,9,8 | 35/4 (8.75) |
| kydrive-02-after-preview-desktop-light.png | 8 | 8 | 7 | 8 | 7 | 7 | 7.5 | 9,7,N/A,9,8 | 33/4 (8.25) |
| kydrive-02-after-share-desktop-light.png | 7 | 8 | 8 | 8 | 7 | 7 | 7.5 | 9,8,N/A,9,7 | 33/4 (8.25) |
| kydrive-02-after-versions-desktop-light.png | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9,8,N/A,9,8 | 17/2 (8.5) |
| kydrive-02-after-trash-desktop-light.png | 7 | 8 | 7 | 7 | 8 | 7 | 7.3 | 8,7,8,8,8 | 39/5 (7.8) |
| kydrive-02-after-appearance-desktop-light.png | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9,8,7,8,9 | 41/5 (8.2) |
| kydrive-02-after-account-desktop-light.png | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 9,8,7,8,8 | 8/1 (8.0) |
| kydrive-02-after-login-mobile-light.png | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 9,8,N/A,9,8 | 17/2 (8.5) |
| kydrive-02-after-files-mobile-light.png | 8 | 8 | 7 | 8 | 7 | 8 | 7.7 | 9,8,7,8,8 | 8/1 (8.0) |
| kydrive-02-after-folder-mobile-light.png | 8 | 8 | 7 | 8 | 7 | 8 | 7.7 | 9,8,7,8,8 | 8/1 (8.0) |
| kydrive-02-after-new-folder-mobile-light.png | 9 | 8 | 8 | 8 | 9 | 8 | 8.4 | 9,9,N/A,9,8 | 35/4 (8.75) |
| kydrive-02-after-preview-mobile-light.png | 8 | 8 | 7 | 8 | 7 | 7 | 7.5 | 9,7,N/A,9,8 | 33/4 (8.25) |
| kydrive-02-after-share-mobile-light.png | 7 | 8 | 7 | 8 | 7 | 7 | 7.3 | 9,8,N/A,9,7 | 33/4 (8.25) |
| kydrive-02-after-versions-mobile-light.png | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9,8,N/A,9,8 | 17/2 (8.5) |
| kydrive-02-after-trash-mobile-light.png | 7 | 8 | 8 | 7 | 7 | 7 | 7.4 | 8,7,7,8,8 | 38/5 (7.6) |
| kydrive-02-after-appearance-mobile-light.png | 8 | 8 | 8 | 8 | 7 | 8 | 7.9 | 9,8,6,8,9 | 8/1 (8.0) |
| kydrive-02-after-account-mobile-light.png | 8 | 8 | 8 | 8 | 7 | 7 | 7.7 | 9,8,6,8,8 | 39/5 (7.8) |
| kydrive-02-after-login-desktop-dark.png | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 9,8,N/A,9,8 | 17/2 (8.5) |
| kydrive-02-after-files-desktop-dark.png | 8 | 8 | 7 | 8 | 7 | 8 | 7.7 | 9,8,8,8,9 | 42/5 (8.4) |
| kydrive-02-after-folder-desktop-dark.png | 8 | 8 | 7 | 8 | 8 | 8 | 7.8 | 9,8,8,8,9 | 42/5 (8.4) |
| kydrive-02-after-new-folder-desktop-dark.png | 9 | 8 | 8 | 8 | 9 | 8 | 8.4 | 9,9,N/A,9,8 | 35/4 (8.75) |
| kydrive-02-after-preview-desktop-dark.png | 8 | 8 | 7 | 8 | 7 | 7 | 7.5 | 9,7,N/A,9,8 | 33/4 (8.25) |
| kydrive-02-after-share-desktop-dark.png | 7 | 8 | 8 | 8 | 7 | 7 | 7.5 | 9,8,N/A,9,7 | 33/4 (8.25) |
| kydrive-02-after-versions-desktop-dark.png | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9,8,N/A,9,8 | 17/2 (8.5) |
| kydrive-02-after-trash-desktop-dark.png | 7 | 8 | 7 | 8 | 8 | 7 | 7.5 | 9,7,8,8,8 | 8/1 (8.0) |
| kydrive-02-after-appearance-desktop-dark.png | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9,8,8,8,9 | 42/5 (8.4) |
| kydrive-02-after-account-desktop-dark.png | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 9,8,8,8,8 | 41/5 (8.2) |
| kydrive-02-after-login-mobile-dark.png | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 9,8,N/A,9,8 | 17/2 (8.5) |
| kydrive-02-after-files-mobile-dark.png | 8 | 8 | 7 | 8 | 7 | 8 | 7.7 | 9,8,7,8,8 | 8/1 (8.0) |
| kydrive-02-after-folder-mobile-dark.png | 8 | 8 | 7 | 8 | 7 | 8 | 7.7 | 9,8,7,8,8 | 8/1 (8.0) |
| kydrive-02-after-new-folder-mobile-dark.png | 9 | 8 | 8 | 8 | 9 | 8 | 8.4 | 9,9,N/A,9,8 | 35/4 (8.75) |
| kydrive-02-after-preview-mobile-dark.png | 8 | 8 | 7 | 8 | 7 | 7 | 7.5 | 9,7,N/A,9,8 | 33/4 (8.25) |
| kydrive-02-after-share-mobile-dark.png | 7 | 8 | 7 | 8 | 7 | 7 | 7.3 | 9,8,N/A,9,7 | 33/4 (8.25) |
| kydrive-02-after-versions-mobile-dark.png | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9,8,N/A,9,8 | 17/2 (8.5) |
| kydrive-02-after-trash-mobile-dark.png | 7 | 8 | 8 | 8 | 7 | 7 | 7.5 | 9,7,7,8,8 | 39/5 (7.8) |
| kydrive-02-after-appearance-mobile-dark.png | 8 | 8 | 8 | 8 | 7 | 8 | 7.9 | 9,8,7,8,9 | 41/5 (8.2) |
| kydrive-02-after-account-mobile-dark.png | 8 | 8 | 8 | 8 | 7 | 7 | 7.7 | 9,8,7,8,8 | 8/1 (8.0) |

Observations for every dimension are in the JSON `files[].observations`.

## Brand by dimension

| Set | n | Sum | Fraction | Exact decimal | Half-up |
|---|---|---|---|---|---|
| palette_tokens | 40 | 358 | 179/20 | 8.95 | 9.0 | N/A=0
| type_geometry | 40 | 316 | 79/10 | 7.9 | 7.9 | N/A=0
| shell | 20 | 146 | 73/10 | 7.3 | 7.3 | N/A=20
| accent_discipline | 40 | 340 | 17/2 | 8.5 | 8.5 | N/A=0
| suite_cohesion | 40 | 324 | 81/10 | 8.1 | 8.1 | N/A=0

## Findings

- **R3-M1** (share, all 4): Share still has no single filled action: Create link and Done are equal outlines, so three sections compete. Native selects with OS chevrons sit beside custom controls, and "Organisation link" contradicts the en-US "Organization" on the sign-in page. On phone the two-character help sits under Access, away from the person field it describes. Fix: Pick one filled action for the dialog, or keep Done as the filled close. Move the help directly under Find a person on phone. Spell it Organization. Style selects with the shared field/chevron treatment.
- **R3-M2** (trash, all 4): Empty trash is the loudest control on the page (pink fill and red border in light), and the row uses a second danger style (red outline) at ~32px against 40px toolbar buttons. On phone Empty trash comes first at equal width with Back to files. Fix: Use one quiet danger-outline style at 40px for both. Put Back to files first, or make it the quieter text button.
- **R3-M3** (files, folder, trash desktop (6)): The search field stops at ~888px while the list card reaches 1240px. Upload is centred on the subtitle/quota block rather than the h1 row. Folder pages keep the workspace subtitle "Private files for your account". Fix: Make search span the content width, or align it to the toolbar grid. Top-align Upload with the h1.
- **R3-M4** (preview, all 4): The viewer header mixes a text link, a ~32px outlined Download and a ~36px Close. A bordered monospace frame nests inside a near-full-height dialog for three lines. On phone the action row ink starts ~13px right of the title. Fix: Use 40px header buttons, drop the inner border, size the body to content with a max-height, and align ghost-button ink to the title.
- **R3-M5** (authenticated phone views (files, folder, trash, appearance, account; 10)): The horizontal nav scrolls with no overflow cue: items are cut against the divider, and on Appearance "Shared with me" is clipped at the left edge. The "Open files in a new tab" checkbox takes a nav-band row, and the phone header shows no signed-in name or role, which the KyIdentity phone reference does. Fix: Add an edge fade or scroll affordance, move the checkbox into Account/Settings, and show the name/role block in the phone header.
- R3-O1 (appearance/account light (4)): The selected nav row is white on Appearance/Account but accent-soft tinted on My files (visual only; cause unproven, as in round2 R2-O4).
- R3-O2 (account, all 4): Radio-choice cards sit inside section cards (card-in-card), and the small outlined "Off" pill reads like a toggle.
- R3-O3 (login, all 4): Native "Match system" select in the corner, a bordered security-check box nested in the card, and the dark stamp tile merges into the page.
- R3-O4 (files, all 4): Each row carries an outlined icon tile and an outlined "..." button, and the root card header repeats "My files" under the h1.
- R3-O5 (versions mobile (2)): Size is dropped at phone width.
- R3-O6 (desktop shell): Account region reads "Administrator / Administrator" (name equals role).
- R3-N1: The capture worker measured a +1 CSS px Trash SVG box vs breadcrumb offset at 390px. I could not see it at DPR1 and did not score it. Uncorrected and not recaptured.

## Prior objections

- Squeezed shared-dialog Close (round2 R2-M1): Resolved in the pixels: share-mobile Close is square (~40px) beside the two-line title, matching the other dialogs. (share-mobile visible_affordances 6->7; offset by detached help placement (density 8->7))
- No initial indication of the Add-as-role search workflow (R2-M2): Partly resolved: help text "Type at least two characters, then choose Add as reader beside a matching person." is visible. The Add button itself only appears after typing (not captured), and the dialog still has no filled action. (affordance observation only; hierarchy unchanged at 7)
- Phone toolbar ink alignment (R2-M3): At DPR1 the Trash glyph and the breadcrumb/label now look aligned. The measured +1px residual is not visible. Uneven gaps between Trash, refresh and New remain. (not separately scored; density still 7 for the checkbox row and toolbar gaps)

## Drift vs own round 2

- Prior 7.865 → current 7.7825; mean delta -0.0825 (byte-identical n=15: -0.12; pixels changed n=25: -0.06).
- The overall fell 0.0825 on a set where 19 files are byte-identical to history. That is stricter reviewer calibration (mainly trash emphasis and desktop search/list alignment), not a product regression. Visible product changes since round2 (square share Close, person-search help text) are real but did not lift totals because other defects on the same screens remain.
- `kydrive-02-after-trash-desktop-light.png` 7.9 → 7.3 (-0.6): Bytes identical to stage01, so this is reviewer drift, not product change: round2 noted the two danger treatments only under coherence; round3 also counts the tinted Empty trash as the most emphasised control (hierarchy 8->7), the second loud hue (color 8->7) and the search/list width mismatch (density 8->7).
- `kydrive-02-after-trash-mobile-light.png` 7.9 → 7.4 (-0.5): Bytes identical to stage01, so this is reviewer drift: round3 counts the equal-width destructive-first Empty trash pair (hierarchy 8->7), the pink fill plus red outline (color 8->7) and the uncued scrolling nav (affordances 8->7), which round2 left at 8.

## Exclusions and caveats

- stage00 baseline (40): historical; hash-verified, not viewed
- stage01 round2 (40): historical; hash-verified, not viewed (15 are byte-identical to current inputs)
- KyIdentity brand references (4): brand-only; suite_cohesion context; excluded from quality
- No whole-product, competitive or 9/10 acceptance claim.
- Screenshots cannot pass behaviour gates; keyboard, focus, contrast, states and responsive gates remain untested. The capture worker's source/DOM measurements (Close 40x40, aria-describedby help) are their records, not my tests.
- Images were opened individually from the original files at native size, with no contact sheet and no resizing by me. Whether the viewer rescales internally is unproven.
- Hover/source explanations not visible in pixels are treated as unproven.
- Missing workflows: Ordinary OIDC account shell and task workflows (no local staff provisioning); Group-granted shared workspace and populated Shared with me; Euro-Office editing and save; Live SSO, external guests, linked computers and recovery integrations; Share search results / Add as role button after typing (not captured); Admin Settings & DB, Recovery, expanded Administration; Below-fold content on phone captures and desktop Account; Loading, error and confirmation states

Gemini: unused (quota exhausted). MySlop: not posted (known 401). No merge or deploy authorization.
