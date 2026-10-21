# KyDrive UI beauty pass: screenshot evidence

Evidence for the `feat/ui-beauty-pass` pull request only; this branch holds no code and is not
meant to be merged.

- `before/` is master `883538f`, and `after/` is `feat/ui-beauty-pass`. Each page is captured in light and dark, at 1280, 1920 and 390 px wide (`<page>-<scheme>-<width>.png`).
- `grid/<page>.jpg` shows before (top row) against after (bottom row).
- **Fixture:** a throwaway local server with fictional `lgtm.llc` data (Alex Morgan, Sam Rivera, the Studio and Finance workspaces), on loopback only and deleted after each run.
- **Browser:** Chromium (Playwright 1.64.1), with `drive.lgtm.llc` treated as a secure origin so the sign-in check runs as it would over HTTPS.
