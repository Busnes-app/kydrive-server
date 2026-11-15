package search

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

// VocabFile is the sealed vocabulary's name inside the index directory.
const VocabFile = "vocab"

// vocabDay is how often the vocabulary is rebuilt from scratch, dropping words no document
// holds any more.
const vocabDay = 24 * time.Hour

// vocab is the set of distinct indexed words (MinPrefixRunes or more, not CJK), for prefix
// expansion. It lives in memory and on disk only sealed. A word stays until the daily rebuild;
// postings still decide every match.
type vocab struct {
	mu    sync.Mutex
	words []string            // sorted
	added map[string]struct{} // not yet merged into words
	dirty bool
	saved time.Time
	stamp int64 // every document updated before this (Unix) is in it
	built int64 // last full build (Unix), 0 for none
}

func newVocab() *vocab { return &vocab{added: map[string]struct{}{}} }

// learn adds a document's terms.
func (v *vocab) learn(counts map[string]int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for t := range counts {
		if r, _ := utf8.DecodeRuneInString(t); !cjk(r) && utf8.RuneCountInString(t) >= MinPrefixRunes {
			v.added[t] = struct{}{}
			v.dirty = true
		}
	}
}

// merge folds added into the sorted words; the caller holds mu.
func (v *vocab) merge() {
	if len(v.added) == 0 {
		return
	}
	fresh := make([]string, 0, len(v.added))
	for t := range v.added {
		if _, ok := slices.BinarySearch(v.words, t); !ok {
			fresh = append(fresh, t)
		}
	}
	slices.Sort(fresh)
	words := make([]string, 0, len(v.words)+len(fresh))
	i := 0
	for _, t := range fresh {
		for ; i < len(v.words) && v.words[i] < t; i++ {
			words = append(words, v.words[i])
		}
		words = append(words, t)
	}
	v.words = append(words, v.words[i:]...)
	clear(v.added)
}

// expand returns every word starting with prefix. They come from every user's files: the
// caller keeps only those its own documents hold (Match), so nobody else's words shape a result.
func (v *vocab) expand(prefix string) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.merge()
	lo, _ := slices.BinarySearch(v.words, prefix)
	hi := lo
	for hi < len(v.words) && strings.HasPrefix(v.words[hi], prefix) {
		hi++
	}
	return slices.Clone(v.words[lo:hi])
}

// The sealed file is AES-256-GCM (crypto.EncryptAESGCMAAD) under the kydrive:search-vocab key,
// bound to the index check (key and TokenizerVersion), of: uvarint stamp, built, word count,
// then per word uvarint length and the word.
func (v *vocab) encode() []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.merge()
	buf := binary.AppendUvarint(nil, uint64(v.stamp))
	buf = binary.AppendUvarint(buf, uint64(v.built))
	buf = binary.AppendUvarint(buf, uint64(len(v.words)))
	for _, w := range v.words {
		buf = binary.AppendUvarint(buf, uint64(len(w)))
		buf = append(buf, w...)
	}
	return buf
}

var errVocab = errors.New("search: damaged vocabulary")

func decodeVocab(buf []byte) (*vocab, error) {
	next := func() (uint64, error) {
		n, k := binary.Uvarint(buf)
		if k <= 0 {
			return 0, errVocab
		}
		buf = buf[k:]
		return n, nil
	}
	v := newVocab()
	var head [3]uint64
	for i := range head {
		n, err := next()
		if err != nil {
			return nil, err
		}
		head[i] = n
	}
	v.stamp, v.built = int64(head[0]), int64(head[1])
	if head[2] > uint64(len(buf)) {
		return nil, errVocab
	}
	v.words = make([]string, 0, head[2])
	for range head[2] {
		l, err := next()
		if err != nil || l > uint64(len(buf)) {
			return nil, errVocab
		}
		w := string(buf[:l])
		buf = buf[l:]
		if len(v.words) > 0 && w <= v.words[len(v.words)-1] {
			return nil, errVocab
		}
		v.words = append(v.words, w)
	}
	if len(buf) != 0 {
		return nil, errVocab
	}
	return v, nil
}

// loadVocab opens the sealed vocabulary; a missing, damaged or foreign one is empty, and the
// worker rebuilds it from the sealed text cache (VocabDue).
func (x *Index) loadVocab() *vocab {
	raw, err := os.ReadFile(filepath.Join(x.dir, VocabFile))
	if err != nil {
		return newVocab()
	}
	plain, err := crypto.DecryptAESGCMAAD(string(raw), x.vkey, x.vaad)
	if err != nil {
		return newVocab()
	}
	v, err := decodeVocab(plain)
	if err != nil {
		return newVocab()
	}
	return v
}

// SaveVocab seals the vocabulary to disk when it changed, at most once a minute unless now is
// set; Close saves it too. The file is replaced atomically.
func (x *Index) SaveVocab(now bool) error {
	v := x.vocabNow()
	v.mu.Lock()
	due := v.dirty && (now || time.Since(v.saved) >= time.Minute)
	if due {
		v.dirty, v.saved, v.stamp = false, time.Now(), time.Now().Unix()
	}
	v.mu.Unlock()
	if !due {
		return nil
	}
	sealed, err := crypto.EncryptAESGCMAAD(v.encode(), x.vkey, x.vaad)
	if err == nil {
		err = writeAtomic(filepath.Join(x.dir, VocabFile), []byte(sealed))
	}
	if err != nil {
		v.mu.Lock()
		v.dirty = true
		v.mu.Unlock()
	}
	return err
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".vocab-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}

func (x *Index) vocabNow() *vocab {
	x.vmu.Lock()
	defer x.vmu.Unlock()
	return x.vocab
}

// VocabDue says which documents' words the vocabulary may lack: those updated at or after
// since. fresh means a full rebuild is due (none yet, or the last is a day old), since is then 0.
func (x *Index) VocabDue(now time.Time) (since int64, fresh bool) {
	v := x.vocabNow()
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.built == 0 || now.Sub(time.Unix(v.built, 0)) >= vocabDay {
		return 0, true
	}
	return v.stamp, false
}

// VocabBuild collects documents' text for the vocabulary (VocabDue); Done applies it.
type VocabBuild struct {
	x     *Index
	v     *vocab
	fresh bool
	start int64
}

// BuildVocab starts a vocabulary update: a fresh one replaces the vocabulary, otherwise the
// words are added to it.
func (x *Index) BuildVocab(fresh bool, now time.Time) *VocabBuild {
	return &VocabBuild{x: x, v: newVocab(), fresh: fresh, start: now.Unix()}
}

// Add adds one document's text.
func (b *VocabBuild) Add(text string) {
	counts, _ := Count(text)
	b.v.learn(counts)
}

// Done applies the update and seals the result. Words indexed meanwhile (Put, Learn) are kept.
func (b *VocabBuild) Done() error {
	x := b.x
	x.vmu.Lock()
	old := x.vocab
	if b.fresh {
		// Put and Learn run on the worker, as builds do, so nothing was added to old meanwhile.
		b.v.built = b.start
		x.vocab = b.v
	}
	x.vmu.Unlock()
	if !b.fresh {
		b.v.mu.Lock()
		added := b.v.added
		b.v.mu.Unlock()
		old.mu.Lock()
		for t := range added {
			old.added[t] = struct{}{}
		}
		old.mu.Unlock()
	}
	cur := x.vocabNow()
	cur.mu.Lock()
	cur.dirty = true
	cur.mu.Unlock()
	return x.SaveVocab(true)
}

// Learn adds a document's words to the vocabulary without touching its postings (its text
// was extracted again after the text cache was lost).
func (x *Index) Learn(text string) {
	counts, _ := Count(text)
	x.vocabNow().learn(counts)
}
