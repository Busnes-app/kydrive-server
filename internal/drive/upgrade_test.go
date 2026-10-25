package drive_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// kyb1Files publishes files whose versions are KYB1 blobs and queues them as the schema step
// does on a database written before KYB2. It returns the file IDs and the distinct blob IDs.
func kyb1Files(t *testing.T, d *drive.Store, w drive.Workspace, root string) ([]string, []string) {
	t.Helper()
	ctx := context.Background()
	if err := d.Quota(ctx, "admin", w.ID, 1<<20); err != nil {
		t.Fatal(err)
	}
	var files, blobs []string
	for i, n := range []int{0, 5, chunk, 2*chunk + 3} {
		v, err := drive.WriteBlobV1(root, testKey, bytes.NewReader(plaintext(n)))
		if err != nil {
			t.Fatal(err)
		}
		f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: string(rune('a'+i)) + ".bin"}, v, 0)
		if err != nil {
			t.Fatal(err)
		}
		files, blobs = append(files, f.ID), append(blobs, v.Blob)
	}
	// A copy shares its source's blob; a second revision of the first file adds one more.
	c, err := d.CopyFile(ctx, "worker", files[3], drive.Location{Workspace: w.ID, Name: "copy.bin"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := drive.WriteBlobV1(root, testKey, bytes.NewReader([]byte("revision two")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Publish(ctx, "worker", drive.File{ID: files[0], Workspace: w.ID, Name: "a.bin"}, v, 1); err != nil {
		t.Fatal(err)
	}
	files, blobs = append(files, c.ID), append(blobs, v.Blob)
	// Rerun the schema step that fills the queue, as on an upgraded database.
	if err = drive.ExecSQL(ctx, d, `DROP TABLE drive_blob_kyb1; DELETE FROM drive_schema WHERE version=?`, drive.SchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err = drive.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM drive_blob_kyb1`); n != len(blobs) {
		t.Fatalf("schema queued %d blobs, want %d", n, len(blobs))
	}
	return files, blobs
}

func magic(t *testing.T, root, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, id))
	if err != nil || len(b) < 4 {
		t.Fatalf("%s: %v", id, err)
	}
	return string(b[:4])
}

func TestUpgradeBlobsRewritesInPlace(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	files, blobs := kyb1Files(t, d, w, root)
	// A KYB2 blob written meanwhile is never queued.
	fresh, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "new.bin"}, blob(t, root, "new"), 0)
	if err != nil {
		t.Fatal(err)
	}
	readable(t, d, root, files...)
	n, err := d.UpgradeBlobs(ctx, root, testKey, 0)
	if err != nil || n != len(blobs) {
		t.Fatalf("upgraded %d of %d: %v", n, len(blobs), err)
	}
	for _, id := range blobs {
		if m := magic(t, root, id); m != "KYB2" {
			t.Fatalf("%s is still %s", id, m)
		}
	}
	readable(t, d, root, append(files, fresh.ID)...)
	if count(t, d, `SELECT COUNT(*) FROM drive_blob_kyb1`) != 0 || count(t, d, `SELECT COUNT(DISTINCT blob) FROM drive_versions`) != len(blobs)+1 {
		t.Fatal("queue not drained or blob IDs changed")
	}
	if entries, _ := os.ReadDir(root); len(entries) != len(blobs)+1 {
		t.Fatalf("%d files in the blob root, want %d", len(entries), len(blobs)+1)
	}
	// Idempotent: a second run touches nothing.
	before, _ := os.ReadFile(filepath.Join(root, blobs[3]))
	if n, err = d.UpgradeBlobs(ctx, root, testKey, 0); err != nil || n != 0 {
		t.Fatalf("rerun %d %v", n, err)
	}
	if after, _ := os.ReadFile(filepath.Join(root, blobs[3])); !bytes.Equal(before, after) {
		t.Fatal("rerun rewrote an upgraded blob")
	}
}

// A run can die at any point; every blob stays readable and the next run finishes.
func TestUpgradeBlobsResumesAfterCrash(t *testing.T) {
	for _, step := range []string{"mid-stage", "staged", "renamed", "committed"} {
		t.Run(step, func(t *testing.T) {
			d, w, root := managerFixture(t)
			ctx := context.Background()
			files, blobs := kyb1Files(t, d, w, root)
			id := blobs[3]
			switch step {
			case "mid-stage":
				// A partial staging file, as a process killed while sealing leaves.
				if err := os.WriteFile(filepath.Join(root, ".upload-crash"), []byte("KYB2age-encryption.org/v1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "staged", "renamed":
				tmp, err := drive.StageUpgrade(ctx, d, root, testKey, id)
				if err != nil || tmp == "" {
					t.Fatalf("stage %q %v", tmp, err)
				}
				if step == "renamed" {
					if err = os.Rename(tmp, filepath.Join(root, id)); err != nil {
						t.Fatal(err)
					}
				}
			case "committed":
				tmp, err := drive.StageUpgrade(ctx, d, root, testKey, id)
				if err != nil {
					t.Fatal(err)
				}
				if err = drive.CommitUpgrade(ctx, d, root, id, tmp); err != nil {
					t.Fatal(err)
				}
			}
			want := map[string]string{"mid-stage": "KYB1", "staged": "KYB1", "renamed": "KYB2", "committed": "KYB2"}[step]
			if m := magic(t, root, id); m != want {
				t.Fatalf("crash state %s, want %s", m, want)
			}
			readable(t, d, root, files...)
			queued := len(blobs)
			if step == "committed" {
				queued--
			}
			if n, err := d.UpgradeBlobs(ctx, root, testKey, 0); err != nil || n != queued {
				t.Fatalf("rerun upgraded %d, want %d: %v", n, queued, err)
			}
			for _, b := range blobs {
				if m := magic(t, root, b); m != "KYB2" {
					t.Fatalf("%s is still %s", b, m)
				}
			}
			readable(t, d, root, files...)
			if count(t, d, `SELECT COUNT(*) FROM drive_blob_kyb1`) != 0 {
				t.Fatal("queue not drained")
			}
			// Leftover staging files are the orphan sweep's.
			if _, err := d.SweepOrphans(ctx, root, -time.Hour); err != nil {
				t.Fatal(err)
			}
			if entries, _ := os.ReadDir(root); len(entries) != len(blobs) {
				t.Fatalf("%d files after the sweep, want %d", len(entries), len(blobs))
			}
			readable(t, d, root, files...)
		})
	}
}

// A purge between staging and commit is not undone; a copy between them keeps its bytes.
func TestUpgradeBlobsRacesPurgeAndCopy(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	files, blobs := kyb1Files(t, d, w, root)
	tmp, err := drive.StageUpgrade(ctx, d, root, testKey, blobs[1])
	if err != nil {
		t.Fatal(err)
	}
	d.SetTrash(ctx, "worker", files[1], 1, true)
	purged, err := d.PurgeFile(ctx, "worker", files[1])
	if err != nil || len(purged) != 1 || drive.RemoveBlobs(root, purged) != nil {
		t.Fatalf("purge %v %v", purged, err)
	}
	if err = drive.CommitUpgrade(ctx, d, root, blobs[1], tmp); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, blobs[1])); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("purged blob brought back")
	}
	if count(t, d, `SELECT COUNT(*) FROM drive_blob_kyb1 WHERE blob=?`, blobs[1]) != 0 {
		t.Fatal("purged blob still queued")
	}
	os.Remove(tmp)

	if tmp, err = drive.StageUpgrade(ctx, d, root, testKey, blobs[2]); err != nil {
		t.Fatal(err)
	}
	c, err := d.CopyFile(ctx, "worker", files[2], drive.Location{Workspace: w.ID, Name: "late copy.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if err = drive.CommitUpgrade(ctx, d, root, blobs[2], tmp); err != nil {
		t.Fatal(err)
	}
	readable(t, d, root, files[2], c.ID)
	// A purged blob that is still queued is dropped without staging anything.
	d.SetTrash(ctx, "worker", files[0], 2, true)
	if purged, err = d.PurgeFile(ctx, "worker", files[0]); err != nil || len(purged) != 2 {
		t.Fatalf("purge %v %v", purged, err)
	}
	drive.RemoveBlobs(root, purged)
	if n, err := d.UpgradeBlobs(ctx, root, testKey, 0); err != nil || n != 1 {
		t.Fatalf("upgraded %d: %v", n, err)
	}
	if count(t, d, `SELECT COUNT(*) FROM drive_blob_kyb1`) != 0 {
		t.Fatal("queue not drained")
	}
	readable(t, d, root, files[3], files[4], c.ID)
}

// A blob whose bytes do not decrypt stays queued and KYB1; the rest are upgraded.
func TestUpgradeBlobsKeepsUnreadableBlobQueued(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	_, blobs := kyb1Files(t, d, w, root)
	path := filepath.Join(root, blobs[3])
	raw, _ := os.ReadFile(path)
	raw[100] ^= 1
	os.WriteFile(path, raw, 0600)
	n, err := d.UpgradeBlobs(ctx, root, testKey, 0)
	if err != nil || n != len(blobs)-1 {
		t.Fatalf("upgraded %d: %v", n, err)
	}
	if count(t, d, `SELECT COUNT(*) FROM drive_blob_kyb1 WHERE blob=?`, blobs[3]) != 1 || magic(t, root, blobs[3]) != "KYB1" {
		t.Fatal("damaged blob dequeued or replaced")
	}
}

// The rate caps throughput: at 1 byte per second the run stops in its first pause when cancelled.
func TestUpgradeBlobsThrottles(t *testing.T) {
	d, w, root := managerFixture(t)
	_, blobs := kyb1Files(t, d, w, root)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	n, err := d.UpgradeBlobs(ctx, root, testKey, 1)
	if !errors.Is(err, context.DeadlineExceeded) || n != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("throttled run: %d %v after %v", n, err, time.Since(start))
	}
	if left := count(t, d, `SELECT COUNT(*) FROM drive_blob_kyb1`); left != len(blobs)-1 {
		t.Fatalf("%d queued after one blob", left)
	}
}

// The check before a rewrite replaces its blob decrypts the staged file under the blob's
// binding and compares the version digest.
func TestUpgradeChecksStagedCopy(t *testing.T) {
	root := t.TempDir()
	p := plaintext(chunk + 1)
	v, err := drive.WriteBlob(root, testKey, bytes.NewReader(p), int64(len(p)))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := drive.WriteBlob(root, testKey, bytes.NewReader(p), int64(len(p)))
	staged := filepath.Join(root, ".upload-staged")
	raw, _ := os.ReadFile(filepath.Join(root, v.Blob))
	os.WriteFile(staged, raw, 0600)
	if err = drive.CheckStaged(staged, testKey, v.Blob, v.Size, v.Digest); err != nil {
		t.Fatal(err)
	}
	if drive.CheckStaged(staged, testKey, other.Blob, v.Size, v.Digest) == nil {
		t.Fatal("copy bound to another blob accepted")
	}
	if drive.CheckStaged(staged, testKey, v.Blob, v.Size, strings.Repeat("0", 64)) == nil {
		t.Fatal("wrong digest accepted")
	}
	raw[len(raw)-1] ^= 1
	os.WriteFile(staged, raw, 0600)
	if drive.CheckStaged(staged, testKey, v.Blob, v.Size, v.Digest) == nil {
		t.Fatal("damaged copy accepted")
	}
}
