package drive

import (
	"cmp"
	"context"
	"errors"
	"regexp"
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
	// Shared is set on an item reached only through Shared with me; Path then starts at the
	// shared item, never above it.
	Shared *SharedFrom `json:"shared,omitempty"`
	score  float64
}

// SharedFrom names where a shared hit comes from, as Shared with me shows it: the workspace's
// name, or a personal workspace owner's display name, and the user's highest role there.
type SharedFrom struct {
	From string `json:"from"`
	Role string `json:"role"`
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
	return s.SearchContent(ctx, user, q, "", limit, nil, false)
}

// SearchContent is Search that also asks m (nil: names only) which readable files' contents
// match. Name matches come first in Search's order, then content-only matches by score. A
// type (ValidType) keeps only hits of that type and allows an empty q, which matches every name.
// shared also searches the items in the user's Shared with me list outside those workspaces
// (searchShared); every such hit returned is re-checked for read access first.
func (s *Store) SearchContent(ctx context.Context, user, q, typ string, limit int, m ContentMatcher, shared bool) ([]Hit, bool, error) {
	q, err := searchQuery(q, typ)
	if err != nil {
		return nil, false, err
	}
	spaces, err := s.Workspaces(ctx, user, false)
	if err != nil {
		return nil, false, err
	}
	hits := []Hit{}
	var files []candidate
	member := map[string]bool{}
	for _, w := range spaces {
		member[w.ID] = true
		found, readable, err := s.searchSpace(ctx, user, w.ID, "", nameQuery(q), typ)
		if err != nil {
			return nil, false, err
		}
		hits = append(hits, found...)
		files = append(files, readable...)
	}
	if shared {
		found, readable, err := s.searchShared(ctx, user, nameQuery(q), typ, member)
		if err != nil {
			return nil, false, err
		}
		hits = append(hits, found...)
		files = append(files, readable...)
	}
	if hits, err = matchContent(ctx, q, m, hits, files); err != nil {
		return nil, false, err
	}
	hits, more, err := sortHits(hits, limit)
	if err != nil || !shared {
		return hits, more, err
	}
	// The share may have ended while matching: answer only what is still readable.
	kept := hits[:0]
	for _, h := range hits {
		if h.Shared != nil {
			if ok, err := s.stillShared(ctx, user, h); err != nil {
				return nil, false, err
			} else if !ok {
				continue
			}
		}
		kept = append(kept, h)
	}
	return kept, more, nil
}

// searchShared is searchSpace over the live items in user's Shared with me list whose
// workspace is not in member (those were searched whole): a shared file, or a shared folder
// with its live subtree. Paths start at the shared item. Only a signed-in user's own context
// gets them: service, device and KyPost contexts search no shares.
func (s *Store) searchShared(ctx context.Context, user, q, typ string, member map[string]bool) ([]Hit, []candidate, error) {
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service || narrowed(ctx) {
		return nil, nil, nil
	}
	items, err := s.SharedWithMe(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	var hits []Hit
	var readable []candidate
	seen := map[string]bool{} // a share nested in another shared folder is searched once
	add := func(h Hit, f *File) {
		if seen[h.ID] {
			return
		}
		seen[h.ID] = true
		if f != nil {
			readable = append(readable, candidate{*f, h})
		}
		if strings.Contains(strings.ToLower(h.Name), q) {
			h.Match = []string{"name"}
			hits = append(hits, h)
		}
	}
	for _, i := range items {
		if member[i.Workspace] {
			continue
		}
		from := &SharedFrom{From: i.WorkspaceName, Role: i.Role}
		top := Hit{Kind: i.Kind, ID: i.ID, Workspace: i.Workspace, Parent: i.Parent, Name: i.Name, Shared: from}
		if i.Kind == "file" {
			files, err := s.queryFiles(ctx, s.db, `f.id=? AND f.trashed=false`, i.ID)
			if err != nil {
				return nil, nil, err
			}
			for _, f := range files {
				if typ == "" || FileType(f.Name) == typ {
					add(top, &f)
				}
			}
			continue
		}
		if typ == "" || typ == "folders" {
			add(top, nil)
		}
		tree := `(WITH RECURSIVE tree(id) AS (SELECT ? UNION SELECT d.id FROM drive_folders d JOIN tree t ON d.parent=t.id WHERE d.trashed=false) SELECT id FROM tree)`
		folders, err := s.queryFolders(ctx, s.db, `workspace=? AND trashed=false AND id IN `+tree, i.Workspace, i.ID)
		if err != nil {
			return nil, nil, err
		}
		files, err := s.queryFiles(ctx, s.db, `f.workspace=? AND f.trashed=false AND f.parent IN `+tree, i.Workspace, i.ID)
		if err != nil {
			return nil, nil, err
		}
		found, cands := matchNames(folders, files, i.Workspace, i.ID, q, typ)
		for _, c := range cands {
			c.hit.Path, c.hit.Shared = below(i.Name, c.hit.Path), from
			add(c.hit, &c.file)
		}
		for _, h := range found {
			if h.Kind == "folder" {
				h.Path, h.Shared = below(i.Name, h.Path), from
				add(h, nil)
			}
		}
	}
	return hits, readable, nil
}

// below is path p beneath the shared folder named top.
func below(top, p string) string {
	if p == "" {
		return top
	}
	return top + "/" + p
}

// stillShared re-checks a shared hit: live, and readable by user now.
func (s *Store) stillShared(ctx context.Context, user string, h Hit) (bool, error) {
	var err error
	if h.Kind == "file" {
		var f File
		if f, err = s.File(ctx, user, h.ID, 1); err == nil && f.Trashed {
			return false, nil
		}
	} else {
		var live int
		if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_folders WHERE id=? AND workspace=? AND trashed=false`, h.ID, h.Workspace).Scan(&live); err == nil && live == 0 {
			return false, nil
		}
		if err == nil {
			err = authorizeItem(ctx, s.db, user, h.Workspace, h.ID, "", 1)
		}
	}
	if errors.Is(err, ErrDenied) {
		return false, nil
	}
	return err == nil, err
}

// candidate is a readable live file in the searched scope and the hit it would be.
type candidate struct {
	file File
	hit  Hit
}

// matchContent marks name hits whose content matched and adds content-only hits.
func matchContent(ctx context.Context, q string, m ContentMatcher, hits []Hit, files []candidate) ([]Hit, error) {
	if m == nil || q == "" || len(files) == 0 {
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
	return s.SearchFolderContent(ctx, user, workspace, folder, q, "", limit, nil)
}

// SearchFolderContent is SearchFolder that also asks m (nil: names only) which files in the
// subtree match by content, as SearchContent does.
func (s *Store) SearchFolderContent(ctx context.Context, user, workspace, folder, q, typ string, limit int, m ContentMatcher) ([]Hit, bool, error) {
	q, err := searchQuery(q, typ)
	if err != nil {
		return nil, false, err
	}
	if err = authorizeItem(ctx, s.db, user, workspace, folder, "", 1); err != nil {
		return nil, false, err
	}
	hits, files, err := s.searchSpace(ctx, user, workspace, folder, nameQuery(q), typ)
	if err != nil {
		return nil, false, err
	}
	if hits, err = matchContent(ctx, q, m, hits, files); err != nil {
		return nil, false, err
	}
	return sortHits(hits, limit)
}

// nameQuery is q as names match it, the content syntax (double quotes, a * ending a word)
// removed. Syntax alone matches no name: "\x00", which no valid name holds.
func nameQuery(q string) string {
	n := strings.TrimSpace(strings.ReplaceAll(wordStar.ReplaceAllString(q, "$1"), `"`, ""))
	if n == "" && q != "" {
		return "\x00"
	}
	return n
}

var wordStar = regexp.MustCompile(`\*+(\s|$)`)

func searchQuery(q, typ string) (string, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	n := utf8.RuneCountInString(q)
	if !ValidType(typ) || n > 100 || n < 2 && !(n == 0 && typ != "") {
		return "", ErrInvalid
	}
	return q, nil
}

// searchSpace matches q against names of type typ ("" = any) in workspace beneath root ("" =
// everywhere) and returns every live file of that type there for content matching; a workspace
// the user lost access to since listing it has neither.
func (s *Store) searchSpace(ctx context.Context, user, workspace, root, q, typ string) ([]Hit, []candidate, error) {
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
	hits, readable := matchNames(folders, files, workspace, root, q, typ)
	return hits, readable, nil
}

// matchNames matches q against the names of type typ among folders and files of workspace
// beneath root ("" = everywhere) and returns every file of that type there as a candidate.
func matchNames(folders []Folder, files []File, workspace, root, q, typ string) ([]Hit, []candidate) {
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
		if (typ == "" || typ == "folders") && strings.Contains(strings.ToLower(f.Name), q) {
			if p, ok := path(f.Parent); ok {
				hits = append(hits, Hit{Kind: "folder", ID: f.ID, Workspace: workspace, Parent: f.Parent, Name: f.Name, Path: p, Match: []string{"name"}})
			}
		}
	}
	for _, f := range files {
		if typ != "" && FileType(f.Name) != typ {
			continue
		}
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
	return hits, readable
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
