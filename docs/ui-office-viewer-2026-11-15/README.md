# Viewing copy in Euro-Office — 2026-11-15

`kydrive-viewer-{desktop,phone}-{light,dark}.png`: `/view.html?copy=<id>` for a viewing copy of a
generated `Q3 site plan.docx`, captured with Playwright (Chromium, 1280×900 and 390×844, OS light
and dark) after a real local sign-in. The server was a disposable test server from this branch
with four copies stored through `IssueViewTicket`/`StoreViewCopy` (no live KyPost), and Euro-Office
was the pinned `ghcr.io/euro-office/documentserver@sha256:beca380d…` image on localhost. Euro-Office
opens in view mode: File and View menus only, no editing toolbar. Its collaboration-name prompt
appears because the disposable account has no display name; Euro-Office draws its own UI and does
not follow KyDrive's theme.
