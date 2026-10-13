package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func installerBundle() applysetup.Common {
	return applysetup.Common{Schema: applysetup.Schema, Service: "kydrive", SetupID: "271333e2-c221-4601-b249-563b3d828530",
		Admins:   []applysetup.Identity{{Issuer: "https://identity.example.test", Subject: "owner-subject", Username: "owner-admin"}},
		Everyday: []applysetup.Identity{{Issuer: "https://identity.example.test", Subject: "daily-subject", Username: "daily"}}}
}

func TestInstallerAccountsResumeAndCheckAreReadOnly(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	b := installerBundle()
	if _, err := st.ApplyInstaller(ctx, b, true); err == nil {
		t.Fatal("missing identities passed inspection")
	}
	r, err := st.ApplyInstaller(ctx, b, false)
	if err != nil || len(r.Created) != 2 {
		t.Fatal(r, err)
	}
	_, audits, err := st.Audit().ListAuditRecords(ctx, 0, 100)
	if err != nil || audits != 2 {
		t.Fatal("atomic audits", audits, err)
	}
	for _, check := range []bool{false, true} {
		r, err = st.ApplyInstaller(ctx, b, check)
		if err != nil || len(r.Created) != 0 || len(r.Present) != 2 {
			t.Fatal("retry/check", r, err)
		}
	}
	_, after, _ := st.Audit().ListAuditRecords(ctx, 0, 100)
	if audits != after {
		t.Fatal("retry/check wrote audit")
	}
	u, err := st.Users().GetUserBySSO(ctx, "scim", "owner-subject")
	if err != nil || u.Role != "admin" || u.PasswordHash != "" {
		t.Fatal("directory admin", u, err)
	}
	if dedicated, err := st.InstallerAdministrator(ctx, u.ID); err != nil || !dedicated {
		t.Fatal("dedicated admin", err)
	}
	u.Role = "user"
	if err = st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ApplyInstaller(ctx, b, false); err == nil {
		t.Fatal("drift silently repaired")
	}
}

func TestInstallerCollisionRollsBackAllAccountsAndOwnership(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	b := installerBundle()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "foreign", Username: "daily", Role: "user", Status: "active", SSOProvider: "local", PasswordHash: "preserve"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyInstaller(ctx, b, false); err == nil {
		t.Fatal("foreign account adopted")
	}
	if _, err := st.Users().GetUserByUsername(ctx, "owner-admin"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("partial account survived", err)
	}
	if _, n, _ := st.Audit().ListAuditRecords(ctx, 0, 100); n != 0 {
		t.Fatal("partial audit survived")
	}
	settings, err := st.Settings().GetAllSettings(ctx)
	if err != nil || len(settings) != 0 {
		t.Fatal("partial ownership survived", settings, err)
	}
	u, err := st.Users().GetUserByID(ctx, "foreign")
	if err != nil || u.PasswordHash != "preserve" {
		t.Fatal("foreign account changed", u, err)
	}
}
