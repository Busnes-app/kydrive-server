package drive_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func TestAppPasswords(t *testing.T) {
	st, w, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	p, secret, err := d.CreateAppPassword(ctx, "worker", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^([a-z2-7]{4}-){7}[a-z2-7]{4}$`).MatchString(secret) || p.Name != "Laptop" || p.ID == "" {
		t.Fatalf("%+v %q", p, secret)
	}
	for _, variant := range []string{secret, strings.ToUpper(secret), strings.ReplaceAll(secret, "-", "")} {
		if id, err := d.AppPasswordUser(ctx, "WORKER", variant); err != nil || id != "worker" {
			t.Fatalf("%q: %q %v", variant, id, err)
		}
	}
	other, otherSecret, err := d.CreateAppPassword(ctx, "stranger", "Phone")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ user, secret string }{{"worker", otherSecret}, {"stranger", secret}, {"worker", secret[:len(secret)-1] + "a"}, {"worker", ""}, {"", secret}} {
		if _, err := d.AppPasswordUser(ctx, c.user, c.secret); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	list, err := d.AppPasswords(ctx, "worker")
	if err != nil || len(list) != 1 || list[0].ID != p.ID || list[0].LastUsed == 0 {
		t.Fatalf("list %+v %v", list, err)
	}
	if err = d.RevokeAppPassword(ctx, "worker", other.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("revoked another user's password: %v", err)
	}

	// Deactivation stops it on the next use; reactivation restores it, nothing was deleted.
	if err = drive.ExecSQL(ctx, d, `UPDATE users SET status='inactive' WHERE id='worker'`); err != nil {
		t.Fatal(err)
	}
	if _, err = d.AppPasswordUser(ctx, "worker", secret); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("inactive user: %v", err)
	}
	if _, _, err = d.CreateAppPassword(ctx, "worker", "Another"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("inactive user created a password: %v", err)
	}
	if err = drive.ExecSQL(ctx, d, `UPDATE users SET status='active' WHERE id='worker'`); err != nil {
		t.Fatal(err)
	}

	if err = d.RevokeAppPassword(ctx, "worker", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.AppPasswordUser(ctx, "worker", secret); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("revoked password works: %v", err)
	}

	scoped := drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: w.ID, Rank: 2})
	if _, _, err = d.CreateAppPassword(scoped, "worker", "Robot"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("service token created a password: %v", err)
	}
	for _, name := range []string{"", " padded", strings.Repeat("x", 101)} {
		if _, _, err = d.CreateAppPassword(ctx, "worker", name); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	for i := range 20 {
		if _, _, err = d.CreateAppPassword(ctx, "worker", fmt.Sprint("device ", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = d.CreateAppPassword(ctx, "worker", "one too many"); !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("21st password: %v", err)
	}
	events, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_events WHERE action LIKE 'app_password.%'`)
	if err != nil || events != 23 { // 22 created, 1 revoked
		t.Fatalf("events=%d err=%v", events, err)
	}
}

// last_used is written at most every five minutes, so a busy mount does not write on every request.
func TestAppPasswordLastUsedThrottled(t *testing.T) {
	st, _, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	p, secret, err := d.CreateAppPassword(ctx, "worker", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.AppPasswordUser(ctx, "worker", secret); err != nil {
		t.Fatal(err)
	}
	if err = drive.ExecSQL(ctx, d, `UPDATE drive_app_passwords SET last_used=last_used-60 WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := drive.QueryInt(ctx, d, `SELECT last_used FROM drive_app_passwords WHERE id=?`, p.ID)
	if _, err = d.AppPasswordUser(ctx, "worker", secret); err != nil {
		t.Fatal(err)
	}
	if after, _ := drive.QueryInt(ctx, d, `SELECT last_used FROM drive_app_passwords WHERE id=?`, p.ID); after != before {
		t.Fatalf("last_used rewritten within five minutes: %d -> %d", before, after)
	}
}
