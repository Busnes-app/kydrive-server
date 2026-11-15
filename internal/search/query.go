package search

import (
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// MinPrefixRunes is the shortest prefix `*` expands; a shorter one is a whole word.
	MinPrefixRunes = 3
	// MaxPrefixWords is how many words one prefix expands to: words the searched documents hold,
	// those in most of them first.
	MaxPrefixWords = 100
)

// Query is a parsed content query. A document must hold every word, a word starting with each
// prefix, and each phrase's terms in order (Phrases are checked against the text by the caller,
// HasPhrases; the index only knows the terms are there).
type Query struct {
	Words    []string
	Prefixes []string
	Phrases  [][]string
}

// ParseQuery reads q: a run of letters or digits followed by `*` is a prefix when it has at
// least MinPrefixRunes runes and is not CJK; text in double quotes is a phrase (one term in
// quotes is a word; an unclosed quote is ignored). Everything else is words. At most
// MaxQueryTerms distinct terms are kept, first come first; a phrase that does not fit is dropped.
func ParseQuery(q string) Query {
	var out Query
	used := map[string]bool{}
	fits := func(terms ...string) bool {
		n := len(used)
		for _, t := range terms {
			if !used[t] {
				n++
			}
		}
		if n > MaxQueryTerms {
			return false
		}
		for _, t := range terms {
			used[t] = true
		}
		return true
	}
	parts := strings.Split(q, `"`)
	for i, part := range parts {
		if i%2 == 1 && i < len(parts)-1 {
			var phrase []string
			scan(part, func(term string, _, _ int) bool { phrase = append(phrase, term); return len(phrase) <= MaxQueryTerms })
			switch {
			case len(phrase) == 1 && !used[phrase[0]] && fits(phrase...):
				out.Words = append(out.Words, phrase[0])
			case len(phrase) > 1 && !slices.ContainsFunc(out.Phrases, func(p []string) bool { return slices.Equal(p, phrase) }) && fits(phrase...):
				out.Phrases = append(out.Phrases, phrase)
			}
			continue
		}
		scan(part, func(term string, _, end int) bool {
			prefix := end < len(part) && part[end] == '*' && utf8.RuneCountInString(term) >= MinPrefixRunes && !cjk([]rune(term)[0])
			key := term
			if prefix {
				key = term + "*" // a prefix and the same whole word are different terms
			}
			if used[key] || !fits(key) {
				return true
			}
			if prefix {
				out.Prefixes = append(out.Prefixes, term)
			} else {
				out.Words = append(out.Words, term)
			}
			return true
		})
	}
	return out
}

// Empty reports whether q has nothing to match.
func (q Query) Empty() bool { return len(q.Words)+len(q.Prefixes)+len(q.Phrases) == 0 }

// HasPhrases reports whether text holds every phrase, each as consecutive terms (white space,
// punctuation, line and page breaks between them do not count).
func HasPhrases(text string, phrases [][]string) bool {
	found := make([]bool, len(phrases))
	left := len(phrases)
	phraseRuns(text, phrases, func(p, _, _ int) bool {
		if !found[p] {
			found[p] = true
			left--
		}
		return left > 0
	})
	return left == 0
}

// phraseRuns calls at for every occurrence of phrase p (one or more terms) in text, in order of
// end, with the byte range [start, end) from its first term's start to its last term's end; at
// returns false to stop.
func phraseRuns(text string, phrases [][]string, at func(p, start, end int) bool) {
	longest := 0
	for _, p := range phrases {
		longest = max(longest, len(p))
	}
	if longest == 0 {
		return
	}
	type token struct {
		term  string
		start int
	}
	ring := make([]token, longest) // the last tokens seen, newest at n-1 (mod longest)
	n := 0
	single := map[string][]int{} // one-term phrases, looked up rather than compared
	for i, p := range phrases {
		if len(p) == 1 {
			single[p[0]] = append(single[p[0]], i)
		}
	}
	scan(text, func(term string, start, end int) bool {
		ring[n%longest] = token{term, start}
		n++
		for _, i := range single[term] {
			if !at(i, start, end) {
				return false
			}
		}
		for i, p := range phrases {
			if len(p) == 1 || n < len(p) {
				continue
			}
			ok := true
			for j := range p {
				if ring[(n-len(p)+j)%longest].term != p[j] {
					ok = false
					break
				}
			}
			if ok && !at(i, ring[(n-len(p))%longest].start, end) {
				return false
			}
		}
		return true
	})
}
