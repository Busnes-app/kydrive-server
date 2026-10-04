# Restore KyDrive

A complete restore requires a sealed KyDrive `.kycap`, its independent Restic repository and k custodian cards. Metadata alone is insufficient. Do not start the restored drive until every referenced blob has been verified.

1. Stop the destination drive. Preserve its previous data directory and image digest for rollback. Choose an empty, private restore directory.
2. Download the selected capsule through a KyRecovery operator session. Compare its ID/timestamp/digest with KyRecovery's record and the product's saved receipt; the product token cannot list or download capsules.
3. Run the product restore command. Pass only the paths and expected service name in arguments; enter k custodian shares on stdin when prompted. Do not put shares in arguments, files, shell history or environment variables.

   ```sh
   kydrive-server restore -capsule /private/KyDrive.kycap -to /private/opened -service KyDrive
   ```

   The library checks the service name before combining shares and verifies capsule integrity/key binding before extracting. `opened/data/ky_server.db` is the consistent database, `encryption.key` restores the deployment encryption key, and `recovery.pub` restores the public key pin when present. Protect the opened directory: it also contains `config/integrations.json` with OIDC, SCIM, session and editor secrets.

4. Restore the exact bulk snapshot named in the verified capsule. The destination `blobs` directory must not exist. Missing blobs, wrong keys, hash differences and an incomplete database/manifest binding fail the command.

   ```sh
   kydrive-server bulk-restore -opened /private/opened \
     -repo /independent/restic-repository -to /new-drive/data/blobs
   ```

   If the capsule contains no `data/drive-bulk.json`, verify that its database has no `drive_versions` rows; an empty drive has no bulk snapshot to restore. Never silently accept a missing manifest for a database with files.

5. Copy `opened/data/ky_server.db`, `encryption.key` and (when present) `recovery.pub` into the new drive data directory, mode 0600 and owned by the runtime UID/GID. Preserve `config/integrations.json` securely; reconstruct runtime configuration from it, keeping the original session/editor/SCIM/client secrets. Set database driver `sqlite`, the new persistent paths and the independent bulk mount. Change origins only deliberately and update the OIDC callback registration and editor integration together.
6. Restore or relocate the retained local capsules. The key pin and sealed KyRecovery token live in the restored files/database. Do not pair to another key to work around a failed pin. Unpairing deletes only URL/token and requires a separate KyRecovery administrator to revoke the old token.
7. Start the same tested image digest. Verify workspace permissions, quotas, versions and trash; download files and open one in the editor. Then run `backup-drill` or the Recovery screen's drill. Do not mark the service recovered unless both metadata and bulk checks pass.

For an upgrade, stop the only writer, take a successful complete backup, retain the previous image, then start the new image against the persistent mounts. For rollback, restore the matched database/blob checkpoint and its integration secrets together; do not assume an older binary understands a newer schema. This package has no HA or destructive migrations.

Retain every Restic snapshot referenced by a retained capsule. Automatic bulk pruning is not implemented. Backup retention, independent destination and recovery objectives must be chosen for the actual deployment; the local fixture establishes mechanics, not a representative production restore-time objective.
