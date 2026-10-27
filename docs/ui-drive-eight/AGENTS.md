# KyDrive UI Council

## Purpose

Keep staged KyDrive captures, independent reviews and visual progression evidence reproducible.

## Ownership

- Parent owns capture inputs, audits, comparison galleries and spreadsheet exports.
- Reviewers own only their assigned raw Markdown/JSON report pairs; preserve published bytes.

## Local Contracts

- Preserve product behavior, roles, authentication, CSP, named themes, saved choices and OS following.
- Use the pinned ky-suite-v2 authority in `authority.json`; quality and brand fit stay separate.
- Freeze current observations and score vectors before reading historical or peer scores, acceptance records or galleries. Compare only with the reviewer's own previous round after freezing.
- Owner progression uses Opus's overall quality mean across the current reviewed set, rounded half-up to one decimal; 8.0 accepts. Sol stays independent; historical rounds and brand scores never enter the gate. Individual cohort shortfalls do not veto the overall result.
- Show score history and pending/unused reviewers every round. Owner layout rejection reopens progression; use one left navigation region.
- Gemini is advisory only after both primaries genuinely approve. Preserve and audit its claims; unavailable advice does not delay accepted progression.

## Work Guidance

- Stage00 is 40 initial-view captures at1280×900 and390×844, DPR1, OS light/dark, from a disposable fixture.
- Authenticated captures show the real local administrator. Ordinary users require external OIDC; ordinary/shared/connected-editor workflows remain missing and unscored. Do not describe this set as whole-product acceptance.
- Keep fixture credentials, database and keys private. Capture conditions, build warnings and unavailable surfaces are recorded in `baseline-setup.md` and the manifest.
- Stage01 records round2 after-views from the same owned fixture and matched surface/size/theme set; keep stage00 and raw round1 reports unchanged. Preserve any superseded initial batch and permit only one confirmation recapture per refinement batch.
- Recreated fixtures disclose changed instance identity and timestamps; match declared synthetic content/roles rather than claiming identical runtime state. Keep corrected metadata roles in a supplemental index so frozen input hashes remain reproducible.
- Stage03 retains the recreated stage02 fixture where available. Its driver moves the pointer before the settling delay and records hovered controls; capture-state corrections are disclosed separately from product improvements. Preserve earlier drivers and originals.
- Product checks follow root/web contracts. Screenshots do not establish complete behavior, accessibility, role or integration gates.

## Verification

- `python3 verify-baseline.py` checks the frozen original batch and regenerates its inventory/exact-duplicate audit.
- `python3 audit-near-duplicates.py` reproduces same-size pixel comparisons using installed Pillow/NumPy; threshold: changed pixels<0.1% and max channel delta≤8.
- Run these from this directory or use their paths from the repository root. Existing `SHA256SUMS` freezes the original publisher batch; supplemental review inputs have their own hashes.

## Child DOX Index

None.
