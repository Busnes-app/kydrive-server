package drive_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/google/uuid"
)

const chunk = 64 << 10

func plaintext(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/chunk)
	}
	return b
}

// sealBytes stores p as a sealed (KYB2) blob and returns its path.
func sealBytes(t *testing.T, p []byte) (string, drive.Version) {
	t.Helper()
	root := t.TempDir()
	v, err := drive.WriteBlob(root, testKey, bytes.NewReader(p), int64(len(p))+1)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, v.Blob), v
}

// sealV1Bytes stores p as a KYB1 blob, as releases before KYB2 did.
func sealV1Bytes(t *testing.T, p []byte) (string, drive.Version) {
	t.Helper()
	root := t.TempDir()
	v, err := drive.WriteBlobV1(root, testKey, bytes.NewReader(p))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, v.Blob), v
}

var formats = map[string]func(*testing.T, []byte) (string, drive.Version){"KYB2": sealBytes, "KYB1": sealV1Bytes}

// net/http reports a body cut short of its Content-Length as io.ErrUnexpectedEOF. That is a
// failed upload, not the end of the data, at every chunk boundary.
func TestWriteBlobRefusesTruncatedBody(t *testing.T) {
	for _, n := range []int{0, 5, chunk - 1, chunk, chunk + 5} {
		r := io.MultiReader(bytes.NewReader(plaintext(n)), iotest.ErrReader(io.ErrUnexpectedEOF))
		root := t.TempDir()
		if _, err := drive.WriteBlob(root, testKey, r, 1<<20); err == nil {
			t.Fatalf("%d bytes then a cut connection: blob published", n)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Fatalf("%d bytes: left %d files", n, len(entries))
		}
	}
}

func TestSealedBlobRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, chunk - 1, chunk, chunk + 1, 2 * chunk, 2*chunk + 1, 3 * chunk, 1 << 20, 16<<20 + 7} {
		p := plaintext(n)
		path, v := sealBytes(t, p)
		sum := sha256.Sum256(p)
		if !v.Sealed || v.Size != int64(n) || v.Digest != hex.EncodeToString(sum[:]) || filepath.Base(path) != v.Blob {
			t.Fatalf("%d: version %+v", n, v)
		}
		raw, err := os.ReadFile(path)
		if err != nil || int64(len(raw)) != drive.SealedSize(int64(n)) || string(raw[:4]) != "KYB2" {
			t.Fatalf("%d: on-disk %d bytes, want %d: %v", n, len(raw), drive.SealedSize(int64(n)), err)
		}
		if !drive.StoredSizeOK(true, int64(n), int64(len(raw))) {
			t.Fatalf("%d: StoredSizeOK refuses KYB2", n)
		}
		if n >= 32 && bytes.Contains(raw, p[:32]) {
			t.Fatalf("%d: plaintext on disk", n)
		}
		r, err := drive.OpenBlob(path, testKey, true, int64(n))
		if err != nil {
			t.Fatal(n, err)
		}
		got, err := io.ReadAll(r)
		r.Close()
		if err != nil || !bytes.Equal(got, p) {
			t.Fatalf("%d: round trip %d bytes %v", n, len(got), err)
		}
	}
	// Two seals of the same bytes share nothing: each blob has its own file key and salt.
	a, _ := sealBytes(t, []byte("same"))
	b, _ := sealBytes(t, []byte("same"))
	x, _ := os.ReadFile(a)
	y, _ := os.ReadFile(b)
	hdr := int(drive.SealedSize(4)) - 4 - 16
	if bytes.Equal(x[:hdr], y[:hdr]) || bytes.Equal(x[hdr:], y[hdr:]) {
		t.Fatal("header or ciphertext reused")
	}
}

// A KYB1 blob written by the last release that wrote KYB1 still reads, under any name.
func TestKYB1FixtureStillReads(t *testing.T) {
	raw, err := os.ReadFile("testdata/kyb1-65541.blob")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), uuid.NewString())
	os.WriteFile(path, raw, 0600)
	if !drive.StoredSizeOK(true, chunk+5, int64(len(raw))) || drive.SealedSize(chunk+5) == int64(len(raw)) {
		t.Fatal("KYB1 size not recognised as KYB1")
	}
	r, err := drive.OpenBlob(path, testKey, true, chunk+5)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, plaintext(chunk+5)) {
		t.Fatalf("KYB1 fixture: %d bytes %v", len(got), err)
	}
}

func TestSealedBlobSeeksEveryOffset(t *testing.T) {
	for name, write := range formats {
		n := 2*chunk + 5
		p := plaintext(n)
		path, _ := write(t, p)
		r, err := drive.OpenBlob(path, testKey, true, int64(n))
		if err != nil {
			t.Fatal(name, err)
		}
		if end, err := r.Seek(0, io.SeekEnd); err != nil || end != int64(n) {
			t.Fatalf("%s: size %d %v", name, end, err)
		}
		buf := make([]byte, 3)
		for off := 0; off <= n; off++ {
			if _, err = r.Seek(int64(off), io.SeekStart); err != nil {
				t.Fatal(name, err)
			}
			k, err := io.ReadFull(r, buf)
			want := p[off:min(off+3, n)]
			if !bytes.Equal(buf[:k], want) || (k < 3 && err == nil) {
				t.Fatalf("%s offset %d: %x want %x (%v)", name, off, buf[:k], want, err)
			}
		}
		// Ranges that span chunk boundaries, read after seeking backwards.
		for _, rg := range [][2]int{{chunk - 10, chunk + 10}, {2*chunk - 1, n}, {0, n}, {5, 2*chunk + 1}} {
			if _, err = r.Seek(int64(rg[0]), io.SeekStart); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, rg[1]-rg[0])
			if _, err = io.ReadFull(r, got); err != nil || !bytes.Equal(got, p[rg[0]:rg[1]]) {
				t.Fatalf("%s range %v: %v", name, rg, err)
			}
		}
		if _, err = r.Seek(-1, io.SeekStart); err == nil {
			t.Fatalf("%s: negative seek accepted", name)
		}
		r.Close()
	}
}

// openRaw reads raw stored under name; it fails the test if unauthenticated bytes come back.
func openRaw(t *testing.T, name string, raw, key, p []byte, size int) error {
	t.Helper()
	bad := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(bad, raw, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := drive.OpenBlob(bad, key, true, int64(size))
	if err != nil {
		return err
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil && len(got) > 0 && !bytes.Equal(got, p[:len(got)]) {
		t.Fatal("unauthenticated bytes returned")
	}
	return err
}

type tamper struct {
	raw  []byte
	key  []byte
	size int
}

func TestSealedBlobRejectsTampering(t *testing.T) {
	p := plaintext(3 * chunk)
	wrongKey := bytes.Repeat([]byte{8}, 32)
	for name, write := range formats {
		path, _ := write(t, p)
		id := filepath.Base(path)
		good, _ := os.ReadFile(path)
		hdr := len(good) - len(p) - 3*16
		ct := chunk + 16
		at := func(i int) int { return hdr + i*ct }
		flip := func(i int) []byte {
			b := bytes.Clone(good)
			b[i] ^= 1
			return b
		}
		swapped := bytes.Clone(good)
		copy(swapped[at(0):at(1)], good[at(1):at(2)])
		copy(swapped[at(1):at(2)], good[at(0):at(1)])
		// One full chunk sealed as final, followed by an appended chunk.
		single, _ := write(t, p[:chunk])
		one, _ := os.ReadFile(single)
		extended := append(bytes.Clone(one), good[at(1):at(2)]...)
		cases := map[string]tamper{
			"magic":               {flip(0), testKey, len(p)},
			"header":              {flip(10), testKey, len(p)},
			"header end":          {flip(hdr - 1), testKey, len(p)},
			"first chunk":         {flip(at(0) + 5), testKey, len(p)},
			"middle tag":          {flip(at(2) - 1), testKey, len(p)},
			"last chunk":          {flip(len(good) - 1), testKey, len(p)},
			"final chunk dropped": {good[:at(2)], testKey, 2 * chunk},
			"truncated":           {good[:len(good)-1], testKey, len(p)},
			"swapped chunks":      {swapped, testKey, len(p)},
			"wrong key":           {good, wrongKey, len(p)},
			"wrong size":          {good, testKey, len(p) - 1},
		}
		for c, tc := range cases {
			if err := openRaw(t, id, tc.raw, tc.key, p, tc.size); err == nil {
				t.Errorf("%s: %s accepted", name, c)
			}
		}
		// Under the single-chunk blob's own name, so only the final flag can refuse it.
		if err := openRaw(t, filepath.Base(single), extended, testKey, p, 2*chunk); err == nil {
			t.Errorf("%s: extended after final accepted", name)
		}
		if err := openRaw(t, id, good, testKey, p, len(p)); err != nil {
			t.Fatal(name, err)
		}
		// The empty blob is still one authenticated chunk.
		empty, _ := write(t, nil)
		if _, err := drive.OpenBlob(empty, wrongKey, true, 0); err == nil {
			t.Fatalf("%s: empty blob opened under the wrong key", name)
		}
		// A sealed blob is never served as plaintext.
		if _, err := drive.OpenBlob(path, testKey, false, int64(len(p))); err == nil {
			t.Fatalf("%s: sealed blob opened as plaintext", name)
		}
	}
}

// A KYB2 blob is bound to its blob ID: moved or copied over another blob's name, it does not
// open, even when both hold the same number of bytes. Cache entries never open as blobs.
func TestSealedBlobBoundToID(t *testing.T) {
	root := t.TempDir()
	a, err := drive.WriteBlob(root, testKey, bytes.NewReader(plaintext(chunk+9)), chunk+9)
	if err != nil {
		t.Fatal(err)
	}
	b, err := drive.WriteBlob(root, testKey, bytes.NewReader(bytes.Repeat([]byte("b"), chunk+9)), chunk+9)
	if err != nil {
		t.Fatal(err)
	}
	rawA, _ := os.ReadFile(filepath.Join(root, a.Blob))
	if err = openRaw(t, b.Blob, rawA, testKey, nil, chunk+9); err == nil {
		t.Fatal("blob A opened under blob B's ID")
	}
	if err = openRaw(t, a.Blob, rawA, testKey, nil, chunk+9); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	if err = drive.WriteThumb(cache, testKey, a.Blob, plaintext(chunk+9)); err != nil {
		t.Fatal(err)
	}
	thumb, _ := os.ReadFile(filepath.Join(cache, a.Blob))
	if err = openRaw(t, a.Blob, thumb, testKey, nil, chunk+9); err == nil {
		t.Fatal("cache entry opened as its blob")
	}
	os.WriteFile(filepath.Join(cache, b.Blob), rawA, 0600)
	if _, err = drive.ReadThumb(cache, testKey, b.Blob); err == nil {
		t.Fatal("blob opened as a cache entry")
	}
	if got, err := drive.ReadThumb(cache, testKey, a.Blob); err != nil || !bytes.Equal(got, plaintext(chunk+9)) {
		t.Fatal("cache entry round trip", err)
	}
}

// http.ServeContent, which serves downloads, answers every Range from either format.
func TestSealedBlobServesRanges(t *testing.T) {
	n := 3*chunk + 100
	p := plaintext(n)
	var ranges [][2]int
	for _, b := range []int{0, chunk, 2 * chunk, 3 * chunk} {
		for _, start := range []int{b - 1, b, b + 1} {
			for _, end := range []int{start, start + 1, start + chunk, n - 1} {
				if start >= 0 && end >= start && end < n {
					ranges = append(ranges, [2]int{start, end})
				}
			}
		}
	}
	for name, write := range formats {
		path, _ := write(t, p)
		get := func(rng string) *httptest.ResponseRecorder {
			r, err := drive.OpenBlob(path, testKey, true, int64(n))
			if err != nil {
				t.Fatal(name, err)
			}
			defer r.Close()
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Range", rng)
			rec := httptest.NewRecorder()
			http.ServeContent(rec, req, "", time.Time{}, r)
			return rec
		}
		for _, rg := range ranges {
			out := get(fmt.Sprintf("bytes=%d-%d", rg[0], rg[1]))
			if out.Code != 206 || !bytes.Equal(out.Body.Bytes(), p[rg[0]:rg[1]+1]) {
				t.Fatalf("%s range %v: %d, %d bytes", name, rg, out.Code, out.Body.Len())
			}
		}
		if out := get("bytes=-10"); out.Code != 206 || !bytes.Equal(out.Body.Bytes(), p[n-10:]) {
			t.Fatalf("%s suffix range %d", name, out.Code)
		}
		if out := get(fmt.Sprintf("bytes=%d-", n)); out.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("%s unsatisfiable range %d", name, out.Code)
		}
	}
}
