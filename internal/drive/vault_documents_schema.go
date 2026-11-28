package drive

// Each row is scoped by the vault's composite tenant key. A previous room stays
// reserved even after a newer binding becomes active.
const vaultDocumentsSchema = `
CREATE TABLE drive_vault_versions (
 org_id TEXT NOT NULL, vault_id TEXT NOT NULL, file_id TEXT NOT NULL, revision BIGINT NOT NULL CHECK(revision>0),
 blob TEXT NOT NULL UNIQUE, digest TEXT NOT NULL, size BIGINT NOT NULL CHECK(size>0), manifest TEXT NOT NULL,
 PRIMARY KEY(org_id,vault_id,file_id,revision),
 FOREIGN KEY(org_id,vault_id) REFERENCES drive_vaults(org_id,id) ON DELETE CASCADE
);
CREATE TABLE drive_vault_bindings (
 org_id TEXT NOT NULL, vault_id TEXT NOT NULL, file_id TEXT NOT NULL, head_hash TEXT NOT NULL,
 binding_id TEXT NOT NULL UNIQUE, binding TEXT NOT NULL,
 PRIMARY KEY(org_id,vault_id,file_id,head_hash),
 FOREIGN KEY(org_id,vault_id) REFERENCES drive_vaults(org_id,id) ON DELETE CASCADE
);
CREATE TABLE drive_vault_rooms (
 room TEXT PRIMARY KEY, binding_id TEXT NOT NULL REFERENCES drive_vault_bindings(binding_id) ON DELETE CASCADE
);
`

// Bind an immutable encrypted CryptPad blob to one editor generation.
const vaultEditorBlobsSchema = `
CREATE TABLE drive_vault_editor_blobs (
 blob TEXT PRIMARY KEY, binding_id TEXT NOT NULL REFERENCES drive_vault_bindings(binding_id) ON DELETE CASCADE
);
`

// Schema 35 charges checkpoint ciphertext against the existing vault and org limits.
const vaultCheckpointsSchema = `
ALTER TABLE drive_vault_editor_blobs ADD COLUMN checkpoint_object TEXT;
ALTER TABLE drive_vault_editor_blobs ADD COLUMN checkpoint_size BIGINT NOT NULL DEFAULT 0;
ALTER TABLE drive_vault_editor_blobs ADD COLUMN checkpoint_until BIGINT NOT NULL DEFAULT 0;
ALTER TABLE drive_vault_editor_blobs ADD COLUMN checkpoint_digest TEXT;
CREATE INDEX drive_vault_editor_blobs_binding ON drive_vault_editor_blobs(binding_id);
CREATE UNIQUE INDEX drive_vault_checkpoints_objects ON drive_vault_editor_blobs(checkpoint_object) WHERE checkpoint_object IS NOT NULL;
`
