# Web

## Purpose
React 19 + TypeScript + Vite PWA frontend embedding KySecurity color tokens (Busnes light/dark defaults plus `Patina Ky`, `Cyber`, `Nord`, `Paper`, `OLED`), client-side WebCrypto PoW CAPTCHA, and administrative management panels.

## Ownership
Owns user interface components, service worker caching, PWA installation manifests, and frontend theme switching.

## Local Contracts
- Web themes default to the Busnes.app cream/light and charcoal/dark palettes with orange accents, following the OS until a browser-local choice is saved. Preserve existing named themes and saved choices.
- A signed-in user with `must_change_password` sees only password replacement and sign-out. Replacement uses `secureFetch`, returns to login after session revocation, and never exposes the normal navigation before completion.
- Strict TypeScript type safety without unused imports.
- The authenticated shell uses a persistent sidebar; the selected page is marked by a quiet surface and slim accent rail, with a horizontal overflow navigation on small screens.
- Dynamic theme selection applies `data-theme` attribute to the root HTML document and persists to `localStorage`.
- Authenticated state-changing requests use `secureFetch` so the `ky_csrf` cookie is mirrored into `X-CSRF-Token`.
- Register the service worker from the production JS bundle; keep `script-src 'self'` intact.
- Worker caching is limited to the same-origin public shell, manifest and assets. HTML is network-first with offline fallback so deployments refresh; dynamic/auth routes stay uncached.
- KyDrive deployments use SQLite; inherited recovery UI still reports snapshot preconditions.
- The suite SSO button reads `Sign On with KyIdentity`; the login description identifies organization files and shared workspaces.

- My files and Shared workspaces live in the main application sidebar, with no redundant Files destination. The selected workspace names the page. Personal workspace creation is an idempotent authenticated POST before listing. New file offers document/spreadsheet/presentation/markdown/rtf/whiteboard creation with a native name dialog, then opens the created file in Euro-Office (for Office/PDF/Markdown/RTF) or the Excalidraw whiteboard (for `.excalidraw`). Choosing a type closes the New file menu.
- "Open files in a new tab" (sidebar, every signed-in user) is a per-account server preference, default on, read from `/api/auth/me` and saved with `PUT /api/auth/preferences`; a failed save reverts the checkbox. On, file links and Create & open use a new tab (the tab is opened inside the click so pop-up blockers allow it, then pointed at the editor; a blocked tab falls back to in-place). Off, files open in the drive tab. The browser suite stays under the server's 20 sign-ins per minute. The drive opens on files with a compact upload/folder toolbar and per-file action menus. Workspace settings contain group permissions and quotas; they do not crowd the file list. Do not expose inherited device pairing without a supported KyDrive client.
- `/whiteboard.html` is a second Vite entry (`src/whiteboard/`) running upstream `@excalidraw/excalidraw` under the global CSP. It opens files through Excalidraw's own loader and autosaves `serializeAsJSON(..., 'local')` through the uploads API with the expected revision; opening never writes, and any rejected save (conflict, quota, access) stops autosave. No collaboration, share links or library persistence.
- Excalidraw is pinned exactly and bumped by Dependabot in its own group; `package.json` overrides (ranges) patch its vulnerable transitive deps until upstream ships fixes. `vite.config.ts` rewrites its esm.sh font fallback to `/excalidraw/`, failing the build if that string moves, and copies canvas fonts into `dist/excalidraw/fonts`. Xiaolai (CJK, 13 MB) goes to `dist-fonts/` instead of the embedded bundle; the image ships it at `KY_EXCALIDRAW_FONTS_DIR`.
- KyDrive owns files, folders, versions/trash, workspace permissions/quotas and operational status/audit. People/group management links to KyIdentity; no separate local directory editor.
- Hide privileged navigation from staff. Directory-managed group membership and workspace access are distinct. Managers can configure group grants; only administrators set quota or see global operations.

## Verification
- Browser setup: build the frontend, run `go build -o .browser/server ./cmd/server` at the repo root, then `cd web && npx playwright install chromium && npm run test:browser`. CI also installs browser OS dependencies.
- `make test-web` or `cd web && npm ci && npm test`, then `npm run build` (vitest with jsdom; `src/pages/Backup.test.tsx` renders the recovery screen against a stubbed status route). `web/dist` and `dist-fonts/` are build output, not committed; the build restores `dist/.gitkeep` so `go:embed` compiles on a fresh clone.

## Shared browser UI

- `src/ky-ui/` is generated from Busnes-app/ky-ui, pinned by `VERSION` file hashes. Change shared colors, navigation states and storage helpers upstream, then run its consumer sync with an explicit worktree map; do not hand-edit vendored files.
- Products own layout, routes, saved choice keys and named palettes. Busnes aliases consume shared tokens; mark primary navigation with `ky-nav-item` while preserving current-page semantics.
- Verify vendored files with `node src/ky-ui/check-vendor.mjs` from this document's directory. Builds/CI run that check. Rendered evidence and capture limitations are recorded in the repository-root `UI-VERIFICATION.md`.\n
## Child DOX Index
- [browser/AGENTS.md](./browser/AGENTS.md): Production-server browser regression harness and disposable test data.
