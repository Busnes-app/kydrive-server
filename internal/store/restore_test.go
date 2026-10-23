package store

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// restoreStore includes both content authorization and each kind of persisted access grant.
func restoreStore(t *testing.T) (*SQLStore, drive.File) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "drive.db")}, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := st.(*SQLStore)
	u := &User{ID: "owner", Username: "owner", PasswordHash: "retained-password-hash", Role: "user", Status: "active", SSOProvider: "local"}
	if err := s.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	ws, err := s.Drive().EnsurePersonalWorkspace(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Drive().Publish(ctx, u.ID, drive.File{Workspace: ws, Name: "retained.txt"}, drive.Version{Blob: "blob", Size: 4, Digest: strings.Repeat("a", 64), Sealed: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.Sessions().CreateSession(ctx, &Session{TokenHash: "session", UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, u.PasswordHash, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Sessions().CreateMFAChallenge(ctx, &MFAChallenge{TokenHash: "challenge", UserID: u.ID, ExpiresAt: now.Add(time.Hour)}, u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO groups(id,display_name,created_at,updated_at) VALUES('g','Group',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO group_members VALUES('g','owner')`,
		`INSERT INTO drive_grants VALUES('` + ws + `','g','reader')`,
		`INSERT INTO drive_shares(id,workspace,resource_kind,resource_id,principal_kind,principal_id,role,created_by,created) VALUES('share','` + ws + `','file','` + f.ID + `','user','owner','reader','owner','now')`,
		`INSERT INTO drive_app_passwords(id,user_id,name,hash,created,scope) VALUES('dav','owner','DAV','dav-hash','now','dav'),('agent','owner','Agent','agent-hash','now','mcp:read')`,
		`INSERT INTO drive_devices(id,user_id,name,platform,hash,created,last_used) VALUES('device','owner','Desktop','linux','device-hash','now',1)`,
		`INSERT INTO drive_device_codes VALUES('code','owner','Desktop','linux','challenge',1,0)`,
		`INSERT INTO drive_service_tokens(id,user_id,workspace,name,role,token_hash,expires) VALUES('service','owner','` + ws + `','Service','reader','service-hash',1)`,
		`INSERT INTO drive_links(id,workspace,resource_kind,resource_id,role,scope,token_hash,created_by,created) VALUES('org','` + ws + `','file','` + f.ID + `','reader','org','org-hash','owner','now'),('guest','` + ws + `','file','` + f.ID + `','reader','guest','guest-hash','owner','now')`,
		`INSERT INTO drive_link_users VALUES('org','owner','now')`,
		`INSERT INTO drive_editor_sessions(id,file_id,revision,user_id,token_hash,expires) VALUES('editor','` + f.ID + `',1,'owner','editor-hash',1)`,
		`INSERT INTO drive_editor_documents VALUES('document','` + f.ID + `',1,1,false)`,
		`INSERT INTO drive_editor_saves VALUES('document','digest',1)`,
		`INSERT INTO drive_upload_sessions(id,owner,workspace,parent,name,name_hmac,file,expected,size,expires) VALUES('upload','owner','` + ws + `','','upload','name-hash','',0,4,1)`,
		`INSERT INTO drive_upload_segments VALUES('upload',0,'segment',4)`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	if err := s.Settings().SetSetting(ctx, "installer.integration", "retained-config-reference"); err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestResetAfterRestore(t *testing.T) {
	ctx := context.Background()
	s, file := restoreStore(t)
	var previousEpoch int64
	for attempt := 1; attempt <= 2; attempt++ {
		if err := s.ResetAfterRestore(ctx); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"sessions", "mfa_challenges", "drive_app_passwords", "drive_devices", "drive_device_codes", "drive_service_tokens", "drive_links", "drive_link_users", "drive_editor_sessions", "drive_editor_documents", "drive_editor_saves", "drive_upload_sessions", "drive_upload_segments"} {
			var n int
			if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil || n != 0 {
				t.Errorf("%s contains restored grants: %d, %v", table, n, err)
			}
		}
		got, err := s.Drive().File(ctx, "owner", file.ID, 1)
		if err != nil || got.Name != file.Name || got.Digest != file.Digest || got.Size != file.Size || got.Revision != file.Revision {
			t.Fatalf("content or owner access changed: %+v, %v", got, err)
		}
		for _, table := range []string{"groups", "group_members", "drive_grants", "drive_shares", "drive_personal_workspaces", "drive_versions"} {
			var n int
			if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil || n != 1 {
				t.Errorf("%s lost retained data: %d, %v", table, n, err)
			}
		}
		u, err := s.Users().GetUserByID(ctx, "owner")
		if err != nil || u.PasswordHash != "retained-password-hash" || u.MustChangePassword {
			t.Fatalf("account credentials changed: %+v, %v", u, err)
		}
		if value, err := s.Settings().GetSetting(ctx, "installer.integration"); err != nil || value != "retained-config-reference" {
			t.Fatal("integration configuration changed", value, err)
		}
		var epoch int64
		if err := s.db.QueryRowContext(ctx, `SELECT editor_epoch FROM drive_files WHERE id=?`, file.ID).Scan(&epoch); err != nil || epoch <= 0 || epoch == previousEpoch {
			t.Fatal("editor key epoch was reused", epoch, previousEpoch, err)
		}
		previousEpoch = epoch
		var audits int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_records WHERE action='system.restore_reset' AND user_id='system'`).Scan(&audits); err != nil || audits != attempt {
			t.Fatal("missing reset audit", audits, err)
		}
	}
}

func TestResetAfterRestoreRollsBackOnFailure(t *testing.T) {
	s, _ := restoreStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER refuse_restore_reset BEFORE DELETE ON drive_devices BEGIN SELECT RAISE(ABORT,'fixture reset failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetAfterRestore(context.Background()); err == nil {
		t.Fatal("failed reset returned success")
	}
	for table, want := range map[string]int{"sessions": 1, "mfa_challenges": 1, "drive_app_passwords": 2, "drive_devices": 1} {
		var got int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil || got != want {
			t.Fatal("reset did not roll back", table, got, err)
		}
	}
	var audits int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_records WHERE action='system.restore_reset'`).Scan(&audits); err != nil || audits != 0 {
		t.Fatal("failed reset was audited as success", audits, err)
	}
}
