package backup

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
)

// fakeRepo models restic's key handling: each key has a password, a command opens with the
// first key matching RESTIC_PASSWORD, and the key in use cannot be removed.
type fakeRepo struct {
	keys     map[string]string // id -> hex password
	next     int
	failNext string // fail the next command with this verb ("add", "remove") once
	calls    []string
}

func (f *fakeRepo) run(_ context.Context, _ Repository, password []byte, _ string, stdin io.Reader, args ...string) ([]byte, error) {
	pw := hex.EncodeToString(password)
	verb := strings.Join(args[:min(2, len(args))], " ")
	f.calls = append(f.calls, verb)
	// No keys is no repository: only init succeeds.
	if len(f.keys) == 0 {
		if verb != "init" {
			return nil, errors.New("unable to open config file: Is there a repository at the following location?")
		}
		f.keys[fmt.Sprintf("%064x", 1)] = pw
		return nil, nil
	}
	if verb == "init" {
		return nil, errors.New("config file already exists")
	}
	current := ""
	for id, p := range f.keys {
		if p == pw {
			current = id
		}
	}
	if current == "" {
		return nil, fmt.Errorf("restic %s failed: %w", args[0], errWrongPassword)
	}
	if f.failNext != "" && len(args) > 1 && args[1] == f.failNext {
		f.failNext = ""
		return nil, errors.New("killed")
	}
	switch verb {
	case "cat config":
		return []byte("{}"), nil
	case "key list":
		type key struct {
			Current bool   `json:"current"`
			ID      string `json:"id"`
		}
		var out []key
		for _, id := range slices.Sorted(maps.Keys(f.keys)) {
			out = append(out, key{id == current, id})
		}
		return json.Marshal(out)
	case "key add":
		b, _ := io.ReadAll(stdin)
		f.next++
		f.keys[fmt.Sprintf("%064x", f.next+100)] = string(bytes.TrimSpace(b))
		return nil, nil
	case "key remove":
		if args[2] == current {
			return nil, errors.New("refusing to remove key currently used")
		}
		if _, ok := f.keys[args[2]]; !ok {
			return nil, errors.New("no matching ID found")
		}
		delete(f.keys, args[2])
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected restic %v", args)
}

func fakeRepoWith(t *testing.T, passwords ...[]byte) *fakeRepo {
	t.Helper()
	f := &fakeRepo{keys: map[string]string{}}
	for i, p := range passwords {
		f.keys[fmt.Sprintf("%064x", i+1)] = hex.EncodeToString(p)
	}
	old := runRestic
	runRestic = f.run
	t.Cleanup(func() { runRestic = old })
	return f
}

func (f *fakeRepo) passwords() []string {
	return slices.Sorted(maps.Values(f.keys))
}

func TestOpenRepositoryMovesRawKeyToDerived(t *testing.T) {
	ctx := context.Background()
	key := bytes.Repeat([]byte{0x01}, 32)
	derived, err := resticPassword(key)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{hex.EncodeToString(derived)}

	t.Run("raw key repository is moved", func(t *testing.T) {
		f := fakeRepoWith(t, key)
		if pw, err := openRepository(ctx, Repository{URL: "repo"}, key); err != nil || !bytes.Equal(pw, derived) {
			t.Fatalf("open: %v", err)
		}
		if got := f.passwords(); !slices.Equal(got, want) {
			t.Fatalf("keys after move: %v", got)
		}
		f.calls = nil
		if _, err := openRepository(ctx, Repository{URL: "repo"}, key); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(f.calls, "key add") || slices.Contains(f.calls, "key remove") {
			t.Fatalf("a moved repository was changed again: %v", f.calls)
		}
	})

	t.Run("interrupted after add is finished by the next run", func(t *testing.T) {
		f := fakeRepoWith(t, key)
		f.failNext = "remove"
		if _, err := openRepository(ctx, Repository{URL: "repo"}, key); err != nil {
			t.Fatalf("a failed raw-key removal must not fail the backup: %v", err)
		}
		if len(f.keys) != 2 {
			t.Fatalf("want both keys after the interrupted run, got %v", f.passwords())
		}
		if _, err := openRepository(ctx, Repository{URL: "repo"}, key); err != nil {
			t.Fatal(err)
		}
		if got := f.passwords(); !slices.Equal(got, want) {
			t.Fatalf("keys after rerun: %v", got)
		}
	})

	t.Run("interrupted during add retries the add", func(t *testing.T) {
		f := fakeRepoWith(t, key)
		f.failNext = "add"
		if _, err := openRepository(ctx, Repository{URL: "repo"}, key); err == nil {
			t.Fatal("a failed add must fail: the derived password opens nothing yet")
		}
		if _, err := openRepository(ctx, Repository{URL: "repo"}, key); err != nil {
			t.Fatal(err)
		}
		if got := f.passwords(); !slices.Equal(got, want) {
			t.Fatalf("keys after rerun: %v", got)
		}
	})

	t.Run("operator keys are left alone", func(t *testing.T) {
		operator := bytes.Repeat([]byte{0x02}, 32)
		f := fakeRepoWith(t, key, operator)
		if _, err := openRepository(ctx, Repository{URL: "repo"}, key); err != nil {
			t.Fatal(err)
		}
		if got := f.passwords(); !slices.Contains(got, hex.EncodeToString(operator)) || slices.Contains(got, hex.EncodeToString(key)) {
			t.Fatalf("keys: %v", got)
		}
	})

	t.Run("foreign repository is refused unchanged", func(t *testing.T) {
		f := fakeRepoWith(t, bytes.Repeat([]byte{0x03}, 32))
		if _, err := openRepository(ctx, Repository{URL: "repo"}, key); !errors.Is(err, errWrongPassword) {
			t.Fatalf("got %v, want errWrongPassword", err)
		}
		if len(f.keys) != 1 {
			t.Fatal("a repository neither password opens was changed")
		}
	})
}

func TestInitBulkRepositoryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	key := bytes.Repeat([]byte{0x01}, 32)
	derived, err := resticPassword(key)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	t.Setenv("KYDRIVE_BULK_BACKUP_REPOSITORY", t.TempDir())
	t.Setenv("KYDRIVE_BULK_BACKUP_CREDENTIALS_FILE", "")
	changed := func(calls []string) bool {
		return slices.Contains(calls, "init") || slices.Contains(calls, "key add") || slices.Contains(calls, "key remove")
	}

	t.Run("missing repository is created with the derived password", func(t *testing.T) {
		f := fakeRepoWith(t)
		if err := InitBulkRepository(ctx, dataDir, key); err != nil {
			t.Fatal(err)
		}
		if got := f.passwords(); !slices.Equal(got, []string{hex.EncodeToString(derived)}) {
			t.Fatalf("keys after init: %v", got)
		}
	})

	for name, pw := range map[string][]byte{"derived": derived, "raw key": key} {
		t.Run("rerun on a "+name+" repository changes nothing", func(t *testing.T) {
			f := fakeRepoWith(t, pw)
			if err := InitBulkRepository(ctx, dataDir, key); err != nil {
				t.Fatalf("rerun: %v", err)
			}
			if changed(f.calls) || !slices.Equal(f.passwords(), []string{hex.EncodeToString(pw)}) {
				t.Fatalf("rerun changed the repository: %v", f.calls)
			}
		})
	}

	t.Run("repository the key cannot open fails clearly", func(t *testing.T) {
		f := fakeRepoWith(t, []byte("someone else's password"))
		err := InitBulkRepository(ctx, dataDir, key)
		if !errors.Is(err, ErrForeignBulkRepository) {
			t.Fatalf("got %v, want ErrForeignBulkRepository", err)
		}
		if changed(f.calls) {
			t.Fatalf("a foreign repository was changed: %v", f.calls)
		}
	})
}
