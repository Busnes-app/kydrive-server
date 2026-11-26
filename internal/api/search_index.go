package api

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/extract"
	"github.com/Busnes-app/kydrive-server/internal/search"
)

// Content search. The index (internal/search) lives in <KY_DATA_DIR>/search, outside every
// backup; RunSearchIndex fills it from the drive change log and rebuilds it from scratch when it
// is missing. What a user may see is decided per request: hits come only from the files
// drive.SearchContent lists as readable, and a snippet only from a file that passes a fresh read
// check, read from its sealed text cache.

const (
	indexPoll      = 3 * time.Second  // how often the worker reads new changes
	indexDebounce  = 10 * time.Second // a changed file waits this long (an editor saves often)
	indexReconcile = time.Hour        // full comparison with the drive, and garbage collection
	snippetCount   = 20               // HTTP hits that get a snippet
	snippetBudget  = 500 * time.Millisecond
	phraseChecks   = 200 // best phrase candidates checked against their text, within snippetBudget
	kindText       = "text"
)

// Content searches one user may run, web and MCP together: plenty for typing, too few to time
// prefix searches into telling what other users' files hold (docs/RECOVERY-AND-ENCRYPTION.md).
const (
	searchesPerMinute = 60
	searchesPerHour   = 600
)

// searchWait counts one content search by user: 0 when allowed, else the seconds to wait.
func (s *Server) searchWait(user string) int {
	if !s.allowAttempt("search-minute:"+user, searchesPerMinute, time.Minute) {
		return 60
	}
	if !s.allowAttempt("search-hour:"+user, searchesPerHour, time.Hour) {
		return 3600
	}
	return 0
}

// textKinds are plain text files indexed as they are (UTF-8, no NUL).
var textKinds = map[string]bool{"txt": true, "md": true, "markdown": true, "csv": true, "tsv": true, "log": true}

func (s *Server) searchRoot() string { return filepath.Join(s.config.Database.DataDir, "search") }

// openSearch opens the index when content search is on: single-tenant SQLite only, so its
// text caches are sealed under the deployment key (searchKeys). Per-org search keys (D14) come
// with the per-org index.
func (s *Server) openSearch() {
	if !s.config.Drive.ContentSearch || !strings.EqualFold(s.config.Database.Driver, "sqlite") || s.store.Drive().Multi() {
		return
	}
	x, err := search.Open(context.Background(), s.searchRoot(), s.config.Security.EncryptionKey)
	if err != nil {
		log.Printf("content search off: %v", err)
		return
	}
	s.search = x
}

func (s *Server) searchKeys() drive.Keys { return drive.Deployment(s.config.Security.EncryptionKey) }

// CloseSearch closes the index; call it after RunSearchIndex has returned.
func (s *Server) CloseSearch() error {
	if s.search == nil {
		return nil
	}
	return s.search.Close()
}

// indexKind is the extractor for a file named name, or "" when its content is not indexed.
// PDFs need Euro-Office.
func (s *Server) indexKind(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	switch {
	case extract.Kind(ext) != "":
		return ext
	case ext == "pdf" && s.editor != nil:
		return "pdf"
	case textKinds[ext]:
		return kindText
	}
	return ""
}

// indexText returns f's text as kind, through the sealed text cache.
func (s *Server) indexText(ctx context.Context, f drive.File, kind string) (string, error) {
	if kind != kindText {
		text, _, err := s.documentText(ctx, f, kind)
		return text, err
	}
	if cached, err := drive.ReadThumb(s.textRoot(), s.searchKeys(), f.Blob); err == nil && len(cached) > 0 {
		return string(cached[1:]), nil
	}
	blob, err := s.openBlob(context.Background(), f)
	if err != nil {
		log.Printf("search index %s: %v", f.ID, err)
		return "", errExtractFailed
	}
	defer blob.Close()
	data, err := io.ReadAll(io.LimitReader(blob, extract.MaxText+1))
	if err != nil {
		log.Printf("search index %s: %v", f.ID, err)
		return "", errExtractFailed
	}
	flag := byte('c')
	if len(data) > extract.MaxText {
		flag, data = 't', data[:extract.MaxText]
		for len(data) > 0 && !utf8.FullRune(data[lastRuneStart(data):]) {
			data = data[:lastRuneStart(data)]
		}
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", extract.ErrUnsupported
	}
	if err := drive.WriteThumb(s.textRoot(), s.searchKeys(), f.Blob, append([]byte{flag}, data...)); err != nil {
		log.Printf("document text cache %s: %v", f.ID, err)
	}
	return string(data), nil
}

func lastRuneStart(b []byte) int {
	i := len(b) - 1
	for i > 0 && !utf8.RuneStart(b[i]) {
		i--
	}
	return max(i, 0)
}

// failCode is the reason a failed extraction is recorded with; never content.
func failCode(err error) string {
	switch {
	case errors.Is(err, extract.ErrTooLarge):
		return "too_large"
	case errors.Is(err, extract.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, errNoPDFConverter):
		return "no_converter"
	}
	return "failed"
}

func (s *Server) textCached(blob string) bool {
	if _, err := drive.BlobPath("", blob); err != nil {
		return false
	}
	o, err := s.textRoot().Open(context.Background(), blob)
	if err != nil {
		return false
	}
	o.Close()
	return true
}

// RunSearchIndex keeps the index in step with the drive until ctx ends: it reconciles at start
// and hourly, and every few seconds queues changed files and indexes those due, throttled to
// about two thirds of a core.
func (s *Server) RunSearchIndex(ctx context.Context) {
	if s.search == nil {
		return
	}
	var reconciled time.Time
	for {
		if time.Since(reconciled) >= indexReconcile {
			if err := s.reconcileSearch(ctx, time.Now()); err != nil && ctx.Err() == nil {
				log.Printf("search index reconcile: %v", err)
			}
			reconciled = time.Now()
		}
		if err := s.tailSearch(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("search index changes: %v", err)
		}
		if err := s.drainSearch(ctx, false, true); err != nil && ctx.Err() == nil {
			log.Printf("search index: %v", err)
		}
		if err := s.search.SaveVocab(false); err != nil {
			log.Printf("search vocabulary: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(indexPoll):
		}
	}
}

// tailSearch queues the files named by drive changes since the stored cursor.
func (s *Server) tailSearch(ctx context.Context, now time.Time) error {
	cursor, err := s.search.Cursor(ctx)
	if err != nil {
		return err
	}
	for {
		files, next, err := s.store.Drive().IndexChanges(ctx, cursor, 500)
		if err != nil || next == cursor {
			return err
		}
		if err = s.search.Enqueue(ctx, files, now, indexDebounce, next); err != nil {
			return err
		}
		cursor = next
	}
}

// reconcileSearch compares the index with every current file: it queues what is unindexed,
// retryable or missing its text cache, and deletes documents no current file holds (purged
// files, old versions, sealed legacy blobs). It is what rebuilds the index after a restore.
// It also brings the prefix vocabulary up to date from the sealed text cache (vocabSearch).
func (s *Server) reconcileSearch(ctx context.Context, now time.Time) error {
	docs, err := s.search.Docs(ctx)
	if err != nil {
		return err
	}
	since, fresh := s.search.VocabDue(now)
	words := map[string]string{} // ref -> blob, documents whose words the vocabulary may lack
	keep := map[string]bool{}
	var todo []string
	for after := ""; ; {
		files, err := s.store.Drive().IndexFiles(ctx, after, 1000)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			break
		}
		for _, f := range files {
			if kind := s.indexKind(f.Name); kind != "" {
				ref := s.search.Ref(f.Blob, kind)
				keep[ref] = true
				d := docs[ref]
				if search.Retry(d.State, d.Attempts, d.Updated, now) || (d.State == search.StateDone && !s.textCached(f.Blob)) {
					todo = append(todo, f.ID)
				}
				if d.State == search.StateDone && (fresh || d.Updated >= since) {
					words[ref] = f.Blob
				}
			}
		}
		after = files[len(files)-1].ID
	}
	if n, err := s.search.Keep(ctx, keep); err != nil {
		return err
	} else if n > 0 {
		log.Printf("search index: removed %d documents", n)
	}
	if err := s.search.Enqueue(ctx, todo, now, 0, -1); err != nil {
		return err
	}
	return s.vocabSearch(ctx, words, fresh, now)
}

// vocabSearch adds the words of the documents in blobs, read from the sealed text cache, to
// the prefix vocabulary, replacing it when fresh: that builds it after an upgrade, after a
// crash lost its latest words, and daily, so no reindex is ever needed. It runs on the worker,
// throttled like indexing; search keeps working meanwhile on the vocabulary as it was.
func (s *Server) vocabSearch(ctx context.Context, blobs map[string]string, fresh bool, now time.Time) error {
	if len(blobs) == 0 && !fresh {
		return nil
	}
	b := s.search.BuildVocab(fresh, now)
	for _, blob := range blobs {
		start := time.Now()
		if cached, err := drive.ReadThumb(s.textRoot(), s.searchKeys(), blob); err == nil && len(cached) > 0 {
			b.Add(string(cached[1:]))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Since(start) / 2):
		}
	}
	return b.Done()
}

// drainSearch indexes queued files until none is due (with force, until the queue is empty).
func (s *Server) drainSearch(ctx context.Context, force, throttle bool) error {
	for {
		due, err := s.search.Due(ctx, time.Now(), force, 20)
		if err != nil || len(due) == 0 {
			return err
		}
		for _, id := range due {
			start := time.Now()
			if err := s.indexFile(ctx, id, start); err != nil {
				return err
			}
			if throttle {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(max(50*time.Millisecond, time.Since(start)/2)):
				}
			}
		}
	}
}

// indexFile indexes the current version of file id, or drops it from the queue when there is
// nothing to do. Only database errors are returned; extraction failures are recorded.
func (s *Server) indexFile(ctx context.Context, id string, now time.Time) error {
	f, err := s.store.Drive().IndexFile(ctx, id)
	if errors.Is(err, drive.ErrNotFound) {
		return s.search.Dequeue(ctx, id)
	}
	if err != nil {
		return err
	}
	kind := s.indexKind(f.Name)
	if kind == "" {
		return s.search.Dequeue(ctx, id)
	}
	ref := s.search.Ref(f.Blob, kind)
	state, attempts, updated, err := s.search.Status(ctx, ref)
	if err != nil {
		return err
	}
	done := state == search.StateDone
	if (done && s.textCached(f.Blob)) || (!done && !search.Retry(state, attempts, updated, now)) {
		return s.search.Dequeue(ctx, id)
	}
	text, err := s.indexText(ctx, f, kind)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("search index %s: %s", f.ID, failCode(err))
		return s.search.Fail(ctx, ref, failCode(err), id, now)
	}
	if done {
		s.search.Learn(text)
		return s.search.Dequeue(ctx, id) // the text cache is back; the postings were kept
	}
	counts, length := search.Count(text)
	return s.search.Put(ctx, ref, counts, length, id, now)
}

// indexIdle brings the index fully up to date now, debounce ignored. Tests use it instead of
// waiting for the worker.
func (s *Server) indexIdle(ctx context.Context) error {
	if s.search == nil {
		return nil
	}
	if err := s.tailSearch(ctx, time.Now()); err != nil {
		return err
	}
	if err := s.reconcileSearch(ctx, time.Now()); err != nil {
		return err
	}
	if err := s.drainSearch(ctx, true, false); err != nil {
		return err
	}
	return s.search.SaveVocab(true)
}

// contentMatcher matches file contents for drive.SearchContent (search.ParseQuery syntax) and
// remembers what to highlight, how many of the caller's files are not yet indexed, how many
// phrase candidates went unchecked and whether a prefix was capped.
type contentMatcher struct {
	s          *Server
	terms      []string // highlighted alone: words and the words prefixes expanded to
	phrases    [][]string
	pending    int
	unverified int
	capped     bool
}

// matcher returns a content matcher, or nil when content search is off.
func (s *Server) matcher() *contentMatcher {
	if s.search == nil {
		return nil
	}
	return &contentMatcher{s: s}
}

func (m *contentMatcher) Match(ctx context.Context, q string, files []drive.File) (map[string]float64, error) {
	query := search.ParseQuery(q)
	refs := map[string]bool{}
	byRef := map[string][]string{}
	blobs := map[string]string{}
	for _, f := range files {
		if kind := m.s.indexKind(f.Name); kind != "" {
			ref := m.s.search.Ref(f.Blob, kind)
			refs[ref] = true
			byRef[ref] = append(byRef[ref], f.ID)
			blobs[ref] = f.Blob
		}
	}
	got, err := m.s.search.Match(ctx, query, refs)
	if err != nil {
		return nil, err
	}
	m.terms = append(slices.Clone(query.Words), got.Expanded...)
	m.phrases, m.pending, m.capped = query.Phrases, got.Pending, got.Capped
	scores := got.Scores
	if len(query.Phrases) > 0 {
		scores = m.verify(scores, blobs, byRef)
	}
	out := map[string]float64{}
	for ref, score := range scores {
		for _, id := range byRef[ref] {
			out[id] = score
		}
	}
	return out, nil
}

// verify keeps the scored refs whose cached text holds every phrase in order, checking the best
// phraseChecks within snippetBudget; files not checked (or without a text cache) are counted in
// unverified and dropped, never guessed.
func (m *contentMatcher) verify(scores map[string]float64, blobs map[string]string, byRef map[string][]string) map[string]float64 {
	refs := make([]string, 0, len(scores))
	for ref := range scores {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b string) int { return cmp.Or(cmp.Compare(scores[b], scores[a]), strings.Compare(a, b)) })
	deadline := time.Now().Add(snippetBudget)
	out := map[string]float64{}
	for i, ref := range refs {
		if i >= phraseChecks || time.Now().After(deadline) {
			for _, r := range refs[i:] {
				m.unverified += len(byRef[r])
			}
			break
		}
		cached, err := drive.ReadThumb(m.s.textRoot(), m.s.searchKeys(), blobs[ref])
		if err != nil || len(cached) == 0 {
			m.unverified += len(byRef[ref])
			continue
		}
		if search.HasPhrases(string(cached[1:]), m.phrases) {
			out[ref] = scores[ref]
		}
	}
	return out
}

// snippet makes the snippet for a content hit after checking, now, that user can read the
// file; ok is false when they cannot, and the hit must be dropped. A missing text cache is
// queued for extraction and gives no snippet.
func (s *Server) snippet(ctx context.Context, user string, h drive.Hit, m *contentMatcher) (*search.Snippet, bool) {
	f, err := s.store.Drive().File(ctx, user, h.ID, 1)
	if err != nil || f.Trashed {
		return nil, false
	}
	r, err := drive.OpenThumb(s.textRoot(), s.searchKeys(), f.Blob)
	if errors.Is(err, fs.ErrNotExist) {
		if err := s.search.Enqueue(ctx, []string{f.ID}, time.Now(), 0, -1); err != nil {
			log.Printf("search index queue %s: %v", f.ID, err)
		}
		return nil, true
	}
	if err != nil {
		return nil, true
	}
	defer r.Close()
	if _, err := r.Seek(1, io.SeekStart); err != nil { // the cache's truncation flag
		return nil, true
	}
	sn, err := search.MakeSnippet(r, m.terms, m.phrases)
	if err != nil {
		return nil, true
	}
	return sn, true
}

// searchHit is a drive hit with its snippet, for HTTP.
type searchHit struct {
	drive.Hit
	Snippet *search.Snippet `json:"snippet,omitempty"`
}

// withSnippets adds snippets to the first n content hits, dropping any the user can no longer
// read, within snippetBudget.
func (s *Server) withSnippets(ctx context.Context, user string, hits []drive.Hit, m *contentMatcher, n int) []searchHit {
	out := make([]searchHit, 0, len(hits))
	deadline := time.Now().Add(snippetBudget)
	for _, h := range hits {
		sh := searchHit{Hit: h}
		if n > 0 && m != nil && contentHit(h) && time.Now().Before(deadline) {
			n--
			sn, ok := s.snippet(ctx, user, h, m)
			if !ok {
				continue
			}
			sh.Snippet = sn
		}
		out = append(out, sh)
	}
	return out
}

func contentHit(h drive.Hit) bool {
	for _, m := range h.Match {
		if m == "content" {
			return true
		}
	}
	return false
}
