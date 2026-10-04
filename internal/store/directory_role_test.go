package store_test

import (
	"context"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"testing"
	"time"
)

func TestDirectoryRoleGrantIsExactAuditedAndRevokesSessions(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	for _, u := range []*store.User{
		{ID: "directory", Username: "yoshi", SSOProvider: "scim", SSOSubject: "identity-123", Role: "user", Status: "active"},
		{ID: "local", Username: "recovery-admin", SSOProvider: "local", SSOSubject: "local-subject", Role: "admin", Status: "active"},
		{ID: "disabled", Username: "disabled", SSOProvider: "scim", SSOSubject: "disabled-subject", Role: "user", Status: "inactive"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "session", UserID: "directory", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []string{"identity", "yoshi", "local-subject", "disabled-subject"} {
		if err := st.Users().SetDirectoryRole(ctx, subject, "admin"); err == nil {
			t.Fatalf("accepted %q", subject)
		}
	}
	if err := st.Users().SetDirectoryRole(ctx, "identity-123", "owner"); err == nil {
		t.Fatal("accepted unknown role")
	}
	if err := st.Users().SetDirectoryRole(ctx, "identity-123", "admin"); err != nil {
		t.Fatal(err)
	}
	u, err := st.Users().GetUserByID(ctx, "directory")
	if err != nil || u.Role != "admin" || u.SSOProvider != "scim" || u.SSOSubject != "identity-123" {
		t.Fatal("identity not preserved", u, err)
	}
	if _, err = st.Sessions().GetSession(ctx, "session"); err == nil {
		t.Fatal("session survived grant")
	}
	rows, n, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || n != 1 || rows[0].Action != "auth.directory_role_changed" {
		t.Fatal("missing audit", rows, n, err)
	}
	if err = st.Users().SetDirectoryRole(ctx, "identity-123", "user"); err != nil {
		t.Fatal(err)
	}
	u, err = st.Users().GetUserByID(ctx, "directory")
	if err != nil || u.Role != "user" {
		t.Fatal("demotion failed", u, err)
	}
}
