package drive

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// Hit is a live file or folder whose name matched. Path joins its ancestor folder names with "/".
type Hit struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Parent    string `json:"parent"`
	Name      string `json:"name"`
	Path      string `json:"path"`
}

// Search matches q case-insensitively against every live name in the workspaces user can read.
// Names are sealed, so they are decrypted and matched here; SQL never sees them. It returns at
// most limit hits and whether more matched.
func (s *Store) Search(ctx context.Context, user, q string, limit int) ([]Hit, bool, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if n := utf8.RuneCountInString(q); n < 2 || n > 100 {
		return nil, false, ErrInvalid
	}
	spaces, err := s.Workspaces(ctx, user, false)
	if err != nil {
		return nil, false, err
	}
	hits := []Hit{}
	for _, w := range spaces {
		folders, err := s.Folders(ctx, user, w.ID, false)
		if errors.Is(err, ErrDenied) {
			continue // access ended since Workspaces
		}
		if err != nil {
			return nil, false, err
		}
		files, err := s.Files(ctx, user, w.ID, false)
		if err != nil && !errors.Is(err, ErrDenied) {
			return nil, false, err
		}
		byID := make(map[string]Folder, len(folders))
		for _, f := range folders {
			byID[f.ID] = f
		}
		path := func(parent string) string {
			var names []string
			for depth := 0; parent != "" && depth < 256; depth++ {
				f, ok := byID[parent]
				if !ok {
					break
				}
				names = append(names, f.Name)
				parent = f.Parent
			}
			slices.Reverse(names)
			return strings.Join(names, "/")
		}
		for _, f := range folders {
			if strings.Contains(strings.ToLower(f.Name), q) {
				hits = append(hits, Hit{Kind: "folder", ID: f.ID, Workspace: w.ID, Parent: f.Parent, Name: f.Name, Path: path(f.Parent)})
			}
		}
		for _, f := range files {
			if strings.Contains(strings.ToLower(f.Name), q) {
				hits = append(hits, Hit{Kind: "file", ID: f.ID, Workspace: w.ID, Parent: f.Parent, Name: f.Name, Path: path(f.Parent)})
			}
		}
	}
	slices.SortFunc(hits, func(a, b Hit) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.Path, b.Path), strings.Compare(a.ID, b.ID))
	})
	if len(hits) > limit {
		return hits[:limit], true, nil
	}
	return hits, false, nil
}
