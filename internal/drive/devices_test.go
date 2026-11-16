package drive_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk-device-link"

func challengeOf(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func TestDeviceLink(t *testing.T) {
	st, w, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	challenge := challengeOf(verifier)
	approve := func(redirect string) (string, error) {
		return d.ApproveLink(ctx, "worker", "Laptop", "linux", challenge, redirect)
	}

	for _, redirect := range []string{
		"http://localhost:8080/cb",            // a name, not a loopback address
		"http://127.0.0.1/cb",                 // no port
		"http://127.0.0.1:/cb",                // empty port
		"http://127.0.0.1:0/cb",               // port 0
		"http://127.0.0.1:65536/cb",           // out of range
		"http://127.0.0.1:080/cb",             // non-canonical port
		"http://[::1]/cb",                     // no port
		"http://::1:8080/cb",                  // unbracketed IPv6
		"https://127.0.0.1:8080/cb",           // loopback is plain http (RFC 8252)
		"http://example.com:8080/cb",          // another host
		"http://127.0.0.2:8080/cb",            // only 127.0.0.1
		"http://127.1:8080/cb",                // shortened IPv4
		"http://0x7f.0.0.1:8080/cb",           // hex IPv4
		"http://[::ffff:127.0.0.1]:8080/cb",   // mapped IPv4
		"http://user@127.0.0.1:8080/cb",       // userinfo
		"http://evil.com@127.0.0.1:8080/cb",   // userinfo
		"http://127.0.0.1:8080@evil.com/cb",   // host is evil.com
		"http://127.0.0.1:8080\\@evil.com/cb", // backslash
		"http://127.0.0.1:8080/cb#frag",       // fragment
		"http://127.0.0.1:8080/cb#",           // empty fragment
		"http://127.0.0.1:8080/cb?code=x",     // query: the server adds its own parameters
		"//127.0.0.1:8080/cb",                 // no scheme
		"javascript:alert(1)",
		"",
		"http://127.0.0.1:8080/" + strings.Repeat("a", 300),
	} {
		if _, err := approve(redirect); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("redirect %q: %v", redirect, err)
		}
	}
	for _, c := range []struct{ name, platform, challenge string }{
		{"", "linux", challenge},
		{" Laptop", "linux", challenge},
		{strings.Repeat("x", 101), "linux", challenge},
		{"Lap‮top", "linux", challenge},
		{"Laptop", "", challenge},
		{"Laptop", "beos", challenge},
		{"Laptop", "linux", ""},
		{"Laptop", "linux", challenge[:42]},
		{"Laptop", "linux", challenge + "A"},
		{"Laptop", "linux", strings.Replace(challenge, challenge[:1], "+", 1)},
	} {
		if _, err := d.ApproveLink(ctx, "worker", c.name, c.platform, c.challenge, "http://127.0.0.1:8080/cb"); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}

	for _, redirect := range []string{"http://127.0.0.1:8080/cb", "http://[::1]:49152/callback", "http://127.0.0.1:1"} {
		code, err := approve(redirect)
		if err != nil || len(code) < 43 {
			t.Fatalf("%s: %q %v", redirect, code, err)
		}
	}

	// Only the hash of a code or a token is stored.
	code, err := approve("http://127.0.0.1:8080/cb")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_device_codes WHERE code_hash=? OR code_hash LIKE ?`, code, "%"+code[:20]+"%"); err != nil || n != 0 {
		t.Fatalf("code stored in clear: %d %v", n, err)
	}

	// A wrong verifier burns the code: the right one cannot follow.
	if _, _, err = d.RedeemLink(ctx, code, verifier+"x", nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("wrong verifier: %v", err)
	}
	if _, _, err = d.RedeemLink(ctx, code, verifier, nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("code reused after a wrong verifier: %v", err)
	}

	code, _ = approve("http://127.0.0.1:8080/cb")
	token, user, err := d.RedeemLink(ctx, code, verifier, nil)
	if err != nil || user != "worker" || !strings.HasPrefix(token, "kyd_") || len(token) != 47 {
		t.Fatalf("redeem %q %q %v", token, user, err)
	}
	if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_devices WHERE hash=? OR hash LIKE ?`, token, "%"+token[4:24]+"%"); err != nil || n != 0 {
		t.Fatalf("token stored in clear: %d %v", n, err)
	}
	if _, _, err = d.RedeemLink(ctx, code, verifier, nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("reused code: %v", err)
	}
	for _, c := range []struct{ code, verifier string }{{"", verifier}, {code, ""}, {"unknown", verifier}, {code, "short"}} {
		if _, _, err = d.RedeemLink(ctx, c.code, c.verifier, nil); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%+v: %v", c, err)
		}
	}

	// Codes live 60 seconds.
	code, _ = approve("http://127.0.0.1:8080/cb")
	if err = drive.ExecSQL(ctx, d, `UPDATE drive_device_codes SET expires=? WHERE used=0`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.RedeemLink(ctx, code, verifier, nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("expired code: %v", err)
	}

	// A code approved before deactivation redeems nothing.
	code, _ = approve("http://127.0.0.1:8080/cb")
	if err = drive.ExecSQL(ctx, d, `UPDATE users SET status='inactive' WHERE id='worker'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.RedeemLink(ctx, code, verifier, nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("inactive user redeemed: %v", err)
	}
	if _, err = approve("http://127.0.0.1:8080/cb"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("inactive user approved: %v", err)
	}
	if err = drive.ExecSQL(ctx, d, `UPDATE users SET status='active' WHERE id='worker'`); err != nil {
		t.Fatal(err)
	}

	scoped := drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: w.ID, Rank: 2})
	if _, err = d.ApproveLink(scoped, "worker", "Laptop", "linux", challenge, "http://127.0.0.1:8080/cb"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("service token approved a link: %v", err)
	}

	// At most 20 live devices; revoking one frees a slot.
	for len(mustDevices(t, d, "worker")) < 20 {
		code, _ = approve("http://127.0.0.1:8080/cb")
		if _, _, err = d.RedeemLink(ctx, code, verifier, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = approve("http://127.0.0.1:8080/cb"); !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("21st device approved: %v", err)
	}
	if err = d.RevokeDevice(ctx, "worker", mustDevices(t, d, "worker")[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = approve("http://127.0.0.1:8080/cb"); err != nil {
		t.Fatalf("slot after revoke: %v", err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_events WHERE action='device.linked' AND user_id='worker'`); n != 20 {
		t.Fatalf("device.linked events: %d", n)
	}
}

func mustDevices(t *testing.T, d *drive.Store, user string) []drive.Device {
	t.Helper()
	list, err := d.Devices(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func link(t *testing.T, d *drive.Store, user string) string {
	t.Helper()
	ctx := context.Background()
	code, err := d.ApproveLink(ctx, user, "Laptop", "windows", challengeOf(verifier), "http://127.0.0.1:8080/cb")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := d.RedeemLink(ctx, code, verifier, nil)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestDeviceToken(t *testing.T) {
	st, w, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	token := link(t, d, "worker")
	if user, err := d.DeviceUser(ctx, token, anyKey); err != nil || user != "worker" {
		t.Fatalf("%q %v", user, err)
	}
	list := mustDevices(t, d, "worker")
	if len(list) != 1 || list[0].Name != "Laptop" || list[0].Platform != "windows" || list[0].LastUsed == 0 || list[0].Created == "" {
		t.Fatalf("%+v", list)
	}
	for _, bad := range []string{"", "kyd_", token[:len(token)-1], strings.TrimPrefix(token, "kyd_"), "Bearer " + token} {
		if _, err := d.DeviceUser(ctx, bad, anyKey); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%q: %v", bad, err)
		}
	}

	// Deactivation stops the token without a wipe instruction.
	if err := drive.ExecSQL(ctx, d, `UPDATE users SET status='inactive' WHERE id='worker'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeviceUser(ctx, token, anyKey); !errors.Is(err, drive.ErrDenied) || errors.As(err, new(drive.ErrDeviceRevoked)) {
		t.Fatalf("inactive user: %v", err)
	}
	if err := drive.ExecSQL(ctx, d, `UPDATE users SET status='active' WHERE id='worker'`); err != nil {
		t.Fatal(err)
	}

	// last_used is written at most every 5 minutes; 90 idle days kill the token.
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_devices SET last_used=?`, time.Now().Add(-4*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeviceUser(ctx, token, anyKey); err != nil {
		t.Fatal(err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_devices WHERE last_used<?`, time.Now().Add(-3*time.Minute).Unix()); n != 1 {
		t.Fatal("last_used rewritten within 5 minutes")
	}
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_devices SET last_used=?`, time.Now().Add(-89*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeviceUser(ctx, token, anyKey); err != nil {
		t.Fatalf("89 idle days: %v", err)
	}
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_devices SET last_used=?`, time.Now().Add(-91*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeviceUser(ctx, token, anyKey); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("91 idle days: %v", err)
	}

	// Revocation answers with the wipe flag for 30 days, then the row goes.
	for _, wipe := range []bool{true, false} {
		token := link(t, d, "worker")
		id := mustDevices(t, d, "worker")[len(mustDevices(t, d, "worker"))-1].ID
		if err := d.RevokeDevice(ctx, "stranger", id, wipe); !errors.Is(err, drive.ErrDenied) {
			t.Fatalf("revoked another user's device: %v", err)
		}
		if err := d.RevokeDevice(ctx, "worker", id, wipe); err != nil {
			t.Fatal(err)
		}
		if err := d.RevokeDevice(ctx, "worker", id, !wipe); !errors.Is(err, drive.ErrDenied) {
			t.Fatalf("revoked twice: %v", err)
		}
		var revoked drive.ErrDeviceRevoked
		if _, err := d.DeviceUser(ctx, token, anyKey); !errors.As(err, &revoked) || revoked.Wipe != wipe {
			t.Fatalf("wipe %v: %v", wipe, err)
		}
		for _, l := range mustDevices(t, d, "worker") {
			if l.ID == id {
				t.Fatal("revoked device listed")
			}
		}
		if err := drive.ExecSQL(ctx, d, `UPDATE drive_devices SET revoked_at=? WHERE id=?`, time.Now().Add(-31*24*time.Hour).Unix(), id); err != nil {
			t.Fatal(err)
		}
		if _, err := d.DeviceUser(ctx, token, anyKey); !errors.Is(err, drive.ErrDenied) || errors.As(err, new(drive.ErrDeviceRevoked)) {
			t.Fatalf("31 days after revoke: %v", err)
		}
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_events WHERE action='device.revoked'`); n != 2 {
		t.Fatalf("device.revoked events: %d", n)
	}

	// Pruning removes revoked rows after 30 days, idle-dead rows and spent or expired codes.
	live := link(t, d, "worker")
	recent := link(t, d, "worker")
	if err := d.RevokeDevice(ctx, "worker", mustDevices(t, d, "worker")[len(mustDevices(t, d, "worker"))-1].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApproveLink(ctx, "worker", "Pending", "macos", challengeOf(verifier), "http://127.0.0.1:9/cb"); err != nil {
		t.Fatal(err)
	}
	if err := d.PruneDevices(ctx, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_devices`); n != 2 {
		t.Fatalf("devices after prune: %d", n)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_device_codes`); n != 0 {
		t.Fatalf("codes after prune: %d", n)
	}
	if _, err := d.DeviceUser(ctx, live, anyKey); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeviceUser(ctx, recent, anyKey); !errors.As(err, new(drive.ErrDeviceRevoked)) {
		t.Fatalf("recently revoked pruned: %v", err)
	}

	// A device acts as its user but never as an administrator, and mints no other credential.
	admin := link(t, d, "admin")
	if _, err := d.DeviceUser(ctx, admin, anyKey); err != nil {
		t.Fatal(err)
	}
	device := drive.WithDevice(ctx)
	if _, err := d.Workspaces(device, "admin", true); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("device administration: %v", err)
	}
	if err := d.Grant(device, "admin", w.ID, "team", "manager"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("device changed a grant: %v", err)
	}
	if _, err := d.CreateWorkspace(device, "admin", "Rogue", 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("device created a workspace: %v", err)
	}
	if err := d.Grant(ctx, "admin", w.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateServiceToken(device, "worker", w.ID, "Robot", "reader", fmt.Sprint(time.Now().UnixNano())); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("device minted a service token: %v", err)
	}
	if _, err := d.CreateServiceToken(ctx, "worker", w.ID, "Robot", "reader", fmt.Sprint(time.Now().UnixNano())); err != nil {
		t.Fatalf("session could not mint a service token: %v", err)
	}
	if _, err := d.Workspaces(device, "worker", false); err != nil {
		t.Fatalf("device listing: %v", err)
	}
}

func TestUnlinkDevice(t *testing.T) {
	st, _, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	mine, other := link(t, d, "worker"), link(t, d, "worker")
	if err := d.UnlinkDevice(ctx, "worker", mine); err != nil {
		t.Fatal(err)
	}
	var revoked drive.ErrDeviceRevoked
	if _, err := d.DeviceUser(ctx, mine, anyKey); !errors.As(err, &revoked) || revoked.Wipe {
		t.Fatalf("unlinked: %v %+v", err, revoked)
	}
	if _, err := d.DeviceUser(ctx, other, anyKey); err != nil {
		t.Fatalf("other device: %v", err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_events WHERE action='device.revoked' AND user_id='worker'`); n != 1 {
		t.Fatalf("device.revoked events: %d", n)
	}
	for _, c := range []struct{ user, token string }{{"worker", mine}, {"stranger", other}, {"worker", "kyd_unknown"}} {
		if err := d.UnlinkDevice(ctx, c.user, c.token); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestPhoneLink(t *testing.T) {
	st, _, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	phone := func() string {
		t.Helper()
		code, err := d.ApprovePhone(ctx, "worker")
		if err != nil || len(code) < 43 {
			t.Fatalf("approve phone: %q %v", code, err)
		}
		return code
	}

	code := phone()
	if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_device_codes WHERE code_hash=? OR code_hash LIKE ?`, code, "%"+code[:20]+"%"); err != nil || n != 0 {
		t.Fatalf("code stored in clear: %d %v", n, err)
	}
	for _, c := range []struct{ name, platform string }{{"", "android"}, {" Pixel", "android"}, {strings.Repeat("x", 101), "android"}, {"Pixel", "linux"}, {"Pixel", ""}} {
		if _, _, err := d.RedeemPhone(ctx, code, c.name, c.platform, nil); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	token, user, err := d.RedeemPhone(ctx, code, "Pixel 9", "android", nil)
	if err != nil || user != "worker" || !strings.HasPrefix(token, "kyd_") || len(token) != 47 {
		t.Fatalf("redeem %q %q %v", token, user, err)
	}
	if _, _, err = d.RedeemPhone(ctx, code, "Pixel 9", "android", nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("reused phone code: %v", err)
	}
	if got, err := d.DeviceUser(ctx, token, anyKey); err != nil || got != "worker" {
		t.Fatalf("device user %q %v", got, err)
	}
	devices, err := d.Devices(ctx, "worker")
	if err != nil || len(devices) != 1 || devices[0].Name != "Pixel 9" || devices[0].Platform != "android" {
		t.Fatalf("devices %+v %v", devices, err)
	}

	// The two kinds of code never cross, and trying burns the code.
	code = phone()
	if _, _, err = d.RedeemLink(ctx, code, verifier, nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("phone code redeemed as a computer: %v", err)
	}
	if _, _, err = d.RedeemPhone(ctx, code, "Pixel 9", "android", nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("phone code reused after a computer attempt: %v", err)
	}
	link, err := d.ApproveLink(ctx, "worker", "Laptop", "linux", challengeOf(verifier), "http://127.0.0.1:8080/cb")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.RedeemPhone(ctx, link, "Pixel 9", "android", nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("computer code redeemed as a phone: %v", err)
	}

	// Phone codes live 120 seconds.
	code = phone()
	if left, err := drive.QueryInt(ctx, d, `SELECT expires FROM drive_device_codes WHERE used=0`); err != nil || int64(left)-time.Now().Unix() < 115 || int64(left)-time.Now().Unix() > 120 {
		t.Fatalf("phone code lifetime: %d %v", int64(left)-time.Now().Unix(), err)
	}
	if err = drive.ExecSQL(ctx, d, `UPDATE drive_device_codes SET expires=? WHERE used=0`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.RedeemPhone(ctx, code, "Pixel 9", "android", nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("expired phone code: %v", err)
	}
}
