package store_test

import (
	"context"
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
