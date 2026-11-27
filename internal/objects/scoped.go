package objects

import (
	"context"
	"io/fs"
	"time"
)

// ScopedStore selects an organization's namespace. Unscoped writes/reads fail closed;
// unscoped removal/listing is reserved for the drive's UUID-based maintenance jobs.
type ScopedStore struct {
	Select func(string) (Store, error)
	All    func(context.Context) ([]Store, error)
}

func Scope(st Store, org string) Store {
	if p, ok := st.(interface{ ForOrg(string) Store }); ok {
		return p.ForOrg(org)
	}
	return st
}
func (s ScopedStore) ForOrg(org string) Store {
	st, err := s.Select(org)
	if err != nil {
		return failedStore{err}
	}
	return st
}
func (s ScopedStore) Create(context.Context) (Draft, error)        { return nil, fs.ErrPermission }
func (s ScopedStore) Open(context.Context, string) (Object, error) { return nil, fs.ErrPermission }
func (s ScopedStore) Remove(ctx context.Context, names ...string) error {
	if len(names) == 0 {
		return nil
	}
	stores, err := s.All(ctx)
	if err != nil {
		return err
	}
	for _, st := range stores {
		if err := st.Remove(ctx, names...); err != nil {
			return err
		}
	}
	return nil
}
func (s ScopedStore) List(ctx context.Context, fn func(string, time.Time) error) error {
	stores, err := s.All(ctx)
	if err != nil {
		return err
	}
	for _, st := range stores {
		if err := st.List(ctx, fn); err != nil {
			return err
		}
	}
	return nil
}

type failedStore struct{ err error }

func (s failedStore) Create(context.Context) (Draft, error)                     { return nil, s.err }
func (s failedStore) Open(context.Context, string) (Object, error)              { return nil, s.err }
func (s failedStore) Remove(context.Context, ...string) error                   { return s.err }
func (s failedStore) List(context.Context, func(string, time.Time) error) error { return s.err }

// ScopedTree gives each org its own upload sessions, including their segments.
type ScopedTree struct {
	Select func(string) (Tree, error)
	All    func(context.Context) ([]Tree, error)
}

func ScopeTree(t Tree, org string) Tree {
	if p, ok := t.(ScopedTree); ok {
		return selectedTree{p, org}
	}
	return t
}

type selectedTree struct {
	tree ScopedTree
	org  string
}

func (s selectedTree) Sub(name string) Store {
	t, err := s.tree.Select(s.org)
	if err != nil {
		return failedStore{err}
	}
	return t.Sub(name)
}
func (s selectedTree) List(ctx context.Context, fn func(string, time.Time) error) error {
	t, err := s.tree.Select(s.org)
	if err != nil {
		return err
	}
	return t.List(ctx, fn)
}
func (s selectedTree) RemoveAll(ctx context.Context, name string) error {
	t, err := s.tree.Select(s.org)
	if err != nil {
		return err
	}
	return t.RemoveAll(ctx, name)
}
func (s ScopedTree) Sub(name string) Store {
	return ScopedStore{Select: func(org string) (Store, error) {
		t, err := s.Select(org)
		if err != nil {
			return nil, err
		}
		return t.Sub(name), nil
	}, All: func(ctx context.Context) ([]Store, error) {
		trees, err := s.All(ctx)
		if err != nil {
			return nil, err
		}
		var out []Store
		for _, t := range trees {
			out = append(out, t.Sub(name))
		}
		return out, nil
	}}
}
func (s ScopedTree) List(ctx context.Context, fn func(string, time.Time) error) error {
	trees, err := s.All(ctx)
	if err != nil {
		return err
	}
	for _, t := range trees {
		if err := t.List(ctx, fn); err != nil {
			return err
		}
	}
	return nil
}
func (s ScopedTree) RemoveAll(ctx context.Context, name string) error {
	trees, err := s.All(ctx)
	if err != nil {
		return err
	}
	for _, t := range trees {
		if err := t.RemoveAll(ctx, name); err != nil {
			return err
		}
	}
	return nil
}
