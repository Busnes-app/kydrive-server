package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/ky-primitives/recoverykey"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

const restoreFixtureToken = "public-fixture-session-token"

func sealFixture(t *testing.T, service string) (string, []string) {
	t.Helper()
	priv, err := recoverykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	shares, err := recoverykey.Split(priv, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	key := recoveryclient.RecoveryKey{Public: priv.Public(), Threshold: 2, TotalShares: 3}
	ctx := context.Background()
	keyBytes := bytes.Repeat([]byte{7}, 32)
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: sourcePath}, keyBytes)
	if err != nil {
		t.Fatal(err)
	}
	u := &store.User{ID: "owner", Username: "owner", PasswordHash: "h", Role: "user", Status: "active", SSOProvider: "local"}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: crypto.SHA256Hex([]byte(restoreFixtureToken)), UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "h", false); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings().SetSetting(ctx, "integration-reference", "retained"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	// The snapshot predates a real sign-out on the original live database. Sealing keeps
	// those snapshot bytes rather than silently collecting the already-revoked state.
	live, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: sourcePath}, keyBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	hash := crypto.SHA256Hex([]byte(restoreFixtureToken))
	if err := live.Sessions().DeleteSession(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Sessions().GetSession(ctx, hash); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("source session did not revoke after the snapshot", err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	payload := recoveryclient.Payload{ServiceName: service, AppVersion: "1.0.0",
		Files: []recoveryclient.File{
			{Path: "data/x.db", Data: []byte("payload"), Mode: 0600},
			{Path: "data/ky_server.db", Data: db, Mode: 0600},
			{Path: "data/encryption.key", Data: []byte(hex.EncodeToString(keyBytes) + "\n"), Mode: 0600},
			{Path: "config/integrations.json", Data: []byte(`{"reference":"retained"}`), Mode: 0600},
		}}
	raw, _, err := recoveryclient.Seal(payload, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "x.kycap")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Non-consecutive indices: {1,2} would let an XOR-shaped bug pass.
	return path, []string{shares[0].String(), shares[2].String()}
}

func TestRestoreExtractsWithTwoShares(t *testing.T) {
	path, shares := sealFixture(t, "busnes_app")
	target := t.TempDir()
	var out bytes.Buffer
	if err := restore(path, target, "busnes_app", shares, &out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "data", "x.db"))
	if err != nil || string(got) != "payload" {
		t.Fatalf("restored file: %q %v", got, err)
	}
	st, err := store.Open(context.Background(), config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(target, "data", "ky_server.db")}, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	request := httptest.NewRequest(http.MethodGet, "https://drive.example.test/api/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-" + auth.SessionCookieName, Value: restoreFixtureToken})
	sessions := auth.NewSessionManager(st, config.SecurityConfig{CookieSecure: true})
	if _, _, err := sessions.AuthenticateRequest(request); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("restored signed-out browser session authenticated after restore", err)
	}
	if _, err := st.Sessions().GetSession(context.Background(), crypto.SHA256Hex([]byte(restoreFixtureToken))); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("restored session was not revoked", err)
	}
	if value, err := st.Settings().GetSetting(context.Background(), "integration-reference"); err != nil || value != "retained" {
		t.Fatal("restore lost integration configuration", value, err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "config", "integrations.json")); err != nil || string(data) != `{"reference":"retained"}` {
		t.Fatal("restore changed sealed configuration", string(data), err)
	}
	if !strings.Contains(out.String(), "busnes_app") {
		t.Fatalf("manifest not printed: %s", out.String())
	}
}

func TestRestoreRefusesAnotherService(t *testing.T) {
	path, shares := sealFixture(t, "someone_else")
	err := restore(path, t.TempDir(), "busnes_app", shares, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "someone_else") {
		t.Fatalf("got %v, want a service-name refusal naming the capsule's service", err)
	}
}

func TestRestoreRefusesTheWrongKit(t *testing.T) {
	path, _ := sealFixture(t, "busnes_app")
	_, otherShares := sealFixture(t, "busnes_app")
	err := restore(path, t.TempDir(), "busnes_app", otherShares, &bytes.Buffer{})
	if !errors.Is(err, capsule.ErrWrongRecoveryKey) {
		t.Fatalf("got %v, want ErrWrongRecoveryKey", err)
	}
}

func TestRestoreRefusesOneShare(t *testing.T) {
	path, shares := sealFixture(t, "busnes_app")
	if err := restore(path, t.TempDir(), "busnes_app", shares[:1], &bytes.Buffer{}); err == nil {
		t.Fatal("one share of a 2-of-3 kit was accepted")
	}
}

func TestRestoreRefusesUnsafeTargetBeforeExtracting(t *testing.T) {
	path, shares := sealFixture(t, "busnes_app")
	for _, name := range []string{"target?other", "target#other"} {
		target := filepath.Join(t.TempDir(), name)
		if err := restore(path, target, "busnes_app", shares, &bytes.Buffer{}); err == nil {
			t.Fatal("accepted SQLite URI delimiter", target)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("unsafe target was written", err)
		}
	}
}

func TestRestoreResetRefusesMissingFilesWithoutCreatingThem(t *testing.T) {
	dir := t.TempDir()
	if err := resetRestored(context.Background(), dir); err == nil {
		t.Fatal("accepted a missing restored database")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatal("reset created deployment files", entries, err)
	}
}

func TestRestoreResetCanResumeWithoutMintingKey(t *testing.T) {
	path, shares := sealFixture(t, "busnes_app")
	target := t.TempDir()
	// Extract as the library does, then make the reset unavailable without losing its data.
	if err := recoveryclient.Restore(path, target, "busnes_app", shares, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(target, "data", "encryption.key")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if err := resetRestored(context.Background(), filepath.Join(target, "data")); err == nil {
		t.Fatal("reset succeeded without the restored deployment key")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("reset minted a replacement deployment key", err)
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := resetRestored(context.Background(), filepath.Join(target, "data")); err != nil {
			t.Fatal("reset could not resume", err)
		}
	}
}

func TestRestoreRequiresResetBeforeSuccess(t *testing.T) {
	priv, err := recoverykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	shares, err := recoverykey.Split(priv, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := recoveryclient.Seal(recoveryclient.Payload{ServiceName: "busnes_app", AppVersion: "1.0.0", Files: []recoveryclient.File{{Path: "data/incomplete", Data: []byte("incomplete"), Mode: 0600}}}, recoveryclient.RecoveryKey{Public: priv.Public(), Threshold: 2, TotalShares: 3})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "incomplete.kycap")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := restore(path, target, "busnes_app", []string{shares[0].String(), shares[2].String()}, &bytes.Buffer{}); !errors.Is(err, errResetFailed) {
		t.Fatal("failed access reset was not reported", err)
	}
	if _, err := os.Stat(filepath.Join(target, "data", "ky_server.db")); !os.IsNotExist(err) {
		t.Fatal("failed restore created a replacement database", err)
	}
}

func TestRestoreResetDoesNotInitializeAnEmptyOrUnrelatedDatabase(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "unrelated"}[unrelated], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ky_server.db")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if unrelated {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`CREATE TABLE unrelated(id TEXT PRIMARY KEY)`)
				if err := errors.Join(err, db.Close()); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "encryption.key"), []byte(strings.Repeat("07", 32)+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := resetRestored(context.Background(), dir); err == nil {
				t.Fatal("invalid restored database became a new deployment")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid database was changed", err)
			}
		})
	}
}
