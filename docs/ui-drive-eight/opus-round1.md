# KyDrive drive-eight — Opus round1 review (stage 00-before baseline)

⚠️ DEGRADED: single-context (the brief forbids extra agents and writes outside this report pair, so Impeccable's detector, overlay and persistence were not run). Its design-review lens was applied inline.

## Reviewer, rubric, sources

- Reviewer: **Claude Opus 5.5** (`claude-opus-5-5`), Opus primary/progression reviewer. Independent of Sol; no sub-agents.
- Rubric: `ky-suite-v2`, schema `ky-ui-council-scores/2`. Weights: hierarchy .20, typography .15, density_layout .20, color .15, visible_affordances .15, coherence .15. File totals use Decimal half-up 0.1. The covered-set mean is exact over the 40 rounded totals, then half-up 0.1. Hidden or missing content is N/A, never 0.
- Authority: `authority.json` `082c11359c5c01973f86f4e01579fc3ac4f5b256715d4c2179f69cd4527427ec`, ky-ui `639e5882cada099dd189197beb2a3e3b4c188658`. All 5 local `authority/` objects match the manifest and `git show 639e5882:<path>`. The incidental ky-ui HEAD `947bf969` was not used.
- Runtime vendor `web/src/ky-ui/VERSION` 0.4.1: all 12 hashes match. `tokens.css` equals the authority. `navigation.css` is `cc6dbaa0…` against the authority's `fbd0aea3…`, a recorded, legitimate navigation difference.
- Product source `fd472e8035222ae38d8bb4b5c5cbd79c066879a1`. Frozen evidence `0354de843b0503b9ff52b51318ab214867b9e0e8` on branch `ui/drive-eight`. Outside this directory the evidence commits change only `docs/AGENTS.md` (+2). The worktree was clean.
- Pinned typeface: Space Grotesk, observed throughout and preserved.

### Input hashes and verification

- `inventory-v1.json` `6c142d3d8f10914011763c38fb4b60fc794afd1c7f422fd60c7b7e423ab9781d`
- `manifest-00-before.json` `6baeb16a621f473f83635a4cac1038c6b0a623fb0246c369051dcea37dee0957`
- `artifacts.json` `1d91caad72e783de2bc361448b70a34ce80a4ef4070a4309f76a98c42cf0dfe6`
- `brand-references.json` `254f93a4754dc75f97f361365342e1632c719cb389fe1fe9bd40bf6a581ec3cb`
- `near-duplicate-audit-v1.json` `79e48e74369b87348a72fa7900d1d0e47d7febab8802bae17a9c01a4e14f7fd1`
- `review-inputs-v1.json` `c0543a876d7c8fc3fea03e79051cbaa333953aa10e155194dbd78d490aab0b4b`
- `baseline-setup.md` `db470512c6d84167c11757708d07594133b722931634b52cd411a3aabed5d22d`
- `source-provenance.json` `c07cbd5f8c34271e0c821333e36516af89c8404cb8e60a8bc72f28e5801aa75d`
- `SHA256SUMS` `b50345b5cb09f38e6b963e31d9f2facbdc636ea80fba4162d3bffe86ae3543e3`
- `duplicate-audit-00-before.json` `383e7cccfdaaa061b3e4d2dceb0a44069c253aa814872cc63d18b7ea137452de`
- `inventory-00-before.csv` `bc83ff7ad715ea3615b59285e8d9376e1a5d865b781933c5d171d1c2bb9a986a`
- `fixture-files.json` `8718d28ebbb8ac0bb6419e639222e693a7a6485e039402037c19ffbb54cf020b`
- `AGENTS.md` `7e8913643d4e30e6ccf839253e8c7cc440659bc10e76b96f45462da229acb38a`

- SHA256SUMS: 50/50 entries match the committed bytes
- csv_line_endings: inventory-00-before.csv has 41 CRLF and 0 bare LF lines (.gitattributes -text)
- inventory_vs_manifest: 40 file→sha256 pairs identical; all bytes and dimensions match on disk
- artifacts: 44 entries match (40 quality originals = inventory, plus 4 brand references)
- brand_references: 4/4 match brand-references.json and git show d7f2a4b6:<source_file> in KyIdentity-server
- exact_duplicates: 40 unique SHA-256
- near_duplicates: Reproduced in memory (Pillow/NumPy RGBA, same-size pairs): 380 comparisons, every changed_fraction and max_channel_delta identical to the audit, 0 near duplicates (minimum changed fraction 0.0243)
- bundled_scripts_not_run: verify-baseline.py and audit-near-duplicates.py rewrite tracked files, so their checks were reproduced read-only instead

## Capture conditions

Playwright 1.64.1, headless Chromium 156.0.8078.4, DPR 1, desktop 1280x900, mobile 390x844 (desktop Chromium at mobile CSS size, not a device claim). OS light/dark emulation; fresh context per viewport/scheme; ky_theme absent (data-theme busnes-light/busnes-dark). Service workers: normal; /sw.js controller active on all 40. en-US, America/New_York. Initial view only (full_page false), overflow_px 0 on all 40. 2026-10-27T19:44:58Z to 19:45:29Z, single run, no recapture.

Disposable SQLite. The actual local recovery administrator after a real bootstrap password change. Synthetic files only (Project materials folder; Quarterly plan.md, Supplier list.csv, Meeting notes.txt live; Archive note.txt trashed). Share and new-folder forms unsubmitted.

T3 native browser prepared and inspected the fixture (Chrome 154, DPR 2 reconnaissance, outside the batch). The parent snapshot timed out twice, so the documented Playwright workflow produced the DPR 1 batch.

**Scope.** Every authenticated capture shows the actual local recovery **administrator**. Ordinary staff need external OIDC, and no ordinary account, granted shared workspace or editor is configured. Nothing here describes ordinary-user navigation, and missing features are not treated as absent.

## Inventory

44 source images: 40 quality originals (all unique, 0 exact and 0 near duplicates) plus 4 unscored KyIdentity brand-only references:

- `brand-references/kyidentity-02-after-users-manage-desktop-light.png` `f13eb7bdc897cf71…` — KyIdentity brand-only reference; excluded from quality
- `brand-references/kyidentity-02-after-users-manage-desktop-dark.png` `c88074992215e2e1…` — KyIdentity brand-only reference; excluded from quality
- `brand-references/kyidentity-02-after-appearance-mobile-light.png` `b82cb35eae6ead72…` — KyIdentity brand-only reference; excluded from quality
- `brand-references/kyidentity-02-after-appearance-mobile-dark.png` `ea7033aea61cc689…` — KyIdentity brand-only reference; excluded from quality

Missing / unscored surfaces:

- Ordinary account shell and task workflows (no real local staff provisioning boundary; staff require external OIDC)
- Group-granted shared workspace and populated Shared with me (no directory/groups/second account)
- Euro-Office editing and save (not configured)
- Live SSO, external guest collaboration, linked computers and recovery integrations (not configured)
- Admin Settings & DB and Recovery pages (outside the capture batch)
- Below-viewport content on mobile files/folder/Appearance/Account and desktop Account (initial-view captures only)

Unavailable workflows:

- gemini: unused: advisory only after both primaries are genuinely satisfied; quota known unavailable, not retried
- myslop: no board post (known HTTP 401; not retried, per brief)
- live_review: not performed (optional, and must not alter fixture state)
- impeccable_detector: not run (brief limits tools and writes; scoring is screenshot-based)
- sol_peer: not read (independent)

## Blind freeze

- Frozen at **2026-10-27T20:03:11.333372Z**, before any historical, peer or acceptance material was read.
- `files` SHA-256 **`d4b1745a41634e1b90dbaa3c138e31a7a3e68c7b7e4a737302148c4c456355da`**. Canonicalization: `sha256(json.dumps(files, sort_keys=True, separators=(',',':'), ensure_ascii=False).encode('utf-8'))`.
- The published `files` array was re-hashed against the freeze before publication and matches.

## Files (40)

Each row identifies the actual product, surface and state from the image, not the filename (all 40 match). Per-dimension observations are in the JSON.

| # | File | Cohort | Surface / state | H | T | D | C | A | Co | Total | ky_fit |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | login-desktop-light | auth | Sign-in / empty | 8 | 8 | 8 | 8 | 7 | 7 | **7.7** | 8.0 |
| 2 | files-desktop-light | drive | My files list / sparse | 6 | 8 | 6 | 7 | 7 | 6 | **6.6** | 6.8 |
| 3 | folder-desktop-light | drive | Empty folder (breadcrumb) / empty | 6 | 8 | 6 | 6 | 7 | 6 | **6.5** | 6.6 |
| 4 | new-folder-desktop-light | drive | New folder dialog / sparse | 8 | 8 | 8 | 8 | 8 | 8 | **8.0** | 8.3 |
| 5 | preview-desktop-light | drive | Text file quick-look dialog / sparse | 8 | 8 | 7 | 8 | 7 | 7 | **7.5** | 8.0 |
| 6 | share-desktop-light | drive | Share dialog / empty | 7 | 8 | 8 | 8 | 7 | 8 | **7.7** | 8.3 |
| 7 | versions-desktop-light | drive | Version history dialog / sparse | 8 | 8 | 8 | 7 | 8 | 8 | **7.9** | 7.8 |
| 8 | trash-desktop-light | drive | Trash view / sparse | 6 | 7 | 6 | 8 | 7 | 6 | **6.6** | 7.2 |
| 9 | appearance-desktop-light | settings | Appearance theme picker / populated | 8 | 8 | 8 | 8 | 8 | 8 | **8.0** | 8.2 |
| 10 | account-desktop-light | settings | Account security and app passwords / empty | 6 | 7 | 7 | 6 | 6 | 6 | **6.4** | 6.6 |
| 11 | login-mobile-light | auth | Sign-in / empty | 8 | 8 | 8 | 8 | 7 | 7 | **7.7** | 8.0 |
| 12 | files-mobile-light | drive | My files list / sparse | 5 | 7 | 5 | 7 | 7 | 6 | **6.1** | 6.8 |
| 13 | folder-mobile-light | drive | Empty folder (breadcrumb) / empty | 5 | 7 | 5 | 7 | 7 | 6 | **6.1** | 7.0 |
| 14 | new-folder-mobile-light | drive | New folder dialog / sparse | 8 | 8 | 8 | 8 | 8 | 8 | **8.0** | 8.3 |
| 15 | preview-mobile-light | drive | Text file quick-look dialog / sparse | 7 | 8 | 7 | 8 | 7 | 7 | **7.3** | 8.0 |
| 16 | share-mobile-light | drive | Share dialog / empty | 7 | 8 | 7 | 8 | 6 | 7 | **7.2** | 8.0 |
| 17 | versions-mobile-light | drive | Version history dialog / sparse | 8 | 8 | 8 | 7 | 8 | 8 | **7.9** | 7.8 |
| 18 | trash-mobile-light | drive | Trash view / sparse | 6 | 7 | 6 | 8 | 7 | 5 | **6.5** | 7.0 |
| 19 | appearance-mobile-light | settings | Appearance theme picker / populated | 8 | 8 | 8 | 8 | 8 | 7 | **7.9** | 8.2 |
| 20 | account-mobile-light | settings | Account security and app passwords / empty | 7 | 8 | 7 | 7 | 7 | 7 | **7.2** | 7.0 |
| 21 | login-desktop-dark | auth | Sign-in / empty | 8 | 8 | 8 | 8 | 7 | 7 | **7.7** | 8.0 |
| 22 | files-desktop-dark | drive | My files list / sparse | 6 | 8 | 6 | 7 | 7 | 6 | **6.6** | 6.8 |
| 23 | folder-desktop-dark | drive | Empty folder (breadcrumb) / empty | 6 | 8 | 6 | 6 | 7 | 6 | **6.5** | 6.6 |
| 24 | new-folder-desktop-dark | drive | New folder dialog / sparse | 8 | 8 | 8 | 8 | 8 | 8 | **8.0** | 8.3 |
| 25 | preview-desktop-dark | drive | Text file quick-look dialog / sparse | 8 | 8 | 7 | 8 | 7 | 7 | **7.5** | 8.0 |
| 26 | share-desktop-dark | drive | Share dialog / empty | 7 | 8 | 8 | 8 | 7 | 8 | **7.7** | 8.3 |
| 27 | versions-desktop-dark | drive | Version history dialog / sparse | 8 | 8 | 8 | 7 | 8 | 8 | **7.9** | 7.8 |
| 28 | trash-desktop-dark | drive | Trash view / sparse | 6 | 7 | 6 | 8 | 7 | 6 | **6.6** | 7.2 |
| 29 | appearance-desktop-dark | settings | Appearance theme picker / populated | 8 | 8 | 8 | 8 | 7 | 8 | **7.9** | 8.2 |
| 30 | account-desktop-dark | settings | Account security and app passwords / empty | 6 | 7 | 7 | 6 | 6 | 6 | **6.4** | 6.6 |
| 31 | login-mobile-dark | auth | Sign-in / empty | 8 | 8 | 8 | 8 | 7 | 7 | **7.7** | 8.0 |
| 32 | files-mobile-dark | drive | My files list / sparse | 5 | 7 | 5 | 7 | 7 | 6 | **6.1** | 6.8 |
| 33 | folder-mobile-dark | drive | Empty folder (breadcrumb) / empty | 5 | 7 | 5 | 7 | 7 | 6 | **6.1** | 7.0 |
| 34 | new-folder-mobile-dark | drive | New folder dialog / sparse | 8 | 8 | 8 | 8 | 8 | 8 | **8.0** | 8.3 |
| 35 | preview-mobile-dark | drive | Text file quick-look dialog / sparse | 7 | 8 | 7 | 8 | 7 | 7 | **7.3** | 8.0 |
| 36 | share-mobile-dark | drive | Share dialog / empty | 7 | 8 | 7 | 8 | 6 | 7 | **7.2** | 8.0 |
| 37 | versions-mobile-dark | drive | Version history dialog / sparse | 8 | 8 | 8 | 7 | 8 | 8 | **7.9** | 7.8 |
| 38 | trash-mobile-dark | drive | Trash view / sparse | 6 | 7 | 6 | 8 | 7 | 5 | **6.5** | 7.0 |
| 39 | appearance-mobile-dark | settings | Appearance theme picker / populated | 8 | 8 | 8 | 8 | 7 | 7 | **7.7** | 8.2 |
| 40 | account-mobile-dark | settings | Account security and app passwords / empty | 7 | 8 | 7 | 7 | 7 | 7 | **7.2** | 7.0 |

## Cohort summaries (covered-set summaries, not rankings)

| Cohort | Surfaces | n | Sum | Fraction | Exact | Half-up 0.1 |
|---|---|---|---|---|---|---|
| drive | files, folder, new-folder, preview, share, versions, trash | 28 | 199.8 | 999/140 | 7.135714285714285714285714286 | **7.1** |
| auth | login | 4 | 30.8 | 77/10 | 7.7 | **7.7** |
| settings | Appearance, Account | 8 | 58.7 | 587/80 | 7.3375 | **7.3** |
| **overall covered set** | all 40 | 40 | 289.3 | 2893/400 | 7.2325 | **7.2** |

Descriptive surface means (Opus covered set, not rankings):

| Surface | Cohort | n | Sum | Exact | Half-up |
|---|---|---|---|---|---|
| login | auth | 4 | 30.8 | 7.7 | 7.7 |
| files | drive | 4 | 25.4 | 6.35 | 6.4 |
| folder | drive | 4 | 25.2 | 6.3 | 6.3 |
| new-folder | drive | 4 | 32.0 | 8.0 | 8.0 |
| preview | drive | 4 | 29.6 | 7.4 | 7.4 |
| share | drive | 4 | 29.8 | 7.45 | 7.5 |
| versions | drive | 4 | 31.6 | 7.9 | 7.9 |
| trash | drive | 4 | 26.2 | 6.55 | 6.6 |
| appearance | settings | 4 | 31.5 | 7.875 | 7.9 |
| account | settings | 4 | 27.2 | 6.8 | 6.8 |

The means cover only the captured administrator set. They are not cross-cohort ranks or whole-product ratings.

## Ky brand fit (separate from quality)

Per file: equal weight over non-N/A dimensions (shell N/A where the shell is absent or hidden by a modal). Summary: mean of per-file exact means. Separate from quality; never added to totals or used for progression. Four verified KyIdentity brand-only captures (brand-references.json, KyIdentity-server d7f2a4b67a50c6d36829c3653a8ad5b2eb40e517), used for suite_cohesion context only; suite_cohesion therefore scored (two Ky products).

- Overall (mean of per-file means, n=40): 7.5825 → **7.6**
- drive: n=28, 7.546428571428571428571428571 → 7.5
- auth: n=4, 8 → 8.0
- settings: n=8, 7.5 → 7.5

| Dimension | n | N/A | Sum | Mean | Half-up |
|---|---|---|---|---|---|
| palette_tokens | 40 | 0 | 340 | 8.5 | 8.5 |
| type_geometry | 40 | 0 | 298 | 7.45 | 7.5 |
| shell | 20 | 20 | 140 | 7 | 7.0 |
| accent_discipline | 40 | 0 | 294 | 7.35 | 7.4 |
| suite_cohesion | 40 | 0 | 284 | 7.1 | 7.1 |

`shell` is N/A on 20 files: the 4 login captures have no shell, and the 16 modal captures hide it behind the product backdrop. Strongest fit: Appearance mirrors the KyIdentity references closely. Weakest: the drive list views (admin accordion stack, in-card toolbar, decorative orange folder tile) and desktop Account (centered column, accent overuse).

## Gates

- `keyboard`: **untested**. No keyboard traversal performed or recorded; screenshots cannot show order, traps or focus return.
- `focus`: **untested**. Only the autofocused Username (login) and focused Folder name (dialog) rings are visible; no per-element focus sweep in both themes.
- `contrast`: **untested**. No computed-style measurement; visual impressions only (e.g. faint quota meter, faded disabled button).
- `states`: **untested**. Empty folder, empty share list, no app passwords captured; loading and error states not captured.
- `responsive`: **untested**. Manifest records overflow_px 0 at 390x844 and 1280x900 for all 40, but navigation reachability and target usability were not exercised.
- `role_security_integration`: **untested**. All authenticated captures are the local recovery administrator; ordinary-staff role, SSO, editor and recovery integrations not exercised.

Screenshots cannot pass any gate. Every fully unexercised gate is untested.

## Findings and smallest fixes

Design verdict: the shell, dialogs and Appearance are clearly authored in the Busnes language and sit close to KyIdentity. The drive list views read as an admin console layered on a file list, and that one structural choice drives most of the gap.

### Material (blocking 8.0 on this evidence)

- **M1** (files/folder/trash, all 12): Administrator Operations & audit / Create shared workspace / Leavers accordions sit above search and the file list in every drive view; on 390px they push the list or empty state below the fold (two rows visible). *Fix:* Move the three admin panels to one administrator destination (a nav item or the existing Settings & DB page) so drive views open on search + list. Keep the contract: administrators only.
- **M2** (files/folder/trash, all 12): The page h1 stays 'My files' in a folder and in Trash, while the list panel repeats 'My files' or carries a small 'Trash' heading; Upload sits in the in-card toolbar rather than the page header. *Fix:* Make the h1 the current location (workspace, Trash) with the breadcrumb under it; drop the duplicate panel title; put the single primary at the page header's right.
- **M3** (account-desktop-light/dark (and mobile below fold)): Disabled 'Create app password' renders as a faded primary; with the enabled 2FA button that is two accent-filled buttons on the view. The centered narrow column starts at x≈384 instead of the shared x≈280 content edge, and 'In use' carries a stray dashed rule. *Fix:* Give disabled buttons a neutral, distinct style; keep one primary per view (make the 2FA enable the primary or a secondary); left-align .page-narrow to the content edge; remove the dashed rule.
- **M4** (folder-desktop-light/dark): The empty folder shows two identical filled 'Upload files' buttons (toolbar and empty-state CTA). *Fix:* Make one of them secondary. The empty-state CTA can be the only filled one when the folder is empty.
- **M5** (trash, all 4 (most visible on mobile)): The Trash panel's tinted header band clips the card's top border and rounded corners. *Fix:* Apply the panel radius/overflow to the header band (or drop the tint) so the card border is continuous.
- **M6** (files (folder icon), versions (Current badge), files/folder (checkbox)): Accent appears outside action/selection/focus: an orange folder icon tile and an orange 'Current' status pill. *Fix:* Neutral ink for folder icons; use a neutral or success-tinted pill for 'Current', as KyIdentity does for status.

### Optional

- **O1**: Quota meter fill is invisible at 350 B of 10 GiB; the bar reads as an empty rule. *Fix:* Give the fill a visible minimum width or drop the bar in favour of the text.
- **O2**: Login: the native theme select floats at the top right; the 'Security check verified.' box is styled like an input; the stamp icon's dark tile vanishes on the dark page. *Fix:* Quiet inline status (no field border); keep the select but align it with page chrome; give the dark stamp a visible tile or border.
- **O3**: Preview: a bordered text frame nested in the dialog body; Download ~32px vs Close ~36px; on mobile Close drops to the action row. *Fix:* Render the text directly on the dialog body; use 40px controls; keep Close in the title row.
- **O4**: Share: 'Done' is the filled primary while the real action, Create link, is secondary; on mobile the header Close is squeezed narrow. *Fix:* Make Done secondary/neutral; give Close a fixed 40px square with flex-shrink 0.
- **O5**: In dark Appearance/Account, unselected radios render as filled grey dots. *Fix:* Use an outlined, unfilled ring for unselected radios in dark themes.
- **O6**: Light theme selected nav uses accent-soft on My files but white on Appearance/Account; the account region repeats 'Administrator'; on phones the new-tab checkbox takes a header row on every page. *Fix:* Use one selected treatment; show username + role; move the preference to Account.

What works: the dialog vocabulary (New folder, Versions, Share) is consistent, with titled header, body, footer and one 40px control style. The empty-folder state gives guidance and a next action. Danger styling in Trash is semantic and confirms intent. Appearance matches the shared picker. Light and dark both hold their own contrast at a glance.

## Progression and satisfaction

- Rule: Opus current overall, rounded half-up to 0.1, >= 8.0 accepts; not averaged with Sol or history; cohort shortfalls do not veto.
- Opus overall covered set: 289.3/40 = 7.2325 → **7.2**. Meets 8.0: **false**. The 9/10 longer-term target: unproven.
- Drift: N/A (baseline).
- **satisfied: false · genuinely_satisfied: false**. Opus covered-set overall is 7.2325, which rounds half-up to 7.2, below the 8.0 progression line. The shortfall comes mostly from M1-M3, which repeat across the 12 drive list views and the Account page. Dialogs, Appearance and login are already near 7.7-8.0. No target is forced: 9/10 remains unproven, and all behaviour gates are untested.
- Gemini: unused (advisory only after both primaries are genuinely satisfied; quota unavailable, not retried). MySlop: no board post (known HTTP 401, not retried).

## Blind-exposure audit

- Read before freeze: AGENTS chain: suite root, product root (worktree), docs/, web/, docs/ui-drive-eight/
- Read before freeze: council skill; impeccable SKILL.md and reference/critique.md; impeccable context output (no PRODUCT/DESIGN)
- Read before freeze: pinned authority: ky-suite-ui SKILL.md, rubric.md, design-contract.md (its 'Historical baseline notes' describe other products' pre-redesign source structure; no scores), tokens.css grep
- Read before freeze: review inputs: authority.json, review-inputs-v1.json, baseline-setup.md, inventory-v1.json, manifest-00-before.json, artifacts.json (first entries), brand-references.json, near-duplicate-audit/duplicate-audit/source-provenance/fixture-files structure, SHA256SUMS, inventory CSV header and first rows (capture-inspection notes, no scores)
- Read before freeze: 40 originals and 4 brand references, viewed individually
- Not read: No previous product review, peer (Sol) output, competitive ranking, PR, acceptance/delivery record, HANDOFF, STATUS, COUNCIL, workbook, CSV score export, gallery, ky-ui/reviews or other apps' scores, before or after the freeze. The ui-fleet-2026-10-26 and ui-visual-refinement-2026-10-26 evidence directories were not opened.
- Post-freeze reads: manifest capture fields only (overflow, dpr, theme, service worker, user agent, fixture). Exposure: **none**.
- Peer files: After both report files were written, a final git status listed peer filenames docs/ui-drive-eight/sol-round1.json and sol-round1.md. Only the names were seen; neither file was opened, read or hashed.

Published files: `docs/ui-drive-eight/opus-round1.json`, `docs/ui-drive-eight/opus-round1.md`. No product, shared metadata or AGENTS edits; no commits, builds, tests, recapture or service operations.
