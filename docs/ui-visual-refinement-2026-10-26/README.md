# Visual refinement evidence: KyDrive (2026-10-26)

Before/after captures and real-browser checks for the fleet visual-refinement batch
(baseline review gaps 6 and 8: a second theme selector in admin Settings, a ~230px phone
header from two navigation rows plus a preference row). Ratings belong to the independent
reviewer; this record claims no score.

## Stages

| Stage | Source | What it shows |
| --- | --- | --- |
| `00-before` | clean `657a485` (origin/master) | baseline; 3/6 checks. The reachability check failed on a **driver** locator (two "My files" buttons: sidebar and breadcrumb), not on the product; the images show every destination. The driver scopes that locator to the header from stage 01 |
| `01-after` | clean `84102c6` | the change; 6/6 checks. No visual defect found, so no confirmation recapture |

`files` (My files with three uploads) and `settings` (admin Settings & DB), light and dark
OS scheme × 1280×900 and 390×844, full page. This driver is self-contained and uses the
same manifest shape as the other fleet products, unlike `../ui-fleet-2026-10-26/`
(viewport-only shots). Manifests record source commit, uncommitted-source hash (null), OS,
Playwright/Chromium, timezone/locale, font state, server binary SHA-256, fixture, time,
PNG SHA-256s and checks.

Fixture: `web/dist` and the server built from the tree on 127.0.0.1:5391, fresh SQLite,
captcha none, no editor/directory/recovery; the bootstrap password replaced through
`/api/auth/change-password`; three synthetic text uploads in My files. The build prints
the known, pre-existing Vite chunk-size warning (present at stage 00 too).

## Changes

- Admin Settings → System: the "Theme on this browser" row now has an **Open Appearance**
  button that opens the Appearance page through the existing navigation, instead of a
  second theme `<select>`. The explanatory sentence is unchanged. The login page keeps its
  compact selector. There is no instance-wide default theme, so nothing else moved.
- Phones (≤720px): brand and sign-out on the top row; both navigations (workspaces and
  Account/Appearance/Recovery/Settings & DB) scroll together as one row, divided by a
  hairline, and `AppHeader` scrolls only that row to keep the current destination in
  view; the "Open files in a new tab" preference sits under it. Desktop is unchanged.
- Page introductions sit 8px under the title (was 3px).

Phone header: 202px → 141px.

## Checks

Per stage in `behaviour-checks.csv`: header height; every workspace and destination,
the preference and Sign out reachable; choosing the last destination keeps it in view with
the page unscrolled; no theme select on Settings; Open Appearance shows the 23 radio cards;
no console or CSP errors. Required checks, including the browser suite, are in the PR.

## Known limits

- The System card's `sqlite` mono badge and the panel header icons are unchanged.
- No shared workspaces exist in this fixture, so a long workspace list in the phone row
  was not captured.

## Reproduce

From the repository root after `cd web && npm ci`:
`node docs/ui-visual-refinement-2026-10-26/capture.mjs 01 after`, then `results.mjs`.
