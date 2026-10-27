# Round 2 spread evidence audit

**18 qualifying quality entries across 14 stage01 originals; zero qualifying brand entries.** Every qualifying quality spread is exactly 2: coherence 14, hierarchy 2, visible_affordances 2; typography, density_layout and color 0. This reproduces the parent's 18-entry count across the same 40 images.

Auditor: GPT-6.1 Sol, high reasoning, Codex/T3, 2026-10-27. Method: single-context post-publication evidence audit, with peer scores and parent objections intentionally known; not a blind review or third score. No scores, publisher bytes, satisfaction flags or adjusted means are produced or changed.

## Scope and unchanged decision

- Quality comparison: all 40 matching unique stage01 originals × six dimensions = **240 comparisons**. The qualifying set is **14 images**, not 18 images; four Share images each contribute two entries.
- Brand comparison: 40 × five possible dimensions = 200 slots; **180 numeric comparisons**, plus 20 shared shell N/As. Absolute differences: 140 zero, 40 one, **none ≥2**. Missing shell is excluded, never scored zero.
- Authoritative product source: `7dd862e4fab842c234e828754c101a56ebab322f`, inspected only through `git show`. Evidence revision: `bc34c4669bcc15d51f0384a0934a91df66ad4802`. Current UI/code and stage02/R3 are outside this judgment.
- Authority: [authority.json](authority.json), pinned ky-ui `639e5882cada099dd189197beb2a3e3b4c188658`, [ky-suite-v2 rubric](authority/skills/ky-suite-ui/references/rubric.md) and [design contract](authority/skills/ky-suite-ui/references/design-contract.md). All five pinned authority-object hashes match.
- **Opus quality stays 314.6 / 40 = 7.865 → 7.9 half-up; progression remains below 8.0.** Opus `satisfied=false`, `genuinely_satisfied=false`; Sol `satisfied=true`, `genuinely_satisfied=true`, quality 340.2 / 40 = 8.505 → 8.5. Never pool reviewers, history or brand; individual cohort scores do not veto the owner's overall rule. These are covered-set summaries, not rankings or whole-product acceptance. No forced target or achieved 9/10 is asserted.
- This audit resolves evidentiary claims where possible and identifies residual taste/unknowns. It does not compel either reviewer to amend their frozen judgment.

## Every qualifying original, individually reopened

All 14 originals below were separately opened with `view_image(detail="original")`: desktop **1280×900**, phone **390×844**, without resizing, crops or contact sheets. Full SHA-256 values appear below. Quality vector order is **hierarchy, typography, density_layout, color, visible_affordances, coherence**. Vectors are verbatim published values.

Every row is limited to visible initial-viewport evidence. Operation, keyboard, focus coverage, hit-area behavior, confirmations, permissions, security, integrations and measured contrast remain unknown unless explicitly described as source evidence.

| Original | Opus quality vector | Sol quality vector | All quality dimensions with spread ≥2 | Visible facts and resolution |
| --- | --- | --- | --- | --- |
| [login-desktop-light](screenshots/kydrive-01-after-login-desktop-light.png) | `[8, 8, 8, 8, 8, 7]` | `[9, 9, 8, 9, 9, 9]` | coherence 7↔9 | Visible: centered stamp/title, nested bordered verified-status row, outlined SSO and detached native theme select. Resolution: nested framing is real; whether these common control types lower coherence to 7 rather than 9 is taste. The apparent username/password are placeholders (see source check below), not evidence of entered credentials. |
| [files-desktop-light](screenshots/kydrive-01-after-files-desktop-light.png) | `[8, 8, 8, 8, 8, 7]` | `[8, 8, 8, 9, 8, 9]` | coherence 7↔9 | Visible: My files repeats as h1 and root breadcrumb; each of four rows has an outlined neutral icon tile and menu button; Administration is collapsed below the list. Resolution: repetition/framing are factual, but the breadcrumb and row menus have task roles. Box-heavy versus coherent is taste, not a demonstrated broken task. |
| [share-desktop-light](screenshots/kydrive-01-after-share-desktop-light.png) | `[7, 8, 8, 8, 7, 7]` | `[9, 8, 9, 9, 8, 9]` | hierarchy 7↔9; coherence 7↔9 | Visible: Find a person is empty, Access is Reader, no result/Add instruction is shown, and Create link/Done are outlines inside the dialog. Resolution: no filled foreground action and weak initial guidance are supported. Missing sharing functionality is not established; native selects beside styled inputs are not by themselves inconsistent controls. |
| [trash-desktop-light](screenshots/kydrive-01-after-trash-desktop-light.png) | `[8, 8, 8, 8, 8, 7]` | `[8, 8, 8, 8, 8, 9]` | coherence 7↔9 | Visible: Empty trash has a pale danger tint, while Delete permanently is a danger outline over the white row; header and row controls have different heights. Resolution: the captured tint difference is real. Both actions use the same quiet-danger class; a permanent two-style defect is unproven, and severity remains taste. |
| [login-mobile-light](screenshots/kydrive-01-after-login-mobile-light.png) | `[8, 8, 8, 8, 8, 7]` | `[9, 9, 8, 9, 9, 9]` | coherence 7↔9 | Visible: single-column fields/primary/SSO fit at 390px; verified status is separately bordered inside the card and theme select remains above it. Resolution: framing and detached select support the stated coherence concern; a two-point severity choice is taste, not a visible usability failure. |
| [share-mobile-light](screenshots/kydrive-01-after-share-mobile-light.png) | `[7, 8, 8, 8, 6, 7]` | `[8, 8, 8, 9, 8, 9]` | visible_affordances 6↔8; coherence 7↔9 | Visible: Close outline spans approximately x331–354 (24px) by 36px; X remains visible. The h2 is one line and the description wraps to two. Empty search has no Add instruction/result; Create link and full-width Done are outlines. Resolution: compressed Close and guidance gap are supported; a two-line title and absent sharing operation are overstatements. |
| [trash-mobile-light](screenshots/kydrive-01-after-trash-mobile-light.png) | `[8, 8, 8, 8, 8, 7]` | `[8, 8, 8, 8, 8, 9]` | coherence 7↔9 | Visible: Empty trash is an outline over the page-colored header, while Delete permanently is an outline over the white row; no separate pink fill is visible in Empty trash. Resolution: the all-four-images tinted-fill claim is overbroad. Header/row sizing and underlying surfaces differ, but their shared danger vocabulary is consistent; penalty magnitude is taste. |
| [login-desktop-dark](screenshots/kydrive-01-after-login-desktop-dark.png) | `[8, 8, 8, 8, 8, 7]` | `[9, 9, 8, 9, 9, 9]` | coherence 7↔9 | Visible: charcoal card, nested verified-status border, peach Sign In and outlined SSO; dark stamp tile merges into the page, leaving the light glyph prominent. Resolution: framing/tile observations are supported; consistency versus excessive enclosure remains taste. No measured contrast or sign-in claim follows. |
| [files-desktop-dark](screenshots/kydrive-01-after-files-desktop-dark.png) | `[8, 8, 8, 8, 8, 7]` | `[8, 8, 8, 9, 8, 9]` | coherence 7↔9 | Visible: repeated My files heading/breadcrumb, four neutral icon tiles with outlined row menus, divided charcoal list, one sidebar and lower Administration disclosure. Resolution: the repetition and outlines are real; whether they justify coherence 7 versus 9 is taste. No content/action clipping is visible. |
| [share-desktop-dark](screenshots/kydrive-01-after-share-desktop-dark.png) | `[7, 8, 8, 8, 7, 7]` | `[9, 8, 9, 9, 8, 9]` | hierarchy 7↔9; coherence 7↔9 | Visible: clearly separated people/Shared with/Organisation link sections; empty person field, Reader/expiry selects and outlined Create link/Done. Resolution: no filled foreground action and missing search-to-add guidance are factual. Clear grouping also supports Sol's reading; a new Share submit operation is not justified by this empty capture. |
| [trash-desktop-dark](screenshots/kydrive-01-after-trash-desktop-dark.png) | `[8, 8, 8, 8, 8, 7]` | `[8, 8, 8, 8, 8, 9]` | coherence 7↔9 | Visible: Empty trash has a muted danger tint against the dark header; Delete permanently is outlined over the charcoal row. Resolution: the visible state difference survives the theme change. Identical quiet-danger classes and hover styling prevent calling this proven permanent component inconsistency; destructive operation/confirmation remain unknown. |
| [login-mobile-dark](screenshots/kydrive-01-after-login-mobile-dark.png) | `[8, 8, 8, 8, 8, 7]` | `[9, 9, 8, 9, 9, 9]` | coherence 7↔9 | Visible: fields and both sign-in choices fit; nested verification border and detached theme select remain; dark stamp tile is not distinct from the page. Resolution: these visible composition details explain Opus's concern while Sol's consistent vocabulary remains defensible. The exact coherence penalty is taste; behavior/contrast are unknown. |
| [share-mobile-dark](screenshots/kydrive-01-after-share-mobile-dark.png) | `[7, 8, 8, 8, 6, 7]` | `[8, 8, 8, 9, 8, 9]` | visible_affordances 6↔8; coherence 7↔9 | Visible: approximately 24×36px Close outline and legible X beside a one-line h2/two-line description; empty search, selects and outlined Create link/Done all fit. Resolution: narrow Close is independently supported in dark; missing initial Add instructions are factual. No screenshot proves Close failure or lack of a working Add operation. |
| [trash-mobile-dark](screenshots/kydrive-01-after-trash-mobile-dark.png) | `[8, 8, 8, 8, 8, 7]` | `[8, 8, 8, 8, 8, 9]` | coherence 7↔9 | Visible: Empty trash is a quiet outline on page-dark header; Delete permanently is a quiet outline on the lighter charcoal row, with no extra tint in Empty trash. Resolution: two underlying surfaces and 40px/32px context sizes are not two distinct danger classes. Generalizing the desktop tint to this original is off-base; coherence severity stays taste. |

## Brand vectors on the qualifying images

Brand is separate from quality. Order: **palette_tokens, type_geometry, shell, accent_discipline, suite_cohesion**. N/A is the publisher's null shell value. Neither raw vector is normalized or rewritten here.

| Original | Opus brand vector | Sol brand vector | Brand dimensions with spread ≥2 |
| --- | --- | --- | --- |
| [login-desktop-light](screenshots/kydrive-01-after-login-desktop-light.png) | `[9, 8, N/A, 9, 8]` | `[9, 8, N/A, 9, 9]` | None |
| [files-desktop-light](screenshots/kydrive-01-after-files-desktop-light.png) | `[9, 8, 8, 9, 9]` | `[9, 8, 9, 9, 9]` | None |
| [share-desktop-light](screenshots/kydrive-01-after-share-desktop-light.png) | `[9, 8, N/A, 9, 8]` | `[9, 8, N/A, 9, 9]` | None |
| [trash-desktop-light](screenshots/kydrive-01-after-trash-desktop-light.png) | `[9, 8, 8, 9, 9]` | `[9, 8, 9, 9, 9]` | None |
| [login-mobile-light](screenshots/kydrive-01-after-login-mobile-light.png) | `[9, 8, N/A, 9, 8]` | `[9, 8, N/A, 9, 9]` | None |
| [share-mobile-light](screenshots/kydrive-01-after-share-mobile-light.png) | `[9, 7, N/A, 9, 8]` | `[9, 8, N/A, 9, 9]` | None |
| [trash-mobile-light](screenshots/kydrive-01-after-trash-mobile-light.png) | `[9, 8, 8, 9, 9]` | `[9, 8, 8, 9, 9]` | None |
| [login-desktop-dark](screenshots/kydrive-01-after-login-desktop-dark.png) | `[9, 8, N/A, 9, 8]` | `[9, 8, N/A, 9, 9]` | None |
| [files-desktop-dark](screenshots/kydrive-01-after-files-desktop-dark.png) | `[9, 8, 8, 9, 9]` | `[9, 8, 9, 9, 9]` | None |
| [share-desktop-dark](screenshots/kydrive-01-after-share-desktop-dark.png) | `[9, 8, N/A, 9, 8]` | `[9, 8, N/A, 9, 9]` | None |
| [trash-desktop-dark](screenshots/kydrive-01-after-trash-desktop-dark.png) | `[9, 8, 8, 9, 9]` | `[9, 8, 9, 9, 9]` | None |
| [login-mobile-dark](screenshots/kydrive-01-after-login-mobile-dark.png) | `[9, 8, N/A, 9, 8]` | `[9, 8, N/A, 9, 9]` | None |
| [share-mobile-dark](screenshots/kydrive-01-after-share-mobile-dark.png) | `[9, 7, N/A, 9, 8]` | `[9, 8, N/A, 9, 9]` | None |
| [trash-mobile-dark](screenshots/kydrive-01-after-trash-mobile-dark.png) | `[9, 8, 8, 9, 9]` | `[9, 8, 8, 9, 9]` | None |

The 26 nonqualifying quality images were included in both complete comparison loops; no quality or brand ≥2 entry was omitted. Both complete 40-file vector sets remain in [opus-round2.json](opus-round2.json) and [sol-round2.json](sol-round2.json).

## Qualitative claims checked against R2

### Phone Share Close — supported, with narrower wording

Both phone Share originals visibly show a roughly **24×36px** Close outline instead of a square 36px outline. A lightweight sample in the light original places its vertical borders at x331 and x354, with panel pixels immediately outside. The X is legible and there is no visible overlap.

The **h2 fits on one line** in both originals; the description beneath it wraps to two. Thus “two-line title” is inaccurate if it means the h2. The shared header text column competes with Close for width. Baseline `web/src/components/Dialog.tsx:21–24` makes them flex siblings; `web/src/styles/theme.css:225` gives `.icon-btn` a 2.25rem width but no nonshrinking flex rule, and `:312` makes the header flex with a 1rem gap. This is consistent with shrinkage; no live layout measurement was performed.

Resolution: the narrow visible control is real and Sol's broad “no material blocker” reading does not negate it. Preventing this shared header control from shrinking is supported. Exact interactive hit area, target success, keyboard operation and focus behavior are untested.

### Empty Share — guidance concern supported; missing operation not established

All four Share originals show empty Find a person, Access Reader, no results, no instruction explaining how to add, and **no filled foreground action**. Create link and Done use secondary outlines. The blurred background's Upload button is outside the active dialog. Clear sections and labels support Sol's positive hierarchy/coherence reading; lack of a highlighted next action explains Opus's concern.

Independent pinned-source check:

- `web/src/pages/drive/ShareDialog.tsx:44–47`: fewer than two trimmed Unicode code points clears results; at least two schedules a people request after 300ms. Matching people depend on the API response, not merely reaching the threshold.
- `:54`: `add` sends the selected principal and role through the existing sharing operation, then clears search/group state.
- `:73–79`: search and Access fields are followed by result rows, each with an explicit **Add as {role}** action. Adding is an action on a result, not an inferred immediate side effect of picking a person.
- `:71–72,90–93`: Done closes the dialog; Create link is its own existing secondary submit operation.

Resolution: explain the **existing search → result → Add as {role}** path. “No visible add control in the empty picture” is accurate; “the product cannot add people” is off-base. A new Share submit workflow or mandatory filled action is not established by this evidence. The source proves an implemented path, not successful network results, grants, notifications, error handling or authorization in a running system.

### Phone toolbar — ink inset supported; shifted container not established

Separately reopened [files-mobile-light](screenshots/kydrive-01-after-files-mobile-light.png), [folder-mobile-light](screenshots/kydrive-01-after-folder-mobile-light.png), [files-mobile-dark](screenshots/kydrive-01-after-files-mobile-dark.png) and [folder-mobile-dark](screenshots/kydrive-01-after-folder-mobile-dark.png).

The panel's heading/breadcrumb starts near **x33**. Trash's first visible icon starts near **x54**, about 21px farther right; refresh and New follow it. That visible ink offset is real in both themes and both locations.

Baseline `web/src/pages/Drive.tsx:181–190` places breadcrumb and `.drive-toolbar-actions` inside the same toolbar. In `theme.css:353–355,522–523`, the toolbar uses common horizontal padding and the phone actions take full width, without a left margin in this two-child layout. `.drive-toolbar-actions:only-child` does not apply because the breadcrumb is another child. The leading `.btn-ghost` inherits **1.1rem horizontal button padding plus border** (`:204–219`), consistent with the approximately 19px empty space before its icon.

Resolution: this is an **internal button-ink inset**, not independently proven left translation of the whole toolbar box. Reducing that leading phone ghost-button padding would address the visible complaint; removing a nonexistent container margin would misdiagnose it. Overall phone vertical density is a separate taste/usability question.

### Light selected row — visible difference, cause unproven

Separately reopened [Appearance desktop](screenshots/kydrive-01-after-appearance-desktop-light.png), [Account desktop](screenshots/kydrive-01-after-account-desktop-light.png), [Appearance phone](screenshots/kydrive-01-after-appearance-mobile-light.png) and [Account phone](screenshots/kydrive-01-after-account-mobile-light.png). Their active rows look near-white; My files in the inspected files originals has a pale accent surface. The selected **orange rail remains visible**, so lost selection state is not established.

Baseline `web/src/components/AppHeader.tsx:51–52,57–66` applies the same `ky-nav-item active` and `aria-current` pattern. `web/src/ky-ui/navigation.css:13–20` supplies one selected background rule across them. Generic `theme.css:204–213` transitions filter/background and brightens hovered buttons; this is compatible with a pointer/transition capture artifact.

The frozen capture driver `capture-round2.mjs:52–67,95–99` clicks these navigation buttons, waits, then moves the pointer to (0,0) immediately before capture, without an explicit post-move transition settle. That weakens a claim of persistent hover but does not eliminate an in-flight visual transition. There is no post-move computed-style/geometry trace.

Resolution: **possible capture artifact, unproven**. Do not claim a stable product selection defect or a product gain from changing it. A controlled future observation could distinguish causes; none was performed in this audit.

### Trash coherence — desktop state difference is real; all-phone fill claim is off-base

All four Trash originals were individually reopened above. Single-image pixel samples, not pairwise diffs:

| Original | Empty trash interior sample | Nearby header sample | Delete permanently interior / row sample |
| --- | --- | --- | --- |
| desktop light | (945,230) RGB 240,224,217 | (936,220) RGB 248,246,240 | (1080,334) / (1080,320): both 255,255,255 |
| desktop dark | (945,230) RGB 42,42,44 | (936,220) RGB 24,35,38 | (1080,334) / (1080,320): both 31,43,46 |
| phone light | (45,407) RGB 248,246,240 | (45,390) RGB 248,246,240 | (217,515) / (217,495): both 255,255,255 |
| phone dark | (45,407) RGB 24,35,38 | (45,390) RGB 24,35,38 | (217,515) / (217,495): both 31,43,46 |

The desktop tint distinction is visible. **Both phone Empty trash backgrounds equal their header**, and Delete backgrounds equal their rows. Opus's repeated per-file tinted-fill explanation is therefore overbroad on phone. Sol's general quiet-danger reading is supported there.

Pinned `Drive.tsx:178` and `:161` use **the same `btn-danger-quiet` class** for Empty trash and Delete permanently (the row adds `btn-sm` and invokes the helper at `:209`). `theme.css:220–222` gives that class transparent background, danger-soft hover fill and smaller contextual geometry. The desktop difference is compatible with hover/transition capture state; a permanent two-style implementation defect is not established. The actual cause was not measured. This qualifies the rationale without amending either coherence vector.

### Artifact role labels — parent evidence bug, not product improvement

The frozen [artifacts-v2.json](artifacts-v2.json) has **eight semantically wrong roles**: four KyIdentity brand references labelled historical, and four stage00 originals (`versions-mobile-dark`, `trash-mobile-dark`, `appearance-mobile-dark`, `account-mobile-dark`) labelled “current round1 scoring original.”

The parent's [artifacts-v2-corrected.json](artifacts-v2-corrected.json) corrects roles by explicit membership and records the original index hash. Verified: **all 84 rows retain identical non-role fields**, including file/hash/bytes/dimensions and any duplicate links; corrected role sets are **40 historical, 40 current, four brand-only**. All 84 actual PNG hashes, byte counts and header dimensions match.

Resolution: R2-M4 is supported as an index-builder/evidence problem. The supplemental correction is appropriate while preserving the original index and both raw review pairs. Neither this correction nor the audit is product gain; quality membership and every score remain unchanged.

### Additional off-base wording: sign-in credentials

The four login originals show “admin” and password dots, but baseline `web/src/pages/Login.tsx:17–18,129–134` initializes both values empty and supplies those exact placeholders. `capture-round2.mjs:71–77` captures login **before** filling either field. Opus's “password filled” wording is unsupported and conflicts with this evidence; Sol's placeholder description is supported. This is an observation correction, not evidence of secret exposure, an authentication flaw or a reason to change a frozen score.

## Brand arithmetic and exporter boundary

Opus explicitly declares **round each per-file non-N/A brand mean to one decimal, then average those rounded means**. That published calculation is **341.6 / 40 = 8.54**, displayed **8.5**. Recomputing each non-N/A mean directly from raw dimensions, then averaging without intermediate rounding, gives **683/80 = 8.5375**, also displayed **8.5**. Neither is substituted into quality.

The parent's exporter `build-report.py:118–140,172–205,215–241` was inspected read-only: it loads report bytes into memory, validates the full frozen files-array digest, derives exact dimension-based brand statistics for export, and recognizes the publisher's declared rounded-file-mean method when validating published brand summaries. It does **not** require rewriting the publisher's JSON to make its presentation equal the exact-derived aggregate. Quality vectors/totals and the Opus progression value are unaffected. The exporter was not executed by this auditor.

## Provenance and limits

The recorded capture conditions are Chromium 156.0.8078.4 / Playwright 1.64.1 headless; DPR1; 1280×900 and 390×844; en-US; America/New_York; OS light/dark with no saved theme; ordinary service-worker behavior; initial viewport, one capture invocation and zero confirmation recaptures. These are attributed to the hash-verified provenance, not freshly reproduced here.

Every authenticated original shows the **actual local recovery administrator**; login is signed out. Ordinary OIDC identity, populated Shared with me/group-owned shared workspaces, connected Euro-Office/save, live SSO, guest-link operation, linked computers and recovery integrations remain missing/unscored. Offscreen content is not inferred absent.

The captured binary reports revision `7dd862e` **and `vcs.modified=true`**. The source-provenance record reports 390 product files and the served index matching the pinned revision. This audit independently verified eight relevant source blobs against that recorded inventory through `git show`; it did not revalidate the binary, server, runtime or build reproducibility. This is not a hermetic-build or deployment-readiness claim.

**No complete behavior, keyboard, focus, contrast, accessibility, responsive interaction, role/security, performance or integration pass.** A visible X, focus ring, pair of viewport sizes or implemented handler cannot pass those gates. Historical exact/near-duplicate results were not recomputed or promoted to fresh pixel evidence.

## Reproduction and integrity

Lightweight comparison logic (all 40, not a prefiltered subset):

```python
quality = ("hierarchy", "typography", "density_layout", "color",
           "visible_affordances", "coherence")
brand = ("palette_tokens", "type_geometry", "shell",
         "accent_discipline", "suite_cohesion")
opus = {r["file"]: r for r in opus_report["files"]}
sol = {r["file"]: r for r in sol_report["files"]}
assert opus.keys() == sol.keys() == inventory.keys()
assert len(opus) == len(opus_report["files"]) == len(sol_report["files"]) == 40

def spreads(keys, field):
    result = {}
    for f in opus:
        a = opus[f][field] if field == "scores" else opus[f]["ky_fit"]["scores"]
        b = sol[f][field] if field == "scores" else sol[f]["ky_fit"]["scores"]
        result[f] = {d: abs(a[d] - b[d]) for d in keys
                     if a[d] is not None and b[d] is not None
                     and abs(a[d] - b[d]) >= 2}
    return {f: values for f, values in result.items() if values}

q, b = spreads(quality, "scores"), spreads(brand, "ky_fit")
assert len(q) == 14 and sum(map(len, q.values())) == 18
assert not b
```

SHA-256 values for the immutable reviewer pairs and frozen/corrected indexes, checked at intake and again after writing:

| Input | SHA-256 |
| --- | --- |
| [opus-round2.json](opus-round2.json) | `36f7df6c2f33ccdd804221c031737d1f1badb21319d27687ffb2017eec611cff` |
| [opus-round2.md](opus-round2.md) | `cfeebd2efdea33c490c760d944ae30a62e613c39d5491d10a381865616ec6b36` |
| [sol-round2.json](sol-round2.json) | `d382df8fd2da3463405c58cb470c17ff0d5b1fe9453d42c59aac1706d541ace3` |
| [sol-round2.md](sol-round2.md) | `07e659ef386287352d8ba2107a1f95b04095cfa9c6ec2db04665db9c03153535` |
| [artifacts-v2.json](artifacts-v2.json) | `1e6644398aaaf2540b9111a4255213e4ffd0b14ff67128cbfcf9c17e30e5779b` |
| [artifacts-v2-corrected.json](artifacts-v2-corrected.json) | `9f9606069af4518f1f8030d0d9866cb3f1885d8308a2b6cd179fc9f2cadf589f` |

Full-files-array canonical freeze digests reproduced unchanged: Opus `014f111dbd7f19746f1926f8407d825fe6f08140ea09c0e14391be9b41b3b16c`; Sol `e801c1d13af83cf5a3b38403fd20ae39db0af635acf6bca5511115fec57774b1`. Canonicalization is each publisher's `json.dumps(files, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode('utf-8')`.

Each qualifying original's dimensions and full hash were verified against both reviewers and the inventory:

| Original | Dimensions | SHA-256 |
| --- | --- | --- |
| [login-desktop-light](screenshots/kydrive-01-after-login-desktop-light.png) | 1280×900 | `c2e55f4bfd21e534d91f4e5f738c3c71a0c90390a48b7376fce1409780fb94b7` |
| [files-desktop-light](screenshots/kydrive-01-after-files-desktop-light.png) | 1280×900 | `50c36f9420d1759c7610a679842d018e70315ea7c4e5b197a14cbee421facf55` |
| [share-desktop-light](screenshots/kydrive-01-after-share-desktop-light.png) | 1280×900 | `67489b253b4a75b73ae8b8b2f5de4ffefc6d12ee93b7e05792f0520c112e326d` |
| [trash-desktop-light](screenshots/kydrive-01-after-trash-desktop-light.png) | 1280×900 | `0e2607199be990da6068785247146830ae48565c92a09d64dd805e50c21d1b23` |
| [login-mobile-light](screenshots/kydrive-01-after-login-mobile-light.png) | 390×844 | `b66bf5cf38318e8bf1cc6383669e7e07a6564b98359ee8b0fa9e864379ff110f` |
| [share-mobile-light](screenshots/kydrive-01-after-share-mobile-light.png) | 390×844 | `e6fd9724e610bf825662e058064baba985cb651d1e42d59352cf2bec0175d644` |
| [trash-mobile-light](screenshots/kydrive-01-after-trash-mobile-light.png) | 390×844 | `7c1380f87a6d8018cb4926b7aefe21ceafb4971cda10a04f03e5e3bf67c336cb` |
| [login-desktop-dark](screenshots/kydrive-01-after-login-desktop-dark.png) | 1280×900 | `9ecc174bd58b9c14adbb06a4616cecc2e434d14908e9b0cf594b32a1d06479fd` |
| [files-desktop-dark](screenshots/kydrive-01-after-files-desktop-dark.png) | 1280×900 | `bf023847bc29842f79d11b4f7992b8d21681231df5a2b6c5f84c7600ee003759` |
| [share-desktop-dark](screenshots/kydrive-01-after-share-desktop-dark.png) | 1280×900 | `49e6a566504a854bb9d3097107de01e5462fba1d468a87440298dfd3ee293d60` |
| [trash-desktop-dark](screenshots/kydrive-01-after-trash-desktop-dark.png) | 1280×900 | `fff9db7c2b33a0ec1c538987778fcc12dac0f70bea8334748e78bbbf57ad1628` |
| [login-mobile-dark](screenshots/kydrive-01-after-login-mobile-dark.png) | 390×844 | `f94d60c999307162dae01e887bb9b6337af4d7e9989babd19e6454e838cce696` |
| [share-mobile-dark](screenshots/kydrive-01-after-share-mobile-dark.png) | 390×844 | `a1d0701920715f2db896ceb1938a09d89d16091341ec2bfc69aa25091cee91f3` |
| [trash-mobile-dark](screenshots/kydrive-01-after-trash-mobile-dark.png) | 390×844 | `8d2b327c2dc65d360303dadb882d5f4b912c9030fe3938eca8014f89c95b5475` |

Eight additional originals were separately reopened at native resolution for qualitative claims: files/folder phone light/dark, and Appearance/Account desktop/phone light (all stage01). Their hashes/bytes/PNG dimensions were checked within the complete 84-artifact lightweight pass.

Read-only verification passed: equal 40-file inventory membership and unique image hashes; 240 quality and 180 numeric brand comparisons; 80 integer quality vectors and exact Decimal weighted/rounded totals; both canonical freezes; all 15 Opus and 14 Sol declared input hashes; five pinned authority objects; 84 image hashes/bytes/PNG dimensions; eight relevant pinned-source blob hashes; supplemental index non-role equality.

No image-pair loops, fixture operations, builds, heavy tests, browser/service operations, extra agents, commits or publisher/exporter mutations were run. The Impeccable launcher/detector and repository full checks were not run under the owner's explicit hard-lock constraints; source/visual checks above are the audit's verification, not product assurance. No MySlop retry/post was made because the owner specified known 401.

DOX pass: the binding parent → KyDrive root → docs → ui-drive-eight chain was read/rechecked for the assigned path. AGENTS files are intentionally unchanged: this evidence record changes no durable purpose, structure, contract, workflow or ownership. **Only `docs/ui-drive-eight/round2-spread-audit.md` was written by this auditor.**
