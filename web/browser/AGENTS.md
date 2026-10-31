# Browser regressions

## Purpose

Exercise the built embedded frontend through Chromium with the real Go server and CSP.

## Ownership

This directory owns test setup, disposable server launch and UI assertions. The parent owns Playwright configuration and CI wiring.

## Local Contracts

- Never reuse a development or production server. Launch the compiled `.browser/server` with a minimal environment and an owned temporary data directory; remove that directory on exit.
- Use loopback only, with `KY_APP_URL` matching the test origin. Bootstrap credentials are disposable test values, not deployment defaults.
- Test light/dark at 390px and 1280px, using real authentication and API state. Do not disable service workers, relax CSP/CSRF, or substitute mocked responses.
- Screenshots and failure traces live in ignored `test-results/` and CI artifacts, not production assets.

## Work Guidance

Prefer browser-native behavior and assertions over screenshot-only checks. `ui.spec.mjs` checks the login page shows no SSO divider or button when SSO is not configured, that the signed-in local admin gets an empty `/api/suite/apps` list and no app rail (the test server sets none), the boxless browser-check line and the Sign in icon, and the Appearance pairs (one radio group, the check mark on the selected segment only). `whiteboard.spec.mjs` round-trips an excalidraw.com-shaped scene (bent arrow, deleted element, image, text, background), checks self-hosted fonts, no external requests and conflict handling. `guest.spec.mjs` turns guest links on, creates a password link to a folder and opens it in a fresh cookie-less context: unlock, list, native download, `no-referrer`/`noindex` headers, no cookies, and no request URL containing the token. `previews.spec.mjs` checks the list loads an image thumbnail and type icons, then opens quick look on an image (decoded), a text file (shown as text, markup inert), a PDF (framed from the preview route) and an HTML file (No preview, nothing runs), and checks the preview's sandbox headers. `file-management.spec.mjs` covers rename, move, folder trash, restore and permanent delete; `uploads.spec.mjs` uploads a folder tree through the folder picker, drops a file whose first chunk connection is reset (the upload resumes), searches and downloads the folder zip, sharing the same saved session; it reuses session cookies saved in `test-results/file-management-session.json` to stay under the server's 20 sign-ins per minute. `launch-fixes.spec.mjs` covers the episode-8 launch bugs: uploading an existing name (Replace makes version 2, Keep both a numbered copy, Cancel nothing), a CSV's type icon, the Actions menu closing after Version history, version dates with no raw RFC 3339, the Trash heading and the Account page layout (cards inside the page padding, each radio beside its label), the search result marking the matched word in a file name, a second shared workspace with the same name (any case) refused with a readable message, and an administrator renaming a shared workspace (a taken name refused readably) and deleting it, empty, through the danger confirmation back to My files. The Council regressions additionally exercise the page-head upload file chooser, folder/Trash h1 identities, the sidebar Administration section, the Workspaces "New workspace" dialog, a single accent-filled button on the drive, checked rows moving to the trash together through the bulk bar, the Modified column and the hover-revealed borderless row menu, and the About Close button returning focus to the licence link. Share checks also require a40px square close target, guidance directly below person search before Access on phone, and aligned phone toolbar ink. Visible icon targets must be at least40px and the rendered search-field border must reach3:1 against its field/page surfaces in the Busnes light/dark projects. The shell follows ky-ui 0.7.0: content at x=272 on desktop, the icon-only phone Sign out with no name or role in the phone header, the licence line at the page end, and Account preference reachability; real Tab through every phone destination must keep its entire focus outline inside the row. `open-files.spec.mjs` changes the preference through Account and verifies persistence plus actual new-tab/in-place opening. Preview checks require40px header controls, Close sharing the title row at both widths, and unframed text. A passing suite covers these workflows, not every page or a complete accessibility audit.

## Verification

Run `npm run test:browser` from `web/` after rebuilding the embedded frontend and server as documented in the parent.

## Child DOX Index

None.
