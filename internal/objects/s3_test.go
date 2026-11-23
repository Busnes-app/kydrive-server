package objects_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/objects"
)

// s3Config is the test bucket from KY_TEST_S3_* (CI starts a local S3-compatible server), or a
// skip.
func s3Config(t *testing.T) objects.S3Config {
	t.Helper()
	endpoint := os.Getenv("KY_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("needs KY_TEST_S3_ENDPOINT, KY_TEST_S3_BUCKET, KY_TEST_S3_ACCESS_KEY and KY_TEST_S3_SECRET_KEY")
	}
	cfg := objects.S3Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: os.Getenv("KY_TEST_S3_BUCKET"),
		AccessKey: os.Getenv("KY_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("KY_TEST_S3_SECRET_KEY"), PathStyle: true,
	}
	b, err := objects.NewBucket(cfg, t.TempDir())
	if err == nil {
		err = objects.CreateBucketForTest(context.Background(), b)
	}
	if err != nil {
		t.Fatal("test bucket:", err)
	}
	return cfg
}

// prefix is a key prefix no other test run uses.
func prefix(t *testing.T) string {
	b := make([]byte, 8)
	rand.Read(b)
	return "test-" + hex.EncodeToString(b) + "/"
}

func TestS3(t *testing.T) {
	b, err := objects.NewBucket(s3Config(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := prefix(t)
	contract(t, b.Store(p+"store/"))
	treeContract(t, b.Tree(p+"tree/"))
}

// An object larger than one read-ahead window reads back whole, and from any offset.
func TestS3ReadsAcrossWindows(t *testing.T) {
	b, err := objects.NewBucket(s3Config(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, ctx := b.Store(prefix(t)), context.Background()
	body := make([]byte, 9<<20+123)
	rand.Read(body)
	w, err := st.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(body)
	if err := w.Publish("big"); err != nil {
		t.Fatal(err)
	}
	o, err := st.Open(ctx, "big")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if o.Size() != int64(len(body)) {
		t.Fatalf("size %d", o.Size())
	}
	got, err := io.ReadAll(io.NewSectionReader(o, 0, o.Size()))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("whole read differs (%v)", err)
	}
	for _, off := range []int64{0, 1, 4<<20 - 1, 4 << 20, 8<<20 + 5, int64(len(body)) - 10} {
		p := make([]byte, 64<<10)
		n, err := o.ReadAt(p, off)
		want := body[off:min(off+int64(len(p)), int64(len(body)))]
		if !bytes.Equal(p[:n], want) || (n < len(p) && !errors.Is(err, io.EOF)) || (n == len(p) && err != nil) {
			t.Fatalf("ReadAt(%d): %d bytes, %v", off, n, err)
		}
	}
	st.Remove(ctx, "big")
}

// A request the service refuses names the operation and key, never the secret.
func TestS3ErrorsNeverCarryTheSecret(t *testing.T) {
	cfg := s3Config(t)
	cfg.SecretKey = "never-in-an-error-" + strings.Repeat("x", 24)
	b, err := objects.NewBucket(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, ctx := b.Store(prefix(t)), context.Background()
	var errs []error
	_, err = st.Open(ctx, "anything")
	errs = append(errs, err)
	w, _ := st.Create(ctx)
	io.WriteString(w, "x")
	errs = append(errs, w.Publish("anything"), st.Remove(ctx, "anything"), st.List(ctx, func(string, time.Time) error { return nil }))
	for _, err := range errs {
		if err == nil {
			t.Fatal("a wrong secret was accepted")
		}
		if errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("a refused request reads as missing: %v", err)
		}
		if strings.Contains(err.Error(), cfg.SecretKey) || strings.Contains(err.Error(), "Signature=") {
			t.Fatalf("error carries the secret or a signature: %v", err)
		}
	}
}

// NewBucket refuses an endpoint that would send credentials in the clear, and a partial config,
// before any request.
func TestNewBucketRefusesUnsafeConfig(t *testing.T) {
	ok := objects.S3Config{Endpoint: "https://s3.example.test", Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s"}
	if _, err := objects.NewBucket(ok, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	local := ok
	local.Endpoint = "http://127.0.0.1:9000"
	if _, err := objects.NewBucket(local, t.TempDir()); err != nil {
		t.Fatal("http on loopback:", err)
	}
	for name, edit := range map[string]func(*objects.S3Config){
		"http off loopback": func(c *objects.S3Config) { c.Endpoint = "http://s3.example.test" },
		"a path":            func(c *objects.S3Config) { c.Endpoint = "https://s3.example.test/bucket" },
		"credentials in it": func(c *objects.S3Config) { c.Endpoint = "https://a:b@s3.example.test" },
		"no scheme":         func(c *objects.S3Config) { c.Endpoint = "s3.example.test" },
		"no bucket":         func(c *objects.S3Config) { c.Bucket = "" },
		"no secret":         func(c *objects.S3Config) { c.SecretKey = "" },
		"no region":         func(c *objects.S3Config) { c.Region = "" },
	} {
		c := ok
		edit(&c)
		if _, err := objects.NewBucket(c, t.TempDir()); err == nil {
			t.Errorf("%s accepted", name)
		} else if strings.Contains(err.Error(), "b@") || strings.Contains(err.Error(), c.SecretKey+"x") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
