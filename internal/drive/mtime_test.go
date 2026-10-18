package drive_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func TestMtimeRoundTrip(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	v := blob(t, root, "dated")
	v.Mtime = "2020-01-02T04:04:05+01:00"
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "dated.txt"}, v, 0)
	if err != nil {
		t.Fatal(err)
	}
	const want = "2020-01-02T03:04:05Z"
	if f.Mtime != want {
		t.Fatalf("published mtime %q", f.Mtime)
	}
	if got, err := d.File(ctx, "worker", f.ID, 1); err != nil || got.Mtime != want {
		t.Fatalf("read mtime %q %v", got.Mtime, err)
	}
	if vs, err := d.Versions(ctx, "worker", f.ID); err != nil || vs[0].Mtime != want {
		t.Fatalf("version mtime %+v %v", vs, err)
	}
	// Without one, a new version reports its creation time.
	f, err = d.Publish(ctx, "worker", f, blob(t, root, "undated"), 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.File(ctx, "worker", f.ID, 1)
	if err != nil || got.Mtime != got.Modified || f.Mtime != f.Modified {
		t.Fatalf("fallback: read %q/%q, published %q/%q, %v", got.Mtime, got.Modified, f.Mtime, f.Modified, err)
	}
}

func TestMtimeRejectsOutOfRange(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	for _, m := range []string{"yesterday", "1969-12-31T23:59:59Z", time.Now().Add(25 * time.Hour).Format(time.RFC3339), "1700000000"} {
		v := blob(t, root, "x")
		v.Mtime = m
		if _, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "x.txt"}, v, 0); !errors.Is(err, drive.ErrInvalid) {
			t.Fatalf("%q: %v", m, err)
		}
		if _, err := d.StartUpload(ctx, "worker", drive.UploadSession{Workspace: w.ID, Name: "x.txt", Mtime: m}, maxUpload); !errors.Is(err, drive.ErrInvalid) {
			t.Fatalf("session %q: %v", m, err)
		}
	}
	v := blob(t, root, "x")
	v.Mtime = time.Now().Add(23 * time.Hour).Format(time.RFC3339) // a fast client clock
	if _, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "x.txt"}, v, 0); err != nil {
		t.Fatal(err)
	}
}

func TestUploadSessionKeepsMtime(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 4, Mtime: "2021-06-07T08:09:10Z"})
	if got, err := d.Upload(ctx, "worker", u.ID); err != nil || got.Mtime != "2021-06-07T08:09:10Z" {
		t.Fatalf("session %+v %v", got, err)
	}
	if _, err := appendChunk(d, staging, "worker", u.ID, 0, "ab"); err != nil {
		t.Fatal(err)
	}
	// An interrupted upload publishes nothing, so no mtime lands anywhere.
	if _, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("incomplete upload completed: %v", err)
	}
	if files, err := d.Files(ctx, "worker", w.ID, false); err != nil || len(files) != 0 {
		t.Fatalf("files %+v %v", files, err)
	}
	if _, err := appendChunk(d, staging, "worker", u.ID, 2, "cd"); err != nil {
		t.Fatal(err)
	}
	f, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID)
	if err != nil || f.Mtime != "2021-06-07T08:09:10Z" {
		t.Fatalf("published %+v %v", f, err)
	}
	if got, err := d.File(ctx, "worker", f.ID, 1); err != nil || got.Mtime != f.Mtime {
		t.Fatalf("read %+v %v", got, err)
	}
}
