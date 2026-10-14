package drive_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

const chunk = 64 << 10

func plaintext(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/chunk)
	}
	return b
}

// sealBytes stores p as a sealed blob and returns its path.
func sealBytes(t *testing.T, p []byte) (string, drive.Version) {
	t.Helper()
	root := t.TempDir()
	v, err := drive.WriteBlob(root, testKey, bytes.NewReader(p), int64(len(p))+1)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, v.Blob), v
}

func TestSealedBlobRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, chunk - 1, chunk, chunk + 1, 3 * chunk, 1 << 20} {
		p := plaintext(n)
		path, v := sealBytes(t, p)
		sum := sha256.Sum256(p)
		if !v.Sealed || v.Size != int64(n) || v.Digest != hex.EncodeToString(sum[:]) {
			t.Fatalf("%d: version %+v", n, v)
		}
		raw, err := os.ReadFile(path)
		if err != nil || int64(len(raw)) != drive.SealedSize(int64(n)) || string(raw[:4]) != "KYB1" {
			t.Fatalf("%d: on-disk %d bytes, want %d: %v", n, len(raw), drive.SealedSize(int64(n)), err)
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
	// Two seals of the same bytes share nothing: each blob has its own salt.
	a, _ := sealBytes(t, []byte("same"))
	b, _ := sealBytes(t, []byte("same"))
	x, _ := os.ReadFile(a)
	y, _ := os.ReadFile(b)
	if bytes.Equal(x[4:36], y[4:36]) || bytes.Equal(x[36:], y[36:]) {
		t.Fatal("salt or ciphertext reused")
	}
}

func TestSealedBlobSeeksEveryOffset(t *testing.T) {
	n := 2*chunk + 5
	p := plaintext(n)
	path, _ := sealBytes(t, p)
	r, err := drive.OpenBlob(path, testKey, true, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if end, err := r.Seek(0, io.SeekEnd); err != nil || end != int64(n) {
		t.Fatalf("size %d %v", end, err)
	}
	buf := make([]byte, 3)
	for off := 0; off <= n; off++ {
		if _, err = r.Seek(int64(off), io.SeekStart); err != nil {
			t.Fatal(err)
		}
		k, err := io.ReadFull(r, buf)
		want := p[off:min(off+3, n)]
		if !bytes.Equal(buf[:k], want) || (k < 3 && err == nil) {
			t.Fatalf("offset %d: %x want %x (%v)", off, buf[:k], want, err)
		}
	}
	if _, err = r.Seek(-1, io.SeekStart); err == nil {
		t.Fatal("negative seek accepted")
	}
}

func TestSealedBlobRejectsTampering(t *testing.T) {
	p := plaintext(3 * chunk)
	path, _ := sealBytes(t, p)
	good, _ := os.ReadFile(path)
	ct := chunk + 16
	at := func(i int) int { return 36 + i*ct }
	open := func(raw []byte, key []byte, size int) error {
		t.Helper()
		bad := filepath.Join(t.TempDir(), "blob")
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
	flip := func(i int) []byte {
		b := bytes.Clone(good)
		b[i] ^= 1
		return b
	}
	swapped := bytes.Clone(good)
	copy(swapped[at(0):at(1)], good[at(1):at(2)])
	copy(swapped[at(1):at(2)], good[at(0):at(1)])
	// One full chunk sealed as final, followed by an appended chunk.
	single, _ := sealBytes(t, p[:chunk])
	one, _ := os.ReadFile(single)
	extended := append(bytes.Clone(one), good[at(1):at(2)]...)
	cases := map[string]struct {
		raw  []byte
		key  []byte
		size int
	}{
		"magic":                {flip(0), testKey, len(p)},
		"salt":                 {flip(10), testKey, len(p)},
		"first chunk":          {flip(at(0) + 5), testKey, len(p)},
		"middle tag":           {flip(at(2) - 1), testKey, len(p)},
		"last chunk":           {flip(len(good) - 1), testKey, len(p)},
		"final chunk dropped":  {good[:at(2)], testKey, 2 * chunk},
		"truncated":            {good[:len(good)-1], testKey, len(p)},
		"extended after final": {extended, testKey, 2 * chunk},
		"swapped chunks":       {swapped, testKey, len(p)},
		"wrong key":            {good, bytes.Repeat([]byte{8}, 32), len(p)},
		"wrong size":           {good, testKey, len(p) - 1},
	}
	for name, c := range cases {
		if err := open(c.raw, c.key, c.size); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := open(good, testKey, len(p)); err != nil {
		t.Fatal(err)
	}
	// The empty blob is still one authenticated chunk.
	empty, _ := sealBytes(t, nil)
	if _, err := drive.OpenBlob(empty, bytes.Repeat([]byte{8}, 32), true, 0); err == nil {
		t.Fatal("empty blob opened under the wrong key")
	}
	// A sealed blob is never served as plaintext.
	if _, err := drive.OpenBlob(path, testKey, false, int64(len(p))); err == nil {
		t.Fatal("sealed blob opened as plaintext")
	}
}
