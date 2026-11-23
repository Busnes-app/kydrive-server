package objects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// S3Config names a bucket on an S3-compatible service. The secret is never logged or put in an
// error.
type S3Config struct {
	Endpoint  string // https://host[:port]; http only for a loopback host
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	PathStyle bool // the bucket in the path, not the host name (MinIO and most self-hosted services)
}

// Bucket is one S3-compatible bucket. Its Stores and Trees share one client, each under its own
// key prefix.
type Bucket struct {
	c      *s3.Client
	bucket string
	spool  string
}

// NewBucket opens cfg's bucket. Drafts are written to files in spool, a private local
// directory, and put whole on Publish.
func NewBucket(cfg S3Config, spool string) (*Bucket, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.User != nil {
		return nil, errors.New("S3 endpoint must be a bare https://host[:port] URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname())) {
		return nil, errors.New("S3 endpoint must use https (http only on a loopback host)")
	}
	if cfg.Region == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("S3 needs a region, a bucket, an access key ID and a secret key")
	}
	creds := aws.Credentials{AccessKeyID: cfg.AccessKey, SecretAccessKey: cfg.SecretKey, Source: "kydrive"}
	c := s3.New(s3.Options{
		BaseEndpoint: aws.String(cfg.Endpoint),
		Region:       cfg.Region,
		UsePathStyle: cfg.PathStyle,
		Credentials:  aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return creds, nil }),
	})
	if err := os.MkdirAll(spool, 0700); err != nil {
		return nil, err
	}
	return &Bucket{c: c, bucket: cfg.Bucket, spool: spool}, nil
}

func loopback(host string) bool {
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

// Store is the Store of the objects whose keys start with prefix, which ends in "/".
func (b *Bucket) Store(prefix string) Store { return s3Store{b, prefix} }

// Tree is the Tree whose Subs are the "directories" under prefix.
func (b *Bucket) Tree(prefix string) Tree { return s3Tree{b, prefix} }

type s3Store struct {
	b      *Bucket
	prefix string
}

func (st s3Store) key(name string) (string, error) {
	if _, err := dir("").path(name); err != nil {
		return "", err
	}
	return st.prefix + name, nil
}

func (st s3Store) Create(ctx context.Context) (Draft, error) {
	f, err := os.CreateTemp(st.b.spool, ".upload-*")
	if err != nil {
		return nil, err
	}
	return &s3Draft{File: f, st: st, ctx: ctx}, nil
}

// s3Draft keeps Create's context, so cancelling it stops the PUT in Publish.
type s3Draft struct {
	*os.File
	st   s3Store
	ctx  context.Context
	done bool
}

func (w *s3Draft) Publish(name string) error {
	defer w.Discard()
	key, err := w.st.key(name)
	if err != nil {
		return err
	}
	size, err := w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err = w.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = w.st.b.c.PutObject(w.ctx, &s3.PutObjectInput{
		Bucket: aws.String(w.st.b.bucket), Key: aws.String(key), Body: w.File, ContentLength: aws.Int64(size),
	})
	return wrap("put", key, err)
}

func (w *s3Draft) Discard() error {
	if w.done {
		return nil
	}
	w.done = true
	w.Close()
	if err := os.Remove(w.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (st s3Store) Open(ctx context.Context, name string) (Object, error) {
	key, err := st.key(name)
	if err != nil {
		return nil, err
	}
	h, err := st.b.c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(st.b.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, wrap("open", key, err)
	}
	rctx, cancel := context.WithCancel(ctx)
	return &s3Object{st: st, key: key, etag: aws.ToString(h.ETag), size: aws.ToInt64(h.ContentLength), ctx: rctx, cancel: cancel}, nil
}

// readAhead is how much one ranged GET fetches: sealed blobs are read in 64 KiB chunks.
const readAhead = 4 << 20

// s3Object reads one version of an object (its ETag), a window at a time, under Open's
// context; Close cancels a read still in flight.
type s3Object struct {
	st     s3Store
	key    string
	etag   string
	size   int64
	ctx    context.Context
	cancel context.CancelFunc

	mu  sync.Mutex
	off int64
	buf []byte
}

func (o *s3Object) Size() int64  { return o.size }
func (o *s3Object) Close() error { o.cancel(); return nil }

func (o *s3Object) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for n < len(p) && off < o.size {
		if off < o.off || off >= o.off+int64(len(o.buf)) {
			if err := o.fill(off, int64(len(p)-n)); err != nil {
				return n, err
			}
		}
		c := copy(p[n:], o.buf[off-o.off:])
		n += c
		off += int64(c)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (o *s3Object) fill(off, want int64) error {
	end := min(off+max(want, readAhead), o.size) - 1
	out, err := o.st.b.c.GetObject(o.ctx, &s3.GetObjectInput{
		Bucket: aws.String(o.st.b.bucket), Key: aws.String(o.key),
		Range: aws.String(fmt.Sprintf("bytes=%d-%d", off, end)), IfMatch: aws.String(o.etag),
	})
	if err != nil {
		return wrap("read", o.key, err)
	}
	defer out.Body.Close()
	buf := make([]byte, end-off+1)
	if _, err = io.ReadFull(out.Body, buf); err != nil {
		return wrap("read", o.key, err)
	}
	o.off, o.buf = off, buf
	return nil
}

func (st s3Store) Remove(ctx context.Context, names ...string) error {
	var errs []error
	for _, name := range names {
		key, err := st.key(name)
		if err == nil {
			_, err = st.b.c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(st.b.bucket), Key: aws.String(key)})
			err = wrap("remove", key, err)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (st s3Store) List(ctx context.Context, fn func(string, time.Time) error) error {
	return st.b.list(ctx, st.prefix, true, func(key string, modified time.Time) error {
		return fn(strings.TrimPrefix(key, st.prefix), modified)
	})
}

// list calls fn for each key under prefix: directly under it when flat, else every one.
func (b *Bucket) list(ctx context.Context, prefix string, flat bool, fn func(string, time.Time) error) error {
	in := &s3.ListObjectsV2Input{Bucket: aws.String(b.bucket), Prefix: aws.String(prefix)}
	if flat {
		in.Delimiter = aws.String("/")
	}
	pages := s3.NewListObjectsV2Paginator(b.c, in)
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return wrap("list", prefix, err)
		}
		for _, o := range page.Contents {
			if err := fn(aws.ToString(o.Key), aws.ToTime(o.LastModified)); err != nil {
				return err
			}
		}
	}
	return nil
}

type s3Tree struct {
	b      *Bucket
	prefix string
}

func (t s3Tree) Sub(name string) Store {
	if _, err := dir("").path(name); err != nil {
		return badName{}
	}
	return s3Store{t.b, t.prefix + name + "/"}
}

// List reports each Sub with its newest object's time: S3 has no directories to date.
func (t s3Tree) List(ctx context.Context, fn func(string, time.Time) error) error {
	newest := map[string]time.Time{}
	err := t.b.list(ctx, t.prefix, false, func(key string, modified time.Time) error {
		name, _, ok := strings.Cut(strings.TrimPrefix(key, t.prefix), "/")
		if ok && modified.After(newest[name]) {
			newest[name] = modified
		}
		return nil
	})
	if err != nil {
		return err
	}
	names := make([]string, 0, len(newest))
	for name := range newest {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := fn(name, newest[name]); err != nil {
			return err
		}
	}
	return nil
}

func (t s3Tree) RemoveAll(ctx context.Context, name string) error {
	if _, err := dir("").path(name); err != nil {
		return err
	}
	sub := t.Sub(name).(s3Store)
	var names []string
	if err := sub.List(ctx, func(n string, _ time.Time) error { names = append(names, n); return nil }); err != nil {
		return err
	}
	return sub.Remove(ctx, names...)
}

// wrap names what failed by operation and key, maps a missing object to fs.ErrNotExist, and
// keeps the service's error code, never a request with its signature.
func wrap(op, key string, err error) error {
	if err == nil {
		return nil
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NotFound", "NoSuchKey":
			return fmt.Errorf("s3 %s %s: %w", op, key, fs.ErrNotExist)
		}
		return fmt.Errorf("s3 %s %s: %s", op, key, ae.ErrorCode())
	}
	return fmt.Errorf("s3 %s %s: %w", op, key, err)
}
