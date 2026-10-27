# KyDrive UI Council — Opus round 5 (stage04, static)

Reviewer: Opus (claude-opus-5-5), independent, static screenshots only. Rubric `ky-suite-v2` from pinned `authority/` (ky-ui 639e588). Product source `c320daf27d06bfadce82225f615a0323fca9f9a6`; evidence `92f52422ca82ff4fb1d3eebe45dcfc71d7c08cc8`. Machine record: `opus-round5.json`.

## Result

- Quality (40 unique current originals): n=40, sum=1638/5, mean=819/100 = 8.19 → **8.2**.
- Brand fit (separate, mean of unrounded per-file means): n=40, sum=1746/5, mean=873/100 = 8.73 → **8.7**.
- Owner progression (Opus mean ≥ 8.0): **accepts**. The pixel-only counterfactual is 321/40 = 8.025 → 8.0, so it does not depend on my calibration.
- Genuinely satisfied: **true** for this static scope. Behaviour gates are incomplete, and focus fails on the strict rule.

## Blind freeze

Every row (quality and brand vectors, observations, totals) was frozen at 2026-10-27T22:25:39.993592Z as `files_sha256` `199f020e6ee1473cfc90600b3946104600459ac2f7ed622ff2ce5495cb5b19cc`, before any history was opened. Blind viewing began at 2026-10-27T22:21:14Z. The array was not rewritten afterwards.
Before the freeze I viewed the 40 current originals one by one at native size, plus the 4 KyIdentity brand references. The other 202 artifacts were hashed but not viewed. After the freeze I opened only my own `opus-round4.json` and six stage03 originals. The JSON has the full exposure ledger.

## Inventory

246/246 artifacts match their pinned sha256, bytes and dimensions. By role: 40 current (stage04, scored), 160 historical (stage00–03), 40 superseded stage03-initial, 4 KyIdentity brand-only and 2 focus probes. Duplicate exclusion covers only the 40 current images and finds 0 exact and 0 near duplicates (380 pairs).

## Quality by file

| file | cohort | H | T | D | C | A | Co | total | brand mean |
|---|---|---|---|---|---|---|---|---|---|
| login-desktop-light | auth | 9 | 8 | 8 | 9 | 8 | 8 | 8.4 | 17/2 |
| files-desktop-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 9/1 |
| folder-desktop-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 9/1 |
| new-folder-desktop-light | drive | 9 | 8 | 8 | 9 | 9 | 9 | 8.7 | 43/5 |
| preview-desktop-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 42/5 |
| share-desktop-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 43/5 |
| versions-desktop-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 43/5 |
| trash-desktop-light | drive | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9/1 |
| appearance-desktop-light | settings | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9/1 |
| account-desktop-light | settings | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 43/5 |
| login-mobile-light | auth | 9 | 8 | 8 | 9 | 8 | 8 | 8.4 | 17/2 |
| files-mobile-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 9/1 |
| folder-mobile-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 9/1 |
| new-folder-mobile-light | drive | 9 | 8 | 8 | 9 | 9 | 9 | 8.7 | 43/5 |
| preview-mobile-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 42/5 |
| share-mobile-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 43/5 |
| versions-mobile-light | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 43/5 |
| trash-mobile-light | drive | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9/1 |
| appearance-mobile-light | settings | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9/1 |
| account-mobile-light | settings | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 43/5 |
| login-desktop-dark | auth | 9 | 8 | 8 | 8 | 8 | 8 | 8.2 | 17/2 |
| files-desktop-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 9/1 |
| folder-desktop-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 9/1 |
| new-folder-desktop-dark | drive | 9 | 8 | 8 | 9 | 9 | 9 | 8.7 | 43/5 |
| preview-desktop-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 42/5 |
| share-desktop-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 43/5 |
| versions-desktop-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 43/5 |
| trash-desktop-dark | drive | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9/1 |
| appearance-desktop-dark | settings | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9/1 |
| account-desktop-dark | settings | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 43/5 |
| login-mobile-dark | auth | 9 | 8 | 8 | 8 | 8 | 8 | 8.2 | 17/2 |
| files-mobile-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 9/1 |
| folder-mobile-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 9/1 |
| new-folder-mobile-dark | drive | 9 | 8 | 8 | 9 | 9 | 9 | 8.7 | 43/5 |
| preview-mobile-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 42/5 |
| share-mobile-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 43/5 |
| versions-mobile-dark | drive | 8 | 8 | 8 | 9 | 8 | 8 | 8.2 | 43/5 |
| trash-mobile-dark | drive | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9/1 |
| appearance-mobile-dark | settings | 8 | 8 | 8 | 8 | 8 | 8 | 8.0 | 9/1 |
| account-mobile-dark | settings | 8 | 8 | 8 | 8 | 8 | 7 | 7.9 | 43/5 |

## Summaries (coverage, not rankings)

- cohort `auth`: n=4, sum=166/5, mean=83/10 = 8.3 → **8.3**
- cohort `drive`: n=28, sum=1154/5, mean=577/70 = 8.242857142857142857142857143 → **8.2**
- cohort `settings`: n=8, sum=318/5, mean=159/20 = 7.95 → **8.0**
- surface `account`: n=4, sum=158/5, mean=79/10 = 7.9 → **7.9**
- surface `appearance`: n=4, sum=32, mean=8 = 8 → **8.0**
- surface `files`: n=4, sum=164/5, mean=41/5 = 8.2 → **8.2**
- surface `folder`: n=4, sum=164/5, mean=41/5 = 8.2 → **8.2**
- surface `login`: n=4, sum=166/5, mean=83/10 = 8.3 → **8.3**
- surface `new-folder`: n=4, sum=174/5, mean=87/10 = 8.7 → **8.7**
- surface `preview`: n=4, sum=164/5, mean=41/5 = 8.2 → **8.2**
- surface `share`: n=4, sum=164/5, mean=41/5 = 8.2 → **8.2**
- surface `trash`: n=4, sum=32, mean=8 = 8 → **8.0**
- surface `versions`: n=4, sum=164/5, mean=41/5 = 8.2 → **8.2**
- device `desktop`: n=20, sum=819/5, mean=819/100 = 8.19 → **8.2**
- device `mobile`: n=20, sum=819/5, mean=819/100 = 8.19 → **8.2**
- theme `dark`: n=20, sum=818/5, mean=409/50 = 8.18 → **8.2**
- theme `light`: n=20, sum=164, mean=41/5 = 8.2 → **8.2**

Brand by dimension: palette_tokens 9 (N/A 0); type_geometry 8.7 (N/A 0); shell 8.5555 (N/A 4); accent_discipline 8.9 (N/A 0); suite_cohesion 8.5 (N/A 0). Shell is N/A only on signed-out login.

## Drift against my own round 4

Round 4 was 791/100 = 7.91; round 5 is 819/100 = 8.19 (Δ 0.28). All 40 images changed. Dimension changes I credit to visible stage03→04 pixels give 8.025. The remaining 0.165 is my own calibration on content that did not change, and is not product gain.

Files with |Δ| ≥ 0.5:

- `kydrive-04-after-login-desktop-light.png` 7.9 → 8.4: Password/SSO/select outlines are visibly darker (F1 pixels, light color +1). Hierarchy +1 and coherence +1 are calibration: the nested status box (F7) and corner select are unchanged; I now judge them minor.
- `kydrive-04-after-login-mobile-light.png` 7.9 → 8.4: Password/SSO/select outlines are visibly darker (F1 pixels, light color +1). Hierarchy +1 and coherence +1 are calibration: the nested status box (F7) and corner select are unchanged; I now judge them minor.
- `kydrive-04-after-files-mobile-light.png` 7.7 → 8.2: Refresh, row-menu and sign-out buttons are now 40px with the darker field-border outline, and the search outline is darker (F1/F2 pixels: color +1, coherence +1). Phone density +1 is calibration: the two-row header and wrapped toolbar layout are unchanged.
- `kydrive-04-after-folder-mobile-light.png` 7.7 → 8.2: Same visible F1/F2 change as Files (color +1, coherence +1). Phone density +1 is calibration on an unchanged layout.
- `kydrive-04-after-preview-mobile-light.png` 7.6 → 8.2: Only the Download/close outlines changed (0.06% of desktop pixels). Density (+1 desktop, +2 phone) and color +1 are calibration: the bounded mostly-empty body and the ragged phone action row (F6) are pixel-identical; I now treat the bound as intentional and the 25px ragged edge as minor.
- `kydrive-04-after-files-mobile-dark.png` 7.7 → 8.2: Refresh, row-menu and sign-out buttons are now 40px with the darker field-border outline, and the search outline is darker (F1/F2 pixels: color +1, coherence +1). Phone density +1 is calibration: the two-row header and wrapped toolbar layout are unchanged.
- `kydrive-04-after-folder-mobile-dark.png` 7.7 → 8.2: Same visible F1/F2 change as Files (color +1, coherence +1). Phone density +1 is calibration on an unchanged layout.
- `kydrive-04-after-preview-mobile-dark.png` 7.6 → 8.2: Only the Download/close outlines changed (0.06% of desktop pixels). Density (+1 desktop, +2 phone) and color +1 are calibration: the bounded mostly-empty body and the ragged phone action row (F6) are pixel-identical; I now treat the bound as intentional and the 25px ragged edge as minor.

Brand drift 3243/400 → 873/100: Pixels: light field/secondary/icon outlines visibly darker and icon buttons 40px, worth at most +1 on palette_tokens (light) and type_geometry. Calibration: the rest of palette_tokens (light 7->9), suite_cohesion +1 on shell screens after re-viewing the KyIdentity references, and dialog shell moved from N/A to 8 (method change: an obscured shell is now scored with a caveat). Brand never enters quality or progression.

## Findings (smallest material first)

- **R5-1** (minor-material, account Kind choice cards (light)): Unselected 'AI agent' card outline is visibly lighter than the field outlines; parent measured 2.3169:1 light. Native radio indicator contrast is unmeasured. Fix: Give unselected .choice the existing --ky-field-border; measure the painted radio separately.
- **R5-2** (minor, account): 'Files / Open files in a new tab' sits flat above two bordered section cards (prior F5). Fix: Make the sections consistently flat or carded.
- **R5-3** (minor, trash): Danger outlines on Empty trash / Delete permanently are paler than neutral outlines; trash header band is tinted unlike the Files panel header (prior F4). Fix: Use the Files panel header surface; check danger outline against 3:1.
- **R5-4** (minor, files): List panel heading repeats the h1 'My files' (prior F3). Fix: Drop the panel label when it equals the page title.
- **R5-5** (minor, preview phone): Open in new tab/Download row ends ~25px short of the Close edge (prior F6). Bounded size accepted as intentional. Fix: Stretch or right-align the phone action row.
- **R5-6** (minor, login): 'Security check verified.' is a bordered box inside the bordered card; dark stamp tile merges into the page (prior F7 in part). Fix: Render the status as an inline line.
- **R5-7** (minor, appearance): Unselected native radios are low-emphasis (grey discs in dark); each swatch carries its own border inside the card. Fix: None required for progression; optional.
- **R5-8** (minor, files/folder/trash header): Storage meter at 350 B of 10 GiB reads as a faint divider. Fix: Optional; no scope added.
- **R5-9** (behaviour (unscored), phone keyboard focus): Appearance outer focus ring 0.1875px residual per parent provenance. Fix: Parent's call; gate remains fail on strict rule.

## Gates

- `keyboard`: **untested**. No task-path keyboard run in this static review.
- `focus`: **fail**. Parent capture provenance at c320daf: limited phone sample 10/10 destinations fit, 8/10 outer rings fit; Appearance outer ring clipped 0.1875 CSS px in both schemes. Strict zero-tolerance gate fails. Not reproduced here; off-image, not counted in PNG quality.
- `contrast`: **untested**. No complete computed-style run. Parent sampled Busnes search/Share fields/secondary/icon boundaries >=3:1 (min 3.38:1) at both sizes; unselected Account choice-card boundary 2.3169:1 light / 3.4804:1 dark; native UA radio indicator UNMEASURED. Sampled controls do not establish the gate.
- `states`: **untested**. Only initial empty/sparse/populated views; loading and error states not pictured.
- `responsive`: **untested**. Manifest overflow_px 0 at 390/1280; target usability and reachability not established from screenshots.

Complete behaviour gates are not established; focus fails on the strict rule. No 9/10, whole-product, competitive or all-theme acceptance.

## Scope limits

- Static initial views of the local administrator plus signed-out login only
- Ordinary OIDC, group-shared, editor, SSO, device and recovery flows absent/unscored
- No keyboard/focus/contrast/states/responsive gate claim
- Not whole-product, competitive, all-theme or 9/10 acceptance
- No clean/hermetic build claim
- Gemini: quota exhausted, not called. MySlop: not posted (forbidden by the brief). No merge or deploy.
