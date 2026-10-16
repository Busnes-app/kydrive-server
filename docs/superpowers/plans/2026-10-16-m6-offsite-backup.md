# M6 Offsite Bulk Backup Implementation Plan

> **For agentic workers:** execute task by task, failing test first. One PR. Leaver handover, the other half of M6, shipped in #22.

**Goal:** a fresh host restores from the offsite repository plus a custodian-opened capsule with every blob verified.

**Spec:** `docs/superpowers/plans/2026-10-07-onedrive-parity-roadmap.md` M6 "Offsite bulk backup". Contracts: `AGENTS.md`, `internal/backup/AGENTS.md`, `docs/RESTORE.md`, `docs/PILOT.md`.

## Decisions

1. **Repository locations.** `KYDRIVE_BULK_BACKUP_REPOSITORY` is a local path outside the data directory (today), or `s3:https://host/bucket[/prefix]` or `rest:https://host[:port]/path`. Every other scheme, `s3:` without an explicit `https://`, plain HTTP and credentials inside the URL are refused at use. Plain HTTP is allowed only with `KY_ENV=development`, for local tests.
2. **Credentials come from a file.** `KYDRIVE_BULK_BACKUP_CREDENTIALS_FILE` is `KEY=VALUE` lines, read on each use and passed only to the `restic` child. Allowed keys: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_DEFAULT_REGION`, `RESTIC_REST_USERNAME`, `RESTIC_REST_PASSWORD`; any other key is refused, so the file cannot inject environment such as `LD_PRELOAD` into restic. The file must not be readable or writable by other users. The server's own `RESTIC_*` and `AWS_*` environment never reaches restic, so stray host credentials cannot redirect it. Reading per use rather than at start-up lets credentials rotate without a restart.
3. **The capsule carries what restore needs.** `config/integrations.json` gains `KYDRIVE_BULK_BACKUP_REPOSITORY` and `KYDRIVE_BULK_BACKUP_CREDENTIALS` (the file's allowed lines). `bulk-restore` takes them from the opened capsule when `-repo` is not given; the restore drill always uses the capsule's, so a passing drill proves a fresh host needs only the capsule and the network. Capsules from before this fall back to the environment.
4. **Weekly integrity check.** `CheckBulkIfDue` runs `restic check --read-data-subset=5%` after a successful scheduled deposit, at most every 7 days, under `bulk.lock` (busy: skipped until the next deposit), recorded in `bulk_last_check` and audited as `system`. The Recovery screen shows the repository (it holds no secret), the last check and its result.
5. **Delete-proof repositories are recommended, not required.** README and `docs/RESTORE.md` document rest-server `--append-only` offsite and S3 with versioning and Object Lock. Prune already reports such repositories' refusal; the check and deposits work against them.

## Review Focus

1. Credentials: never in a URL, a log line, an error message, a status response or the server's environment; only in the restic child and the sealed capsule.
2. Repository parsing: no scheme, host or path trick reaches restic outside the three accepted forms.
3. Restore from the capsule alone, against the real rest-server (append-only) and S3.

---

- [ ] `TestBulkRepositoryLocations`, `TestBulkCredentialsFile` (allowed keys, unknown key, mode, comments), `TestResticEnvironment` (server `AWS_*`/`RESTIC_*` dropped, file credentials passed).
- [ ] `repository.go`: `Repository{URL, Env}`, parsing and credentials; `runRestic` takes a `Repository`.
- [ ] Capsule: integrations fields; `RestoreBulk` and the drill use the capsule's repository.
- [ ] `CheckBulk`, `CheckBulkIfDue`, scheduler hook, status route, Recovery screen line.
- [ ] `scripts/accept-offsite-backup.sh`: real rest-server `--append-only` and MinIO; init, deposit, drill, check, prune refused on append-only, restore into a fresh directory from the capsule.
- [ ] Docs: README, `docs/RESTORE.md`, `docs/PILOT.md`, root `AGENTS.md`, `internal/backup/AGENTS.md`, roadmap status.
