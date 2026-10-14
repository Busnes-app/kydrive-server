package drive_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/google/uuid"
)

// legacy stores body as a plaintext blob, as releases before sealing did.
func legacy(t *testing.T, root, body string) drive.Version {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	v := drive.Version{Blob: uuid.NewString(), Digest: hex.EncodeToString(sum[:]), Size: int64(len(body))}
	os.MkdirAll(root, 0700)
	if err := os.WriteFile(filepath.Join(root, v.Blob), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return v
}

// readable fails unless every version of every live or trashed file opens to its digest.
func readable(t *testing.T, d *drive.Store, root string, ids ...string) {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		vs, err := d.Versions(ctx, "worker", id)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range vs {
			path, _ := drive.BlobPath(root, v.Blob)
			r, err := drive.OpenBlob(path, testKey, v.Sealed, v.Size)
			if err != nil {
				t.Fatalf("%s r%d: %v", id, v.Revision, err)
			}
			h := sha256.New()
			io.Copy(h, r)
			r.Close()
			if hex.EncodeToString(h.Sum(nil)) != v.Digest {
				t.Fatalf("%s r%d: digest differs", id, v.Revision)
			}
		}
	}
}

func count(t *testing.T, d *drive.Store, q string, args ...any) int {
	t.Helper()
	n, err := drive.QueryInt(context.Background(), d, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSealLegacyBlobsSwapsEveryReference(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	old := legacy(t, root, "legacy bytes")
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, old, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.CopyFile(ctx, "worker", f.ID, drive.Location{Workspace: w.ID, Name: "b.txt"})
	if err != nil || c.Sealed {
		t.Fatalf("copy %+v %v", c, err)
	}
	if _, err = d.Publish(ctx, "worker", f, blob(t, root, "new"), 1); err != nil {
		t.Fatal(err)
	}
	n, err := d.SealLegacyBlobs(ctx, root, testKey)
	if err != nil || n != 1 {
		t.Fatalf("sealed %d %v", n, err)
	}
	if count(t, d, `SELECT COUNT(*) FROM drive_versions WHERE sealed=0`) != 0 || count(t, d, `SELECT COUNT(DISTINCT blob) FROM drive_versions`) != 2 {
		t.Fatal("plaintext versions remain or copies split")
	}
	if _, err = os.Stat(filepath.Join(root, old.Blob)); !errors.Is(err, os.ErrNotExist) || count(t, d, `SELECT COUNT(*) FROM drive_blob_retire`) != 0 {
		t.Fatalf("old blob not retired: %v", err)
	}
	readable(t, d, root, f.ID, c.ID)
	if n, err = d.SealLegacyBlobs(ctx, root, testKey); err != nil || n != 0 {
		t.Fatalf("rerun sealed %d %v", n, err)
	}
	// Bytes that no longer match their metadata are left alone, not blessed.
	bad := legacy(t, root, "tampered")
	g, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "c.txt"}, bad, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, bad.Blob), []byte("tampereD"), 0600)
	if n, err = d.SealLegacyBlobs(ctx, root, testKey); err != nil || n != 0 {
		t.Fatalf("tampered sealed %d %v", n, err)
	}
	if v, _ := d.File(ctx, "worker", g.ID, 1); v.Sealed || v.Blob != bad.Blob {
		t.Fatal("tampered blob swapped")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 3 {
		t.Fatalf("stray blobs: %d", len(entries))
	}
}

// A crash after the swap commits leaves the old blob and its retire row; the rerun removes it.
func TestSealLegacyBlobsFinishesAfterCrash(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	old := legacy(t, root, "legacy bytes")
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, old, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = drive.SealUntilCommit(ctx, d, root, testKey, old.Blob); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, old.Blob)); err != nil || count(t, d, `SELECT COUNT(*) FROM drive_blob_retire WHERE blob=?`, old.Blob) != 1 {
		t.Fatalf("crash state: %v", err)
	}
	readable(t, d, root, f.ID)
	n, err := d.SealLegacyBlobs(ctx, root, testKey)
	if err != nil || n != 0 {
		t.Fatalf("rerun %d %v", n, err)
	}
	if _, err = os.Stat(filepath.Join(root, old.Blob)); !errors.Is(err, os.ErrNotExist) || count(t, d, `SELECT COUNT(*) FROM drive_blob_retire`) != 0 {
		t.Fatalf("retire not finished: %v", err)
	}
	readable(t, d, root, f.ID)
}

// A purge or copy landing between the sealed copy and the swap never leaves a version
// without bytes, and the purge never releases a blob the swap still needs.
func TestSealLegacyBlobsRacesPurgeAndCopy(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	old := legacy(t, root, "purged")
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, old, 0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := drive.SealCopy(ctx, d, root, testKey, old.Blob)
	if err != nil {
		t.Fatal(err)
	}
	d.SetTrash(ctx, "worker", f.ID, 1, true)
	blobs, err := d.PurgeFile(ctx, "worker", f.ID)
	if err != nil || len(blobs) != 1 || drive.RemoveBlobs(root, blobs) != nil {
		t.Fatalf("purge %v %v", blobs, err)
	}
	if err = drive.SwapBlob(ctx, d, root, old.Blob, v); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, v.Blob)); !errors.Is(err, os.ErrNotExist) || count(t, d, `SELECT COUNT(*) FROM drive_blob_retire`) != 0 {
		t.Fatal("orphaned sealed copy or retire row")
	}

	shared := legacy(t, root, "shared")
	f, err = d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "b.txt"}, shared, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v, err = drive.SealCopy(ctx, d, root, testKey, shared.Blob); err != nil {
		t.Fatal(err)
	}
	c, err := d.CopyFile(ctx, "worker", f.ID, drive.Location{Workspace: w.ID, Name: "c.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err = drive.SwapBlob(ctx, d, root, shared.Blob, v); err != nil {
		t.Fatal(err)
	}
	d.SetTrash(ctx, "worker", f.ID, 1, true)
	if blobs, err = d.PurgeFile(ctx, "worker", f.ID); err != nil || len(blobs) != 0 {
		t.Fatalf("shared sealed blob released: %v %v", blobs, err)
	}
	if _, err = d.SealLegacyBlobs(ctx, root, testKey); err != nil {
		t.Fatal(err)
	}
	readable(t, d, root, c.ID)
}

// Run sealing alongside copies, purges and retention; afterwards every version has its bytes.
func TestSealLegacyBlobsConcurrentWithPurge(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	if err := d.Quota(ctx, "admin", w.ID, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 0, 1); err != nil {
		t.Fatal(err)
	}
	var files []string
	for i := range 20 {
		f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: uuid.NewString()}, legacy(t, root, uuid.NewString()), 0)
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if _, err = d.Publish(ctx, "worker", f, legacy(t, root, uuid.NewString()), 1); err != nil {
				t.Fatal(err)
			}
		}
		files = append(files, f.ID)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := d.SealLegacyBlobs(ctx, root, testKey); err != nil {
			t.Error(err)
		}
	})
	var kept []string
	wg.Go(func() {
		for i, id := range files {
			if i%3 == 0 {
				f, _ := d.File(ctx, "worker", id, 1)
				d.SetTrash(ctx, "worker", id, f.Revision, true)
				blobs, err := d.PurgeFile(ctx, "worker", id)
				if err == nil {
					drive.RemoveBlobs(root, blobs)
				}
				continue
			}
			c, err := d.CopyFile(ctx, "worker", id, drive.Location{Workspace: w.ID, Name: uuid.NewString()})
			if err != nil {
				t.Error(err)
			}
			kept = append(kept, id, c.ID)
		}
		blobs, err := d.ApplyRetention(ctx, time.Now(), 0)
		if err != nil {
			t.Error(err)
		}
		drive.RemoveBlobs(root, blobs)
	})
	wg.Wait()
	if _, err := d.SealLegacyBlobs(ctx, root, testKey); err != nil {
		t.Fatal(err)
	}
	readable(t, d, root, kept...)
	if count(t, d, `SELECT COUNT(*) FROM drive_versions WHERE sealed=0`) != 0 {
		t.Fatal("plaintext versions remain")
	}
	// Disk holds exactly the referenced blobs.
	entries, _ := os.ReadDir(root)
	if refs := count(t, d, `SELECT COUNT(DISTINCT blob) FROM drive_versions`); len(entries) != refs {
		t.Fatalf("%d blobs on disk, %d referenced", len(entries), refs)
	}
}
