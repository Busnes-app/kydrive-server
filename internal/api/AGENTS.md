# API

## Purpose
Exposes HTTP REST routes, authentication endpoints, Single Sign-On callbacks, SCIM endpoints, backup restore drill handlers, and static React PWA hosting.

## Ownership
Owns HTTP routing, request parsing, session cookie validation, CORS headers, and error response formatting.

## Local Contracts
- POST `/api/auth/change-password` accepts a restricted local session, current password and a different policy-valid new password. Browser CSRF and per-IP/account limits apply. Success revokes all sessions and requires sign-in again; flagged sessions get `password_change_required` on protected routes and public-only settings.
- All JSON API endpoints return structured errors `{"error": "message"}` upon failure.
- Non-API routes fall back to serving `web.Handler()` for client-side SPA routing.
- New routes are unauthenticated only by deliberate choice; privileged ones are registered wrapped in `s.requireAdmin` in `routes()`, so the trust level of every route is readable in one place.
- Backup routes and theme writes are admin-only: capsules and settings carry site data and secrets. The scaffold has no step-up; admin-only plus `TestPrivilegedEndpointsRequireAdmin` is its equivalent for every destructive backup route. Routes are registered with method patterns, and because the SPA catch-all answers any method, tests pin that a wrong method never reaches a backup handler rather than expecting 405.

| Method | Path | Handler | Response |
|---|---|---|---|
| POST | `/api/backup/drill` | `handleBackupDrill` | `recoveryclient.DrillResult`; 409 when another HTTP/CLI drill holds the data-directory lock |
| POST | `/api/backup/export-capsule` | `handleExportCapsule` | `.kycap` attachment; POST so the CSRF check covers it |
| POST | `/api/backup/pair-remote` | `handlePairRemoteRecovery` | `{recovery_key_id, threshold, total_shares}` |
| POST | `/api/backup/deposit` | `handleRunBackup` | `recoveryclient.Result` (+`receipt_unrecorded`) |
| DELETE | `/api/backup/pairing` | `handleUnpair` | `{paired:false}`; URL and token rows only, key pin stays |
| POST | `/api/backup/pin-key` | `handlePinKey` | write-once; 409 on a different key |
| PUT | `/api/backup/schedule` | `handleSetSchedule` | `{interval_sec}` read back from the store |
| GET | `/api/backup/status` | `handleBackupStatus` | pairing, key, local copies, schedule, members, `database_driver`; never the token |

- `POST /api/backup/deposit` is one `recoveryclient.Run`: seal once, deliver to the local directory and to KyRecovery when paired. 412 no key, key pin missing, no destination, no database snapshot, or a private destination with `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` off; 409 key mismatch or a run in flight; 413 over the capsule caps; 502 when KyRecovery refused (`recoveryclient.ErrRemote`, naming a local copy that was written, so the `ErrPrivateDestination` arm must stay above it: the lib wraps both on the dial path); 500 for a failure before a byte left; 200 with `receipt_unrecorded` when the store holds the capsule but the receipt was not written. It runs on a context detached from the request with a 16-minute write deadline; the acting admin is resolved before the upload and the audit row is written on that same detached context.
- The write-once, irreversible backup handlers (`handlePairRemoteRecovery`, `handlePinKey`, `handleRunBackup`) run on `context.WithoutCancel(r.Context())` so a dropped connection cannot leave a pin, a pairing or a deposit half-written with no audit row; the idempotent ones (`handleSetSchedule`, `handleUnpair`) stay on the request context. Their routes are registered as `s.tracked(s.requireAdmin(s.handleX))`, so the `detached` counter is incremented the moment `ServeHTTP` dispatches -- before `requireAdmin`'s session lookup, which is itself a store round-trip that `ReadTimeout` (15s) lets outlast `shutdownTimeout` (5s). Registering inside the handler was too late: `Shutdown` returns after its timeout with requests still active, and one still in the auth lookup would leave `WaitDetached()` reading zero and the store closing under a request about to pin a key. The header-read window before `ServeHTTP` is entered cannot be covered by any counter, because no handler goroutine exists yet; `Shutdown`'s own drain is what covers it. The counter is a mutex and a `sync.Cond`, not a `sync.WaitGroup`, which panics when an `Add` from zero races an in-progress `Wait` -- two admin requests at SIGTERM do exactly that. `WaitDetached()` is what `cmd/server` blocks on before closing the store, because `http.Server.Shutdown` returns without knowing these goroutines exist.
- Audit actions: `backup.paired`, `backup.pair_failed`, `admin.backup_run` (details start with `outcome="success|failure"`), `admin.backup_unpair`, `admin.backup_key_pin`, `admin.backup_schedule`, `admin.backup_export` (a downloaded capsule, resource the capsule ID). `AuditDetails` flattens the lib's details map into the bounded audit field with locally derived fields first and remote error text last; values are quoted and `=` is escaped before the final `AuditSafe` cut. `cmd/server` uses it for the scheduler and CLI rows. Details carry key or capsule IDs, digests and paths, never the token.
- Rate-limit keys for `login:`, `mfa:` and `pair:` come from `auth.ClientIP`, never from `RemoteAddr` or a raw header, so a limit is neither shared by everyone behind a proxy nor bypassable by forging `X-Forwarded-For`.
- CORS permits only the exact configured `KY_APP_URL` origin and credentialed browser writes require matching CSRF cookie/header tokens.
- API request bodies are capped at 1 MiB and all responses receive baseline CSP, anti-framing, MIME-sniffing, and referrer-policy headers.
- `GET /api/settings` tiers its payload: public fields for the login screen, `db_driver`/`scim_enabled` for any session, and `extra_settings` for admins only; KyRecovery tokens are omitted in both sealed and legacy plaintext forms, dropped by the `kyrecovery_token` key prefix rather than by literal key name.

- `/api/drive/*` routes use a validated session or workspace-scoped service token; admin-only routes do not accept service tokens. Middleware places the validated actor in request context; domain code rechecks current membership inside mutations. Uploads stream with a 128 MiB cap; other request bodies stay capped at 1 MiB.
- `GET /api/drive/files/{id}` returns file metadata (`id`, `workspace`, `parent`, `name`, `size`, `revision`, `editable`) for authorized workspace members.
- `/api/editor/download/{session}` uses only a hashed, expiring editor credential and current membership. `/api/editor/callback` trusts the verified JWT payload, restricts output origin/path, publishes via the persisted editor revision pointer and acknowledges exact retries.
- Euro-Office editor integration routes `docx`, `odt`, `txt`, `rtf`, and `md` to `documentType: word`, `xlsx`, `ods`, and `csv` to `documentType: cell`, `pptx` and `odp` to `documentType: slide`, and `pdf` to `documentType: pdf`.
- `/editor.html` scopes its CSP to the configured Euro origin and uses a same-origin bootstrap script. Operational status exposes pending editor revocations without credentials.

- `GET /excalidraw/fonts/Xiaolai/` serves files from `<KY_EXCALIDRAW_FONTS_DIR>/Xiaolai` only, unauthenticated like the embedded fonts, with no directory listings; unset returns 404 rather than the SPA shell. `excalidraw_fonts_test.go` pins confinement.

- POST `/api/drive/personal-workspace` idempotently ensures the authenticated account’s private workspace; service credentials are refused. GET workspace lists distinguish `kind: personal|shared` and exclude other accounts’ personal workspaces, even from administrative browsing.
- POST `/api/drive/workspaces/{id}/documents` accepts `{name, kind: document|spreadsheet|presentation|markdown|rtf|whiteboard, parent}`, adds the corresponding extension (`.docx`, `.xlsx`, `.pptx`, `.md`, `.rtf`, `.excalidraw`), and publishes a bundled blank template through the normal authorization/quota/version path. Templates are product-owned assets under `templates/`; DOCX uses minimal OOXML, XLSX/PPTX originate from the selected Euro-Office local blank fixtures with cleared core metadata, MD/RTF/Excalidraw use standardized blank formats.

## Verification
- `go test -v ./internal/api/...` (`authz_test.go` pins the per-role exposure of every privileged route; `backup_test.go` the backup routes, on SQLite only because a run snapshots the database)
- `scripts/smoke-test.sh` asserts the same boundaries against a running binary

## Child DOX Index
- `templates/` — blank Office Open XML, Markdown, RTF, and Excalidraw creation assets and provenance.
