package search

import (
	"cmp"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	// SnippetScan is how much text a snippet looks through for a match.
	SnippetScan = 4 << 20
	// snippetBytes is the length of a snippet's window; snippetLead the context before its
	// first match.
	snippetBytes = 240
	snippetLead  = 60
)

// Snippet is a short plain-text excerpt. Highlights are [start, end) offsets in UTF-16 code
// units of Text (JavaScript string indexes). It is never markup.
type Snippet struct {
	Text       string   `json:"text"`
	Highlights [][2]int `json:"highlights"`
	CutStart   bool     `json:"cut_start"`
	CutEnd     bool     `json:"cut_end"`
}

type hit struct {
	key        int // index into the distinct terms and phrases
	start, end int
}

// MakeSnippet reads at most SnippetScan bytes of text from r and returns the window with the most
// distinct terms, the first such, or nil when none of terms occurs there. A phrase's terms count
// only where the whole phrase occurs, and the whole occurrence is highlighted.
func MakeSnippet(r io.Reader, terms []string, phrases [][]string) (*Snippet, error) {
	buf, err := io.ReadAll(io.LimitReader(r, SnippetScan))
	if err != nil {
		return nil, err
	}
	// Cut a character split by the limit.
	if len(buf) == SnippetScan {
		i := len(buf) - 1
		for i > 0 && !utf8.RuneStart(buf[i]) {
			i--
		}
		if !utf8.FullRune(buf[i:]) {
			buf = buf[:i]
		}
	}
	return snippetOf(string(buf), terms, phrases), nil
}

func snippetOf(text string, terms []string, phrases [][]string) *Snippet {
	// A term is a phrase of one; a phrase occurrence is one hit from its first term's start to
	// its last term's end.
	var keys [][]string
	seen := map[string]bool{}
	for _, k := range append(phraseList(terms), phrases...) {
		if id := strings.Join(k, " "); !seen[id] {
			seen[id] = true
			keys = append(keys, k)
		}
	}
	// A window starts at a hit and counts the distinct keys of the hits wholly inside its first
	// span bytes. Hits arrive in order of end, so once one ends past that span the window is
	// final, and it and older hits starting before it are needed by no window still open.
	const span = snippetBytes - snippetLead
	recent := make([]hit, 0, 64)
	var best hit
	bestN := 0
	counted, round := make([]int, len(keys)), 0
	score := func(w hit) {
		round++
		n := 0
		for _, h := range recent {
			if h.start >= w.start && h.end <= w.start+span && counted[h.key] != round {
				counted[h.key] = round
				n++
			}
		}
		if n > bestN || n == bestN && w.start < best.start {
			best, bestN = w, n
		}
	}
	phraseRuns(text, keys, func(key, start, end int) bool {
		if len(recent) == cap(recent) {
			closed := func(w hit) bool { return w.start+span < end }
			for _, w := range recent {
				if closed(w) {
					score(w)
				}
			}
			recent = slices.DeleteFunc(recent, closed)
			recent = slices.Grow(recent, len(recent)) // keep at most half full
		}
		recent = append(recent, hit{key, start, end})
		return true
	})
	for _, w := range recent {
		score(w)
	}
	if bestN == 0 {
		return nil
	}
	start := backToBoundary(text, max(0, best.start-snippetLead))
	end := backToBoundary(text, min(len(text), start+snippetBytes))
	// Prefer to cut at a space on both sides, unless that loses the match.
	if start > 0 {
		for i := start; i < best.start; i++ {
			if text[i] == ' ' || text[i] == '\n' || text[i] == '\t' {
				start = i + 1
				break
			}
		}
	}
	if end < len(text) {
		for i := end - 1; i > best.end; i-- {
			if text[i] == ' ' || text[i] == '\n' || text[i] == '\t' {
				end = i
				break
			}
		}
	}
	var hits []hit
	phraseRuns(text, keys, func(key, s, e int) bool {
		if s >= start && e <= end {
			hits = append(hits, hit{key, s, e})
		}
		return e <= end
	})
	slices.SortStableFunc(hits, func(a, b hit) int { return cmp.Compare(a.start, b.start) })
	return render(text, start, end, hits)
}

func phraseList(terms []string) [][]string {
	out := make([][]string, len(terms))
	for i, t := range terms {
		out[i] = []string{t}
	}
	return out
}

// backToBoundary returns the start of the character holding byte i, or i when it starts one;
// an invalid byte is a character of its own, as ranging over text decodes it.
func backToBoundary(text string, i int) int {
	for j := i - 1; j >= 0 && j > i-utf8.UTFMax; j-- {
		if utf8.RuneStart(text[j]) {
			if _, n := utf8.DecodeRuneInString(text[j:]); j+n > i {
				return j
			}
			break
		}
	}
	return i
}

// render writes text[start:end] with control characters and runs of white space as one space,
// and maps every hit wholly inside it to UTF-16 offsets.
func render(text string, start, end int, hits []hit) *Snippet {
	out := &Snippet{CutStart: start > 0, CutEnd: end < len(text), Highlights: [][2]int{}}
	var b []rune
	u16 := 0
	starts, ends := map[int]int{}, map[int]int{}
	for _, h := range hits {
		if h.start >= start && h.end <= end {
			starts[h.start], ends[h.end] = -1, -1
		}
	}
	space := false
	for i, r := range text[start:end] {
		pos := start + i
		if _, ok := ends[pos]; ok {
			ends[pos] = u16
		}
		if _, ok := starts[pos]; ok {
			starts[pos] = u16
		}
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == utf8.RuneError {
			if space || len(b) == 0 {
				continue
			}
			r, space = ' ', true
		} else {
			space = false
		}
		b = append(b, r)
		u16 += utf16.RuneLen(r)
	}
	if _, ok := ends[end]; ok {
		ends[end] = u16
	}
	for len(b) > 0 && b[len(b)-1] == ' ' {
		b = b[:len(b)-1]
		u16--
	}
	out.Text = string(b)
	for _, h := range hits {
		s, okS := starts[h.start]
		e, okE := ends[h.end]
		if !okS || !okE || s < 0 || e <= s || e > u16 {
			continue
		}
		// Hits come in text order; overlapping ones (CJK bigrams) merge into one range.
		if n := len(out.Highlights); n > 0 && s <= out.Highlights[n-1][1] {
			out.Highlights[n-1][1] = max(out.Highlights[n-1][1], e)
			continue
		}
		out.Highlights = append(out.Highlights, [2]int{s, e})
	}
	return out
}
