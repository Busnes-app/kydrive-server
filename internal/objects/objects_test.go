package objects_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/objects"
)

// contract is what every Store promises; each backend's test runs it.
func contract(t *testing.T, st objects.Store) {
	ctx := context.Background()
	read := func(name string) (string, error) {
		o, err := st.Open(ctx, name)
		if err != nil {
			return "", err
		}
		defer o.Close()
		b, err := io.ReadAll(io.NewSectionReader(o, 0, o.Size()))
		return string(b), err
	}
	names := func() []string {
		var out []string
		if err := st.List(ctx, func(name string, _ time.Time) error { out = append(out, name); return nil }); err != nil {
			t.Fatal(err)
		}
		slices.Sort(out)
		return out
	}

	if _, err := read("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a missing object: %v", err)
	}
	w, err := st.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, "hello")
	if _, err := read("a"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a draft is visible before Publish: %v", err)
	}
	if err := w.Publish("a"); err != nil {
		t.Fatal(err)
	}
	if got, err := read("a"); err != nil || got != "hello" {
		t.Fatalf("published %q, %v", got, err)
	}
	if err := w.Discard(); err != nil {
		t.Fatal("Discard after Publish:", err)
	}
	if got, _ := read("a"); got != "hello" {
		t.Fatal("Discard after Publish removed the object")
	}

	w, _ = st.Create(ctx)
	io.WriteString(w, "replaced")
	if err := w.Publish("a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := read("a"); got != "replaced" {
		t.Fatalf("Publish over an object left %q", got)
	}

	w, _ = st.Create(ctx)
	io.WriteString(w, "dropped")
	if err := w.Discard(); err != nil {
		t.Fatal(err)
	}
	if got := names(); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("after a discarded draft: %v", got)
	}

	for _, bad := range []string{"", ".hidden", "a/b", `a\b`, ".."} {
		w, _ := st.Create(ctx)
		if err := w.Publish(bad); err == nil {
			t.Errorf("published under %q", bad)
		}
		if _, err := st.Open(ctx, bad); err == nil {
			t.Errorf("opened %q", bad)
		}
	}
	if got := names(); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("refused names left %v", got)
	}

	if err := st.Remove(ctx, "a", "never-there"); err != nil {
		t.Fatal(err)
	}
	if _, err := read("a"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a removed object: %v", err)
	}
}

func TestDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	st := objects.Dir(root)
	if err := st.List(context.Background(), func(string, time.Time) error { return errors.New("called") }); err != nil {
		t.Fatal("a store not yet written to lists nothing:", err)
	}
	contract(t, st)
	if st.Root() != root {
		t.Fatalf("Root %q", st.Root())
	}
}

// A draft is a dot-named file in the store's directory until Publish, as KyDrive's staging files
// always were, so the orphan sweep can find what a crash left; a published object is the file
// itself, owner-only.
func TestDirFilesAsBefore(t *testing.T) {
	root := t.TempDir()
	st := objects.Dir(root)
	w, err := st.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, "x")
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ".upload-") {
		t.Fatalf("draft files: %v", entries)
	}
	if err := w.Publish("name"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "name"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("published file: %v %v", info, err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("files after Publish: %v", entries)
	}
}

// treeContract is what every Tree promises; each backend's test runs it.
func treeContract(t *testing.T, tr objects.Tree) {
	ctx := context.Background()
	subs := func() []string {
		var out []string
		if err := tr.List(ctx, func(name string, _ time.Time) error { out = append(out, name); return nil }); err != nil {
			t.Fatal(err)
		}
		slices.Sort(out)
		return out
	}
	if got := subs(); len(got) != 0 {
		t.Fatalf("a new tree lists %v", got)
	}
	for _, name := range []string{"one", "two"} {
		w, err := tr.Sub(name).Create(ctx)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, name)
		if err := w.Publish("segment"); err != nil {
			t.Fatal(err)
		}
	}
	contract(t, tr.Sub("three"))
	// A Sub with no objects may vanish from List (S3 has no empty directories), so the
	// contract, which empties "three", is followed by one more object there.
	w, err := tr.Sub("three").Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Publish("segment"); err != nil {
		t.Fatal(err)
	}
	if got := subs(); !slices.Equal(got, []string{"one", "three", "two"}) {
		t.Fatalf("subs %v", got)
	}
	if err := tr.RemoveAll(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if err := tr.RemoveAll(ctx, "never-there"); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Sub("one").Open(ctx, "segment"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a removed sub's object: %v", err)
	}
	if o, err := tr.Sub("two").Open(ctx, "segment"); err != nil {
		t.Fatal("RemoveAll removed another sub:", err)
	} else {
		o.Close()
	}
	for _, bad := range []string{"", ".x", "a/b", ".."} {
		if err := tr.RemoveAll(ctx, bad); err == nil {
			t.Errorf("RemoveAll(%q)", bad)
		}
		if _, err := tr.Sub(bad).Create(ctx); err == nil {
			t.Errorf("Sub(%q) took a draft", bad)
		}
	}
}

// A DirTree's subs are directories of root, as resumable-upload staging always was.
func TestDirTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "staging")
	treeContract(t, objects.DirTree(root))
	if _, err := os.Stat(filepath.Join(root, "two", "segment")); err != nil {
		t.Fatal("not staging/<sub>/<name>:", err)
	}
}
