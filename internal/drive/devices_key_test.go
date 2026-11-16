package drive_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// anyKey accepts every request, signed or not: the signature itself is the transport's check.
var anyKey = func(string, *ecdsa.PublicKey) error { return nil }

func deviceKey(t *testing.T, curve elliptic.Curve) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	k, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return k, der
}

func TestDeviceEnrolsItsKeyOnlyWhenLinking(t *testing.T) {
	st, _, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	priv, der := deviceKey(t, elliptic.P256())
	code, err := d.ApprovePhone(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	_, p384 := deviceKey(t, elliptic.P384())
	for _, bad := range [][]byte{p384, []byte("not a key"), der[:len(der)-1]} {
		if _, _, err := d.RedeemPhone(ctx, code, "Pixel 9", "android", bad); !errors.Is(err, drive.ErrInvalid) {
			t.Fatalf("bad key accepted: %v", err)
		}
	}
	// A refused key does not spend the code.
	token, _, err := d.RedeemPhone(ctx, code, "Pixel 9", "android", der)
	if err != nil {
		t.Fatal(err)
	}
	var gotID string
	var gotKey *ecdsa.PublicKey
	if _, err := d.DeviceUser(ctx, token, func(id string, key *ecdsa.PublicKey) error { gotID, gotKey = id, key; return nil }); err != nil {
		t.Fatal(err)
	}
	if gotID == "" || gotKey == nil || !gotKey.Equal(&priv.PublicKey) {
		t.Fatalf("enrolled key not handed to verify: %q %v", gotID, gotKey)
	}
	// An unsigned link has no key.
	unsigned := link(t, d, "worker")
	if _, err := d.DeviceUser(ctx, unsigned, func(_ string, key *ecdsa.PublicKey) error {
		if key != nil {
			t.Error("unsigned device has a key")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceVerifyRunsBeforeRevocationOrUseIsRevealed(t *testing.T) {
	st, _, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	token := link(t, d, "worker")
	refused := errors.New("bad signature")
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_devices SET last_used=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeviceUser(ctx, token, func(string, *ecdsa.PublicKey) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("verify error not returned: %v", err)
	}
	if n, err := drive.QueryInt(ctx, d, `SELECT last_used FROM drive_devices`); err != nil || n != 1 {
		t.Fatalf("a refused request kept the device alive: %d %v", n, err)
	}
	if err := d.RevokeDevice(ctx, "worker", mustDevices(t, d, "worker")[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeviceUser(ctx, token, func(string, *ecdsa.PublicKey) error { return refused }); !errors.Is(err, refused) || errors.As(err, new(drive.ErrDeviceRevoked)) {
		t.Fatalf("revocation revealed before verify: %v", err)
	}
}
