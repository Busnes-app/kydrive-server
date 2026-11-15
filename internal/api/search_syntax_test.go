package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/search"
)

// searchType is search with a type.
func (w *searchWorld) searchType(t *testing.T, who *http.Cookie, q, typ string) searchResponse {
	t.Helper()
	rec := do(t, w.srv, "GET", "/api/drive/search?q="+url.QueryEscape(q)+"&type="+typ, who)
	if rec.Code != http.StatusOK {
		t.Fatalf("search %q %s: %d %s", q, typ, rec.Code, rec.Body.String())
	}
	var r searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	r.raw = rec.Body.String()
	return r
}

func (r searchResponse) sortedNames() string {
	var out []string
	for _, h := range r.Results {
		out = append(out, h.Name)
	}
	slices.Sort(out)
	return fmt.Sprint(out)
}

// Prefixes (3+ letters and *) and quoted phrases: only the caller's readable files, phrases in
// order across line and page breaks, with the type filter, unicode folding, the name match
// ignoring the syntax, and snippets showing the phrase.
func TestContentSearchPrefixAndPhrase(t *testing.T) {
	w := newSearchWorld(t)
	w.put(t, "usr_alice", drive.File{Workspace: w.aliceMine, Name: "notes.txt"}, []byte("The conference planning notes.\n\nLoading the Kernel\n\f\fmodule takes time."))
	w.put(t, "usr_alice", drive.File{Workspace: w.aliceMine, Name: "howto.md"}, []byte("Configure the module first, then the kernel."))
	w.put(t, "usr_alice", drive.File{Workspace: w.aliceMine, Name: "costs.csv"}, []byte("item,cost\nkernel module,12\nconference,30"))
	w.put(t, "usr_alice", drive.File{Workspace: w.aliceMine, Name: "cv.txt"}, []byte("Résumé of Zoë"))
	w.put(t, "usr_alice", drive.File{Workspace: w.aliceMine, Name: "conference call.log"}, []byte("dial in"))
	w.put(t, "usr_bob", drive.File{Workspace: w.bobMine, Name: "bob.txt"}, []byte("confidential kernel module confbobword"))
	w.index(t)

	for _, c := range []struct{ q, typ, want string }{
		{"conf*", "", "[conference call.log costs.csv howto.md notes.txt]"}, // the .log by name
		{"CONF* kernel", "", "[costs.csv howto.md notes.txt]"},
		{"conf*", "documents", "[howto.md notes.txt]"}, // a .log is no document type
		{"conf*", "spreadsheets", "[costs.csv]"},
		{`"kernel module"`, "", "[costs.csv notes.txt]"}, // a paragraph and page break between; howto.md has them apart
		{`"kernel module"`, "spreadsheets", "[costs.csv]"},
		{`"module kernel"`, "", "[]"},
		{`"conference call"`, "", "[conference call.log]"}, // by name, quotes ignored there
		{"resu*", "", "[cv.txt]"},
		{"RÉSU*", "", "[cv.txt]"},
		{"zoe", "", "[cv.txt]"},
		{"co*", "", "[conference call.log costs.csv]"}, // under 3 letters: names holding "co", no content (no word "co")
		{"**", "", "[]"},                               // syntax alone: no name, no content
		{`""`, "", "[]"},                               // the same
		{"confbob*", "", "[]"},                         // bob's word: never alice's hit
		{`"confidential kernel"`, "", "[]"},
	} {
		r := w.searchType(t, w.alice, c.q, c.typ)
		if got := r.sortedNames(); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.q, c.typ, got, c.want)
		}
		if strings.Contains(r.raw, "confbobword") || strings.Contains(r.raw, "bob.txt") || strings.Contains(r.raw, "confidential") {
			t.Errorf("%s: bob's text: %s", c.q, r.raw)
		}
		if r.Contents.Unverified != 0 || r.Contents.Capped {
			t.Errorf("%s: %s", c.q, r.raw)
		}
	}
	r := w.search(t, w.alice, `"kernel module"`)
	for _, h := range r.Results {
		if h.Name == "notes.txt" && (h.Snippet == nil || len(h.Snippet.Highlights) != 1 || !strings.Contains(h.Snippet.Text, "Kernel module")) {
			t.Fatalf("phrase snippet: %s", r.raw)
		}
	}
	r = w.search(t, w.alice, "conf*")
	if !strings.Contains(r.raw, "conference") || !strings.Contains(r.raw, `"highlights":[[`) {
		t.Fatalf("prefix snippet: %s", r.raw)
	}
	// Bob finds his own word by prefix.
	if r = w.search(t, w.bob, "confbob*"); r.sortedNames() != "[bob.txt]" {
		t.Fatalf("bob: %s", r.raw)
	}
}

// A phrase is checked in the best phraseChecks candidates only; the rest are counted, not shown.
func TestPhraseChecksAreCapped(t *testing.T) {
	w := newSearchWorld(t)
	for i := range 205 {
		w.put(t, "usr_alice", drive.File{Workspace: w.aliceMine, Name: fmt.Sprintf("n%03d.txt", i)}, fmt.Appendf(nil, "alpha beta %d", i))
	}
	w.index(t)
	r := w.search(t, w.alice, `"alpha beta"`)
	if len(r.Results) != 200 || r.Contents.Unverified != 5 {
		t.Fatalf("%d results, %d unverified", len(r.Results), r.Contents.Unverified)
	}
	if r = w.search(t, w.alice, `"beta alpha"`); len(r.Results) != 0 || r.Contents.Unverified != 5 {
		t.Fatalf("reversed: %d results, %d unverified", len(r.Results), r.Contents.Unverified)
	}
}

// Upgrading from an index with no vocabulary needs no reindex: the reconcile builds it from the
// sealed text cache, whole words keep working meanwhile, and the postings are untouched.
func TestVocabularyBuildsWithoutReindex(t *testing.T) {
	w := newSearchWorld(t)
	w.put(t, "usr_alice", drive.File{Workspace: w.aliceMine, Name: "notes.txt"}, []byte("conference planning"))
	w.index(t)
	if r := w.search(t, w.alice, "conf*"); r.sortedNames() != "[notes.txt]" {
		t.Fatalf("before: %s", r.raw)
	}
	updated := func() string {
		t.Helper()
		db, err := sql.Open("sqlite", filepath.Join(api.SearchRootForTest(w.srv), search.File)+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var s string
		if err = db.QueryRow(`SELECT group_concat(updated||':'||len) FROM docs`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if err := w.srv.CloseSearch(); err != nil {
		t.Fatal(err)
	}
	before := updated()
	if err := os.Remove(filepath.Join(api.SearchRootForTest(w.srv), search.VocabFile)); err != nil {
		t.Fatal(err)
	}
	w.srv = api.NewServer(w.cfg, w.st)
	t.Cleanup(func() { w.srv.CloseSearch() })
	if r := w.search(t, w.alice, "conference"); r.sortedNames() != "[notes.txt]" {
		t.Fatalf("whole words during the build: %s", r.raw)
	}
	if r := w.search(t, w.alice, "conf*"); r.sortedNames() != "[]" {
		t.Fatalf("no vocabulary yet: %s", r.raw)
	}
	w.index(t)
	if r := w.search(t, w.alice, "conf*"); r.sortedNames() != "[notes.txt]" {
		t.Fatalf("after the build: %s", r.raw)
	}
	if after := updated(); after != before {
		t.Fatalf("reindexed: %s, was %s", after, before)
	}
}
