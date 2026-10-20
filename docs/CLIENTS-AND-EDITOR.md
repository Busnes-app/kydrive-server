# Clients and editor

[Product overview](../README.md) · [Standalone installation](../INSTALL.md)

Commands and unlinked file paths below are relative to the repository root.

## Previews

Clicking a file that has no editor (images, PDFs, audio, video, plain text) opens a quick look in the browser; every file's menu also has Preview. What the file actually contains decides whether it renders, never its name: PNG, JPEG, GIF, WebP, PDF, MP3, WAV, Ogg, MP4, WebM and UTF-8 text are shown, and everything else, HTML and SVG included, offers Download instead. Text shows its first 256 KiB as plain text. Previews are served sandboxed (`Content-Security-Policy: sandbox`, `nosniff`), so nothing in a file runs as KyDrive, and need the same access as a download.

The file list shows a type icon for every file and a thumbnail for PNG, JPEG, GIF and WebP images and, when Euro-Office is configured, for the first page of Office documents (DOCX, ODT, RTF, XLSX, ODS, CSV, PPTX, ODP) and PDFs. Documents are rendered by the Document Server from a one-time link to that file alone, and its picture is checked like an upload. Thumbnails are made on the server once per version, encrypted on disk like the files themselves under `<KY_DATA_DIR>/thumbs`, left out of backups, and safe to delete. Images over 64 MiB, over 16384 pixels a side or too large to decode in 256 MiB keep their icon.

## WebDAV

Workspaces can be opened in file managers and office apps at `<KY_APP_URL>/dav/`. Each user creates an **app password** on their Account page; it is shown once, works only for WebDAV, and can be revoked on its own. Sign in with your KyDrive username and the app password. Deactivating an account stops all of its app passwords on the next request.

- **Windows:** File Explorer → *Map network drive* → `https://drive.example.com/dav/`. Windows refuses downloads over 50 MB by default; raise `HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters\FileSizeLimitInBytes` (up to `4294967295`) and restart the WebClient service.
- **macOS:** Finder → *Go* → *Connect to Server* → `https://drive.example.com/dav/`.
- **Linux:** `davs://drive.example.com/dav/` in Files (GNOME) or `webdavs://drive.example.com/dav/` in Dolphin (KDE).

For a cached drive, an offline folder (rclone) or Cyberduck, see [Desktop access](DESKTOP-ACCESS.md).

The top level lists your workspaces by name; two with the same name show as `Name` and `Name (2)`. Workspaces cannot be created, renamed or deleted over WebDAV. Deleting a file or folder moves it to the trash. Saving over a file creates a new version; with `If-Match` a stale save is refused (412), otherwise the last save wins and the earlier one stays in the history. A `PUT` may carry `X-OC-Mtime` (Unix seconds, as rclone's `owncloud` vendor sends it) to keep the file's own modification time. Uploads are capped at `KYDRIVE_MAX_FILE_SIZE` and one `PUT` must finish within `KY_READ_TIMEOUT`.

WebDAV requires HTTPS outside `KY_ENV=development`. Behind a TLS-terminating proxy, list the proxy in `KY_TRUSTED_PROXIES` so its `X-Forwarded-Proto` is believed, and let it pass request bodies up to `KYDRIVE_MAX_FILE_SIZE`. Failed WebDAV sign-ins are limited to 20 a minute per client and 10 per 15 minutes per username. Locks are held in memory and lost on restart; clients take them again.

## Linked computers

The KyDrive desktop app ([kydrive-desktop](https://github.com/Busnes-app/kydrive-desktop), in development) keeps chosen workspaces in a local folder. Choosing **Link** in the app opens KyDrive in the browser; after the usual sign-in, the user approves the computer by name. The app receives its own credential, kept in the computer's keychain, and never sees a password. Shared with me is not synced yet.

- **Account → Computers** lists linked computers with their last use, at most 20 per user. **Revoke** stops one at once. Its dialog offers *Also remove KyDrive files from this computer*: the files go the next time the computer reaches KyDrive, edits that never uploaded move to an `Unsynced` folder, and a computer offline for 30 days is never told. It is not a guaranteed remote wipe.
- **Settings → Computers** (administrators) sets whether that box starts checked, on by default, and can lock it with *Users cannot change this*.
- A computer's credential syncs files only: it cannot share, create links, open the editor, change grants or create other credentials. It stops when the account is deactivated and after 90 days unused.

## AI agents

AI agents reach a user's files over MCP (Streamable HTTP, stateless) at `<KY_APP_URL>/mcp`, with
a Bearer token the user creates under **Account → App passwords → AI agent**. The token is shown
once with copy-ready OpenClaw and Hermes Agent config that reads it from `KYDRIVE_TOKEN`; the
KyDrive skill is served publicly at `/agent/skills/kydrive/SKILL.md`.

- **Access levels.** *Read only* (default) offers `list_folder`, `search_files` (names only; there
  is no full-text index), `get_file_info` and `read_file`. *Read and write* adds `write_file`,
  `create_folder`, `move_item` and `move_to_trash`. The level can be changed later on the same
  page. Every operation is still authorized as the user, so a token never exceeds their role.
- **Folder limit.** A token may be limited to one folder or workspace, chosen by path when it is
  created. That folder is then the agent's `/`; paths resolve downward from it one name at a time,
  so nothing outside it can be named. If the folder is trashed, the agent is told so.
- **Paths** are absolute, as in WebDAV: `/<workspace>/<folder>/<name>`, with workspace names as
  WebDAV shows them. Empty, `.` and `..` parts, backslashes and control characters are refused,
  never cleaned.
- **Nothing is deleted for good.** `move_to_trash` uses the trash; `write_file` replaces a file
  only when given its current revision, and the old content stays in the version history.
  Workspaces cannot be created, renamed or trashed.
- **Text only.** `read_file` returns UTF-8 text and refuses anything else, Office documents and
  PDFs included: KyDrive has no text extractor. Its result labels the text as untrusted content.
- **Limits.** `read_file` returns at most 256 KiB a call (64 KiB by default) and pages with
  `next_offset`; `list_folder` at most 500 entries; `search_files` at most 50 results;
  `write_file` at most 4 MiB (or `KYDRIVE_MAX_FILE_SIZE`, if lower); a request body at most 6 MiB.
- **Sign-in.** Only `Authorization: Bearer kya_…`; cookies, Basic and WebDAV passwords are
  refused, and an agent token is refused by WebDAV. Plain-HTTP requests are refused outside
  `KY_ENV=development` (as for WebDAV).
- **Browsers are refused.** Agents are not browsers, so any request carrying an `Origin` header,
  whatever its value, gets 403 before the token is read, and `/mcp` sends no CORS headers at all.
  No KyDrive route ever sends `Access-Control-Allow-Origin: *`; the rest of the API reflects only
  `KY_APP_URL`'s own origin. Creating, revoking or changing an agent token needs a signed-in
  session and its CSRF token, and a cross-site request is refused even with both, so a web page
  cannot mint a token for itself (the flaw behind Joplin's GHSA-9728-v7ww-mxjv). The screens that
  issue a token or widen its access always name the agent and the level it gets. Failed sign-ins are
  limited to 20 a minute per client and audited as `mcp.auth_failed` without the token.
  Installer administrators cannot use agents. Writes are audited as `mcp.file_write`,
  `mcp.folder_create`, `mcp.move` and `mcp.trash` with item IDs, never names or content.
- **Lifetime.** A token unused for 90 days is refused and deleted when presented, and the hourly
  retention pass removes the rest. Deactivating the account stops it on the next request.

## Editor

Set `KYDRIVE_EDITOR_URL` to the browser-visible HTTPS Euro-Office origin and `KYDRIVE_EDITOR_SECRET` to the same JWT secret supplied to Euro-Office. `KYDRIVE_EDITOR_DRIVE_URL` defaults to `KY_APP_URL`; the editor must resolve and reach it for authorized downloads and signed callbacks. Optional `KYDRIVE_EDITOR_INTERNAL_URL` changes server-to-editor transport, while output URLs must still name the configured public origin. No redirects are followed. HTTP origins are accepted only with `KY_ENV=development` for local testing.

DOCX/ODT/TXT use the document editor; XLSX/ODS/CSV use the spreadsheet editor; PPTX/ODP use the presentation editor; PDFs open in the PDF editor from the file menu. Other files open in quick look or download. Browser configuration, callbacks and commands use HS256 JWT. The download URL names one editor session and carries no credential: Euro-Office must sign the request with the shared secret for that exact URL, and the session must be unrevoked, unexpired and backed by current membership. The signed browser configuration expires after 10 minutes. A force-saved document keeps its key while it stays open, so later co-editors join it. Saved output is restricted to the editor origin's `/cache/` paths, capped at 128 MiB and committed as an immutable version. Stale saves fail with a conflict; replayed identical output is acknowledged without another version. Force-save sequences use a persisted revision pointer.

Password change or reset, sign-out, MFA changes, deactivation, deletion, membership loss, permission downgrade, trash and session expiry queue durable revocations; an account revocation also gives the affected files a new document key. Expiry waits until Euro-Office closes the document (at most 7 days past expiry), so a connected editor is not dropped mid-edit. The worker scans every two seconds and retries failed Euro-Office `drop` commands with backoff up to an hour. The administration UI shows pending commands. Revoked users lose online editing and file access; plaintext already delivered to a browser cannot be recalled. Euro-Office processes plaintext. This is not CryptPad-style client-side encryption.

The local image under test is Euro-Office `v9.3.4-hotfix.1`, amd64 digest in `deploy/render.py`. The documented `9.3.1` tag was unavailable. Its runtime carries AGPLv3 and inherited ONLYOFFICE strings. Release workflow and BuildKit provenance bind the selected amd64 digest to its Euro-Office source commit. This establishes observed build correspondence; an independently reproducible rebuild is not claimed. See [provenance evidence](EURO-PROVENANCE.md) and [pilot gates](PILOT.md).
