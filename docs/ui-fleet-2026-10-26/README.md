# KyDrive fleet Appearance — 2026-10-26

Applies the approved Busnes look to KyDrive's web shell and gives it the shared ky-ui 0.4.0 Appearance theme-preview cards (native radios) over the suite catalog: Match system plus every catalog theme, read dynamically from `web/src/ky-ui/themes.js`. Shared files come from ky-ui `4f83f34` and pass `sync-consumers --check`; they are generated, never hand-edited. No score is claimed here: the council rates these captures.

## Stages

- **00 before**: base commit, current theme control and main view.
- **01 after**: suite shell scale (240px sidebar, 24px title, 40px controls and nav rows, shared field border and type) and the Appearance cards.
- **02 after**: owner correction — shared description class and copy, consistent wrapping, preview hairline, no theme selector in the shell navigation. Only affected Appearance/shell views were recaptured.
- **02 confirm**: one confirmation recapture after the single stage 02 fix batch, where a fix was needed.

Surfaces: My files (three synthetic uploads), Settings & DB (where the theme control lived before) and the new Appearance page.

## Catalog and saved choice

- Storage key `ky_theme`; catalog IDs saved by earlier versions (`system`, `busnes-light`, `busnes-dark`, `patina`, `cyber`, `nord`, `paper`, `oled`) still select their theme; new choices are saved by catalog ID.
- Match system follows the device until a choice is saved; that default is unchanged.

## Product notes

- New Appearance page in the primary navigation for every role. By owner decision the sidebar (phone header) has no theme dropdown; login and the administrator Settings page keep a compact dropdown over the same saved choice.
- The five themes KyDrive shipped keep their exact `styles/theme.css` values and are not overwritten inline. KyDrive's Paper Clean differs from the catalog's base `paper` (accent `#0369a1` vs `#0284c7`, button text `#ffffff` vs `#0d0f14`, danger and success), so the Paper Clean preview card shows the catalog accent, not KyDrive's.

## Capture batches

Every manifest in `screenshots/` lists the source commit, a hash of any uncommitted diff, OS, driver, viewport, theme source, fixture and the SHA-256 of every PNG. `results.csv` pairs each after image with its stage 00 before; `checks.csv` holds the in-app checks. `source.patch` is the source-only diff (no vendored or built files) for independent review.

| Batch | Source commit | Uncommitted diff | Images | Surfaces | Captured |
| --- | --- | --- | --- | --- | --- |
| `kydrive-00-before` | `e772e28629` | none | 8 | files, settings | 2026-10-26T11:02Z |
| `kydrive-01-after` | `f82abfa40a` | `9446f8bccea0…` | 12 | appearance, files, settings | 2026-10-26T11:20Z |
| `kydrive-02-after` | `e9de227d32` | none | 12 | appearance, files, settings | 2026-10-26T11:38Z |
| `kydrive-02-confirm` | `f34d876023` | none | 8 | appearance, files | 2026-10-26T11:40Z |

Conditions: linux 7.2.10-1-cachyos, playwright chromium 156.0.8078.4 (headless), device scale 1, 1280×900 and 390×844, light/dark by OS emulation with no saved choice, America/Chicago, en-US. Fixture: web built from this tree; fresh SQLite data dir (web/browser/server.mjs environment on port 5873); captcha none; no editor, directory or recovery configured; KY_ADMIN_PASSWORD bootstrap "admin" replaced through /api/auth/change-password; three synthetic text uploads (Quarterly plan.md, Supplier list.csv, Meeting notes.txt) in My files through the Upload files input.

## In-app theme check

`node docs/ui-fleet-2026-10-26/capture.mjs --verify <prefix>`: Sky and Sun (light named themes), Busnes Dark, Busnes Light and Match system, asserting the page background, the hovered idle navigation row and the selected row at 4.5:1 or better from computed colours in the running product, then arrow-key selection with a visible focus ring.

| Run | Source commit | Passed |
| --- | --- | --- |
| `kydrive-01-after` | `f82abfa40a` | 16/16 |
| `kydrive-02-after` | `e9de227d32` | 16/16 |

## Checks

| Check | Result |
| --- | --- |
| Web unit tests (vitest) | 99/99 pass |
| Typecheck and web build | pass |
| Production-server browser suite (`npm run test:browser`, 48 tests incl. the Appearance radios) | 48/48 pass after the dropdown removal |
| `npm audit` | 0 vulnerabilities |
| `go mod verify` and `go mod tidy` clean | pass |
| Vendored ky-ui check | pass at 4f83f34 |
| restic, smoke, deploy unittest and Docker jobs | not run locally: CI runs them |

Go: `go build ./...`, `go vet ./...`, gofmt and the full `go test -race -count=1 ./...` passed locally on this machine (CachyOS, Go 1.27.1); Go code is unchanged by this work. Docker image, publish and attestation jobs were not run locally: CI runs them on this PR.

## Reproduce

```sh
cd web && npm ci && npm run build   # then any product-specific bundle step in AGENTS.md
cd .. && KY_PLAYWRIGHT=<path to @playwright/test/index.mjs, if web/ has none> \
  node docs/ui-fleet-2026-10-26/capture.mjs <prefix> [surface,...]
node docs/ui-fleet-2026-10-26/capture.mjs --verify <prefix>
node docs/ui-fleet-2026-10-26/results.mjs
```

Each run starts a disposable loopback server on its own port with generated secrets and removes its data directory afterwards. No live service, personal data or authentication bypass is used.
