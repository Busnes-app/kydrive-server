package search

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

func TestTermsFoldCaseAccentsAndWidth(t *testing.T) {
	cases := map[string][]string{
		"Résumé RESUME":         {"resume"},
		"Straße STRASSE":        {"strasse"},
		"ﬁle Ｂｕｄｇｅｔ İstanbul":   {"file", "budget", "istanbul"},
		"ΟΔΟΣ οδός":             {"οδοσ"},
		"a b 2026-10-21 x_y":    {"2026", "10", "21"},
		"中文字":                   {"中文", "文字"},
		"中":                     {"中"},
		"한국어 カタカナ":              {"한국", "국어", "カタ", "タカ", "カナ"},
		"éte":                  {"ete"},
		strings.Repeat("x", 65): nil,
		strings.Repeat("x", 64): {strings.Repeat("x", 64)},
		"one two three four five six seven eight nine": {"one", "two", "three", "four", "five", "six", "seven", "eight"},
	}
	for in, want := range cases {
		if got := Terms(in); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("Terms(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCountCapsTermsAndFrequency(t *testing.T) {
	counts, length := Count(strings.Repeat("budget ", 300) + "plan")
	if counts["budget"] != maxTF || counts["plan"] != 1 || length != 301 {
		t.Fatalf("%v %d", counts, length)
	}
	var b strings.Builder
	for i := range MaxDocTerms + 10 {
		fmt.Fprintf(&b, "w%d ", i)
	}
	if counts, _ = Count(b.String()); len(counts) != MaxDocTerms || counts["w0"] != 1 || counts[fmt.Sprintf("w%d", MaxDocTerms)] != 0 {
		t.Fatalf("%d distinct", len(counts))
	}
}

// marked renders a snippet with its highlights in brackets, slicing by UTF-16 offsets as a
// browser does.
func marked(s *Snippet) string {
	u := utf16.Encode([]rune(s.Text))
	var b strings.Builder
	at := 0
	for _, h := range s.Highlights {
		b.WriteString(string(utf16.Decode(u[at:h[0]])) + "[" + string(utf16.Decode(u[h[0]:h[1]])) + "]")
		at = h[1]
	}
	b.WriteString(string(utf16.Decode(u[at:])))
	return b.String()
}

func TestSnippetHighlightsInUTF16(t *testing.T) {
	s, err := MakeSnippet(strings.NewReader("Intro 😀 line.\n\nThe Résumé\tof Zoë: résumé done."), Terms("resume zoe"))
	if err != nil || s == nil {
		t.Fatal(s, err)
	}
	if got := marked(s); got != "Intro 😀 line. The [Résumé] of [Zoë]: [résumé] done." || s.CutStart || s.CutEnd {
		t.Fatalf("%q %+v", got, s)
	}
	// CJK bigrams merge into one highlight.
	s, _ = MakeSnippet(strings.NewReader("報告の中文字です"), Terms("中文字"))
	if got := marked(s); got != "報告の[中文字]です" {
		t.Fatalf("%q", got)
	}
	if s, _ = MakeSnippet(strings.NewReader("nothing here"), Terms("budget")); s != nil {
		t.Fatalf("%+v", s)
	}
	// A long text gets a window around the best match and says it was cut.
	long := strings.Repeat("filler words ", 200) + "alpha " + strings.Repeat("more ", 10) + "alpha beta " + strings.Repeat("tail ", 200)
	s, _ = MakeSnippet(strings.NewReader(long), Terms("alpha beta"))
	if !s.CutStart || !s.CutEnd || !strings.Contains(marked(s), "[alpha] [beta]") || len(s.Text) > snippetBytes {
		t.Fatalf("%q %+v", marked(s), s)
	}
	// Markup is text.
	s, _ = MakeSnippet(strings.NewReader("<b onclick=x>budget</b>"), Terms("budget"))
	if s.Text != "<b onclick=x>budget</b>" {
		t.Fatalf("%q", s.Text)
	}
}

func FuzzSnippet(f *testing.F) {
	for _, s := range []string{"Résumé 😀 中文字 budget", "a\x00b\xffc budget", "ﬁ ß İ"} {
		f.Add(s, "budget résumé 中文")
	}
	f.Fuzz(func(t *testing.T, text, q string) {
		s, err := MakeSnippet(strings.NewReader(text), Terms(q))
		if err != nil {
			t.Fatal(err)
		}
		if s == nil {
			return
		}
		u := utf16.Encode([]rune(s.Text))
		last := 0
		for _, h := range s.Highlights {
			if h[0] < last || h[1] <= h[0] || h[1] > len(u) {
				t.Fatalf("bad range %v in %q", h, s.Text)
			}
			if utf16.IsSurrogate(rune(u[h[0]])) && h[0] > 0 && utf16.IsSurrogate(rune(u[h[0]-1])) && !utf8.ValidString(string(utf16.Decode(u[:h[0]]))) {
				t.Fatalf("range splits a character: %v in %q", h, s.Text)
			}
			last = h[1]
		}
		if !utf8.ValidString(s.Text) {
			t.Fatalf("invalid text %q", s.Text)
		}
	})
}

func openIndex(t *testing.T, dir string, key byte) *Index {
	t.Helper()
	x, err := Open(context.Background(), dir, bytes.Repeat([]byte{key}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

func TestIndexMatchesWithinTheGivenDocumentsOnly(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	x := openIndex(t, t.TempDir(), 1)
	put := func(blob, text string) string {
		ref := x.Ref(blob, "text")
		counts, n := Count(text)
		if err := x.Put(ctx, ref, counts, n, "f-"+blob, now); err != nil {
			t.Fatal(err)
		}
		return ref
	}
	a := put("a", "budget plan budget")
	b := put("b", "budget only")
	c := put("c", "plan budget secret")
	all := map[string]bool{a: true, b: true, c: true}
	scores, pending, err := x.Match(ctx, Terms("budget plan"), all)
	if err != nil || len(scores) != 2 || scores[b] != 0 || pending != 0 {
		t.Fatalf("%v %d %v", scores, pending, err)
	}
	// Only the given refs count, so a document outside them neither matches nor shapes scores.
	mine := map[string]bool{a: true, b: true}
	scores, _, _ = x.Match(ctx, Terms("secret"), mine)
	if len(scores) != 0 {
		t.Fatalf("%v", scores)
	}
	without, _, _ := x.Match(ctx, Terms("budget plan"), mine)
	scores, _, _ = x.Match(ctx, Terms("budget plan"), map[string]bool{a: true, b: true, x.Ref("other", "text"): true})
	if without[a] != scores[a] {
		t.Fatalf("an unindexed ref changed the score: %v %v", without, scores)
	}
	// Pending counts unsettled refs; a failure settles after MaxAttempts.
	d := x.Ref("d", "pdf")
	if _, pending, _ = x.Match(ctx, nil, map[string]bool{a: true, d: true}); pending != 1 {
		t.Fatalf("pending %d", pending)
	}
	for range MaxAttempts {
		if err := x.Fail(ctx, d, "failed", "f-d", now); err != nil {
			t.Fatal(err)
		}
	}
	state, attempts, _, _ := x.Status(ctx, d)
	if _, pending, _ = x.Match(ctx, nil, map[string]bool{a: true, d: true}); pending != 0 || state != StateFailed || attempts != MaxAttempts || Retry(state, attempts, now.Unix(), now.Add(time.Hour)) {
		t.Fatalf("pending %d state %d attempts %d", pending, state, attempts)
	}
	if !Retry(StateFailed, 1, now.Unix(), now.Add(2*time.Minute)) || Retry(StateFailed, 1, now.Unix(), now) || !Retry(0, 0, 0, now) || Retry(StateDone, 0, 0, now) {
		t.Fatal("retry rules")
	}
	// Keep removes other documents and their postings.
	if n, err := x.Keep(ctx, map[string]bool{a: true}); err != nil || n != 3 {
		t.Fatalf("kept: %d %v", n, err)
	}
	var postings int
	if err := x.db.QueryRow(`SELECT COUNT(*) FROM postings`).Scan(&postings); err != nil || postings != 2 {
		t.Fatalf("%d postings left %v", postings, err)
	}
}

func TestIndexQueue(t *testing.T) {
	ctx, now := context.Background(), time.Unix(1_000_000, 0)
	x := openIndex(t, t.TempDir(), 1)
	if err := x.Enqueue(ctx, []string{"f1", "f2"}, now, 10*time.Second, 42); err != nil {
		t.Fatal(err)
	}
	if c, _ := x.Cursor(ctx); c != 42 {
		t.Fatalf("cursor %d", c)
	}
	if due, _ := x.Due(ctx, now, false, 10); len(due) != 0 {
		t.Fatalf("debounce ignored: %v", due)
	}
	if due, _ := x.Due(ctx, now, true, 10); len(due) != 2 {
		t.Fatalf("force: %v", due)
	}
	// Requeued again and again, a file still comes due a minute after it was first queued.
	for i := range 10 {
		if err := x.Enqueue(ctx, []string{"f1"}, now.Add(time.Duration(i)*10*time.Second), 10*time.Second, -1); err != nil {
			t.Fatal(err)
		}
	}
	if due, _ := x.Due(ctx, now.Add(time.Minute), false, 10); !slices.Contains(due, "f1") {
		t.Fatalf("starved: %v", due)
	}
	if c, _ := x.Cursor(ctx); c != 42 {
		t.Fatalf("cursor moved: %d", c)
	}
	if err := x.Dequeue(ctx, "f1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := x.Queued(ctx); n != 1 {
		t.Fatalf("queued %d", n)
	}
}

func TestIndexStartsAgainUnderAnotherKey(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	x, err := Open(ctx, dir, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ref := x.Ref("a", "text")
	if err = x.Put(ctx, ref, map[string]int{"budget": 1}, 1, "f", time.Now()); err != nil {
		t.Fatal(err)
	}
	x.Close()
	x = openIndex(t, dir, 1)
	if state, _, _, _ := x.Status(ctx, ref); state != StateDone {
		t.Fatal("reopened with the same key lost the index")
	}
	x.Close()
	y := openIndex(t, dir, 2)
	var docs int
	if err := y.db.QueryRow(`SELECT COUNT(*) FROM docs`).Scan(&docs); err != nil || docs != 0 {
		t.Fatalf("another key kept %d docs %v", docs, err)
	}
	if _, err := Open(ctx, dir, []byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, File)); err != nil {
		t.Fatal(err)
	}
}

// The index file and its WAL hold neither the words nor the refs' blob IDs.
func TestIndexHoldsNoPlaintext(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	x := openIndex(t, dir, 1)
	marker := "zebracanary"
	counts, n := Count("The " + marker + " report about Zürichquokka and 秘密計画")
	if err := x.Put(ctx, x.Ref("0f8fad5b-d9cb-469f-a165-70867728950e", "docx"), counts, n, "file-id", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(filepath.Join(dir, File+suffix))
		if err != nil {
			t.Fatal(err)
		}
		for _, plain := range []string{marker, "report", "zurichquokka", "Zürich", "秘密", "0f8fad5b", "docx"} {
			if bytes.Contains(raw, []byte(plain)) {
				t.Errorf("%s holds %q", File+suffix, plain)
			}
		}
	}
}
