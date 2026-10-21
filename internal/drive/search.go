package drive

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// Hit is a live file or folder whose name or content matched. Path joins its ancestor folder
// names with "/". Match lists "name" and/or "content".
type Hit struct {
	Kind      string   `json:"kind"`
	ID        string   `json:"id"`
	Workspace string   `json:"workspace"`
	Parent    string   `json:"parent"`
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Match     []string `json:"match"`
	score     float64
}

// ContentMatcher scores the files among files whose content matches q, keyed by file ID. files
// are exactly the live files the user can read in the searched scope; the matcher must not
// look beyond them.
type ContentMatcher interface {
	Match(ctx context.Context, q string, files []File) (map[string]float64, error)
}

// Search matches q case-insensitively against every live name in the workspaces user can read.
// Names are sealed, so they are decrypted and matched here; SQL never sees them. It returns at
// most limit hits and whether more matched.
func (s *Store) Search(ctx context.Context, user, q string, limit int) ([]Hit, bool, error) {
	return s.SearchContent(ctx, user, q, limit, nil)
}

// SearchContent is Search that also asks m (nil: names only) which readable files' contents
// match. Name matches come first in Search's order, then content-only matches by score.
func (s *Store) SearchContent(ctx context.Context, user, q string, limit int, m ContentMatcher) ([]Hit, bool, error) {
	q, err := searchQuery(q)
	if err != nil {
		return nil, false, err
	}
	spaces, err := s.Workspaces(ctx, user, false)
	if err != nil {
		return nil, false, err
	}
	hits := []Hit{}
	var files []candidate
	for _, w := range spaces {
		found, readable, err := s.searchSpace(ctx, user, w.ID, "", q)
		if err != nil {
			return nil, false, err
		}
		hits = append(hits, found...)
		files = append(files, readable...)
	}
	if hits, err = matchContent(ctx, q, m, hits, files); err != nil {
		return nil, false, err
	}
	return sortHits(hits, limit)
}

// candidate is a readable live file in the searched scope and the hit it would be.
type candidate struct {
	file File
	hit  Hit
}

// matchContent marks name hits whose content matched and adds content-only hits.
func matchContent(ctx context.Context, q string, m ContentMatcher, hits []Hit, files []candidate) ([]Hit, error) {
	if m == nil || len(files) == 0 {
		return hits, nil
	}
	list := make([]File, len(files))
	for i, c := range files {
		list[i] = c.file
	}
	scores, err := m.Match(ctx, q, list)
	if err != nil {
		return nil, err
	}
	named := map[string]int{}
	for i, h := range hits {
		if h.Kind == "file" {
			named[h.ID] = i
		}
	}
	for _, c := range files {
		score, ok := scores[c.file.ID]
		if !ok {
			continue
		}
		if i, ok := named[c.file.ID]; ok {
			hits[i].Match = append(hits[i].Match, "content")
			hits[i].score = score
			continue
		}
		h := c.hit
		h.Match, h.score = []string{"content"}, score
		hits = append(hits, h)
	}
	return hits, nil
}

// SearchFolder is Search within the live subtree of folder in workspace ("" = the whole
// workspace), the folder itself excluded; each Path is relative to folder.
func (s *Store) SearchFolder(ctx context.Context, user, workspace, folder, q string, limit int) ([]Hit, bool, error) {
	return s.SearchFolderContent(ctx, user, workspace, folder, q, limit, nil)
}

// SearchFolderContent is SearchFolder that also asks m (nil: names only) which files in the
// subtree match by content, as SearchContent does.
func (s *Store) SearchFolderContent(ctx context.Context, user, workspace, folder, q string, limit int, m ContentMatcher) ([]Hit, bool, error) {
	q, err := searchQuery(q)
	if err != nil {
		return nil, false, err
	}
	if err = authorizeItem(ctx, s.db, user, workspace, folder, "", 1); err != nil {
		return nil, false, err
	}
	hits, files, err := s.searchSpace(ctx, user, workspace, folder, q)
	if err != nil {
		return nil, false, err
	}
	if hits, err = matchContent(ctx, q, m, hits, files); err != nil {
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

// searchSpace matches q against names in workspace beneath root ("" = everywhere) and returns
// every live file there for content matching; a workspace the user lost access to since
// listing it has neither.
func (s *Store) searchSpace(ctx context.Context, user, workspace, root, q string) ([]Hit, []candidate, error) {
	folders, err := s.Folders(ctx, user, workspace, false)
	if errors.Is(err, ErrDenied) {
		return nil, nil, nil // access ended since Workspaces
	}
	if err != nil {
		return nil, nil, err
	}
	files, err := s.Files(ctx, user, workspace, false)
	if errors.Is(err, ErrDenied) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var walk func(string) (string, bool)
	byID := make(map[string]Folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}
	// path joins parent's names below root, and reports whether parent is root or beneath it.
	// Every live file needs one for content matching, so results are kept per parent.
	type joined struct {
		path string
		ok   bool
	}
	paths := map[string]joined{}
	path := func(parent string) (string, bool) {
		if j, ok := paths[parent]; ok {
			return j.path, j.ok
		}
		p, ok := walk(parent)
		paths[parent] = joined{p, ok}
		return p, ok
	}
	walk = func(parent string) (string, bool) {
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
	var readable []candidate
	for _, f := range folders {
		if strings.Contains(strings.ToLower(f.Name), q) {
			if p, ok := path(f.Parent); ok {
				hits = append(hits, Hit{Kind: "folder", ID: f.ID, Workspace: workspace, Parent: f.Parent, Name: f.Name, Path: p, Match: []string{"name"}})
			}
		}
	}
	for _, f := range files {
		p, ok := path(f.Parent)
		if !ok {
			continue
		}
		h := Hit{Kind: "file", ID: f.ID, Workspace: workspace, Parent: f.Parent, Name: f.Name, Path: p}
		readable = append(readable, candidate{f, h})
		if strings.Contains(strings.ToLower(f.Name), q) {
			h.Match = []string{"name"}
			hits = append(hits, h)
		}
	}
	return hits, readable, nil
}

func sortHits(hits []Hit, limit int) ([]Hit, bool, error) {
	if hits == nil {
		hits = []Hit{}
	}
	// Name matches first, by lowercase name, path and ID; then content-only matches by score.
	named := func(h Hit) bool { return h.Match[0] == "name" }
	slices.SortFunc(hits, func(a, b Hit) int {
		if named(a) != named(b) {
			if named(a) {
				return -1
			}
			return 1
		}
		if !named(a) && a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.Path, b.Path), strings.Compare(a.ID, b.ID))
	})
	if len(hits) > limit {
		return hits[:limit], true, nil
	}
	return hits, false, nil
}
