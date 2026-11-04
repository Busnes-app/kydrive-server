package backup_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/ky-primitives/recoverykey"
	"github.com/Busnes-app/kydrive-server/internal/backup"
)

// dbParts returns the database members in payload order and their concatenation.
func dbParts(payload recoveryclient.Payload) ([]string, []byte) {
	var names []string
	var db []byte
	for _, f := range payload.Files {
		if strings.HasPrefix(f.Path, "data/ky_server.db") {
			names = append(names, f.Path)
			db = append(db, f.Data...)
		}
	}
	return names, db
}

// A database past capsule.MaxFileBytes is sealed as ordered parts the real library accepts,
// and the drill reassembles them into the identical database before checking it.
func TestCollectSplitsADatabaseLargerThanOneMember(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	cfg, st := sqliteInstance(t)
	ctx := context.Background()
	if err := st.Settings().SetSetting(ctx, "filler", strings.Repeat("x", int(capsule.MaxFileBytes))); err != nil {
		t.Fatal(err)
	}
	payload, err := backup.Collect(ctx, cfg, "1.0.0", backup.PurposeDeposit)
	if err != nil {
		t.Fatal(err)
	}
	names, db := dbParts(payload)
	if want := []string{"data/ky_server.db.000-of-002", "data/ky_server.db.001-of-002"}; !slices.Equal(names, want) {
		t.Fatalf("database members %v, want %v", names, want)
	}
	for _, f := range payload.Files {
		if int64(len(f.Data)) > capsule.MaxFileBytes {
			t.Fatalf("%s is %d bytes, over the member limit", f.Path, len(f.Data))
		}
	}
	priv, err := recoverykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := recoveryclient.Seal(payload, recoveryclient.RecoveryKey{Public: priv.Public(), Threshold: 2, TotalShares: 3})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	opened, _, err := capsule.Open(raw, priv, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range backup.Checks(dir, opened) {
		if !c.Passed {
			t.Errorf("check %q failed: %s", c.Name, c.Message)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "data", "ky_server.db"))
	if err != nil || !bytes.Equal(got, db) {
		t.Fatalf("reassembled database differs from the snapshot (%d of %d bytes): %v", len(got), len(db), err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "data", "ky_server.db.*")); len(left) != 0 {
		t.Fatalf("parts left after joining: %v", left)
	}
}

// writePayload lays the payload out as an opened capsule would.
func writePayload(t *testing.T, payload recoveryclient.Payload, skip string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range payload.Files {
		if f.Path == skip {
			continue
		}
		full := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, f.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDrillJoinsManyPartsAndRefusesAMissingOne(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	backup.SetDBPartBytes(t, 4096)
	cfg, _ := payloadConfig(t)
	payload, err := backup.Collect(context.Background(), cfg, "1.0.0", backup.PurposeDeposit)
	if err != nil {
		t.Fatal(err)
	}
	names, db := dbParts(payload)
	if len(names) < 3 {
		t.Fatalf("want several parts, got %v", names)
	}
	dir := writePayload(t, payload, "")
	for _, c := range backup.Checks(dir, manifestFor(payload)) {
		if !c.Passed {
			t.Errorf("check %q failed: %s", c.Name, c.Message)
		}
	}
	if got, err := os.ReadFile(filepath.Join(dir, "data", "ky_server.db")); err != nil || !bytes.Equal(got, db) {
		t.Fatalf("reassembled database differs: %v", err)
	}

	dir = writePayload(t, payload, names[1])
	checks := backup.Checks(dir, manifestFor(payload))
	if len(checks) != 1 || checks[0].Passed || checks[0].Name != "Database parts" {
		t.Fatalf("a missing part was not refused: %+v", checks)
	}
}

func TestJoinDatabaseRefusesAnInconsistentSet(t *testing.T) {
	for name, files := range map[string][]string{
		"extra":                     {"ky_server.db.000-of-002", "ky_server.db.001-of-002", "ky_server.db.002-of-002"},
		"gap":                       {"ky_server.db.000-of-003", "ky_server.db.002-of-003"},
		"missing last":              {"ky_server.db.000-of-003", "ky_server.db.001-of-003"},
		"duplicate":                 {"ky_server.db.000-of-002", "ky_server.db.001-of-002", "ky_server.db.001-of-003"},
		"whole and different parts": {"ky_server.db", "ky_server.db.000-of-001"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(f), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := backup.JoinDatabase(dir); !errors.Is(err, backup.ErrDatabaseParts) {
				t.Fatalf("got %v, want ErrDatabaseParts", err)
			}
			for _, f := range files {
				if got, err := os.ReadFile(filepath.Join(dir, f)); err != nil || string(got) != f {
					t.Fatalf("%s changed: %q %v", f, got, err)
				}
			}
			if !slices.Contains(files, "ky_server.db") {
				if _, err := os.Stat(filepath.Join(dir, "ky_server.db")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("a refused set still produced a database")
				}
			}
		})
	}
}

func TestJoinDatabaseLeavesASingleMemberAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ky_server.db"), []byte("whole"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := backup.JoinDatabase(dir); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "ky_server.db")); err != nil || string(got) != "whole" {
		t.Fatalf("single-member database changed: %q %v", got, err)
	}
	if err := backup.JoinDatabase(filepath.Join(dir, "absent")); err != nil {
		t.Fatalf("a missing directory is not a split database: %v", err)
	}
}

func TestSizeWarningAtEightyPercent(t *testing.T) {
	limit := capsule.MaxExpandedBytes * 8 / 10
	if w := backup.SizeWarningFor(limit); w != "" {
		t.Fatalf("warned at the threshold: %s", w)
	}
	if w := backup.SizeWarningFor(limit + 1); !strings.Contains(w, "80%") {
		t.Fatalf("no warning past 80%%: %q", w)
	}
}

// Files that only share the name, such as an operator's copy, are not parts.
func TestJoinDatabaseIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"ky_server.db", "ky_server.db.bak", "ky_server.db-wal"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := backup.JoinDatabase(dir); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "ky_server.db")); err != nil || string(got) != "ky_server.db" {
		t.Fatalf("database changed: %q %v", got, err)
	}
}

// A join interrupted after it published the database finishes on the next run.
func TestJoinDatabaseFinishesAnInterruptedJoin(t *testing.T) {
	dir := t.TempDir()
	parts := map[string]string{"ky_server.db.000-of-002": "first half ", "ky_server.db.001-of-002": "second half", "ky_server.db": "first half second half"}
	for name, data := range parts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := backup.JoinDatabase(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "ky_server.db" {
		t.Fatalf("left behind: %v", entries)
	}
}

// Removal stopped after the last part: the leading parts left behind still finish.
func TestJoinDatabaseFinishesAnInterruptedCleanup(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{"ky_server.db.000-of-002": "first half ", "ky_server.db": "first half second half"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := backup.JoinDatabase(dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
	// A leftover that does not match the database is refused, and nothing is removed.
	for name, data := range map[string]string{"ky_server.db.000-of-002": "other bytes"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := backup.JoinDatabase(dir); !errors.Is(err, backup.ErrDatabaseParts) {
		t.Fatalf("mismatched leftover: %v", err)
	}
}
