// Package search is KyDrive's content search index: a blind index of keyed token hashes in its
// own SQLite file, plus the tokenizer and the snippet maker that work on plaintext in memory
// only. Nothing it writes holds a word, a name or a blob ID.
package search

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// TokenizerVersion is stored in the index; changing how text becomes tokens must bump it, which
// rebuilds the index.
const TokenizerVersion = 1

const (
	// MaxQueryTerms is how many distinct terms a query matches; later ones are ignored.
	MaxQueryTerms = 8
	// MaxDocTerms is how many distinct terms one document indexes, first seen first.
	MaxDocTerms = 50000
	// maxTF caps a term's stored frequency.
	maxTF = 255
	// maxTokenBytes drops longer tokens (hashes, base64), which nobody searches for.
	maxTokenBytes = 64
)

// folded caches the folding of non-ASCII runes; it is bounded by the size of Unicode.
var folded sync.Map // rune -> string

// fold returns r compatibility-decomposed, without combining marks and lower-cased, with ß and
// final sigma folded: "É" -> "e", "ﬁ" -> "fi", "ß" -> "ss", "Ａ" -> "a".
func fold(r rune) string {
	if r < utf8.RuneSelf {
		if 'A' <= r && r <= 'Z' {
			r += 'a' - 'A'
		}
		return string(r)
	}
	if cjk(r) {
		return string(r) // decomposing would split Hangul into jamo and kana from their marks
	}
	if v, ok := folded.Load(r); ok {
		return v.(string)
	}
	var b strings.Builder
	for _, d := range norm.NFKD.String(string(r)) {
		if unicode.Is(unicode.Mn, d) {
			continue
		}
		switch d = unicode.ToLower(d); d {
		case 'ß', 'ẞ':
			b.WriteString("ss")
		case 'ς':
			b.WriteRune('σ')
		default:
			b.WriteRune(d)
		}
	}
	folded.Store(r, b.String())
	return b.String()
}

// cjk scripts are written without spaces; their runs index as overlapping bigrams.
func cjk(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

// scan calls emit for every token of text with its byte range in text; emit returns false to
// stop. A token is a run of letters and digits after folding, 2 runes to 64 bytes, or one or two
// adjacent CJK characters (a lone one is a token, longer runs give every bigram).
func scan(text string, emit func(term string, start, end int) bool) {
	var word strings.Builder
	wordStart, wordEnd, wordRunes := 0, 0, 0
	type cjkRune struct {
		r          rune
		start, end int
	}
	var run []cjkRune
	flushWord := func() bool {
		ok := true
		if wordRunes >= 2 && word.Len() <= maxTokenBytes {
			ok = emit(word.String(), wordStart, wordEnd)
		}
		word.Reset()
		wordRunes = 0
		return ok
	}
	flushCJK := func() bool {
		ok := true
		switch {
		case len(run) == 1:
			ok = emit(string(run[0].r), run[0].start, run[0].end)
		case len(run) > 1:
			for i := 0; ok && i+1 < len(run); i++ {
				ok = emit(string([]rune{run[i].r, run[i+1].r}), run[i].start, run[i+1].end)
			}
		}
		run = run[:0]
		return ok
	}
	for i, r := range text {
		end := i + utf8.RuneLen(r)
		if r == utf8.RuneError {
			end = i + 1
		}
		for _, f := range fold(r) {
			switch {
			case cjk(f):
				if !flushWord() {
					return
				}
				run = append(run, cjkRune{f, i, end})
			case wordRune(f):
				if !flushCJK() {
					return
				}
				if wordRunes == 0 {
					wordStart = i
				}
				word.WriteRune(f)
				wordRunes++
				wordEnd = end
			default:
				if !flushWord() || !flushCJK() {
					return
				}
			}
		}
		if unicode.Is(unicode.Mn, r) && wordRunes > 0 {
			wordEnd = end // a combining mark belongs to the word it follows
		}
	}
	if flushWord() {
		flushCJK()
	}
}

// Terms returns the distinct terms of query q in order, at most MaxQueryTerms.
func Terms(q string) []string {
	seen := map[string]bool{}
	var out []string
	scan(q, func(term string, _, _ int) bool {
		if !seen[term] {
			seen[term] = true
			out = append(out, term)
		}
		return len(out) < MaxQueryTerms
	})
	return out
}

// Count returns how often each term occurs in text (capped at 255; at most MaxDocTerms distinct,
// first seen first) and how many tokens text has.
func Count(text string) (map[string]int, int) {
	counts := map[string]int{}
	length := 0
	scan(text, func(term string, _, _ int) bool {
		length++
		if n, ok := counts[term]; ok {
			counts[term] = min(n+1, maxTF)
		} else if len(counts) < MaxDocTerms {
			counts[term] = 1
		}
		return true
	})
	return counts, length
}
