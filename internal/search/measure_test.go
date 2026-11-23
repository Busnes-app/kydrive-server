package search_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/search"
)

// TestMeasureSearch measures index growth and query latency on a generated corpus. It runs only
// with KYDRIVE_SEARCH_MEASURE=<documents> (e.g. 15000) and needs /usr/share/dict/words:
//
//	KYDRIVE_SEARCH_MEASURE=15000 go test ./internal/search -run TestMeasureSearch -v -timeout 1h
//
// Each document is 1 500 words drawn Zipf-distributed (s 1.1) from the dictionary in a fixed
// shuffled order; 3% hold "kernel module" and 5% the two words apart.
func TestMeasureSearch(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("KYDRIVE_SEARCH_MEASURE"))
	if n <= 0 {
		t.Skip("set KYDRIVE_SEARCH_MEASURE to a document count")
	}
	f, err := os.Open("/usr/share/dict/words")
	if err != nil {
		t.Skip(err)
	}
	var dict []string
	seen := map[string]bool{}
	for sc := bufio.NewScanner(f); sc.Scan(); {
		if w := strings.ToLower(sc.Text()); !seen[w] && !strings.ContainsAny(w, "'- ./&0123456789") {
			seen[w] = true
			dict = append(dict, w)
		}
	}
	f.Close()
	rng := rand.New(rand.NewPCG(1, 2))
	rng.Shuffle(len(dict), func(i, j int) { dict[i], dict[j] = dict[j], dict[i] })
	zipf := rand.NewZipf(rand.New(rand.NewPCG(3, 4)), 1.1, 1, uint64(len(dict)-1))
	text := func(i int) string {
		var b strings.Builder
		for j := range 1500 {
			b.WriteString(dict[zipf.Uint64()])
			b.WriteByte(' ')
			if j == 700 {
				switch {
				case i%100 < 3:
					b.WriteString("kernel module ")
				case i%100 < 8:
					b.WriteString("kernel and the module ")
				}
			}
		}
		return b.String()
	}
	ctx, dir, key := context.Background(), t.TempDir(), bytes.Repeat([]byte{7}, 32)
	x, err := search.Open(ctx, dir, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = x.BuildVocab(true, time.Now()).Done(); err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{}
	blobOf := map[string]string{}
	cache := t.TempDir()
	var indexCPU time.Duration
	for i := range n {
		body := text(i)
		blob := fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
		ref := x.Ref(blob, "text")
		refs[ref], blobOf[ref] = true, blob
		start := time.Now()
		counts, length := search.Count(body)
		if err = x.Put(ctx, ref, counts, length, blob, time.Now()); err != nil {
			t.Fatal(err)
		}
		indexCPU += time.Since(start)
		if err = drive.WriteThumb(objects.Dir(cache), key, blob, append([]byte{0}, body...)); err != nil {
			t.Fatal(err)
		}
	}
	if err = x.SaveVocab(true); err != nil {
		t.Fatal(err)
	}
	if err = x.Close(); err != nil {
		t.Fatal(err)
	}
	size := func(name string) int64 {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return 0
		}
		return st.Size()
	}
	t.Logf("documents %d, dictionary %d words, index CPU %.2f ms/document", n, len(dict), float64(indexCPU.Microseconds())/1000/float64(n))
	t.Logf("index.db %.1f MB (+wal %.1f MB), vocab %.2f MB sealed (%.1f%% of index.db)", mb(size(search.File)), mb(size(search.File+"-wal")), mb(size(search.VocabFile)), 100*float64(size(search.VocabFile))/float64(size(search.File)+size(search.File+"-wal")))

	// Reopen: the vocabulary loads from its sealed file. Heap is measured around the load.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	x, err = search.Open(ctx, dir, key)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	load := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("open with vocabulary %.0f ms, heap +%.1f MB", float64(load.Microseconds())/1000, mb(int64(after.HeapAlloc)-int64(before.HeapAlloc)))

	time3 := func(q string, refs map[string]bool) (time.Duration, search.Matched) {
		var best time.Duration
		var m search.Matched
		for range 3 {
			start := time.Now()
			if m, err = x.Match(ctx, search.ParseQuery(q), refs); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); best == 0 || d < best {
				best = d
			}
		}
		return best, m
	}
	// A caller who reads 1% of the documents still pays for every word sharing the prefix.
	few := map[string]bool{}
	for ref := range refs {
		if len(few) < max(n/100, 1) {
			few[ref] = true
		}
	}
	for _, scope := range []struct {
		name string
		refs map[string]bool
	}{{"all", refs}, {"1%", few}} {
		for _, q := range []string{dict[0], dict[5000], dict[200000], "kernel", "con*", "conf*", "pre*", "kern*", "zyx*", "con* pre* sub* tri*", `"kernel module"`} {
			d, m := time3(q, scope.refs)
			t.Logf("%-4s %-22q %7.1f ms  %6d matches  %3d expanded  capped %v", scope.name, q, float64(d.Microseconds())/1000, len(m.Scores), len(m.Expanded), m.Capped)
		}
	}
	// Phrase check: the best 200 candidates, decrypting each one's sealed text as the API does.
	_, m := time3(`"kernel module"`, refs)
	cand := make([]string, 0, len(m.Scores))
	for ref := range m.Scores {
		cand = append(cand, ref)
	}
	slices.SortFunc(cand, func(a, b string) int { return strings.Compare(a, b) })
	cand = cand[:min(len(cand), 200)]
	phrases := search.ParseQuery(`"kernel module"`).Phrases
	start = time.Now()
	hits := 0
	for _, ref := range cand {
		raw, err := drive.ReadThumb(objects.Dir(cache), key, blobOf[ref])
		if err != nil {
			t.Fatal(err)
		}
		if search.HasPhrases(string(raw[1:]), phrases) {
			hits++
		}
	}
	d := time.Since(start)
	t.Logf("phrase check %d candidates: %d true, %.1f ms (%.2f ms each, decryption included)", len(cand), hits, float64(d.Microseconds())/1000, float64(d.Microseconds())/1000/float64(len(cand)))
}

func mb(n int64) float64 { return float64(n) / (1 << 20) }
