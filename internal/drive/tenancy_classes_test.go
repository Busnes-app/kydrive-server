package drive

// Every table in the main database has exactly one class (TestEveryTableIsClassified).
// OrgOwnedTables carry org_id; DerivedTables reach an org through the foreign key to the
// table named; PlatformTables hold no customer content.
var (
	OrgOwnedTables = []string{
		"users", "groups", "audit_records",
		"drive_workspaces", "drive_events", "drive_service_tokens", "drive_editor_documents",
		"drive_editor_saves", "drive_app_passwords", "drive_changes", "drive_change_floor",
		"drive_devices", "drive_device_codes", "drive_peer_settings", "drive_peer_folders",
		"drive_peer_tickets", "drive_peer_picks", "drive_peer_guest", "drive_view_copies",
	}
	// NullableOrgID tables may hold platform rows (org_id NULL).
	NullableOrgID = map[string]bool{"audit_records": true, "drive_events": true}
	DerivedTables = map[string]string{
		"sessions": "users", "group_members": "groups", "mfa_challenges": "users",
		"drive_personal_workspaces": "drive_workspaces", "drive_grants": "drive_workspaces",
		"drive_folders": "drive_workspaces", "drive_files": "drive_workspaces",
		"drive_versions": "drive_files", "drive_editor_sessions": "drive_files",
		"drive_upload_sessions": "drive_workspaces", "drive_upload_segments": "drive_upload_sessions",
		"drive_shares": "drive_workspaces", "drive_links": "drive_workspaces",
		"drive_link_users": "drive_links", "drive_suite_groups": "drive_workspaces",
	}
	PlatformTables = []string{
		"schema_migrations", "server_settings", "sso_logout_tokens", "sso_logouts", "sso_subject_logouts",
		"drive_schema", "drive_key", "drive_name_scrub", "drive_blob_retire", "drive_blob_kyb1",
		"drive_orgs", "drive_erasures",
	}
	// MultiTenantTables exist on a multi-tenant database only (orgKeysDDL), each reaching an
	// org through its foreign key to the table named: the org data keys (D14).
	MultiTenantTables = map[string]string{"drive_org_keys": "drive_orgs"}
	// SharedStateTables are platform tables on PostgreSQL only: the replicas' short-lived
	// security state (store.Shared), keyed by limit, key or token, never by org.
	SharedStateTables = []string{"shared_windows", "shared_spent", "shared_tickets", "shared_dav_locks"}
)
