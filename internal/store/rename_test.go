package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestRenameLocalAdminKeepsDirectoryIdentitySeparate(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	local := &store.User{ID: "local", Username: "admin", Role: "admin", Status: "active", SSOProvider: "local", PasswordHash: "unchanged"}
	directory := &store.User{ID: "directory", Username: "staff", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "identity-subject"}
	for _, u := range []*store.User{local, directory} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Users().RenameLocalAdmin(ctx, "staff", "recovery"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("directory rename admitted", err)
	}
	if err := st.Users().RenameLocalAdmin(ctx, "admin", "STAFF"); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatal("collision admitted", err)
	}
	if err := st.Users().RenameLocalAdmin(ctx, "admin", "recovery-admin"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Users().GetUserByID(ctx, "local")
	if err != nil || got.Username != "recovery-admin" || got.PasswordHash != "unchanged" || got.Role != "admin" || got.SSOProvider != "local" {
		t.Fatal("local identity changed", got, err)
	}
	directory.Username = "admin"
	if err := st.Users().UpdateUser(ctx, directory); err != nil {
		t.Fatal("directory username still occupied", err)
	}
	rows, n, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || n != 1 || rows[0].Action != "auth.local_admin_renamed" {
		t.Fatal("missing atomic audit", rows, n, err)
	}
}
