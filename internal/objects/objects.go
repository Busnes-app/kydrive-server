// Package objects stores named, immutable objects: KyDrive's blobs and the caches derived from
// them. Dir keeps them as files in one directory, as KyDrive always has; an S3-compatible
// backend for the hosted install implements the same Store.
package objects

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store holds objects by name. A name is one path element that does not start with a dot;
// callers choose names (blob IDs) and validate them.
type Store interface {
	// Create starts a new object. Nothing is visible under any name until Publish.
	Create(ctx context.Context) (Draft, error)
	// Open reads a published object; a missing one is fs.ErrNotExist.
	Open(ctx context.Context, name string) (Object, error)
	// Remove deletes names; a missing name is already gone.
	Remove(ctx context.Context, names ...string) error
	// List calls fn for every entry, unfinished drafts included (their names start with a
	// dot), with the time it was last written.
	List(ctx context.Context, fn func(name string, modified time.Time) error) error
}

// Draft is an object being written.
type Draft interface {
	io.Writer
	// Publish makes the written bytes durable and visible as name, replacing any object of
	// that name. After an error nothing new is visible.
	Publish(name string) error
	// Discard drops the draft; it is safe after Publish.
	Discard() error
}

// Object is a published object, read at any offset.
type Object interface {
	io.ReaderAt
	io.Closer
	Size() int64
}

// Local is a Store whose objects are files, for the single-tenant maintenance jobs that
// rewrite a file in place (legacy sealing, format upgrade) or hand files to Restic.
type Local interface {
	Store
	// Root is the directory that holds the files.
	Root() string
}

// Dir is the Store of the files in root, created on first write.
func Dir(root string) Local { return dir(root) }

type dir string

func (d dir) Root() string { return string(d) }

func (d dir) path(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", fs.ErrInvalid
	}
	return filepath.Join(string(d), name), nil
}

func (d dir) Create(context.Context) (Draft, error) {
	if err := os.MkdirAll(string(d), 0700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(string(d), ".upload-*")
	if err != nil {
		return nil, err
	}
	return &draft{File: f, dir: d}, nil
}

type draft struct {
	*os.File
	dir    dir
	closed bool
}

func (w *draft) Publish(name string) error {
	path, err := w.dir.path(name)
	if err == nil {
		err = w.Sync()
	}
	if err == nil {
		w.closed = true
		err = w.Close()
	}
	if err == nil {
		err = os.Rename(w.Name(), path)
	}
	if err == nil {
		if err = syncDir(string(w.dir)); err != nil {
			os.Remove(path)
		}
	}
	if err != nil {
		w.Discard()
	}
	return err
}

func (w *draft) Discard() error {
	if !w.closed {
		w.closed = true
		w.Close()
	}
	if err := os.Remove(w.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (d dir) Open(_ context.Context, name string) (Object, error) {
	path, err := d.path(name)
	if err != nil {
		return nil, err
	}
	return OpenFile(path)
}

// OpenFile opens a file as an Object, for files outside any Store (resumable upload staging).
func OpenFile(path string) (Object, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return file{f, info.Size()}, nil
}

type file struct {
	*os.File
	size int64
}

func (f file) Size() int64 { return f.size }

func (d dir) Remove(_ context.Context, names ...string) error {
	var errs []error
	for _, name := range names {
		path, err := d.path(name)
		if err == nil {
			err = os.Remove(path)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if len(names) > 0 {
		if err := syncDir(string(d)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (d dir) List(_ context.Context, fn func(string, time.Time) error) error {
	entries, err := os.ReadDir(string(d))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := fn(e.Name(), info.ModTime()); err != nil {
			return err
		}
	}
	return nil
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
