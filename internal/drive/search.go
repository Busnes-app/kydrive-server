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
	q, err := searchQuery(q)
	if err != nil {
		return nil, false, err
	}
	spaces, err := s.Workspaces(ctx, user, false)
	if err != nil {
		return nil, false, err
	}
	hits := []Hit{}
	for _, w := range spaces {
		found, err := s.searchSpace(ctx, user, w.ID, "", q)
		if err != nil {
			return nil, false, err
		}
		hits = append(hits, found...)
	}
	return sortHits(hits, limit)
}

// SearchFolder is Search within the live subtree of folder in workspace ("" = the whole
// workspace), the folder itself excluded; each Path is relative to folder.
func (s *Store) SearchFolder(ctx context.Context, user, workspace, folder, q string, limit int) ([]Hit, bool, error) {
	q, err := searchQuery(q)
	if err != nil {
		return nil, false, err
	}
	if err = authorizeItem(ctx, s.db, user, workspace, folder, "", 1); err != nil {
		return nil, false, err
	}
	hits, err := s.searchSpace(ctx, user, workspace, folder, q)
	if err != nil {
		return nil, false, err
	}
	return sortHits(hits, limit)
}

func searchQuery(q string) (string, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if n := utf8.RuneCountInString(q); n < 2 || n > 100 {
		return "", ErrInvalid
	}
	return q, nil
}

// searchSpace matches q in workspace beneath root ("" = everywhere); a workspace the user lost
// access to since listing it has no hits.
func (s *Store) searchSpace(ctx context.Context, user, workspace, root, q string) ([]Hit, error) {
	folders, err := s.Folders(ctx, user, workspace, false)
	if errors.Is(err, ErrDenied) {
		return nil, nil // access ended since Workspaces
	}
	if err != nil {
		return nil, err
	}
	files, err := s.Files(ctx, user, workspace, false)
	if err != nil && !errors.Is(err, ErrDenied) {
		return nil, err
	}
	byID := make(map[string]Folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}
	// path joins parent's names below root, and reports whether parent is root or beneath it.
	path := func(parent string) (string, bool) {
		var names []string
		for depth := 0; parent != root; depth++ {
			f, ok := byID[parent]
			if !ok || depth == 256 {
				if root != "" {
					return "", false
				}
				break
			}
			names = append(names, f.Name)
			parent = f.Parent
		}
		slices.Reverse(names)
		return strings.Join(names, "/"), true
	}
	var hits []Hit
	for _, f := range folders {
		if strings.Contains(strings.ToLower(f.Name), q) {
			if p, ok := path(f.Parent); ok {
				hits = append(hits, Hit{Kind: "folder", ID: f.ID, Workspace: workspace, Parent: f.Parent, Name: f.Name, Path: p})
			}
		}
	}
	for _, f := range files {
		if strings.Contains(strings.ToLower(f.Name), q) {
			if p, ok := path(f.Parent); ok {
				hits = append(hits, Hit{Kind: "file", ID: f.ID, Workspace: workspace, Parent: f.Parent, Name: f.Name, Path: p})
			}
		}
	}
	return hits, nil
}

func sortHits(hits []Hit, limit int) ([]Hit, bool, error) {
	if hits == nil {
		hits = []Hit{}
	}
	slices.SortFunc(hits, func(a, b Hit) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.Path, b.Path), strings.Compare(a.ID, b.ID))
	})
	if len(hits) > limit {
		return hits[:limit], true, nil
	}
	return hits, false, nil
}
