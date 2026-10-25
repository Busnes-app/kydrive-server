package drive_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

const maxUpload = 1 << 20

func startUpload(t *testing.T, d *drive.Store, user string, u drive.UploadSession) drive.UploadSession {
	t.Helper()
	u, err := d.StartUpload(context.Background(), user, u, maxUpload)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func appendChunk(d *drive.Store, staging, user, id string, start int64, body string) (drive.UploadSession, error) {
	return d.AppendUpload(context.Background(), staging, testKey, user, id, start, int64(len(body)), strings.NewReader(body))
}

func readFile(t *testing.T, blobs string, f drive.File) string {
	t.Helper()
	path, err := drive.BlobPath(blobs, f.Blob)
	if err != nil {
		t.Fatal(err)
	}
	r, err := drive.OpenBlob(path, testKey, f.Sealed, f.Size)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUploadSessionRoundTrip(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 30})
	for i, part := range []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc"} {
		got, err := appendChunk(d, staging, "worker", u.ID, int64(i*10), part)
		if err != nil || got.Received != int64(i*10+10) {
			t.Fatalf("chunk %d: %+v %v", i, got, err)
		}
	}
	f, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("a", 10) + strings.Repeat("b", 10) + strings.Repeat("c", 10)
	sum := sha256.Sum256([]byte(want))
	if f.Name != "big.bin" || f.Revision != 1 || f.Size != 30 || f.Digest != hex.EncodeToString(sum[:]) || readFile(t, blobs, f) != want {
		t.Fatalf("published %+v", f)
	}
	if _, err = os.Stat(filepath.Join(staging, u.ID)); !os.IsNotExist(err) {
		t.Fatalf("staging left behind: %v", err)
	}
	if _, err = d.Upload(ctx, "worker", u.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("completed session still open: %v", err)
	}
}

func TestEmptyUpload(t *testing.T) {
	st, w, blobs := fixture(t)
	d, staging := st.Drive(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "empty.txt"})
	if f, err := d.CompleteUpload(context.Background(), staging, blobs, testKey, "worker", u.ID); err != nil || f.Size != 0 {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestUploadResumeAfterDroppedChunk(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 10})
	dropped := io.MultiReader(strings.NewReader("aaaaa"), iotest.ErrReader(io.ErrUnexpectedEOF))
	if _, err := d.AppendUpload(ctx, staging, testKey, "worker", u.ID, 0, 10, dropped); err == nil {
		t.Fatal("dropped chunk accepted")
	}
	if got, err := d.Upload(ctx, "worker", u.ID); err != nil || got.Received != 0 {
		t.Fatalf("after drop: %+v %v", got, err)
	}
	// A body shorter than its declared length is a dropped chunk too.
	if _, err := d.AppendUpload(ctx, staging, testKey, "worker", u.ID, 0, 10, strings.NewReader("short")); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("short body: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(staging, u.ID))
	if len(entries) != 0 {
		t.Fatalf("partial segments left: %v", entries)
	}
	if _, err := appendChunk(d, staging, "worker", u.ID, 0, "0123456789"); err != nil {
		t.Fatal(err)
	}
	f, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID)
	if err != nil || readFile(t, blobs, f) != "0123456789" {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestUploadRejectsBadRanges(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 10})
	if _, err := appendChunk(d, staging, "worker", u.ID, 5, "abcde"); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("gap accepted: %v", err)
	}
	if _, err := appendChunk(d, staging, "worker", u.ID, 0, "01234567890"); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("overrun accepted: %v", err)
	}
	if _, err := d.AppendUpload(ctx, staging, testKey, "worker", u.ID, 0, 3, strings.NewReader("abcdef")); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("long body accepted: %v", err)
	}
	if _, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("incomplete upload published: %v", err)
	}
}

func TestUploadSegmentRace(t *testing.T) {
	st, w, _ := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 10})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, body := range []string{"aaaaa", "bbbbb"} {
		wg.Go(func() { _, err := appendChunk(d, staging, "worker", u.ID, 0, body); errs <- err })
	}
	wg.Wait()
	close(errs)
	ok, conflict := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, drive.ErrConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	segments, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_upload_segments WHERE session=?`, u.ID)
	if err != nil || ok != 1 || conflict != 1 || segments != 1 {
		t.Fatalf("ok=%d conflict=%d segments=%d err=%v", ok, conflict, segments, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(staging, u.ID)); len(entries) != 1 {
		t.Fatalf("loser's segment kept: %v", entries)
	}
}

func TestUploadSessionLimits(t *testing.T) {
	st, w, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	if _, err := d.StartUpload(ctx, "worker", drive.UploadSession{Workspace: w.ID, Name: "a", Size: 50}, 40); !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("over max: %v", err)
	}
	if _, err := d.StartUpload(ctx, "worker", drive.UploadSession{Workspace: w.ID, Name: "a", Size: 101}, maxUpload); !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("over quota: %v", err)
	}
	if _, err := d.StartUpload(ctx, "worker", drive.UploadSession{Workspace: w.ID, Name: "a/b", Size: 1}, maxUpload); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := d.StartUpload(ctx, "stranger", drive.UploadSession{Workspace: w.ID, Name: "a", Size: 1}, maxUpload); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("no access: %v", err)
	}
	for range 8 {
		startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "a", Size: 1})
	}
	if _, err := d.StartUpload(ctx, "worker", drive.UploadSession{Workspace: w.ID, Name: "a", Size: 1}, maxUpload); !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("ninth session: %v", err)
	}
}

func TestUploadCompleteRechecksAccess(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 5})
	if _, err := appendChunk(d, staging, "worker", u.ID, 0, "abcde"); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", ExternalID: "external-team"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("lost access still published: %v", err)
	}
	files, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_files`)
	entries, _ := os.ReadDir(blobs)
	if err != nil || files != 0 || len(entries) != 0 {
		t.Fatalf("files=%d blobs=%v err=%v", files, entries, err)
	}
}

func TestUploadNewVersion(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "notes.txt"}, blob(t, blobs, "v1"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.StartUpload(ctx, "worker", drive.UploadSession{Workspace: w.ID, File: f.ID, Expected: 2, Size: 2}, maxUpload); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	if _, err = d.StartUpload(ctx, "worker", drive.UploadSession{Workspace: w.ID, Name: "notes.txt", Size: 2}, maxUpload); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("taken name: %v", err)
	}
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, File: f.ID, Expected: 1, Size: 2})
	if u.Name != "notes.txt" {
		t.Fatalf("new version renamed the file: %+v", u)
	}
	if _, err = appendChunk(d, staging, "worker", u.ID, 0, "v2"); err != nil {
		t.Fatal(err)
	}
	got, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID)
	if err != nil || got.ID != f.ID || got.Revision != 2 || readFile(t, blobs, got) != "v2" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestUploadOwnedByCreator(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 5})
	if _, err := d.Upload(ctx, "admin", u.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("read: %v", err)
	}
	if _, err := appendChunk(d, staging, "admin", u.ID, 0, "abcde"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("append: %v", err)
	}
	if _, err := d.CompleteUpload(ctx, staging, blobs, testKey, "admin", u.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("complete: %v", err)
	}
	if err := d.CancelUpload(ctx, staging, "admin", u.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := d.Upload(ctx, "worker", "../../etc"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("path id: %v", err)
	}
	if err := d.CancelUpload(ctx, staging, "worker", u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Upload(ctx, "worker", u.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("cancelled session still open: %v", err)
	}
}

func TestExpiredUploadSessionsRemoved(t *testing.T) {
	st, w, _ := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	old := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "old.bin", Size: 10})
	live := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "live.bin", Size: 10})
	for _, id := range []string{old.ID, live.ID} {
		if _, err := appendChunk(d, staging, "worker", id, 0, "abcde"); err != nil {
			t.Fatal(err)
		}
	}
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_upload_sessions SET expires=0 WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	// A crash can leave a directory with no session; it goes once it is an hour old.
	stray, fresh := filepath.Join(staging, "stray"), filepath.Join(staging, "fresh")
	for _, dir := range []string{stray, fresh} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	hourAgo := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stray, hourAgo, hourAgo); err != nil {
		t.Fatal(err)
	}
	if n, err := d.ExpireUploads(ctx, staging); err != nil || n != 1 {
		t.Fatalf("expired %d: %v", n, err)
	}
	for dir, want := range map[string]bool{old.ID: false, "stray": false, live.ID: true, "fresh": true} {
		if _, err := os.Stat(filepath.Join(staging, dir)); (err == nil) != want {
			t.Errorf("%s present=%v", dir, err == nil)
		}
	}
	if _, err := d.Upload(ctx, "worker", old.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("expired session open: %v", err)
	}
}

func TestStagingIsSealed(t *testing.T) {
	st, w, _ := fixture(t)
	d, staging := st.Drive(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "secret.txt", Size: 22})
	if _, err := appendChunk(d, staging, "worker", u.ID, 0, "plaintext-marker-12345"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(staging, u.ID))
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	b, err := os.ReadFile(filepath.Join(staging, u.ID, entries[0].Name()))
	if err != nil || !bytes.HasPrefix(b, []byte("KYB2")) || bytes.Contains(b, []byte("plaintext-marker")) {
		t.Fatalf("segment not sealed: %q %v", b, err)
	}
	names, err := drive.RawNames(context.Background(), d, "drive_upload_sessions")
	if err != nil || len(names) != 1 || strings.Contains(names[0], "secret") {
		t.Fatalf("session name stored in clear: %v %v", names, err)
	}
}

// A completion re-seals the whole file, so only one may run per session: a second one must be
// refused before it writes anything, or concurrent completions multiply disk use past the limits.
func TestUploadCompletionIsClaimed(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 5})
	if _, err := appendChunk(d, staging, "worker", u.ID, 0, "abcde"); err != nil {
		t.Fatal(err)
	}
	// Another request holds the claim: this one is refused and writes no blob.
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_upload_sessions SET completing=? WHERE id=?`, time.Now().Unix(), u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("second completion: %v", err)
	}
	if entries, _ := os.ReadDir(blobs); len(entries) != 0 {
		t.Fatalf("refused completion wrote %d blobs", len(entries))
	}
	// A claim left by a crashed request expires.
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_upload_sessions SET completing=? WHERE id=?`, time.Now().Add(-time.Hour).Unix(), u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID); err != nil {
		t.Fatalf("stale claim blocked completion: %v", err)
	}
}

func TestConcurrentCompletionsPublishOnce(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 5})
	if _, err := appendChunk(d, staging, "worker", u.ID, 0, "abcde"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { <-start; _, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID); errs <- err })
	}
	close(start)
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, drive.ErrConflict), errors.Is(err, drive.ErrDenied): // claimed, or already done
		default:
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(blobs)
	if ok != 1 || len(entries) != 1 {
		t.Fatalf("successes=%d blobs=%d", ok, len(entries))
	}
}

// A failed completion releases its claim so the owner can retry.
func TestFailedCompletionReleasesClaim(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx, staging := st.Drive(), context.Background(), t.TempDir()
	u := startUpload(t, d, "worker", drive.UploadSession{Workspace: w.ID, Name: "big.bin", Size: 5})
	if _, err := appendChunk(d, staging, "worker", u.ID, 0, "abcde"); err != nil {
		t.Fatal(err)
	}
	// Fill the quota so Publish fails after the re-seal.
	if _, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "filler"}, blob(t, blobs, strings.Repeat("x", 96)), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CompleteUpload(ctx, staging, blobs, testKey, "worker", u.ID); !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("over quota: %v", err)
	}
	if n, err := drive.QueryInt(ctx, d, `SELECT completing FROM drive_upload_sessions WHERE id=?`, u.ID); err != nil || n != 0 {
		t.Fatalf("claim kept after failure: %d %v", n, err)
	}
}
