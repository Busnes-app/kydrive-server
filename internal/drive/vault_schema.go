package drive

// The control-plane catalogue is separate from legacy file routes. Each child's
// foreign key includes its organisation, including for SQLite-to-Postgres copies.
func vaultSchema(d dialect) string {
	ddl := `
CREATE TABLE drive_vault_user_keys (
 org_id TEXT NOT NULL DEFAULT '` + DefaultOrg + `' REFERENCES drive_orgs(id), user_id TEXT NOT NULL, body TEXT NOT NULL,
 PRIMARY KEY(org_id,user_id)
);
CREATE TABLE drive_vaults (
 org_id TEXT NOT NULL DEFAULT '` + DefaultOrg + `' REFERENCES drive_orgs(id), id TEXT NOT NULL, log_id TEXT NOT NULL UNIQUE,
 sequence BIGINT NOT NULL CHECK(sequence>0), hash TEXT NOT NULL, PRIMARY KEY(org_id,id)
);
CREATE TABLE drive_vault_records (
 org_id TEXT NOT NULL DEFAULT '` + DefaultOrg + `', vault_id TEXT NOT NULL, sequence BIGINT NOT NULL CHECK(sequence>0),
 body TEXT NOT NULL, new_recipient TEXT, PRIMARY KEY(org_id,vault_id,sequence),
 FOREIGN KEY(org_id,vault_id) REFERENCES drive_vaults(org_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX drive_vault_never_reuse_recipient ON drive_vault_records(org_id,vault_id,new_recipient) WHERE new_recipient IS NOT NULL;
`
	for _, table := range []string{"drive_vault_user_keys", "drive_vaults", "drive_vault_records"} {
		ddl += d.trigger(table+"_org_exists", "INSERT", table, "NOT EXISTS(SELECT 1 FROM drive_orgs WHERE id=NEW.org_id AND status!='deleted')", errOrg+" no such org")
		ddl += d.trigger(table+"_org_fixed", "UPDATE OF org_id", table, "NEW.org_id"+d.isNot()+"OLD.org_id", errOrg+" org_id is immutable")
	}
	return ddl
}
