package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// Disabling a directory account ends its credentials, so enabling it again revives none.
func TestDirectoryDisableEndsCredentials(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	u, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "scim", Subject: "s-cred", Username: "bob@example.com", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	_, password, err := st.Drive().CreateAppPassword(ctx, u.ID, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := st.Drive().CreateAgentToken(ctx, u.ID, "Agent", drive.ScopeMCPWrite, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, active := range []bool{false, true} {
		if _, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: u.ID, Username: "bob@example.com", Active: active}); err != nil {
			t.Fatal(err)
		}
	}
	if id, err := st.Drive().AppPasswordUser(ctx, "bob@example.com", password); err == nil {
		t.Fatalf("WebDAV password revived for %s", id)
	}
	if id, _, err := st.Drive().AgentTokenUser(ctx, token, time.Now()); err == nil {
		t.Fatalf("agent token revived for %s", id)
	}
}

// An MFA reset ends sessions and credentials but leaves the account active; a local account
// is refused.
func TestRevokeDirectoryCredentials(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	u, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "kysignon", Subject: "s-mfa", Username: "carol@example.com", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "mfa", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "", false); err != nil {
		t.Fatal(err)
	}
	_, password, err := st.Drive().CreateAppPassword(ctx, u.ID, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := st.Drive().CreateAgentToken(ctx, u.ID, "Agent", drive.ScopeMCPWrite, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Users().RevokeDirectoryCredentials(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sessions().GetSession(ctx, "mfa"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session kept", err)
	}
	if id, err := st.Drive().AppPasswordUser(ctx, "carol@example.com", password); err == nil {
		t.Fatalf("WebDAV password kept for %s", id)
	}
	if id, _, err := st.Drive().AgentTokenUser(ctx, token, time.Now()); err == nil {
		t.Fatalf("agent token kept for %s", id)
	}
	if got, _ := st.Users().GetUserByID(ctx, u.ID); got.Status != "active" {
		t.Fatalf("account disabled: %+v", got)
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "local-mfa", Username: "admin2", Role: "admin", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().RevokeDirectoryCredentials(ctx, "local-mfa"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("local account: %v", err)
	}
}
