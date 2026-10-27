---
name: ky-suite-ui
description: Rate, redesign or accept Ky suite web UI (KyPost, KyCalendar, KyMessages and other Ky products) in the shared Busnes cream/charcoal/orange design language. Use for competitive screenshot scoring and re-ranking, staged redesigns with before/after captures, and checking a Ky screen against the 9/10 target.
---

# Ky suite UI

The goal is one refined Busnes design language across the Ky web products, measured
honestly against competitors. Three modes share one evidence discipline:

- **Rate**: score screenshots and rank them within matched cohorts. Read
  [references/rubric.md](references/rubric.md) before scoring anything.
- **Redesign**: change product UI in stages, with before/after captures for every stage.
  Read [references/design-contract.md](references/design-contract.md) before editing.
- **Accept**: decide whether a Ky product has reached 9/10. Read both references.

## Sources of truth

Read these before acting; they outrank anything restated here.

- `ky-ui/src/tokens.css`: the palette and shared runtime tokens. Read colors from it on
  every run; this skill holds no palette values.
- `ky-ui/AGENTS.md`, `ky-ui/reviews/AGENTS.md` and the target product's `AGENTS.md`
  chain: ownership, checks and local contracts.
- The newest council directory under `ky-ui/reviews/`: inventories, source evidence
  and staged captures. In Rate mode, load earlier scores only at the drift step. Local product checkouts can lag `origin`; record the
  exact revision you inspect or capture.

## Evidence discipline

A score describes a picture. A gate describes behavior. Keep them apart.

- Every score cites something visible in the image. Mark what the image cannot show as
  unknown, including features, interaction, keyboard, focus, screen readers, real
  contrast and performance. Do not infer that a feature is missing because it is not on screen.
- Missing evidence is `N/A`: never zero, and never part of a mean.
- Identify product and surface from what is on screen, not from the filename.
- Each artifact you publish states its reviewer model, input hashes, rubric version,
  capture conditions and arithmetic, so another agent can recompute it.
- A target score is not an achieved score. Report the number the evidence supports.

## Rate

1. Inventory the complete source set with hashes, dimensions and duplicate links before
   viewing for scores (rubric: Inventory). Done when every file is listed.
2. View every unique image at full resolution and fill its evidence row: observed
   product, surface, cohort, state, theme, viewport, capture artifacts, per-dimension
   scores with observations, confidence and caveat. Score blind to earlier rounds.
   Done when every unique file has a complete row or an explicit `N/A` reason.
3. Score the Ky brand fit separately for Ky screens only.
4. Rank within cohorts, then compare with the previous round and explain drift.
5. Recompute every total and mean with a script, and check that the per-file list
   matches the inventory exactly.

Write `<round>-review.md` (findings, cohort tables, missing evidence) and
`<round>-scores.json` into the council directory. Use the field names in the rubric.
For council delivery, export the ratings, evidence, checks and PR links to a spreadsheet workbook; JSON remains the machine-readable scoring record.

## Redesign

Work in three stages, each landing before the next begins:

1. **Shell**: sidebar or top bar, wordmark, navigation, account region, page header.
2. **Task views**: lists, calendar grids, the reading pane, settings indexes, admin tables.
3. **Forms and dialogs**: compose, event and contact editors, settings forms, confirmations.

For each stage and product:

- **Before**: capture the batch from the stated revision with synthetic data, using the
  same fixture you will use for the after captures.
- **Change**: edit product code toward the design contract. Shared tokens go upstream to
  ky-ui; geometry and composition stay in the product.
- **After**: capture the same batch under identical conditions.
- **Verify**: one bounded round. Inspect every after capture, fix everything it shows in
  a single batch, then confirm with at most one recapture. Record what remains rather
  than starting another round.
- **Check**: run the product's required checks from its `AGENTS.md`, and record which
  ones passed, failed or were not run.

The batch is light and dark × desktop 1280×900 and mobile 390×844, for every surface the
stage touches. Name files
`<product>-<stage nn>-<before|after>-<surface>-<desktop|mobile>-<light|dark>.png` in
`<council>/screenshots/`. Stage `00` is the baseline. Keep a manifest beside the images
with the revision, browser, viewport, theme source, fixture and date. Keep real names,
addresses and mail out of the repository.

Preserve existing behavior. That covers routes, copy, named themes and saved theme
choices, roles and authorization, CSP, and security defaults. Ask before changing a
feature or claim.

## Accept

A Ky product reaches 9/10 only when all of the following hold:

- Current implemented screens, captured from a recorded revision, score ≥ 9.0 on quality
  in every cohort the product ships, with no unmatched or `N/A` Ky cohort left unstated.
- Ky brand fit is ≥ 9.0.
- Every behavior gate in the rubric passes with recorded evidence. An untested gate
  means not accepted.

Anything less is reported as the scores and gate results that were measured.

## KyMessages scope

The KyMessages operator console (`web/`) and the non-administrator `MemberHome` are Ky
surfaces and follow the design contract. Chat is stock Element Web, themed only through
its `config.json` (`custom_themes`, generated from the ky-ui tokens). Keep Element stock,
with no fork or rewrite, and score it in the chat cohort only when it has been captured.
