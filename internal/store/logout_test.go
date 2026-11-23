package store_test

import (
	"context"
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
	other, err := st.Sessions().EndSSOSession(ctx, "shared-sid", drive.DefaultOrg, time.Now().Add(time.Minute))
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
	if other, err := st.Sessions().EndSSOSession(ctx, "shared-sid", "", time.Now().Add(time.Minute)); err != nil || other != 0 {
		t.Fatalf("unscoped: other=%d err=%v", other, err)
	}
	if _, err := st.Sessions().GetSession(ctx, "in-b"); err == nil {
		t.Error("an unscoped logout left a session")
	}
}

func TestSpendLogoutTokenOnce(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	for i, want := range []bool{true, false} {
		fresh, err := st.Sessions().SpendLogoutToken(ctx, "jti-1", time.Now().Add(time.Minute))
		if err != nil || fresh != want {
			t.Fatalf("spend %d: %v %v", i+1, fresh, err)
		}
	}
	// An expired entry is forgotten: the token itself no longer verifies by then.
	if _, err := st.Sessions().SpendLogoutToken(ctx, "jti-2", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if fresh, err := st.Sessions().SpendLogoutToken(ctx, "jti-2", time.Now().Add(time.Minute)); err != nil || !fresh {
		t.Fatalf("an expired jti stayed spent: %v %v", fresh, err)
	}
}
