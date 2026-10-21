package search

import (
	"io"
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
	term       string
	start, end int
}

// MakeSnippet reads at most SnippetScan bytes of text from r and returns the window with the most
// distinct terms, the first such, or nil when none of terms occurs there.
func MakeSnippet(r io.Reader, terms []string) (*Snippet, error) {
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
	return snippetOf(string(buf), terms), nil
}

func snippetOf(text string, terms []string) *Snippet {
	want := map[string]bool{}
	for _, t := range terms {
		want[t] = true
	}
	var hits []hit
	scan(text, func(term string, start, end int) bool {
		if want[term] {
			hits = append(hits, hit{term, start, end})
		}
		return true
	})
	if len(hits) == 0 {
		return nil
	}
	// The window starting at the hit that sees the most distinct terms within snippetBytes.
	best, bestN := 0, 0
	for i := range hits {
		seen := map[string]bool{}
		for j := i; j < len(hits) && hits[j].end-hits[i].start <= snippetBytes-snippetLead; j++ {
			seen[hits[j].term] = true
		}
		if len(seen) > bestN {
			best, bestN = i, len(seen)
		}
		if bestN == len(want) {
			break
		}
	}
	start := backToBoundary(text, max(0, hits[best].start-snippetLead))
	end := min(len(text), start+snippetBytes)
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	// Prefer to cut at a space on both sides, unless that loses the match.
	if start > 0 {
		for i := start; i < hits[best].start; i++ {
			if text[i] == ' ' || text[i] == '\n' || text[i] == '\t' {
				start = i + 1
				break
			}
		}
	}
	if end < len(text) {
		for i := end - 1; i > hits[best].end; i-- {
			if text[i] == ' ' || text[i] == '\n' || text[i] == '\t' {
				end = i
				break
			}
		}
	}
	return render(text, start, end, hits)
}

func backToBoundary(text string, i int) int {
	for i > 0 && !utf8.RuneStart(text[i]) {
		i--
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
