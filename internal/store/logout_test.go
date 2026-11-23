package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// A sid logout scoped to an org ends that org's sessions of the sid and leaves, and counts,
// any other org's: a sid is one login of one account, so those would be a KyIdentity bug.
func TestEndSSOSessionStaysInItsOrg(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	db := testdb.Open(t, cfg)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	exec(`INSERT INTO drive_orgs(id,status) VALUES('org-b','active')`)
	for _, u := range []struct{ id, org string }{{"in-a", drive.DefaultOrg}, {"in-b", "org-b"}} {
		exec(`INSERT INTO users(id,username,sso_provider,sso_subject,created_at,updated_at,org_id) VALUES(?,?,'kysignon',?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, u.id, u.id, u.id, u.org)
		if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: u.id, UserID: u.id, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), SSOSID: "shared-sid"}, "", false); err != nil {
			t.Fatal(err)
		}
	}
	other, err := st.Sessions().EndSSOSession(ctx, "shared-sid", drive.DefaultOrg, time.Now().Add(time.Minute), store.LogoutToken{})
	if err != nil || other != 1 {
		t.Fatalf("other=%d err=%v", other, err)
	}
	if _, err := st.Sessions().GetSession(ctx, "in-a"); err == nil {
		t.Error("the claimed org's session survived")
	}
	if _, err := st.Sessions().GetSession(ctx, "in-b"); err != nil {
		t.Error("another org's session was ended:", err)
	}
	// Unscoped (single-tenant), the sid ends everywhere.
	if other, err := st.Sessions().EndSSOSession(ctx, "shared-sid", "", time.Now().Add(time.Minute), store.LogoutToken{}); err != nil || other != 0 {
		t.Fatalf("unscoped: other=%d err=%v", other, err)
	}
	if _, err := st.Sessions().GetSession(ctx, "in-b"); err == nil {
		t.Error("an unscoped logout left a session")
	}
}

// A logout token is spent in the transaction that revokes: a revocation that fails leaves
// it unspent, so the retry ends the browser and editor sessions, and only then is it spent.
func TestLogoutTokenIsSpentWithItsRevocation(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	db := testdb.Open(t, cfg)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	exec(`INSERT INTO users(id,username,sso_provider,sso_subject,status,created_at,updated_at) VALUES('u','u','kysignon','u','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "browser", UserID: "u", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), SSOSID: "sid"}, "", false); err != nil {
		t.Fatal(err)
	}
	ws, err := st.Drive().EnsurePersonalWorkspace(ctx, "u")
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.Drive().Publish(ctx, "u", drive.File{Workspace: ws, Name: "doc.txt"}, drive.Version{Blob: "blob", Size: 1, Digest: strings.Repeat("a", 64), Sealed: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO drive_editor_sessions(id,file_id,revision,user_id,token_hash,expires,sso_sid) VALUES('editor',?,1,'u','editor-hash',?,'sid')`, f.ID, time.Now().Add(time.Hour).Unix())
	open := func() (browser, editor bool) {
		_, err := st.Sessions().GetSession(ctx, "browser")
		var revoked bool
		if err := db.QueryRow(`SELECT revoked FROM drive_editor_sessions WHERE id='editor'`).Scan(&revoked); err != nil {
			t.Fatal(err)
		}
		return err == nil, !revoked
	}

	// One revocation fails after the token would have been spent.
	if testdb.Postgres() {
		exec(`CREATE FUNCTION fail_once() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected'; END$$`)
		exec(`CREATE TRIGGER fail_once BEFORE DELETE ON sessions FOR EACH ROW EXECUTE FUNCTION fail_once()`)
	} else {
		exec(`CREATE TRIGGER fail_once BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'injected'); END`)
	}
	token := store.LogoutToken{JTI: "jti-1", Expires: time.Now().Add(time.Minute)}
	if _, err := st.Sessions().EndSSOSession(ctx, "sid", "", time.Now().Add(time.Minute), token); err == nil {
		t.Fatal("the injected failure did not fail the revocation")
	}
	if b, e := open(); !b || !e {
		t.Fatalf("a failed revocation half applied: browser %v, editor %v", b, e)
	}
	exec(`DROP TRIGGER fail_once` + map[bool]string{true: ` ON sessions`}[testdb.Postgres()])

	if _, err := st.Sessions().EndSSOSession(ctx, "sid", "", time.Now().Add(time.Minute), token); err != nil {
		t.Fatal("retry:", err)
	}
	if b, e := open(); b || e {
		t.Fatalf("the retry left access: browser %v, editor %v", b, e)
	}
	if _, err := st.Sessions().EndSSOSession(ctx, "sid", "", time.Now().Add(time.Minute), token); !errors.Is(err, store.ErrLogoutTokenSpent) {
		t.Fatalf("a replay was not recognised: %v", err)
	}
	// An expired entry is forgotten: the token itself no longer verifies by then.
	old := store.LogoutToken{JTI: "jti-2", Expires: time.Now().Add(-time.Second)}
	if err := st.Sessions().EndSSOSubject(ctx, "kysignon", "u", time.Now(), time.Now().Add(time.Minute), old); err != nil {
		t.Fatal(err)
	}
	old.Expires = time.Now().Add(time.Minute)
	if err := st.Sessions().EndSSOSubject(ctx, "kysignon", "u", time.Now(), time.Now().Add(time.Minute), old); err != nil {
		t.Fatalf("an expired jti stayed spent: %v", err)
	}
}
