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
