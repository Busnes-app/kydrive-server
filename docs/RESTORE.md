# Restore KyDrive

A complete restore requires a sealed KyDrive `.kycap`, its independent Restic repository and k custodian cards. Metadata alone is insufficient. Do not start the restored drive until every referenced blob has been verified.

1. Stop the destination drive. Preserve its previous data directory and image digest for rollback. Choose an empty, private restore directory.
2. Download the selected capsule through a KyRecovery operator session. Compare its ID/timestamp/digest with KyRecovery's record and the product's saved receipt; the product token cannot list or download capsules.
3. Run the product restore command. Pass only the paths and expected service name in arguments; enter k custodian shares on stdin when prompted. Do not put shares in arguments, files, shell history or environment variables.

   ```sh
   kydrive-server restore -capsule /private/KyDrive.kycap -to /private/opened -service KyDrive
   ```

   Restore automatically invalidates every restored browser session and MFA challenge, DAV and AI-agent password, desktop device/link code, service credential, organisation/guest link and editor session, and discards unfinished uploads. User accounts, file ownership, group membership, direct shares and file versions remain. Users sign in and recreate the access credentials and links they need. Editor keys receive a new random epoch so an old editor cannot reconnect to the restored document.

   If extraction succeeds but the access reset fails, the command exits with an error: keep the server stopped and repair the reported cause, then run `kydrive-server restore-reset -to /private/opened`. This command can be repeated and never generates a replacement deployment key or a missing database. Restore targets containing `?` or `#` are refused because SQLite interprets them as URI delimiters.

   The library checks the service name before combining shares and verifies capsule integrity/key binding before extracting. `opened/data/ky_server.db` is the consistent database, `encryption.key` restores the deployment encryption key, and `recovery.pub` restores the public key pin when present. Protect the opened directory: it also contains `config/integrations.json` with OIDC, SCIM, session and editor secrets.

4. Restore the exact bulk snapshot named in the verified capsule. The destination `blobs` directory must not exist. Missing blobs, wrong keys, hash differences and an incomplete database/manifest binding fail the command.

   ```sh
   kydrive-server bulk-restore -opened /private/opened -to /new-drive/data/blobs
   ```

   Without `-repo`, the command uses the repository and credentials the capsule sealed in `config/integrations.json` (`KYDRIVE_BULK_BACKUP_REPOSITORY`, `KYDRIVE_BULK_BACKUP_CREDENTIALS`), checked as a credentials file is: an offsite `s3:https://` or `rest:https://` repository needs only network access from the new host. Pass `-repo /path` to read a copy elsewhere, such as a local mirror of the offsite repository; `-repo` uses no credentials. Capsules sealed before they carried the repository require `-repo`.

   The Restic password is derived from the capsule's deployment key (`kydrive:restic`); a repository from before that derivation still opens with the raw key, and `bulk-restore` tries both without changing the repository's keys. The next backup moves it to the derived password.

   If the capsule contains no `data/drive-bulk.json`, verify that its database has no `drive_versions` rows; an empty drive has no bulk snapshot to restore. Never silently accept a missing manifest for a database with files.

5. Copy `opened/data/ky_server.db`, `encryption.key` and (when present) `recovery.pub` into the new drive data directory, mode 0600 and owned by the runtime UID/GID. Blobs and file and folder names are encrypted under that key; a missing or different key leaves every file and name unreadable; supplying it as `KY_ENCRYPTION_KEY` from a secret instead of the file keeps it out of the data directory (README, Encryption at rest). Preserve `config/integrations.json` securely; reconstruct runtime configuration from it, keeping the original session/editor/SCIM/client secrets. Set database driver `sqlite`, the new persistent paths and the independent bulk mount. Change origins only deliberately and update the OIDC callback registration and editor integration together.
6. Restore or relocate the retained local capsules. The key pin and sealed KyRecovery token live in the restored files/database. Do not pair to another key to work around a failed pin. Unpairing deletes only URL/token and requires a separate KyRecovery administrator to revoke the old token.
7. Before exposing the restored server, reconcile accounts and permissions with current KyIdentity state: removals and changes after the backup are absent from its snapshot. Reapply local password/MFA changes made since the backup, using the existing operator reset procedure if the old audit record is unavailable; preserved password hashes and recovery codes are not evidence that a credential remains trusted. Restore does not rotate encryption or integration keys.
8. Start the same tested image digest. The content search index and the thumbnail and document text caches are not in backups; the drive rebuilds them in the background, so content search finds files only as they are reindexed (search shows how many of a user's files are pending). Verify workspace permissions, quotas, versions and trash; download files and open one in the editor. Then run `backup-drill` or the Recovery screen's drill; it restores the whole bulk snapshot into `KY_DRILL_DIR` (default `<data dir>/drill`) and refuses to start without the snapshot's size plus 10% and 64 MiB free there, so point it at a scratch volume when the data volume is tight. `bulk-restore` applies the same check to its destination. Do not mark the service recovered unless both metadata and bulk checks pass.

For an upgrade, stop the only writer, take a successful complete backup, retain the previous image, then start the new image against the persistent mounts. For rollback, restore the matched database/blob checkpoint and its integration secrets together; do not assume an older binary understands a newer schema. This package has no HA or destructive migrations.

Retain every Restic snapshot referenced by a retained capsule. Bulk pruning is opt-in, off by default, never forgets the newest KY_BACKUP_KEEP deposit snapshots. KyRecovery-held capsules older than the policy may name forgotten snapshots: set the policy to outlive KyRecovery's retention. An append-only rest-server or Object Lock repository refuses prune by design; prune it from a trusted machine with credentials that may delete, never from the drive host. With pruning off, purged and retention-expired files remain in older snapshots until those snapshots are retired. A capsule naming a forgotten snapshot fails the bulk restore; restore from a newer capsule. Backup retention, independent destination and recovery objectives must be chosen for the actual deployment; the local fixture establishes mechanics, not a representative production restore-time objective.
