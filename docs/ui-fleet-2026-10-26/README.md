# KyDrive fleet Appearance — 2026-10-26

Applies the approved Busnes look to KyDrive's web shell and gives it the shared ky-ui 0.4.0 Appearance theme-preview cards (native radios) over the suite catalog: Match system plus every catalog theme, read dynamically from `web/src/ky-ui/themes.js`. Shared files come from ky-ui `4f83f34` and pass `sync-consumers --check`; they are generated, never hand-edited. No score is claimed here: the council rates these captures.

## Stages

- **00 before**: base commit, current theme control and main view.
- **01 after**: suite shell scale (240px sidebar, 24px title, 40px controls and nav rows, shared field border and type) and the Appearance cards.
- **02 after**: owner correction — shared description class and copy, consistent wrapping, preview hairline, no theme selector in the shell navigation. Only affected Appearance/shell views were recaptured.
- **02 confirm**: one confirmation recapture after the single stage 02 fix batch, where a fix was needed.
- **03 after**: owner decisions on the reported conflicts (phone inset for two card columns at 390px, preview fidelity for KyDrive's shipped themes, KyYard's Polished Ky kept), light/dark × desktop/phone Appearance only. Its before is the latest stage 02 batch of the same view (`previous_file` in `results.csv`). The in-app check gained three assertions: every named preview matches the applied palette, two columns with no overflow at 390px, and a keyboard choice survives a reload.
- **03 confirm**: one confirmation recapture after the single stage 03 fix, where a fix was needed.
- **04 before / 04 after**: fleet review follow-up, Appearance only. Before is clean `8f1b4f8`; after is the one-line copy change. The Match system mode label changes from "Light or dark" to "Follows device", as in the other products; that label is the only pixel difference in the four captures. Fleet review F6, a phone nav row left off-screen, was not reproduced here. In a real-app probe at 390px, the Workspaces row does not overflow (358/358px). The Primary row overflows, but its current row is only ever reached by a click, which scrolls it into view. A reload, sign-out or pending link selects My files or Shared with me, which stay in view. The navigation is therefore unchanged.
- **05 before / 05 after**: fleet review F7, with the `delete-dialog-sky` and `delete-dialog-forest` surfaces: the real "Delete permanently" confirmation for the trashed synthetic Meeting notes.txt, under a saved Sky or Forest palette, never confirmed. Before is clean `a949b9a` (master after #81). After is the `.btn-danger` label rule. On Sky the label goes from dark to light on the same fill; Forest keeps a dark label (`--bg` instead of `--button-text`). Elsewhere the dialog differs by at most 9/255 per channel.

Surfaces: My files (three synthetic uploads), Settings & DB (where the theme control lived before) and the new Appearance page.

## Catalog and saved choice

- Storage key `ky_theme`; catalog IDs saved by earlier versions (`system`, `busnes-light`, `busnes-dark`, `patina`, `cyber`, `nord`, `paper`, `oled`) still select their theme; new choices are saved by catalog ID.
- Match system follows the device until a choice is saved; that default is unchanged.

## Product notes

- New Appearance page in the primary navigation for every role. By owner decision the sidebar (phone header) has no theme dropdown; login and the administrator Settings page keep a compact dropdown over the same saved choice.
- The five themes KyDrive shipped keep their exact `styles/theme.css` values and are not overwritten inline. Their preview cards carry the theme's own `data-theme`, so those same blocks colour the card (sidebar = `--panel`, as `.app-header` renders them; the hairline keeps the page border). No second hex catalog is copied. Before stage 03 these five cards showed the catalog's colours; Paper Clean's accent was visibly wrong.

## Capture batches

Every manifest in `screenshots/` lists the source commit, a hash of any uncommitted diff, OS, driver, viewport, theme source, fixture and the SHA-256 of every PNG. `results.csv` pairs each after image with its stage 00 before; `checks.csv` holds the in-app checks. `source.patch` is the source-only diff (no vendored or built files) for independent review.

| Batch | Source commit | Uncommitted diff | Images | Surfaces | Captured |
| --- | --- | --- | --- | --- | --- |
| `kydrive-00-before` | `e772e28629` | none | 8 | files, settings | 2026-10-26T11:02Z |
| `kydrive-01-after` | `f82abfa40a` | `9446f8bccea0…` | 12 | appearance, files, settings | 2026-10-26T11:20Z |
| `kydrive-02-after` | `e9de227d32` | none | 12 | appearance, files, settings | 2026-10-26T11:38Z |
| `kydrive-02-confirm` | `f34d876023` | none | 8 | appearance, files | 2026-10-26T11:40Z |
| `kydrive-03-after` | `d5d2be112a` | `4fd571c39e5e…` | 4 | appearance | 2026-10-26T12:08Z |
| `kydrive-03-confirm` | `ad49c4494a` | none | 4 | appearance | 2026-10-26T12:11Z |
| `kydrive-04-before` | `8f1b4f8` | none | 4 | appearance | 2026-10-26 |
| `kydrive-04-after` | `8f1b4f8` | `6a76845168fe…` | 4 | appearance | 2026-10-26 |
| `kydrive-05-before` | `a949b9ac01` | none | 8 | delete-dialog-sky, delete-dialog-forest | 2026-10-26T14:44Z |
| `kydrive-05-after` | `a949b9ac01` | `d20aa423a353…` | 8 | delete-dialog-sky, delete-dialog-forest | 2026-10-26T14:54Z |

Conditions: linux 7.2.10-1-cachyos, playwright chromium 156.0.8078.4 (headless), device scale 1, 1280×900 and 390×844, light/dark by OS emulation with no saved choice, America/Chicago, en-US. Fixture: web built from this tree; fresh SQLite data dir (web/browser/server.mjs environment on port 5873); captcha none; no editor, directory or recovery configured; KY_ADMIN_PASSWORD bootstrap "admin" replaced through /api/auth/change-password; three synthetic text uploads (Quarterly plan.md, Supplier list.csv, Meeting notes.txt) in My files through the Upload files input.

## In-app theme check

`node docs/ui-fleet-2026-10-26/capture.mjs --verify <prefix>`: Sky and Sun (light named themes), Busnes Dark, Busnes Light and Match system, asserting the page background, the hovered idle navigation row and the selected row at 4.5:1 or better from computed colours in the running product, then arrow-key selection with a visible focus ring.

| Run | Source commit | Passed |
| --- | --- | --- |
| `kydrive-01-after` | `f82abfa40a` | 16/16 |
| `kydrive-02-after` | `e9de227d32` | 16/16 |
| `kydrive-03-after` | `d5d2be112a` | 19/19 |
| `kydrive-03-confirm` | `ad49c4494a` | 19/19 |

## Checks

| Check | Result |
| --- | --- |
| Web unit tests (vitest) | 100/100 pass at stage 03 |
| Typecheck and web build | pass |
| Production-server browser suite (`npm run test:browser`, 48 tests, incl. Appearance radios and a preview-vs-applied check for the five shipped themes) | 48/48 pass at `ad49c44` (12:16–12:18Z on the shared 5391 slot) |
| `npm audit` | 0 vulnerabilities |
| `go mod verify` and `go mod tidy` clean | pass |
| Vendored ky-ui check | pass at 4f83f34 |
| restic, smoke, deploy unittest and Docker jobs | not run locally: CI runs them |

Go: `go build ./...`, `go vet ./...`, gofmt and the full `go test -race -count=1 ./...` passed locally (CachyOS, Go 1.27.1) at stage 02; Go code is unchanged since then and by this work (`go build ./...` re-run at stage 03), so those results stand. Docker image, publish and attestation jobs were not run locally: CI runs them.

## Reproduce

```sh
cd web && npm ci && npm run build   # then any product-specific bundle step in AGENTS.md
cd .. && KY_PLAYWRIGHT=<path to @playwright/test/index.mjs, if web/ has none> \
  node docs/ui-fleet-2026-10-26/capture.mjs <prefix> [surface,...]
node docs/ui-fleet-2026-10-26/capture.mjs --verify <prefix>
node docs/ui-fleet-2026-10-26/results.mjs
```

Each run starts a disposable loopback server on its own port with generated secrets and removes its data directory afterwards. No live service, personal data or authentication bypass is used.
