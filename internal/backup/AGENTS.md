# Backup

## Purpose
Adapts the scaffold to `github.com/Busnes-app/ky-primitives/recoveryclient`, which owns the
KyRecovery pairing, sealing, deposit, restore and drill contract. This package supplies only
what differs per product: a `Settings` adapter over `store.SettingsStore`, a `Sealer` under the
deployment key, the payload the scaffold seals (`Collect`), and the drill's verification
checks (`Checks`).

## Ownership
Owns the settings adapter (`settings.go`), payload collection (`payload.go`), and restore-drill
checks (`drill.go`), serialized drill entry point and locks (`run_drill.go`), Restic bulk binding and pruning (`bulk.go`) and the bulk retention policy (`retention.go`). It holds no private key, no share, and no pairing state of its own — those
live in `recoveryclient` and in the settings rows it reads and writes through the adapter.

## Local Contracts
- `Settings` maps `store.ErrNotFound` to `recoveryclient.ErrNotFound`; every other error passes
  through unchanged.
- `NewSealer` seals the KyRecovery token under the deployment key with label
  `kydrive-server:setting:kyrecovery_token`, domain-separated so a row copied from another
  setting will not open.
- `Collect` snapshots SQLite with the lib's `SQLiteSnapshot` (`VACUUM INTO`; the store runs in
  WAL mode, so a plain file read misses uncheckpointed commits) and returns
  `ErrNoDatabaseSnapshot` for any other driver, so a capsule without a consistent database is
  never sealed. It also carries the encryption key (`data/encryption.key`, required — restores
  a database whose MFA secrets are gone otherwise) and the pinned recovery public key
  (`data/recovery.pub`, only when paired).
- `Checks(dir, opened)` reads the opened capsule's manifest, normalizes JSON lists and
  fails malformed or incomplete recipes. Required files include all capsule members and
  the database, settings and encryption key; SQLite integrity and required environment
  checks cannot be disabled. File checks accept only clean relative manifest members;
  SQLite opens read-only and missing/empty databases fail.
- HTTP and CLI call `RunDrill`, which holds an OS advisory lock on `<data dir>/drill.lock`
  across scratch preparation and the library drill. Contention returns `ErrDrillBusy`;
  closing the descriptor or process exit releases ownership. Keep the lock file in place.
  The Unix lock matches the Linux container deployment.
- `DrillRoot` is `Backup.DrillDir` (`KY_DRILL_DIR`, default `<data dir>/drill`) and forced to 0700; `RunDrill` bounds the whole drill, bulk restore included, by `Backup.DrillTimeout` (zero, in literal configs, is unbounded); opened payloads stay in the
  library's private, disposable subdirectories. Drills use throwaway keys, not custodian shares.
- `Members` names what `Collect` would seal now, for the status route and the screen; keep
  the two in step.
- `GET /api/settings`'s `extra_settings` never carries `kyrecovery_token_enc` or a legacy
  plaintext `kyrecovery_token`; the filter drops every key with the `kyrecovery_token`
  prefix, so a rename in the lib cannot leak. An admin sees the paired `kyrecovery_url`,
  never the credential.
- Pairing, the write-once key pin, `Run` (one seal, every destination), the schedule, local
  copies and their pruning, drill mechanics, restore and the decrypt guard are the lib's;
  their contracts are in the `recoveryclient` README. `client_test.go` pins only what this
  package's wiring buys: `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` admits RFC1918 and CGNAT and
  nothing else.

- `Collect` also seals `config/integrations.json` with session, OIDC, SCIM and editor secrets. Keep this member out of responses and logs.
- A drive with versions requires `KYDRIVE_BULK_BACKUP_REPOSITORY` outside its primary data directory. Restic encrypts/deduplicates the exact blobs referenced by the SQLite snapshot; `data/drive-bulk.json` binds snapshot ID, blob UUIDs, sizes and SHA-256 digests to the sealed capsule.
- `drive-bulk.json` is version 2: each file carries `sealed`, and a sealed blob is checked at `drive.SealedSize` and decrypted to verify its plaintext digest. `RestoreBulk` accepts version 1 (plaintext blobs only) and version 2; restore decrypts with the key the capsule carries.
- `RestoreBulk` compares manifest references with the restored database and authenticates/verifies every file before publishing a new blobs directory. Drills perform this restore in scratch space. Before calling restic it refuses when the target's filesystem (`statfs`, `freeSpace` seam) has less than the manifest's on-disk bytes plus 10% and 64 MiB free.
- `Collect` bounds itself by `Backup.CollectTimeout` (recoveryclient calls the collector without a context); restic runs under `CommandContext`, so the timeout kills it. The HTTP deposit collects on `Server.collecting` and the scheduler on the server context: shutdown cancels a deposit still collecting (nothing has left the server), so `backupWaitTimeout` covers only sealing and upload.
- `CollectBulk` records verified blobs in `<data dir>/bulk-verified.json` (blob row plus file mtime). A blob whose row and mtime match is only size-checked; others are decrypted and hashed. The whole set is re-verified when the record is missing, unreadable or its full pass is over 7 days old. A failure to save the record is logged.
- Bulk snapshots are tagged `kydrive` plus the `Collect` purpose (`deposit`, `drill`, `export`); older ones carry only `kydrive`.
- `PruneBulk` is opt-in, off by default, never forgets the newest KY_BACKUP_KEEP deposit snapshots. KyRecovery-held capsules older than the policy may name forgotten snapshots: set the policy to outlive KyRecovery's retention. An append-only rest-server or Object Lock repository refuses prune by design. It runs three `restic forget --group-by ''` passes, then always `prune` (so a run killed after forget is finished by the next): deposits keep the policy plus `--keep-last KY_BACKUP_KEEP`; every non-deposit snapshot outside the policy horizon (max of days, 7×weeks, 31×months) goes, except the newest KY_BACKUP_KEEP of any kind, which covers untagged ones a retained local copy names; drill snapshots go after a day. Every drill writes one (the manifest it restores must name a snapshot of the live database); deduplication makes it metadata-sized, and with pruning off it stays until the policy is turned on. Restic's `--keep-daily` counts days that have snapshots, so per-tag grouping would never age out legacy or rare export snapshots.
- `PruneBulkIfDue` (scheduler, after a successful scheduled deposit) runs it at most once per 24 hours and records every attempt, success or failure, in `bulk_last_prune`. The policy is the `bulk_retention` setting; all zero is off.
- The Restic password is `hex(DeriveKey(deployment key, "kydrive:restic"))`; `bulk-init` uses it. Repositories initialized before it use `hex(deployment key)`. `CollectBulk` and `PruneBulk` call `openRepository` first: when the raw key still opens the repository it adds the derived key (`key add --new-password-file /dev/stdin`, opened with the raw key), then removes the raw key's ID with the derived password; reruns finish an interrupted move, other keys are untouched, and a failed removal is logged, not fatal. `RestoreBulk` (CLI and drill) never changes keys: it tries the derived password and falls back to the raw key when restic reports a wrong password (exit 12 from 0.17; exit 1 with "wrong password or no key found" in 0.16, which CI installs). Both derive from the raw key the capsule carries.
- `<data dir>/bulk.lock` serializes bulk repository use across processes: `CollectBulk` and `RunDrill` (around the drill's bulk restore) wait for it; `PruneBulk` returns `ErrBulkBusy` and the prune is skipped until the next deposit. `restic` failures carry the tail of its stderr.

## Verification
- `go test -v ./internal/backup/...` covers decoded seal/open checks, malformed recipes,
  subprocess lock contention/exit, scratch cleanup and the synthetic KyDrive pairing fixture
  in `testdata/pairing-kydrive.json`. The fixture uses a 32-byte 0x01 deployment key and retains
  no recovery private key.
- `repokey_test.go` pins the key move against a fake runner (`runRestic` seam), including interruption after add and during add; `TestBulkRawKeyRepositoryIsMoved` checks it with real Restic.
- `bulk_test.go` also pins that an unchanged blob is not re-read until the weekly full pass, that `KY_BACKUP_COLLECT_TIMEOUT` kills a hung `restic` (a fake on PATH) and that a restore without free space is refused before publishing.
- `bulk_test.go` needs `restic` on PATH (it skips otherwise; run with `-v` and confirm it did not skip). It pins that a purged blob
  leaves the repository after prune (`restic list blobs`, `check --read-data`), the newest capsule still restores, an off policy
  forgets nothing, legacy and drill snapshots age out, and `bulk.lock` contention.

## Child DOX Index
None.
