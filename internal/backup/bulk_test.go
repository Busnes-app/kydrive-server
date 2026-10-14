package backup_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kydrive-server/internal/backup"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/google/uuid"
)

type bulkFixture struct {
	dir, repo string
	key       []byte
	st        store.Store
	cfg       *config.Config
	workspace string
}

// newBulkFixture is a SQLite drive with an initialized independent Restic repository.
func newBulkFixture(t *testing.T) *bulkFixture {
	t.Helper()
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic CLI required")
	}
	ctx := context.Background()
	f := &bulkFixture{dir: t.TempDir(), repo: filepath.Join(t.TempDir(), "independent-repo"), key: make([]byte, 32)}
	t.Setenv("KYDRIVE_BULK_BACKUP_REPOSITORY", f.repo)
	f.key[0] = 12
	dbCfg := testdb.Config(t)
	if dbCfg.Driver != "sqlite" {
		t.Skip("drive SQLite-only")
	}
	dbCfg.DataDir = f.dir
	st, err := store.Open(ctx, dbCfg, f.key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f.st = st
	if err = st.Users().CreateUser(ctx, &store.User{ID: "admin", Username: "admin", Role: "admin", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	g := &store.Group{ID: "team", DisplayName: "Team", Members: []string{"admin"}}
	if err = st.Groups().ReplaceGroup(ctx, g, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "admin", "Team", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "admin", w.ID, g.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	f.workspace = w.ID
	f.cfg = &config.Config{Database: dbCfg, Security: config.SecurityConfig{EncryptionKey: f.key},
		Server: config.ServerConfig{AppName: "KyDrive"}, Backup: config.BackupConfig{Keep: 1}}
	if err = backup.InitBulkRepository(ctx, f.dir, f.key); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *bulkFixture) publish(t *testing.T, name, content string) (drive.File, drive.Version) {
	t.Helper()
	v, err := drive.WriteBlob(filepath.Join(f.dir, "blobs"), f.key, strings.NewReader(content), 1024)
	if err != nil {
		t.Fatal(err)
	}
	file, err := f.st.Drive().Publish(context.Background(), "admin", drive.File{Workspace: f.workspace, Name: name}, v, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file, v
}

func (f *bulkFixture) collect(t *testing.T, purpose backup.Purpose) recoveryclient.Payload {
	t.Helper()
	payload, err := backup.Collect(context.Background(), f.cfg, "test", purpose)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// restic runs the real CLI against the fixture repository.
func (f *bulkFixture) restic(t *testing.T, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("restic", append([]string{"--repo", f.repo, "--no-cache", "-q"}, args...)...)
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+hex.EncodeToString(f.key))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("restic %v: %v %s", args, err, out)
	}
	return out
}

// snapshots returns each snapshot's tags joined, newest last.
func (f *bulkFixture) snapshots(t *testing.T) []string {
	t.Helper()
	var list []struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(f.restic(t, "snapshots", "--json"), &list); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range list {
		out = append(out, strings.Join(s.Tags, ","))
	}
	return out
}

// legacy adds a snapshot tagged only "kydrive", as every snapshot was before purpose tags.
func (f *bulkFixture) legacy(t *testing.T, content string, age time.Duration) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "blob"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	f.restic(t, "backup", "--tag", "kydrive", "--time", time.Now().Add(-age).Format("2006-01-02 15:04:05"), src)
}

// hasBlob reports whether the repository still stores content. A file this small is one
// Restic data blob whose ID is the SHA-256 of its bytes.
func (f *bulkFixture) hasBlob(t *testing.T, digest string) bool {
	t.Helper()
	return slices.Contains(strings.Split(string(f.restic(t, "list", "blobs")), "\n"), "data "+digest)
}

func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// stored is the digest of v's bytes on disk, which is what Restic stores for a sealed blob.
func (f *bulkFixture) stored(t *testing.T, v drive.Version) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, "blobs", v.Blob))
	if err != nil {
		t.Fatal(err)
	}
	return digest(string(raw))
}

// read returns the plaintext of v restored under dir.
func (f *bulkFixture) read(t *testing.T, dir string, v drive.Version, sealed bool) string {
	t.Helper()
	r, err := drive.OpenBlob(filepath.Join(dir, v.Blob), f.key, sealed, v.Size)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func restoreInto(t *testing.T, f *bulkFixture, payload recoveryclient.Payload) (string, string, error) {
	t.Helper()
	opened := t.TempDir()
	for _, file := range payload.Files {
		p := filepath.Join(opened, file.Path)
		os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, file.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(t.TempDir(), "restored")
	return opened, target, backup.RestoreBulk(context.Background(), opened, f.repo, target)
}

func TestBulkBackupRestoreAndMissingBytes(t *testing.T) {
	f := newBulkFixture(t)
	_, v := f.publish(t, "important.txt", "important bytes")
	opened, target, err := restoreInto(t, f, f.collect(t, backup.PurposeDeposit))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, target, v, true); got != "important bytes" {
		t.Fatalf("restore: %q", got)
	}
	if tags := f.snapshots(t); len(tags) != 1 || tags[0] != "kydrive,deposit" {
		t.Fatalf("snapshot tags: %v", tags)
	}
	// Wrong restored key and missing repository must each fail before publishing files.
	os.WriteFile(filepath.Join(opened, "data/encryption.key"), []byte(hex.EncodeToString(make([]byte, 32))), 0600)
	if err = backup.RestoreBulk(context.Background(), opened, f.repo, filepath.Join(t.TempDir(), "bad")); err == nil {
		t.Fatal("wrong key accepted")
	}
	os.Remove(filepath.Join(f.dir, "blobs", v.Blob))
	if _, err = backup.Collect(context.Background(), f.cfg, "test", backup.PurposeDeposit); err == nil {
		t.Fatal("missing live blob accepted as backup")
	}
}

// A drive holding a plaintext blob from before sealing beside a sealed one: version 1
// capsules (plaintext only) still restore, version 1 cannot carry sealed blobs, sealed blobs
// restore as ciphertext that decrypts, and a sealed blob failing authentication is never
// backed up.
func TestBulkManifestVersionsAndSealedBlobs(t *testing.T) {
	f := newBulkFixture(t)
	ctx := context.Background()
	body := "legacy bytes"
	legacy := drive.Version{Blob: uuid.NewString(), Digest: digest(body), Size: int64(len(body))}
	os.MkdirAll(filepath.Join(f.dir, "blobs"), 0700)
	if err := os.WriteFile(filepath.Join(f.dir, "blobs", legacy.Blob), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Drive().Publish(ctx, "admin", drive.File{Workspace: f.workspace, Name: "legacy.txt"}, legacy, 0); err != nil {
		t.Fatal(err)
	}
	manifest := func(opened string) backup.BulkManifest {
		var m backup.BulkManifest
		raw, _ := os.ReadFile(filepath.Join(opened, backup.BulkManifestPath))
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	setVersion := func(opened string, m backup.BulkManifest, version int) {
		m.Version = version
		raw, _ := json.Marshal(m)
		os.WriteFile(filepath.Join(opened, backup.BulkManifestPath), raw, 0600)
	}
	opened, _, err := restoreInto(t, f, f.collect(t, backup.PurposeDeposit))
	if err != nil {
		t.Fatal(err)
	}
	m := manifest(opened)
	if m.Version != 2 || len(m.Files) != 1 || m.Files[0].Sealed {
		t.Fatalf("legacy manifest %+v", m)
	}
	setVersion(opened, m, 1)
	target := filepath.Join(t.TempDir(), "restored-v1")
	if err = backup.RestoreBulk(ctx, opened, f.repo, target); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, target, legacy, false); got != body {
		t.Fatalf("v1 restore: %q", got)
	}

	_, v := f.publish(t, "important.txt", "important bytes")
	opened, target, err = restoreInto(t, f, f.collect(t, backup.PurposeDeposit))
	if err != nil {
		t.Fatal(err)
	}
	if m = manifest(opened); m.Version != 2 || len(m.Files) != 2 {
		t.Fatalf("mixed manifest %+v", m)
	}
	raw, err := os.ReadFile(filepath.Join(target, v.Blob))
	if err != nil || strings.Contains(string(raw), "important") || int64(len(raw)) != drive.SealedSize(v.Size) {
		t.Fatalf("sealed blob restored as %q %v", raw, err)
	}
	if got := f.read(t, target, v, true); got != "important bytes" {
		t.Fatalf("restore: %q", got)
	}
	if got := f.read(t, target, legacy, false); got != body {
		t.Fatalf("legacy restore: %q", got)
	}
	setVersion(opened, m, 1)
	if err = backup.RestoreBulk(ctx, opened, f.repo, filepath.Join(t.TempDir(), "v1-sealed")); err == nil {
		t.Fatal("version 1 manifest with sealed blobs accepted")
	}
	live := filepath.Join(f.dir, "blobs", v.Blob)
	raw, _ = os.ReadFile(live)
	raw[len(raw)-1] ^= 1
	os.WriteFile(live, raw, 0600)
	if _, err = backup.Collect(ctx, f.cfg, "test", backup.PurposeDeposit); err == nil {
		t.Fatal("tampered sealed blob accepted as backup")
	}
}

// Three deposits with a purge between them, beside two month-old legacy snapshots: an off
// policy forgets nothing; a one-day policy leaves only the newest deposit, the purged bytes
// and the legacy bytes leave the repository, and the newest capsule still restores.
func TestPruneBulkForgetsPurgedBytes(t *testing.T) {
	f := newBulkFixture(t)
	ctx := context.Background()
	_, kept := f.publish(t, "kept.txt", "bytes every capsule keeps")
	secret, purged := f.publish(t, "secret.txt", "secret bytes the manager purged")
	purgedStored := f.stored(t, purged)
	f.legacy(t, "legacy-only bytes", 60*24*time.Hour)
	f.legacy(t, "legacy-only bytes", 59*24*time.Hour)
	oldest := f.collect(t, backup.PurposeDeposit)
	if err := f.st.Drive().SetTrash(ctx, "admin", secret.ID, secret.Revision, true); err != nil {
		t.Fatal(err)
	}
	blobs, err := f.st.Drive().PurgeFile(ctx, "admin", secret.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = drive.RemoveBlobs(filepath.Join(f.dir, "blobs"), blobs); err != nil {
		t.Fatal(err)
	}
	f.collect(t, backup.PurposeDeposit)
	newest := f.collect(t, backup.PurposeDeposit)
	if !f.hasBlob(t, purgedStored) || !f.hasBlob(t, digest("legacy-only bytes")) {
		t.Fatal("positive control: purged and legacy bytes are in the repository before prune")
	}

	n, err := backup.PruneBulk(ctx, f.dir, f.key, backup.BulkPolicy{}, 1)
	if err != nil || n != 0 || len(f.snapshots(t)) != 5 {
		t.Fatalf("off policy: forgot %d, %v, left %v", n, err, f.snapshots(t))
	}
	n, err = backup.PruneBulk(ctx, f.dir, f.key, backup.BulkPolicy{Daily: 1}, 1)
	if err != nil || n != 4 {
		t.Fatalf("prune: forgot %d, %v", n, err)
	}
	if tags := f.snapshots(t); len(tags) != 1 || tags[0] != "kydrive,deposit" {
		t.Fatalf("left %v, want the newest deposit", tags)
	}
	if f.hasBlob(t, purgedStored) || f.hasBlob(t, digest("legacy-only bytes")) {
		t.Fatal("purged or legacy bytes survived prune")
	}
	f.restic(t, "check", "--read-data")
	_, target, err := restoreInto(t, f, newest)
	if err != nil {
		t.Fatalf("newest capsule no longer restores: %v", err)
	}
	if got := f.read(t, target, kept, true); got != "bytes every capsule keeps" {
		t.Fatalf("restored %q", got)
	}
	if _, _, err = restoreInto(t, f, oldest); err == nil || !strings.Contains(err.Error(), "restic restore failed") {
		t.Fatalf("a capsule naming a forgotten snapshot: %v", err)
	}
}

// The newest keepLast snapshots of any kind survive, so an untagged one a retained local copy
// names is kept. Drill snapshots go after a day even under a longer policy. The scheduled
// entry point runs at most once a day and records each run.
func TestPruneBulkKeepsNewestLegacyAndAgesDrills(t *testing.T) {
	f := newBulkFixture(t)
	ctx := context.Background()
	f.publish(t, "kept.txt", "bytes every capsule keeps")
	f.legacy(t, "legacy-only bytes", 30*24*time.Hour)
	f.collect(t, backup.PurposeDeposit)
	if n, err := backup.PruneBulk(ctx, f.dir, f.key, backup.BulkPolicy{Daily: 1}, 2); err != nil || n != 0 {
		t.Fatalf("keepLast 2 forgot %d legacy snapshots: %v", n, err)
	}
	if n, err := backup.PruneBulk(ctx, f.dir, f.key, backup.BulkPolicy{Daily: 1}, 1); err != nil || n != 1 {
		t.Fatalf("keepLast 1: forgot %d, %v", n, err)
	}

	f.restic(t, "backup", "--tag", "kydrive,drill", "--time", time.Now().Add(-72*time.Hour).Format("2006-01-02 15:04:05"), t.TempDir())
	f.collect(t, backup.PurposeDrill)
	f.collect(t, backup.PurposeExport)
	settings := backup.Settings(ctx, f.st.Settings())
	now := time.Now()
	if _, ran, err := backup.PruneBulkIfDue(ctx, f.cfg, settings, now); ran || err != nil {
		t.Fatalf("off policy ran: %v", err)
	}
	if err := backup.SetBulkPolicy(settings, backup.BulkPolicy{Daily: 30}); err != nil {
		t.Fatal(err)
	}
	res, ran, err := backup.PruneBulkIfDue(ctx, f.cfg, settings, now)
	if !ran || err != nil || res.Forgotten != 1 {
		t.Fatalf("due prune: %+v %v %v", res, ran, err)
	}
	if tags := f.snapshots(t); strings.Join(tags, " ") != "kydrive,deposit kydrive,drill kydrive,export" {
		t.Fatalf("left %v", tags)
	}
	if last, ok, err := backup.LastBulkPrune(settings); !ok || err != nil || !last.At.Equal(now.UTC()) || last.Forgotten != 1 {
		t.Fatalf("last prune %+v %v %v", last, ok, err)
	}
	if _, ran, _ := backup.PruneBulkIfDue(ctx, f.cfg, settings, now.Add(23*time.Hour)); ran {
		t.Fatal("ran twice in a day")
	}
	if _, ran, err := backup.PruneBulkIfDue(ctx, f.cfg, settings, now.Add(25*time.Hour)); !ran || err != nil {
		t.Fatalf("not due after a day: %v", err)
	}
}

// A failed prune is recorded with restic's reason, so it is retried tomorrow, not every deposit.
func TestPruneBulkIfDueRecordsFailure(t *testing.T) {
	f := newBulkFixture(t)
	ctx := context.Background()
	settings := backup.Settings(ctx, f.st.Settings())
	if err := backup.SetBulkPolicy(settings, backup.BulkPolicy{Weekly: 4}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.repo); err != nil {
		t.Fatal(err)
	}
	res, ran, err := backup.PruneBulkIfDue(ctx, f.cfg, settings, time.Now())
	// restic's wording varies by version; its "Fatal:" prefix shows stderr was kept.
	if !ran || err == nil || !strings.Contains(res.Error, "Fatal:") {
		t.Fatalf("failure not recorded: %+v %v %v", res, ran, err)
	}
	if last, ok, _ := backup.LastBulkPrune(settings); !ok || last.Error != res.Error {
		t.Fatalf("last prune %+v", last)
	}
}

// startLockHolder runs TestDrillLockProcess in a child holding <dir>/name until killed.
func startLockHolder(t *testing.T, dir, name string) func() {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDrillLockProcess$")
	cmd.Env = append(os.Environ(), "KY_DRILL_LOCK_HELPER=1", "KY_DRILL_LOCK_DIR="+dir, "KY_DRILL_LOCK_NAME="+name)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stop := func() { stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }
	t.Cleanup(stop)
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("helper did not acquire lock: %q %v", line, err)
	}
	return stop
}

// Another process holding bulk.lock skips the prune without recording a run, and a backup
// waits for the lock instead of failing.
func TestBulkLockContention(t *testing.T) {
	f := newBulkFixture(t)
	ctx := context.Background()
	f.publish(t, "kept.txt", "bytes every capsule keeps")
	settings := backup.Settings(ctx, f.st.Settings())
	if err := backup.SetBulkPolicy(settings, backup.BulkPolicy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	stop := startLockHolder(t, f.dir, "bulk.lock")
	if _, err := backup.PruneBulk(ctx, f.dir, f.key, backup.BulkPolicy{Daily: 1}, 1); !errors.Is(err, backup.ErrBulkBusy) {
		t.Fatalf("prune under contention: %v", err)
	}
	if _, ran, err := backup.PruneBulkIfDue(ctx, f.cfg, settings, time.Now()); ran || !errors.Is(err, backup.ErrBulkBusy) {
		t.Fatalf("scheduled prune under contention: ran=%v %v", ran, err)
	}
	if _, ok, _ := backup.LastBulkPrune(settings); ok {
		t.Fatal("a skipped prune was recorded")
	}
	done := make(chan error, 1)
	go func() {
		_, err := backup.Collect(ctx, f.cfg, "test", backup.PurposeDeposit)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("backup did not wait for the lock: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("backup still blocked after the holder exited")
	}
	if _, ran, err := backup.PruneBulkIfDue(ctx, f.cfg, settings, time.Now()); !ran || err != nil {
		t.Fatalf("prune after release: %v", err)
	}
	// The drill holds bulk.lock around its own bulk restore without deadlocking on it.
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	result, err := backup.RunDrill(ctx, f.cfg, f.collect(t, backup.PurposeDrill))
	if err != nil || !result.Passed || !slices.ContainsFunc(result.Checks, func(c recoveryclient.Check) bool { return c.Name == "Drive bulk restore" && c.Passed }) {
		t.Fatalf("drill with bulk restore: %+v %v", result, err)
	}
}
