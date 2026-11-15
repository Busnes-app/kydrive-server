package search

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseQuery(t *testing.T) {
	for _, c := range []struct{ q, want string }{
		{"conf*", "w[] p[conf] f[]"},
		{"Conf* plan", "w[plan] p[conf] f[]"},
		{"co* x*", "w[co] p[] f[]"},           // under 3 letters: a whole word; one letter is no term
		{"*", "w[] p[] f[]"},                  // nothing to expand
		{"Résu*", "w[] p[resu] f[]"},          // folded like indexed words
		{"ＣＯＮＦ*", "w[] p[conf] f[]"},          // width folded
		{"東京都*", "w[東京 京都] p[] f[]"},          // CJK has no prefixes
		{"conf* conf", "w[conf] p[conf] f[]"}, // a prefix and the same word are two terms
		{`"kernel module" load`, "w[load] p[] f[[kernel module]]"},
		{"\"Kernel,\n\fModule\"", "w[] p[] f[[kernel module]]"}, // breaks are not words
		{`"kernel"`, "w[kernel] p[] f[]"},                       // one word in quotes
		{`"kernel module`, "w[kernel module] p[] f[]"},          // unclosed: words
		{`one two three four five six "alpha beta gamma"`, "w[one two three four five six] p[] f[]"}, // a phrase that does not fit is dropped
		{`"to be or not to be"`, "w[] p[] f[[to be or not to be]]"},
		{`"the the cat"`, "w[] p[] f[[the the cat]]"}, // repeats kept in order
		{`"conf*"`, "w[conf] p[] f[]"},                // no prefixes inside quotes
		{"one two three four five six seven eight nine*", "w[one two three four five six seven eight] p[] f[]"},
	} {
		got := ParseQuery(c.q)
		if s := fmt.Sprintf("w%v p%v f%v", got.Words, got.Prefixes, got.Phrases); s != c.want {
			t.Errorf("%q: %s, want %s", c.q, s, c.want)
		}
	}
	if !ParseQuery(`"" *`).Empty() || ParseQuery("ab").Empty() {
		t.Error("Empty")
	}
}

func TestHasPhrases(t *testing.T) {
	text := "Loading the Kernel\n\nmodule: page one.\f\fPage two ends the file."
	for _, c := range []struct {
		q    string
		want bool
	}{
		{`"kernel module"`, true},   // across a paragraph break
		{`"one page two"`, true},    // across a page break
		{`"module kernel"`, false},  // order counts
		{`"loading kernel"`, false}, // a word between (one-letter words are no terms, so do not count)
		{`"kernel module" "ends the file"`, true},
		{`"kernel module" "the end"`, false}, // every phrase
	} {
		if got := HasPhrases(text, ParseQuery(c.q).Phrases); got != c.want {
			t.Errorf("%s: %v", c.q, got)
		}
	}
	if !HasPhrases("東京都庁", ParseQuery(`"東京都"`).Phrases) {
		t.Error("CJK phrase")
	}
}

func TestSnippetHighlightsThePhrase(t *testing.T) {
	text := "kernel alone here. Then module alone. Later: load the Kernel module now."
	q := ParseQuery(`"kernel module"`)
	s := snippetOf(text, q.Words, q.Phrases)
	if s == nil || len(s.Highlights) != 1 || s.Text[s.Highlights[0][0]:s.Highlights[0][1]] != "Kernel module" {
		t.Fatalf("%+v", s)
	}
}

// Highlighting phrases in one unbroken CJK run does work linear in its length: doubling the
// text roughly doubles the allocation, it does not quadruple it.
func TestSnippetPhrasesLinear(t *testing.T) {
	q := ParseQuery(`"中文中"`) // the bigrams 中文 文中, matching at every other rune
	alloc := func(runes int) uint64 {
		text := strings.Repeat("中文", runes/2)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if snippetOf(text, q.Words, q.Phrases) == nil {
			t.Fatal("no snippet")
		}
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	small, large := alloc(4000), alloc(8000)
	if large > 3*small {
		t.Fatalf("allocated %d bytes for 4000 runes, %d for 8000", small, large)
	}
}

// Dense matches across the whole scan, with the phrase repeated, keep a bounded number of hits
// and still give a valid highlighted snippet.
func TestSnippetHitsAreBounded(t *testing.T) {
	q := ParseQuery(`"ab cd" "ab cd" "ab  cd" ab cd`)
	if len(q.Phrases) != 1 {
		t.Fatalf("duplicate phrases kept: %v", q.Phrases)
	}
	text := strings.Repeat("ab cd ", SnippetScan/6)
	hits, _ := findHits(text, q.Words, q.Phrases)
	if len(hits) > 2*snippetHits {
		t.Fatalf("%d hits kept", len(hits))
	}
	for i, h := range hits[1:] {
		if h.start < hits[i].start {
			t.Fatal("hits out of order")
		}
	}
	sn := snippetOf(text, q.Words, q.Phrases)
	if sn == nil || len(sn.Highlights) == 0 || sn.CutStart {
		t.Fatalf("snippet %+v", sn)
	}
}

func put(t *testing.T, x *Index, blob, text string) string {
	t.Helper()
	ref := x.Ref(blob, "text")
	counts, n := Count(text)
	if err := x.Put(context.Background(), ref, counts, n, "f-"+blob, time.Now()); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestPrefixMatch(t *testing.T) {
	ctx := context.Background()
	x := openIndex(t, t.TempDir(), 1)
	a := put(t, x, "a", "conference planning")
	b := put(t, x, "b", "configure the kernel")
	c := put(t, x, "c", "confidential résumé")
	d := put(t, x, "d", "cone of silence")
	all := map[string]bool{a: true, b: true, c: true, d: true}
	match := func(q string, refs map[string]bool) []string {
		t.Helper()
		m, err := x.Match(ctx, ParseQuery(q), refs)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for ref := range m.Scores {
			out = append(out, map[string]string{a: "a", b: "b", c: "c", d: "d"}[ref])
		}
		slices.Sort(out)
		return out
	}
	for q, want := range map[string]string{
		"conf*":        "[a b c]",
		"CONF* kernel": "[b]",
		"con*":         "[a b c d]",
		"resum*":       "[c]", // accents folded on both sides
		"conf":         "[]",  // whole words stay whole
		"zzz*":         "[]",
		"conf* zzz*":   "[]",
	} {
		if got := fmt.Sprint(match(q, all)); got != want {
			t.Errorf("%s: %s, want %s", q, got, want)
		}
	}
	// Expansion never reaches documents the caller cannot read, nor ranks by them.
	if got := fmt.Sprint(match("conf*", map[string]bool{d: true, b: true})); got != "[b]" {
		t.Errorf("scoped: %s", got)
	}
	m, _ := x.Match(ctx, ParseQuery("conf*"), all)
	slices.Sort(m.Expanded)
	if fmt.Sprint(m.Expanded) != "[conference confidential configure]" || m.Capped {
		t.Errorf("expanded %v capped %v", m.Expanded, m.Capped)
	}
}

func TestPrefixCap(t *testing.T) {
	ctx := context.Background()
	x := openIndex(t, t.TempDir(), 1)
	var words []string
	for i := range MaxPrefixWords + 20 {
		words = append(words, fmt.Sprintf("pre%03d", i))
	}
	all := map[string]bool{}
	all[put(t, x, "all", strings.Join(words, " "))] = true
	// The last 20 words are also in a second document, so they rank first.
	all[put(t, x, "some", strings.Join(words[MaxPrefixWords:], " "))] = true
	m, err := x.Match(ctx, ParseQuery("pre*"), all)
	if err != nil || !m.Capped || len(m.Expanded) != MaxPrefixWords || !slices.Contains(m.Expanded, words[len(words)-1]) || len(m.Scores) != 2 {
		t.Fatalf("capped %v, %d words, %d docs, %v", m.Capped, len(m.Expanded), len(m.Scores), err)
	}
}

// However many words share a prefix, Match keeps the frequencies of MaxPrefixWords of them.
func TestPrefixKeepsCappedFrequencies(t *testing.T) {
	kept := 0
	testFrequencies = func(string) { kept++ }
	t.Cleanup(func() { testFrequencies = nil })
	for _, n := range []int{2 * MaxPrefixWords, 8 * MaxPrefixWords} {
		x := openIndex(t, t.TempDir(), 1)
		var words []string
		for i := range n {
			words = append(words, fmt.Sprintf("many%04d", i))
		}
		refs := map[string]bool{put(t, x, "all", strings.Join(words, " ")): true}
		kept = 0
		m, err := x.Match(context.Background(), ParseQuery("many*"), refs)
		if err != nil || !m.Capped || len(m.Expanded) != MaxPrefixWords {
			t.Fatalf("%d words: capped %v, %d expanded, %v", n, m.Capped, len(m.Expanded), err)
		}
		if kept != MaxPrefixWords {
			t.Fatalf("%d words: kept frequencies of %d", n, kept)
		}
	}
}

// Words only other users' documents hold never decide what a prefix covers, whether it was
// capped, or what it reports: planting MaxPrefixWords words of one's own reveals nothing.
func TestPrefixIgnoresUnreadableWords(t *testing.T) {
	ctx := context.Background()
	var mine []string
	for i := range MaxPrefixWords {
		mine = append(mine, fmt.Sprintf("probe%03d", i))
	}
	search := func(others bool) Matched {
		x := openIndex(t, t.TempDir(), 1)
		refs := map[string]bool{put(t, x, "mine", strings.Join(mine, " ")): true}
		if others {
			for i := range 3 { // in more documents than any of mine
				put(t, x, fmt.Sprintf("theirs%d", i), "probesecret probe000x")
			}
		}
		m, err := x.Match(ctx, ParseQuery("probe*"), refs)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(m.Expanded)
		return m
	}
	alone, with := search(false), search(true)
	if with.Capped || len(with.Expanded) != MaxPrefixWords || slices.Contains(with.Expanded, "probesecret") || len(with.Scores) != 1 {
		t.Fatalf("capped %v, %d words, %d docs", with.Capped, len(with.Expanded), len(with.Scores))
	}
	if fmt.Sprint(alone) != fmt.Sprint(with) {
		t.Fatalf("other users' words changed the result:\n%v\n%v", alone, with)
	}
}

// The vocabulary is sealed on disk: no word, no length table in the clear, and a tampered,
// foreign-key or other-tokenizer file opens empty (and is rebuilt by the worker).
func TestVocabularyIsSealed(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	x := openIndex(t, dir, 1)
	if err := x.BuildVocab(true, time.Now()).Done(); err != nil { // the first build, as the worker does
		t.Fatal(err)
	}
	marker := "zebracanaryquokka"
	ref := put(t, x, "a", "The "+marker+" report about Zürichplan")
	if err := x.SaveVocab(true); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, plain := range []string{marker, "zebra", "report", "zurichplan", "Zürich"} {
			if bytes.Contains(raw, []byte(plain)) || bytes.Contains(bytes.ToLower(raw), []byte(plain)) {
				t.Errorf("%s holds %q", e.Name(), plain)
			}
		}
	}
	path := filepath.Join(dir, VocabFile)
	sealed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = x.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopened: prefixes still expand, with no reindex.
	y := openIndex(t, dir, 1)
	if m, _ := y.Match(ctx, ParseQuery("zebra*"), map[string]bool{ref: true}); len(m.Scores) != 1 {
		t.Fatalf("after reopen: %+v", m)
	}
	if since, fresh := y.VocabDue(time.Now()); fresh || since == 0 {
		t.Fatalf("due %d %v", since, fresh)
	}
	y.Close()

	// One flipped byte: empty, and a full rebuild is due.
	bad := slices.Clone(sealed)
	bad[len(bad)/2] ^= 'A' ^ 'B'
	for name, raw := range map[string][]byte{"tampered": bad, "truncated": sealed[:len(sealed)-4]} {
		if err = os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		z := openIndex(t, dir, 1)
		if m, _ := z.Match(ctx, ParseQuery("zebra*"), map[string]bool{ref: true}); len(m.Scores) != 0 {
			t.Errorf("%s vocabulary used", name)
		}
		if _, fresh := z.VocabDue(time.Now()); !fresh {
			t.Errorf("%s: no rebuild due", name)
		}
		z.Close()
	}
	// Another key cannot open it.
	if err = os.WriteFile(path, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	k := &Index{dir: dir, vkey: bytes.Repeat([]byte{2}, 32), vaad: y.vaad}
	if v := k.loadVocab(); len(v.words) != 0 {
		t.Error("opened under another key")
	}
	k = &Index{dir: dir, vkey: y.vkey, vaad: []byte("kydrive:search-vocab:other:2")}
	if v := k.loadVocab(); len(v.words) != 0 {
		t.Error("opened for another tokenizer")
	}
}

// A vocabulary build (after an upgrade, a crash or daily) replaces or extends it from texts.
func TestVocabularyBuild(t *testing.T) {
	ctx := context.Background()
	x := openIndex(t, t.TempDir(), 1)
	ref := put(t, x, "a", "conference")
	refs := map[string]bool{ref: true}
	now := time.Now()
	if _, fresh := x.VocabDue(now); !fresh {
		t.Fatal("a new index needs a first build")
	}
	b := x.BuildVocab(true, now)
	b.Add("configure only") // the build's texts are the whole vocabulary
	if err := b.Done(); err != nil {
		t.Fatal(err)
	}
	// The rebuilt vocabulary lacks the document's word, and holds one no searched document has.
	if m, _ := x.Match(ctx, ParseQuery("conf*"), refs); len(m.Expanded)+len(m.Scores) != 0 {
		t.Fatalf("fresh: %v %v", m.Expanded, m.Scores)
	}
	if since, fresh := x.VocabDue(now.Add(time.Hour)); fresh || since < now.Unix() {
		t.Fatalf("due %d %v", since, fresh)
	}
	if _, fresh := x.VocabDue(now.Add(vocabDay)); !fresh {
		t.Fatal("no daily rebuild")
	}
	b = x.BuildVocab(false, now)
	b.Add("conference")
	if err := b.Done(); err != nil {
		t.Fatal(err)
	}
	m, _ := x.Match(ctx, ParseQuery("conf*"), refs)
	slices.Sort(m.Expanded)
	if fmt.Sprint(m.Expanded) != "[conference]" || len(m.Scores) != 1 {
		t.Fatalf("catch-up: %v %v", m.Expanded, m.Scores)
	}
}

func TestDecodeVocabRefusesGarbage(t *testing.T) {
	v := newVocab()
	v.learn(map[string]int{"alpha": 1, "beta": 1, "gamma": 1})
	good := v.encode()
	if w, err := decodeVocab(good); err != nil || fmt.Sprint(w.words) != "[alpha beta gamma]" {
		t.Fatalf("%v %v", w, err)
	}
	for i := range good {
		decodeVocab(good[:i]) // never panics
	}
	if _, err := decodeVocab(append(slices.Clone(good), 0)); err == nil {
		t.Error("trailing byte accepted")
	}
}
