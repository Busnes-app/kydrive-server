package objects_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/objects"
)

func TestScopedStoresAndTrees(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	all := []objects.Store{objects.Dir(filepath.Join(root, "a")), objects.Dir(filepath.Join(root, "b"))}
	st := objects.ScopedStore{Select: func(org string) (objects.Store, error) {
		if org == "a" {
			return all[0], nil
		}
		if org == "b" {
			return all[1], nil
		}
		return nil, fs.ErrPermission
	}, All: func(context.Context) ([]objects.Store, error) { return all, nil }}
	if _, err := st.Create(ctx); !errors.Is(err, fs.ErrPermission) {
		t.Fatal("unscoped create", err)
	}
	for _, org := range []string{"a", "b"} {
		contract(t, objects.Scope(st, org))
	}
	a, err := objects.Scope(st, "a").Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.Write([]byte("a"))
	if err := a.Publish("same"); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Scope(st, "b").Open(ctx, "same"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("cross-org open", err)
	}
	if err := st.Remove(ctx, "same"); err != nil {
		t.Fatal(err)
	}
	trees := []objects.Tree{objects.DirTree(filepath.Join(root, "ta")), objects.DirTree(filepath.Join(root, "tb"))}
	tr := objects.ScopedTree{Select: func(org string) (objects.Tree, error) {
		if org == "a" {
			return trees[0], nil
		}
		return trees[1], nil
	}, All: func(context.Context) ([]objects.Tree, error) { return trees, nil }}
	treeContract(t, objects.ScopeTree(tr, "a"))
	treeContract(t, objects.ScopeTree(tr, "b"))
	draft, err := objects.Scope(tr.Sub("session"), "a").Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	draft.Write([]byte("segment"))
	if err := draft.Publish("segment"); err != nil {
		t.Fatal(err)
	}
	count := 0
	if err := tr.List(ctx, func(string, time.Time) error { count++; return nil }); err != nil || count == 0 {
		t.Fatal(count, err)
	}
	if err := tr.RemoveAll(ctx, "session"); err != nil {
		t.Fatal(err)
	}
}
func TestValidateS3RejectsExternalHTTPAndSecretURLs(t *testing.T) {
	cfg := objects.S3Config{Endpoint: "https://objects.e2e.test", Bucket: "drive", Region: "us-east-1", AccessKey: "key", SecretKey: "secret"}
	for _, endpoint := range []string{"http://127.attacker.test", "http://objects.e2e.test", "https://objects.e2e.test#frag", "https://objects.e2e.test?", "https://user:secret@objects.e2e.test"} {
		cfg.Endpoint = endpoint
		if err := objects.ValidateS3(cfg); err == nil {
			t.Errorf("accepted %s", endpoint)
		}
	}
}
