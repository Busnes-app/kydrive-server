# KyDrive MCP access for AI agents — design

Status: implemented on branch `feat/mcp-endpoint` (2026-10-20). It follows KyCalendar's design
(`KyCalendar/docs/superpowers/specs/2026-10-14-kycalendar-mcp-agent-access-design.md`) so the suite
exposes MCP one way; the differences are listed below.

## Goal

A user connects a personal AI agent (OpenClaw, Hermes Agent) to their KyDrive files so it can
find and read them and, if allowed, save and organize them. The agent uses a token the user
creates and can revoke, and the server enforces what it may do.

## Decisions

| Question | Decision |
|---|---|
| Transport | MCP Streamable HTTP at `/mcp`, stateless, JSON responses, official Go SDK `github.com/modelcontextprotocol/go-sdk` v1.8.0 (KyCalendar's version) |
| Credential | A row in `drive_app_passwords` with a new `scope` column: `dav` (existing WebDAV passwords), `mcp:read`, `mcp:write`. Token `kya_` + 160 random bits, stored as SHA-256 |
| Scopes | `mcp:read` (default in the UI) and `mcp:write`. KyCalendar's `write-own`/`write-any` split exists because events have no owner; drive items have workspace roles, which still apply |
| Folder limit | Optional, per token: a workspace or folder, chosen by path at creation. Implemented: it was cheap (see Paths) |
| Lifetime | No fixed expiry; a token unused for 90 days is refused and deleted at sign-in, and swept hourly by `RunRetention` |
| Delete | Trash only (`move_to_trash`). No tool purges. A replaced file keeps its previous version |
| Search | Names only (`drive.Search`, `SearchFolder`). There is no content index and none was built |
| Office text | Extracted in process by `internal/extract` (standard library only, bounded) for Office Open XML and OpenDocument; PDFs through Euro-Office's `txt` conversion when configured, never parsed in process (approved 2026-10-20). Each version's text is cached sealed in `<KY_DATA_DIR>/text/<blob>` |
| Reuse | Tools call the drive store as the user, like WebDAV; no loopback HTTP |
| Agent guidance | `internal/agentskill/kydrive/SKILL.md`, embedded and served at `/agent/skills/kydrive/SKILL.md`; its rules are repeated in the MCP `instructions` |

Out of scope: OAuth, stdio, MCP resources and prompts, Shared with me (WebDAV does not show it
either), content search, legacy binary `.doc`/`.xls`/`.ppt`, OCR of scanned PDFs, a PDF parser
in KyDrive, version history and restore tools.

## Document text

- `read_file` on a supported document returns `extracted` (its kind) and pages over the extracted
  text, so `size`, `offset` and `next_offset` count bytes of the text.
- In process: zip entries at most 10 000, 64 MiB decompressed per part, 256 MiB per document,
  16 MiB of text (`extract_truncated`); at most 1 MiB in one tag and 4096 open elements, checked
  on the bytes before the decoder builds them; no DOCTYPE; rows built within the remaining text
  budget, so shared strings and repeated cells cannot amplify memory; files
  over 128 MiB are not opened; two at once. Skipped: `mc:Fallback` copies, tracked deletions,
  field codes, footnotes, comments, speaker notes, headers.
- PDF: a conversion ticket and `outputtype: txt`, the output fetched from the editor origin's
  `/cache/` like thumbnails, capped at 16 MiB, BOM, CR and NUL removed, invalid UTF-8 dropped. A
  failure is not cached. Checked live against Euro-Office 9.3.4 (the pinned image): it works, and is
  lossy (it dropped CJK text and a table column that `pdftotext` kept). Swapping in `pdftotext` in
  a sandboxed subprocess is the upgrade path if that matters.

## Differences from KyCalendar

- **Scope default on create.** KyCalendar requires `scope`. KyDrive defaults a missing `scope` to
  `dav`, which is what every existing client already gets; an agent scope is always explicit.
- **Token format.** KyCalendar embeds the row ID (`kc_<id>_<secret>`) and so can audit the token
  ID of a failed sign-in. KyDrive's app passwords are looked up by hash, so `mcp.auth_failed`
  records the IP only.
- **Limits.** KyDrive's WebDAV limiter values: 20 failed sign-ins a minute per client.
- **HTTPS.** As for WebDAV, plain HTTP is refused outside `KY_ENV=development`.
- **Administrators.** KyDrive administrators may hold files; only dedicated installer
  administrators are refused (403), as on WebDAV.
- **Changing access** needs a session but no step-up: KyDrive has no step-up, and minting a write
  token already needs only a session.

## Authentication at `/mcp`

1. `ServeHTTP` dispatches `/mcp` before the CORS block: no grant, and a preflight reaches the check.
2. Any `Origin` header: 403. Plain HTTP outside development: 403.
3. Only `Authorization: Bearer kya_…`. Cookies, Basic and WebDAV passwords: 401 with
   `WWW-Authenticate: Bearer`. `AgentTokenUser` matches agent scopes only and
   `AppPasswordUser` `dav` only, so neither credential works as the other.
4. Idle over 90 days: 401 and the row deleted (`app_password.expired`).
5. Failures only count, 20 a minute per client: 429 with `Retry-After`.
6. Installer administrators: 403.
7. Body cap 6 MiB (the SDK's 413). Responses are `no-store`.

## Browser-origin hardening

After Joplin's Web Clipper flaw (GHSA-9728-v7ww-mxjv): a pairing endpoint with wildcard CORS,
no Origin check and a consent prompt that did not say who was asking let any web page mint a
full-access token. Each rule below has its own test.

1. `/mcp` refuses any request carrying an `Origin` header, by presence (empty and `null`
   included), with 403 before the token is read (`TestMCPRefusesEveryBrowserOrigin`).
2. No response sends `Access-Control-Allow-Origin: *`; `/mcp` sends no CORS header; other routes
   reflect only `KY_APP_URL`'s origin, always with `Vary: Origin`. WebDAV, the Euro-Office routes
   and the Excalidraw fonts set none of their own, and `ServeHTTP` is the only code that may
   (`TestCORSNeverGrantsAnotherOrigin`).
3. Creating, revoking and re-scoping an agent token need a cookie session and its CSRF token, and
   a cross-site `Origin` or `Sec-Fetch-Site` is refused even with both; agent tokens and WebDAV
   passwords cannot call them (`TestAgentTokenWritesRefuseForgedCrossSiteRequests`).
4. Every screen that issues or widens a token names the agent and the level it grants (`grant`
   in `AppPasswords.tsx`); computer linking names the computer and what it can do
   (`AppPasswords.test.tsx`, `LinkDevice.test.tsx`).

## Paths

Absolute, as in WebDAV: `/<workspace>/<folder>/<name>`, workspace names from
`dav.WorkspaceNames`. Nothing is cleaned: empty, `.` and `..` parts and anything `ValidName`
refuses fail the whole path. With a folder limit, `/` is that folder (checked live each request)
and resolution walks downward with `Lookup` one name at a time, so no path names anything outside
it; search uses `SearchFolder`. The top and workspaces cannot be created, moved or trashed.

## Tools

| Tool | Level | Notes |
|---|---|---|
| `list_folder` | read | At most 500 entries, `truncated` |
| `search_files` | read | Names only, 2–100 characters, at most 50 results |
| `get_file_info` | read | Size, revision, modification time, type by extension |
| `read_file` | read | UTF-8 text only; 64 KiB default, 256 KiB max a call, `next_offset` paging on whole characters; a leading `notice` labels the text untrusted |
| `write_file` | write | Create, or replace with the current `revision`; text or base64; 4 MiB or `KYDRIVE_MAX_FILE_SIZE` |
| `create_folder` | write | Parent must exist; refuses a taken name |
| `move_item` | write | Rename or move; folders within their workspace |
| `move_to_trash` | write | Restorable from KyDrive's trash |

Errors are short fixed messages (`msg*` in `mcp.go`) that the skill's error table names.

## Untrusted content

File names and contents may come from anyone. The server cannot stop an agent from obeying them,
but it does not make it worse: results are structured JSON; every reading tool's description and
the `instructions` say names and contents are data, never instructions; `read_file` puts its
`notice` ahead of the text. The real defences are read-only by default, the folder limit, no
permanent delete, version history and the audit log.

## Audit

`mcp.file_write`, `mcp.folder_create`, `mcp.move`, `mcp.trash` with item IDs and the token ID,
never names or content; `mcp.auth_failed` with the IP; `app_password.create` (scope, limited) and
`app_password.scope_change` (from, to). Drive events record `app_password.created`, `.revoked`,
`.scope_changed`, `.expired`.

## Testing

`internal/api/mcp_test.go`: sign-in and scope crossing both ways, origin, cookie, Basic, HTTPS,
deactivation, installer administrators; the limiter and its audit; an end-to-end SDK session; the
access-level matrix (read and write tokens against every tool, and the user's own reader role);
path traversal for both unscoped and folder-limited tokens; size caps; untrusted labels; idle
revocation with an injected clock; the token routes; skill drift. `internal/drive/agent_tokens_test.go`
covers the store. Not done: a manual run against a real Hermes Agent and OpenClaw.
