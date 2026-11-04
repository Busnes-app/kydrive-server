package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
)

// A database upgraded with case-only clashes has no unique index yet: the store keeps new
// variants out, renames nothing, and signs the exact spelling in.
func TestCaseVariantsRefusedWhileClashesRemain(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "drive.db")}, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := st.(*SQLStore)
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `DROP INDEX idx_users_username_lower`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alice", "alice"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO users (id, username, sso_provider, created_at, updated_at) VALUES (?, ?, 'local', ?, ?)`, "u-"+name, name, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Users().CreateUser(ctx, &User{ID: "u-3", Username: "ALICE", Role: "user", Status: "active", SSOProvider: "local"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("new case variant: %v", err)
	}
	if _, err := s.Users().ApplyDirectoryUser(ctx, &DirectoryUser{Provider: "scim", Subject: "s", Username: "aLice", Active: true}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("provisioned case variant: %v", err)
	}
	u, err := s.Users().GetUserByUsername(ctx, "alice")
	if err != nil || u.ID != "u-alice" {
		t.Fatalf("sign-in by exact spelling: %+v %v", u, err)
	}
	// An existing clash keeps working: updating one of them is not a new variant.
	u.Email = "alice@example.com"
	if err := s.Users().UpdateUser(ctx, u); err != nil {
		t.Fatalf("update of an existing clash: %v", err)
	}
	if u, err = s.Users().GetUserByUsername(ctx, "Alice"); err != nil || u.ID != "u-Alice" {
		t.Fatalf("sign-in by the other spelling: %+v %v", u, err)
	}
}
