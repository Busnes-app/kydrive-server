package objects

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"
)

// Tree is a set of Stores, one per name, that are removed whole: resumable-upload staging,
// one Store of segments per upload session.
type Tree interface {
	// Sub is the Store called name; it exists once something is written to it.
	Sub(name string) Store
	// List calls fn for every Sub that holds an object, with the time it last changed; an
	// empty Sub may or may not be listed.
	List(ctx context.Context, fn func(name string, modified time.Time) error) error
	// RemoveAll deletes the Sub called name and everything in it; a missing one is gone.
	RemoveAll(ctx context.Context, name string) error
}

// DirTree is the Tree of the directories in root, one per Sub.
func DirTree(root string) Tree { return dirTree(root) }

type dirTree string

func (t dirTree) Sub(name string) Store {
	path, err := dir(t).path(name)
	if err != nil {
		return badName{}
	}
	return dir(path)
}

func (t dirTree) List(_ context.Context, fn func(string, time.Time) error) error {
	entries, err := os.ReadDir(string(t))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() {
			continue
		}
		if err := fn(e.Name(), info.ModTime()); err != nil {
			return err
		}
	}
	return nil
}

func (t dirTree) RemoveAll(_ context.Context, name string) error {
	path, err := dir(t).path(name)
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

// badName is the Store of a name no Tree accepts: every call fails.
type badName struct{}

func (badName) Create(context.Context) (Draft, error)        { return nil, fs.ErrInvalid }
func (badName) Open(context.Context, string) (Object, error) { return nil, fs.ErrInvalid }
func (badName) Remove(context.Context, ...string) error      { return fs.ErrInvalid }
func (badName) List(context.Context, func(string, time.Time) error) error {
	return fs.ErrInvalid
}
