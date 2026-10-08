# M1 File Management and Reclaiming Space Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Users can rename, move and copy files and folders, trash and restore folders, permanently delete trashed items and old versions, and admins can enable per-workspace retention, so KyDrive workspaces stop filling up forever.

**Architecture:** All domain logic lives in `internal/drive` (SQLite, HTTP-free, authorization re-checked inside each transaction). A new drive-owned, append-only schema migration list upgrades existing pilot databases. Purges delete metadata in a transaction and return the blob IDs that lost their last reference; the HTTP layer or the hourly retention worker removes those files after commit. The React drive page gains folder rows with breadcrumbs, a move/copy/rename dialog, and a trash view with permanent delete.

**Tech Stack:** Go 1.2x `net/http` mux patterns, `modernc.org/sqlite`, React 19 + TypeScript + Vite, vitest + Testing Library, Playwright.

**Spec:** `docs/superpowers/plans/2026-10-07-onedrive-parity-roadmap.md` (milestone M1 and the 2026-10-07 decisions). Contracts: `AGENTS.md`, `internal/drive/AGENTS.md`, `internal/api/AGENTS.md`, `web/AGENTS.md`.

## Global Constraints

- Read the DOX chain before editing: `../AGENTS.md` → `AGENTS.md` → `internal/drive/AGENTS.md`, `internal/api/AGENTS.md`, `web/AGENTS.md`, `web/browser/AGENTS.md`.
- Branch from `master` after `feature/open-in-new-tab` is merged: `git switch -c feature/file-management master`.
- SQLite only. Domain functions take plain Go values; no `net/http` in `internal/drive`.
- Every mutation writes one `drive_events` row in the same transaction.
- Administration never bypasses content grants: admins set retention; only workspace managers (rank 3, which includes a personal workspace's owner) purge.
- Service tokens are reader/editor (rank ≤ 2), so they can never purge; admin routes refuse them.
- Blob removal happens only after the metadata commit, and only for blobs the purging transaction itself found unreferenced. Never scan the blob directory and delete.
- Restic snapshots are never pruned.
- Request bodies stay capped at 1 MiB (already enforced by the server) and decoded with `decodeDrive` (unknown fields rejected).
- Retention bounds: `trash_days` 0–3650 and `keep_versions` 0–1000; 0 means off. Off is the default.
- UI copy is short, plain and uses the existing Busnes theme classes; no new dependencies.

## Review Focus

1. **A restore or copy racing a purge of the same blob.** Expected: never a version row pointing at a deleted blob. Copy and restore read the source blob inside their own publishing transaction (Task 5); purge decides "unreferenced" inside its transaction (Task 6). Pinned by `TestPurgeKeepsBlobsStillReferenced` and `TestRestoreAfterPurgeFails`.
2. **Moving a file another person has open into a workspace they cannot access.** Expected: their editor session is revoked on the next revocation pass. Pinned by `TestMoveAcrossWorkspacesRevokesLostAccess` (Task 3).
3. **Moving a folder into its own descendant.** Expected: `ErrInvalid` and no change. Pinned by `TestMoveFolderRejectsCycles` (Task 4).
4. **Restoring an item whose parent folder is still in the trash, or whose name is now taken.** Expected: it lands at the workspace root, or the restore fails with a conflict and nothing changes. Pinned by `TestFolderTrashRestoreEdgeCases` (Task 2).
5. **Retention deleting a version an open editor is based on, or purging the instant an upgraded pilot starts.** Expected: versions under live sessions are skipped, and files trashed before the upgrade count from upgrade time, not from the epoch. Pinned by `TestRetentionSkipsLiveEditorVersions` (Task 7) and `TestSchemaUpgradesLegacyDatabase` (Task 1).

Known limitation (document, do not fix in M1): if a purge removes a blob between a bulk backup's database snapshot and its blob hard-link step, that backup run fails and is retried at the next interval. It fails loudly, never silently.

---

### Task 1: Drive-owned schema migrations

**Files:**
- Create: `internal/drive/schema.go`
- Create: `internal/drive/schema_test.go`
- Modify: `internal/drive/store.go:69-91` (call `migrate`), `:219`, `:239` (`INSERT INTO drive_workspaces`), `:358` (`INSERT INTO drive_folders`), `:533` (`INSERT INTO drive_files`)

**Interfaces:**
- Produces: tables `drive_files(+trashed_at TEXT, +trash_batch TEXT)`, `drive_folders(+trashed BOOLEAN, +trashed_at TEXT, +trash_batch TEXT)` with unique index `drive_live_folders` on live names only, `drive_workspaces(+trash_days INTEGER, +keep_versions INTEGER)`, table `drive_schema(version)`. Helper `func now() string` (RFC3339Nano UTC).

- [ ] **Step 1: Write the failing test**

```go
// internal/drive/schema_test.go
package drive_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// A pilot database predates trash bookkeeping. Upgrading must keep its folders, stamp
// already-trashed files with the upgrade time (so retention does not purge them at once),
// free trashed folder names, and be idempotent.
func TestSchemaUpgradesLegacyDatabase(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	if cfg.Driver != "sqlite" {
		t.Skip("drive is SQLite-only")
	}
	legacy, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.ExecContext(ctx, `
CREATE TABLE drive_workspaces(id TEXT PRIMARY KEY,name TEXT NOT NULL,quota BIGINT NOT NULL CHECK(quota>0));
CREATE TABLE drive_folders(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,UNIQUE(workspace,parent,name));
CREATE TABLE drive_files(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,revision BIGINT NOT NULL,trashed BOOLEAN NOT NULL DEFAULT false);
INSERT INTO drive_workspaces VALUES('w','Legacy',100);
INSERT INTO drive_folders VALUES('d','w','','Reports');
INSERT INTO drive_files VALUES('f','w','d','old.txt',1,true);`)
	legacy.Close()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		st, err := store.Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
	}
	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var folders, stamped, version, days int
	err = db.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM drive_folders WHERE id='d' AND trashed=false),
 (SELECT COUNT(*) FROM drive_files WHERE id='f' AND trashed_at!='' AND julianday('now')-julianday(trashed_at)<1),
 (SELECT MAX(version) FROM drive_schema),
 (SELECT trash_days FROM drive_workspaces WHERE id='w')`).Scan(&folders, &stamped, &version, &days)
	if err != nil || folders != 1 || stamped != 1 || version != 1 || days != 0 {
		t.Fatalf("upgrade: err=%v folders=%d stamped=%d version=%d days=%d", err, folders, stamped, version, days)
	}
	if _, err = db.ExecContext(ctx, `UPDATE drive_folders SET trashed=true WHERE id='d'; INSERT INTO drive_folders(id,workspace,parent,name) VALUES('d2','w','','Reports')`); err != nil {
		t.Fatalf("trashed folder name not reusable: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/drive -run TestSchemaUpgradesLegacyDatabase -v`
Expected: FAIL with `no such table: drive_schema` or `no such column: trashed_at`.

- [ ] **Step 3: Write the migration runner**

```go
// internal/drive/schema.go
package drive

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// schema upgrades drive tables in place; entry i is version i+1. Append only: never edit
// a shipped entry. Files trashed before version 1 count from the upgrade, not the epoch.
var schema = []string{
	`ALTER TABLE drive_files ADD COLUMN trashed_at TEXT NOT NULL DEFAULT '';
ALTER TABLE drive_files ADD COLUMN trash_batch TEXT NOT NULL DEFAULT '';
UPDATE drive_files SET trashed_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE trashed;
CREATE TABLE drive_folders_v2(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,trashed BOOLEAN NOT NULL DEFAULT false,trashed_at TEXT NOT NULL DEFAULT '',trash_batch TEXT NOT NULL DEFAULT '');
INSERT INTO drive_folders_v2(id,workspace,parent,name) SELECT id,workspace,parent,name FROM drive_folders;
DROP TABLE drive_folders;
ALTER TABLE drive_folders_v2 RENAME TO drive_folders;
CREATE UNIQUE INDEX drive_live_folders ON drive_folders(workspace,parent,name) WHERE trashed=false;
ALTER TABLE drive_workspaces ADD COLUMN trash_days INTEGER NOT NULL DEFAULT 0 CHECK(trash_days BETWEEN 0 AND 3650);
ALTER TABLE drive_workspaces ADD COLUMN keep_versions INTEGER NOT NULL DEFAULT 0 CHECK(keep_versions BETWEEN 0 AND 1000);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS drive_schema(version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	for i, ddl := range schema {
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			var done int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_schema WHERE version=?`, i+1).Scan(&done); err != nil || done == 1 {
				return err
			}
			if _, err := tx.ExecContext(ctx, ddl); err != nil {
				return fmt.Errorf("drive schema %d: %w", i+1, err)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO drive_schema VALUES(?)`, i+1)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
```

- [ ] **Step 4: Call it and name insert columns**

In `store.go` `New`, replace `return &Store{db: db}, nil` with:

```go
	s := &Store{db: db}
	if err = s.migrate(ctx); err != nil {
		return nil, err
	}
	return s, nil
```

The new columns break positional `INSERT ... VALUES` statements. Change them to named columns:
- `:219` → `INSERT INTO drive_workspaces(id,name,quota) VALUES(?,?,?)`
- `:239` → same
- `:358` → `INSERT INTO drive_folders(id,workspace,parent,name) VALUES(?,?,?,?)`
- `:533` → `INSERT INTO drive_files(id,workspace,parent,name,revision) VALUES(?,?,?,?,?)`

- [ ] **Step 5: Run the drive and API suites**

Run: `go test -race ./internal/drive ./internal/api`
Expected: PASS, including the new test and every existing publish/folder test.

- [ ] **Step 6: Commit**

```bash
git add internal/drive/schema.go internal/drive/schema_test.go internal/drive/store.go
git commit -m "drive: add append-only schema migrations with trash bookkeeping"
```

---

### Task 2: Folder trash and restore

**Files:**
- Create: `internal/drive/trash.go`
- Create: `internal/drive/trash_test.go`
- Modify: `internal/drive/store.go` (`Folder` struct `:36-41`, `parentOK` `:332-345`, `AddFolder` `:346-364`, `Folders` `:365-383`, `SetTrash` `:463-482`)
- Modify: `internal/api/drive_handlers.go:185` (pass `trash` query to `Folders`)

**Interfaces:**
- Consumes: Task 1 columns, `now()`.
- Produces:
  - `type Folder struct { ID, Workspace, Parent, Name string; Trashed bool }` (JSON `id, workspace, parent, name, trashed`)
  - `func (s *Store) Folders(ctx context.Context, user, workspace string, trashed bool) ([]Folder, error)`
  - `func (s *Store) SetFolderTrash(ctx context.Context, user, id string, trash bool) error`
  - `func subtree(ctx context.Context, tx *sql.Tx, root, cond string, args ...any) ([]string, error)`: root plus descendants matching `cond` (SQL over alias `d`, constant strings only)
  - `func column(ctx context.Context, q rowser, query string, args ...any) ([]string, error)` and `type rowser interface{ QueryContext(context.Context, string, ...any) (*sql.Rows, error) }`
  - `parentOK` now rejects trashed folders. `SetTrash` stamps `trashed_at`/`trash_batch`, refuses a no-op toggle with `ErrConflict`, and restores to the root when the parent folder is trashed.

- [ ] **Step 1: Write the failing tests**

```go
// internal/drive/trash_test.go
package drive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func names(t *testing.T, d *drive.Store, user, workspace string, trashed bool) map[string]bool {
	t.Helper()
	ctx := context.Background()
	out := map[string]bool{}
	files, err := d.Files(ctx, user, workspace, trashed)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		out[f.Name] = true
	}
	folders, err := d.Folders(ctx, user, workspace, trashed)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		out[f.Name+"/"] = true
	}
	return out
}

func TestFolderTrashCascadesAndRestoresItsBatch(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	top, err := d.AddFolder(ctx, "worker", w.ID, "", "Top")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := d.AddFolder(ctx, "worker", w.ID, top.ID, "Sub")
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: sub.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	early, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: top.ID, Name: "early.txt"}, blob(t, root, "e"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetTrash(ctx, "worker", early.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "worker", top.ID, true); err != nil {
		t.Fatal(err)
	}
	if live := names(t, d, "worker", w.ID, false); len(live) != 0 {
		t.Fatalf("live after trash: %v", live)
	}
	if got := names(t, d, "worker", w.ID, true); !got["Top/"] || !got["Sub/"] || !got["a.txt"] || !got["early.txt"] {
		t.Fatalf("trash: %v", got)
	}
	if _, err = d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: sub.ID, Name: "late.txt"}, blob(t, root, "l"), 0); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("upload into trashed folder: %v", err)
	}
	if _, err = d.AddFolder(ctx, "worker", w.ID, "", "Top"); err != nil {
		t.Fatalf("trashed folder name not reusable: %v", err)
	}
	if err = d.SetFolderTrash(ctx, "worker", top.ID, false); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("restore over a live name: %v", err)
	}
	if err = d.SetFolderTrash(ctx, "worker", mustFolder(t, d, w.ID, "Top", false), true); err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "worker", top.ID, false); err != nil {
		t.Fatal(err)
	}
	live := names(t, d, "worker", w.ID, false)
	if !live["Top/"] || !live["Sub/"] || !live["a.txt"] || live["early.txt"] {
		t.Fatalf("restore must bring back its own batch only: %v", live)
	}
	if f, _ := d.File(ctx, "worker", a.ID, 1); f.Parent != sub.ID || f.Trashed {
		t.Fatalf("restored file moved: %+v", f)
	}
}

func TestFolderTrashRestoreEdgeCases(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	top, _ := d.AddFolder(ctx, "worker", w.ID, "", "Top")
	sub, _ := d.AddFolder(ctx, "worker", w.ID, top.ID, "Sub")
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: top.ID, Name: "f.txt"}, blob(t, root, "f"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "worker", top.ID, true); err != nil {
		t.Fatal(err)
	}
	// Restoring one item out of a trashed folder puts it at the root.
	if err = d.SetFolderTrash(ctx, "worker", sub.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetTrash(ctx, "worker", f.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	folders, _ := d.Folders(ctx, "worker", w.ID, false)
	if len(folders) != 1 || folders[0].ID != sub.ID || folders[0].Parent != "" {
		t.Fatalf("sub folder: %+v", folders)
	}
	if got, _ := d.File(ctx, "worker", f.ID, 1); got.Parent != "" {
		t.Fatalf("file parent: %q", got.Parent)
	}
	// Toggling to the state an item is already in is a conflict, not a silent success.
	if err = d.SetTrash(ctx, "worker", f.ID, 1, false); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("double restore: %v", err)
	}
	if err = d.SetFolderTrash(ctx, "stranger", sub.ID, true); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("stranger trashed folder: %v", err)
	}
}

func mustFolder(t *testing.T, d *drive.Store, workspace, name string, trashed bool) string {
	t.Helper()
	folders, err := d.Folders(context.Background(), "worker", workspace, trashed)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		if f.Name == name {
			return f.ID
		}
	}
	t.Fatalf("no folder %q", name)
	return ""
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/drive -run 'FolderTrash' -v`
Expected: FAIL to compile: `d.SetFolderTrash undefined`, `too many arguments in call to d.Folders`.

- [ ] **Step 3: Implement**

`store.go` changes:

```go
type Folder struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Parent    string `json:"parent"`
	Name      string `json:"name"`
	Trashed   bool   `json:"trashed"`
}
```

- `AddFolder`: `f := Folder{ID: uuid.NewString(), Workspace: workspace, Parent: parent, Name: name}`.
- `parentOK` query: `SELECT COUNT(*) FROM drive_folders WHERE id=? AND workspace=? AND trashed=false`.
- `Folders(ctx, user, workspace string, trashed bool)` query: `SELECT id,workspace,parent,name,trashed FROM drive_folders WHERE workspace=? AND trashed=? ORDER BY name`, scanning `&f.Trashed`.
- `SetTrash` body inside the transaction, after `authorize`:

```go
		query := `UPDATE drive_files SET trashed=true,trashed_at=?,trash_batch=? WHERE id=? AND revision=? AND trashed=false`
		args := []any{now(), uuid.NewString(), id, expected}
		if !trash {
			// A file whose folder is still in the trash comes back at the workspace root.
			query = `UPDATE drive_files SET trashed=false,trashed_at='',trash_batch='',parent=CASE WHEN parent='' OR EXISTS(SELECT 1 FROM drive_folders d WHERE d.id=drive_files.parent AND d.trashed=false) THEN parent ELSE '' END WHERE id=? AND revision=? AND trashed=true`
			args = []any{id, expected}
		}
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return ErrConflict
		}
```

(The existing `RowsAffected != 1 → ErrConflict` check and the event row stay.)

`internal/drive/trash.go`:

```go
package drive

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

type rowser interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func column(ctx context.Context, q rowser, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// subtree returns root and every descendant folder matching cond, a constant SQL condition
// over alias d. UNION stops on a cycle left by older data.
func subtree(ctx context.Context, tx *sql.Tx, root, cond string, args ...any) ([]string, error) {
	return column(ctx, tx, `WITH RECURSIVE tree(id) AS (SELECT ? UNION SELECT d.id FROM drive_folders d JOIN tree t ON d.parent=t.id WHERE `+cond+`) SELECT id FROM tree`, append([]any{root}, args...)...)
}

// SetFolderTrash trashes a folder and everything live beneath it as one batch, or restores
// exactly that batch. Items trashed earlier stay in the trash. A folder whose parent is
// still trashed comes back at the workspace root.
func (s *Store) SetFolderTrash(ctx context.Context, user, id string, trash bool) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace, parent, batch string
		if err := tx.QueryRowContext(ctx, `SELECT workspace,parent,trash_batch FROM drive_folders WHERE id=? AND trashed=?`, id, !trash).Scan(&workspace, &parent, &batch); err != nil {
			if err == sql.ErrNoRows {
				var n int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_folders WHERE id=?`, id).Scan(&n); err == nil && n == 1 {
					return ErrConflict
				}
				return ErrDenied
			}
			return err
		}
		if err := authorize(ctx, tx, user, workspace, 2); err != nil {
			return err
		}
		if trash {
			tree, err := subtree(ctx, tx, id, `d.trashed=false`)
			if err != nil {
				return err
			}
			batch, stamp := uuid.NewString(), now()
			for _, folder := range tree {
				if _, err = tx.ExecContext(ctx, `UPDATE drive_folders SET trashed=true,trashed_at=?,trash_batch=? WHERE id=?`, stamp, batch, folder); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE drive_files SET trashed=true,trashed_at=?,trash_batch=? WHERE parent=? AND trashed=false`, stamp, batch, folder); err != nil {
					return err
				}
			}
			return event(ctx, tx, user, "folder.trashed", id)
		}
		tree, err := subtree(ctx, tx, id, `d.trash_batch=?`, batch)
		if err != nil {
			return err
		}
		if parent != "" {
			var live int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_folders WHERE id=? AND trashed=false`, parent).Scan(&live); err != nil {
				return err
			}
			if live == 0 {
				if _, err = tx.ExecContext(ctx, `UPDATE drive_folders SET parent='' WHERE id=?`, id); err != nil {
					return err
				}
			}
		}
		for _, folder := range tree {
			if _, err = tx.ExecContext(ctx, `UPDATE drive_files SET trashed=false,trashed_at='',trash_batch='' WHERE parent=? AND trash_batch=?`, folder, batch); err != nil {
				return ErrConflict
			}
			if _, err = tx.ExecContext(ctx, `UPDATE drive_folders SET trashed=false,trashed_at='',trash_batch='' WHERE id=?`, folder); err != nil {
				return ErrConflict
			}
		}
		return event(ctx, tx, user, "folder.restored", id)
	})
}
```

`internal/api/drive_handlers.go:185`: `s.store.Drive().Folders(r.Context(), s.driveUser(r).ID, r.PathValue("id"), r.URL.Query().Get("trash") == "true")`.

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/drive ./internal/api`
Expected: PASS. If an existing test trashes an already-trashed file, that is the new no-op conflict. Change the test to toggle state, and say so in the commit message.

- [ ] **Step 5: Commit**

```bash
git add internal/drive internal/api/drive_handlers.go
git commit -m "drive: trash and restore folders as one batch"
```

---

### Task 3: Rename and move files, including across workspaces

**Files:**
- Create: `internal/drive/move.go`
- Create: `internal/drive/move_test.go`
- Modify: `internal/drive/store.go:525-531` (extract the quota check into `fits`)

**Interfaces:**
- Consumes: `parentOK` (rejects trashed folders, from Task 2).
- Produces:
  - `type Location struct { Workspace, Parent, Name string }`
  - `func fits(ctx context.Context, q queryer, workspace string, extra int64) error`
  - `func (s *Store) MoveFile(ctx context.Context, user, id string, expected int64, to Location) (File, error)`: no new version; `expected` is the current revision; editor rights on source and target; a cross-workspace move must fit every retained version in the target quota.

- [ ] **Step 1: Write the failing tests**

```go
// internal/drive/move_test.go
package drive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestMoveFileRenamesAndRelocates(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	folder, _ := d.AddFolder(ctx, "worker", w.ID, "", "Docs")
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "taken.txt"}, blob(t, root, "t"), 0); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		expected int64
		to       drive.Location
		want     error
	}{
		"taken name":    {1, drive.Location{Workspace: w.ID, Name: "taken.txt"}, drive.ErrConflict},
		"stale":         {7, drive.Location{Workspace: w.ID, Name: "b.txt"}, drive.ErrConflict},
		"bad name":      {1, drive.Location{Workspace: w.ID, Name: "a/b"}, drive.ErrInvalid},
		"foreign folder": {1, drive.Location{Workspace: w.ID, Parent: "missing", Name: "b.txt"}, drive.ErrInvalid},
	} {
		if _, err = d.MoveFile(ctx, "worker", f.ID, c.expected, c.to); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	moved, err := d.MoveFile(ctx, "worker", f.ID, 1, drive.Location{Workspace: w.ID, Parent: folder.ID, Name: "b.txt"})
	if err != nil || moved.Name != "b.txt" || moved.Parent != folder.ID || moved.Revision != 1 {
		t.Fatalf("move: %+v %v", moved, err)
	}
	if vs, _ := d.Versions(ctx, "worker", f.ID); len(vs) != 1 {
		t.Fatalf("move created a version: %d", len(vs))
	}
	reader := drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: w.ID, Rank: 1})
	if _, err = d.MoveFile(reader, "worker", f.ID, 1, drive.Location{Workspace: w.ID, Name: "c.txt"}); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("reader token moved: %v", err)
	}
}

func TestMoveAcrossWorkspacesRevokesLostAccess(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "colleague", Username: "colleague", Email: "colleague@local.test", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", ExternalID: "external-team", Members: []string{"worker", "colleague"}}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "private", DisplayName: "Private", ExternalID: "external-private", Members: []string{"worker"}}, true); err != nil {
		t.Fatal(err)
	}
	target, err := d.CreateWorkspace(ctx, "admin", "Private", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Grant(ctx, "admin", target.ID, "private", "editor"); err != nil {
		t.Fatal(err)
	}
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "plan.txt"}, blob(t, root, "plan"), 0)
	if err != nil {
		t.Fatal(err)
	}
	session, err := d.EditorSession(ctx, "colleague", f.ID, "colleague-token")
	if err != nil {
		t.Fatal(err)
	}
	small, _ := d.CreateWorkspace(ctx, "admin", "Small", 1)
	if err = d.Grant(ctx, "admin", small.ID, "private", "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.MoveFile(ctx, "worker", f.ID, 1, drive.Location{Workspace: small.ID, Name: "plan.txt"}); !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("quota ignored: %v", err)
	}
	if _, err = d.MoveFile(ctx, "colleague", f.ID, 1, drive.Location{Workspace: target.ID, Name: "plan.txt"}); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("moved into a workspace without access: %v", err)
	}
	if _, err = d.MoveFile(ctx, "worker", f.ID, 1, drive.Location{Workspace: target.ID, Name: "plan.txt"}); err != nil {
		t.Fatal(err)
	}
	pending, err := d.PendingRevocations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != session.ID {
		t.Fatalf("colleague's editor not revoked: %+v", pending)
	}
	if _, err = d.File(ctx, "colleague", f.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("colleague still reads moved file: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/drive -run TestMove -v`
Expected: FAIL to compile: `undefined: drive.Location`, `d.MoveFile undefined`.

- [ ] **Step 3: Implement**

In `store.go`, replace the quota block in `publishTX` with `if err := fits(ctx, tx, f.Workspace, v.Size); err != nil { return err }` and add:

```go
// fits returns ErrQuota unless workspace can take extra more bytes of retained versions.
func fits(ctx context.Context, q queryer, workspace string, extra int64) error {
	var used, quota int64
	if err := q.QueryRowContext(ctx, `SELECT quota,COALESCE((SELECT SUM(v.size) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=?),0) FROM drive_workspaces WHERE id=?`, workspace, workspace).Scan(&quota, &used); err != nil {
		return err
	}
	if used > quota || extra > quota-used {
		return ErrQuota
	}
	return nil
}
```

`internal/drive/move.go`:

```go
package drive

import (
	"context"
	"database/sql"
	"fmt"
)

type Location struct{ Workspace, Parent, Name string }

// MoveFile renames or relocates a file without creating a version. Another workspace needs
// editor rights there and room for every retained version; open editors that lose access
// are revoked by the next PendingRevocations pass.
func (s *Store) MoveFile(ctx context.Context, user, id string, expected int64, to Location) (File, error) {
	if !ValidName(to.Name) {
		return File{}, ErrInvalid
	}
	f, err := s.File(ctx, user, id, 2)
	if err != nil {
		return f, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := authorize(ctx, tx, user, f.Workspace, 2); err != nil {
			return err
		}
		if err := authorize(ctx, tx, user, to.Workspace, 2); err != nil {
			return err
		}
		if err := parentOK(ctx, tx, to.Workspace, to.Parent); err != nil {
			return err
		}
		if to.Workspace != f.Workspace {
			var size int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM drive_versions WHERE file_id=?`, id).Scan(&size); err != nil {
				return err
			}
			if err := fits(ctx, tx, to.Workspace, size); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE drive_files SET workspace=?,parent=?,name=? WHERE id=? AND workspace=? AND revision=? AND trashed=false`, to.Workspace, to.Parent, to.Name, id, f.Workspace, expected)
		if err != nil {
			return fmt.Errorf("%w: name exists", ErrConflict)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrConflict
		}
		return event(ctx, tx, user, "file.moved", fmt.Sprintf("%s:%s->%s", id, f.Workspace, to.Workspace))
	})
	if err != nil {
		return f, err
	}
	f.Workspace, f.Parent, f.Name = to.Workspace, to.Parent, to.Name
	return f, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/drive`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/drive
git commit -m "drive: rename and move files, across workspaces with quota and revocation"
```

---

### Task 4: Rename and move folders

**Files:**
- Modify: `internal/drive/move.go`
- Modify: `internal/drive/move_test.go`

**Interfaces:**
- Produces: `func (s *Store) MoveFolder(ctx context.Context, user, id, parent, name string) (Folder, error)`. Moves stay within the folder's workspace. Moving into itself or a descendant returns `ErrInvalid`; a taken name returns `ErrConflict`.

- [ ] **Step 1: Write the failing test**

```go
func TestMoveFolderRejectsCycles(t *testing.T) {
	st, w, _ := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	a, _ := d.AddFolder(ctx, "worker", w.ID, "", "A")
	b, _ := d.AddFolder(ctx, "worker", w.ID, a.ID, "B")
	c, _ := d.AddFolder(ctx, "worker", w.ID, b.ID, "C")
	if _, err := d.AddFolder(ctx, "worker", w.ID, "", "Taken"); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		id, parent, name string
		want             error
	}{
		"into self":       {a.ID, a.ID, "A", drive.ErrInvalid},
		"into grandchild": {a.ID, c.ID, "A", drive.ErrInvalid},
		"taken":           {b.ID, "", "Taken", drive.ErrConflict},
		"bad name":        {b.ID, "", "..", drive.ErrInvalid},
		"missing parent":  {b.ID, "nope", "B", drive.ErrInvalid},
	} {
		if _, err := d.MoveFolder(ctx, "worker", tc.id, tc.parent, tc.name); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	moved, err := d.MoveFolder(ctx, "worker", c.ID, "", "C renamed")
	if err != nil || moved.Parent != "" || moved.Name != "C renamed" {
		t.Fatalf("move: %+v %v", moved, err)
	}
	if _, err = d.MoveFolder(ctx, "stranger", b.ID, "", "Mine"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("stranger moved folder: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/drive -run TestMoveFolderRejectsCycles -v`
Expected: FAIL to compile: `d.MoveFolder undefined`.

- [ ] **Step 3: Implement**

```go
// MoveFolder renames or re-parents a live folder within its workspace.
func (s *Store) MoveFolder(ctx context.Context, user, id, parent, name string) (Folder, error) {
	f := Folder{ID: id, Parent: parent, Name: name}
	if !ValidName(name) || parent == id {
		return f, ErrInvalid
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT workspace FROM drive_folders WHERE id=? AND trashed=false`, id).Scan(&f.Workspace); err != nil {
			return ErrDenied
		}
		if err := authorize(ctx, tx, user, f.Workspace, 2); err != nil {
			return err
		}
		if err := parentOK(ctx, tx, f.Workspace, parent); err != nil {
			return err
		}
		var cycle int
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE up(id) AS (SELECT ? UNION SELECT d.parent FROM drive_folders d JOIN up ON d.id=up.id WHERE d.parent!='') SELECT COUNT(*) FROM up WHERE id=?`, parent, id).Scan(&cycle); err != nil {
			return err
		}
		if cycle > 0 {
			return ErrInvalid
		}
		if _, err := tx.ExecContext(ctx, `UPDATE drive_folders SET parent=?,name=? WHERE id=?`, parent, name, id); err != nil {
			return fmt.Errorf("%w: name exists", ErrConflict)
		}
		return event(ctx, tx, user, "folder.moved", id)
	})
	return f, err
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/drive`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/drive
git commit -m "drive: rename and move folders without cycles"
```

---

### Task 5: Copy files; make version restore race-free

**Files:**
- Modify: `internal/drive/move.go` (add `CopyFile`)
- Modify: `internal/drive/store.go:483-497` (`RestoreVersion`)
- Modify: `internal/drive/move_test.go`

**Interfaces:**
- Produces: `func (s *Store) CopyFile(ctx context.Context, user, id string, to Location) (File, error)`: reader rights on the source, editor rights on the target; the current version becomes revision 1 of a new file that shares the immutable blob; the target quota counts it.
- Changes: `RestoreVersion` reads the old version's blob inside the publishing transaction.

- [ ] **Step 1: Write the failing test**

```go
func TestCopyFileSharesBlobAndCountsQuota(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "twelve bytes"), 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.CopyFile(ctx, "worker", f.ID, drive.Location{Workspace: w.ID, Name: "a copy.txt"})
	if err != nil || c.ID == f.ID || c.Revision != 1 || c.Digest != f.Digest {
		t.Fatalf("copy: %+v %v", c, err)
	}
	ws, _ := d.Workspaces(ctx, "worker", false)
	if ws[0].Used != 24 {
		t.Fatalf("quota must count the copy: %d", ws[0].Used)
	}
	if _, err = d.CopyFile(ctx, "worker", f.ID, drive.Location{Workspace: w.ID, Name: "a copy.txt"}); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("copy over a live name: %v", err)
	}
	if _, err = d.CopyFile(ctx, "stranger", f.ID, drive.Location{Workspace: w.ID, Name: "x.txt"}); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("stranger copied: %v", err)
	}
	if err = d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err = d.CopyFile(ctx, "worker", f.ID, drive.Location{Workspace: w.ID, Name: "y.txt"}); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("copied a trashed file: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/drive -run TestCopyFile -v`
Expected: FAIL to compile: `d.CopyFile undefined`.

- [ ] **Step 3: Implement**

`move.go` (add `"github.com/google/uuid"` to its imports):

```go
// CopyFile publishes the source's current bytes as revision 1 of a new file. The source is
// read inside the same transaction, so a concurrent purge cannot remove the shared blob.
func (s *Store) CopyFile(ctx context.Context, user, id string, to Location) (File, error) {
	out := File{ID: uuid.NewString(), Workspace: to.Workspace, Parent: to.Parent, Name: to.Name, Revision: 1}
	if !ValidName(to.Name) {
		return out, ErrInvalid
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		src, err := scanFile(tx.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE f.id=? AND f.trashed=false`, id))
		if err != nil {
			return err
		}
		if err = authorize(ctx, tx, user, src.Workspace, 1); err != nil {
			return err
		}
		out.Size, out.Digest, out.Blob = src.Size, src.Digest, src.Blob
		return publishTX(ctx, tx, user, out, Version{FileID: out.ID, Revision: 1, Blob: src.Blob, Digest: src.Digest, Size: src.Size, Created: now()}, 0)
	})
	return out, err
}
```

`store.go` `RestoreVersion`:

```go
func (s *Store) RestoreVersion(ctx context.Context, user, id string, revision, expected int64) (File, error) {
	f, err := s.File(ctx, user, id, 2)
	if err != nil {
		return f, err
	}
	if f.Trashed {
		return f, ErrConflict
	}
	v := Version{FileID: id, Revision: expected + 1, Created: now()}
	// Read the old version inside the publish transaction: a purge cannot slip in between.
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT blob,digest,size FROM drive_versions WHERE file_id=? AND revision=?`, id, revision).Scan(&v.Blob, &v.Digest, &v.Size); err != nil {
			return ErrInvalid
		}
		return publishTX(ctx, tx, user, f, v, expected)
	})
	if err != nil {
		return f, err
	}
	f.Revision, f.Size, f.Digest, f.Blob = v.Revision, v.Size, v.Digest, v.Blob
	return f, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/drive ./internal/api`
Expected: PASS, including the existing version-restore tests.

- [ ] **Step 5: Commit**

```bash
git add internal/drive
git commit -m "drive: copy files and read restored versions inside the publish transaction"
```

---

### Task 6: Permanent delete of files, folders and versions

**Files:**
- Create: `internal/drive/purge.go`
- Create: `internal/drive/purge_test.go`
- Modify: `internal/drive/blobs.go` (add `RemoveBlobs`)

**Interfaces:**
- Consumes: `subtree`, `column` (Task 2).
- Produces (each returns the blob IDs that lost their last reference; the caller removes them after the call returns):
  - `func (s *Store) PurgeFile(ctx context.Context, user, id string) ([]string, error)`: trashed files only (`ErrConflict` if live); manager (rank 3).
  - `func (s *Store) PurgeFolder(ctx context.Context, user, id string) ([]string, error)`: trashed folders only; purges the whole trashed subtree.
  - `func (s *Store) PurgeVersion(ctx context.Context, user, id string, revision int64) ([]string, error)`: never the current revision (`ErrInvalid`); `ErrConflict` while a live editor session uses it.
  - `func purgeFile(ctx context.Context, tx *sql.Tx, id string, at int64) ([]string, error)` and `func purgeVersion(ctx context.Context, tx *sql.Tx, id string, revision, current, at int64) ([]string, error)`, both unauthorized internals for Task 7.
  - `func RemoveBlobs(root string, ids []string) error`

- [ ] **Step 1: Write the failing tests**

```go
// internal/drive/purge_test.go
package drive_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func managerFixture(t *testing.T) (*drive.Store, drive.Workspace, string) {
	t.Helper()
	st, w, root := fixture(t)
	if err := st.Drive().Grant(context.Background(), "admin", w.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
	return st.Drive(), w, root
}

func TestPurgeKeepsBlobsStillReferenced(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "shared"), 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.CopyFile(ctx, "worker", f.ID, drive.Location{Workspace: w.ID, Name: "b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.PurgeFile(ctx, "worker", f.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("purged a live file: %v", err)
	}
	if err = d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PurgeFile(ctx, "admin", f.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("admin bypassed content grants: %v", err)
	}
	blobs, err := d.PurgeFile(ctx, "worker", f.ID)
	if err != nil || len(blobs) != 0 {
		t.Fatalf("shared blob released early: %v %v", blobs, err)
	}
	if _, err = d.File(ctx, "worker", f.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("purged file still readable: %v", err)
	}
	if err = d.SetTrash(ctx, "worker", c.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	blobs, err = d.PurgeFile(ctx, "worker", c.ID)
	if err != nil || len(blobs) != 1 || blobs[0] != c.Blob {
		t.Fatalf("last reference: %v %v", blobs, err)
	}
	if err = drive.RemoveBlobs(root, blobs); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, c.Blob)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blob kept: %v", err)
	}
	if err = drive.RemoveBlobs(root, blobs); err != nil {
		t.Fatalf("removing twice must be harmless: %v", err)
	}
	if err = drive.RemoveBlobs(root, []string{"../escape"}); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("non-UUID blob accepted: %v", err)
	}
}

func TestRestoreAfterPurgeFails(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "one"), 0)
	if _, err := d.Publish(ctx, "worker", f, blob(t, root, "two"), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PurgeVersion(ctx, "worker", f.ID, 2); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("purged current version: %v", err)
	}
	blobs, err := d.PurgeVersion(ctx, "worker", f.ID, 1)
	if err != nil || len(blobs) != 1 {
		t.Fatalf("purge version: %v %v", blobs, err)
	}
	if _, err = d.RestoreVersion(ctx, "worker", f.ID, 1, 2); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("restored a purged version: %v", err)
	}
}

func TestPurgeWaitsForEditorRevocation(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if _, err := d.EditorSession(ctx, "worker", f.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PurgeFile(ctx, "worker", f.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("purged under an open editor: %v", err)
	}
	pending, err := d.PendingRevocations(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	if err = d.MarkDropped(ctx, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PurgeFile(ctx, "worker", f.ID); err != nil {
		t.Fatalf("purge after revocation: %v", err)
	}
}

func TestPurgeFolderRemovesTrashedSubtree(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	top, _ := d.AddFolder(ctx, "worker", w.ID, "", "Top")
	sub, _ := d.AddFolder(ctx, "worker", w.ID, top.ID, "Sub")
	if _, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: sub.ID, Name: "a.txt"}, blob(t, root, "a"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PurgeFolder(ctx, "worker", top.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("purged a live folder: %v", err)
	}
	if err := d.SetFolderTrash(ctx, "worker", top.ID, true); err != nil {
		t.Fatal(err)
	}
	blobs, err := d.PurgeFolder(ctx, "worker", top.ID)
	if err != nil || len(blobs) != 1 {
		t.Fatalf("purge folder: %v %v", blobs, err)
	}
	if got := names(t, d, "worker", w.ID, true); len(got) != 0 {
		t.Fatalf("trash not empty: %v", got)
	}
	ws, _ := d.Workspaces(ctx, "worker", false)
	if ws[0].Used != 0 {
		t.Fatalf("usage not released: %d", ws[0].Used)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/drive -run 'Purge|RestoreAfterPurge' -v`
Expected: FAIL to compile: `d.PurgeFile undefined`, `drive.RemoveBlobs undefined`.

- [ ] **Step 3: Implement**

`blobs.go` (add imports `errors`, `io/fs`):

```go
// RemoveBlobs deletes blobs whose last reference was purged. A missing file is already gone.
func RemoveBlobs(root string, ids []string) error {
	var errs []error
	for _, id := range ids {
		path, err := BlobPath(root, id)
		if err == nil {
			err = os.Remove(path)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if len(ids) > 0 {
		errs = append(errs, SyncDirectory(root))
	}
	return errors.Join(errs...)
}
```

`internal/drive/purge.go`:

```go
package drive

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// unreferenced returns the candidates no version row still names. Called inside the purging
// transaction, so a copy or restore committed after it sees the blob gone and fails.
func unreferenced(ctx context.Context, tx *sql.Tx, blobs []string) ([]string, error) {
	out := []string{}
	for _, b := range blobs {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_versions WHERE blob=?`, b).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			out = append(out, b)
		}
	}
	return out, nil
}

// purgeFile deletes a trashed file and its history once no editor can still save to it.
func purgeFile(ctx context.Context, tx *sql.Tx, id string, at int64) ([]string, error) {
	var open int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_editor_sessions WHERE file_id=? AND (revoked=false OR drop_done=false) AND expires>?`, id, at).Scan(&open); err != nil {
		return nil, err
	}
	if open > 0 {
		return nil, fmt.Errorf("%w: editor session still open", ErrConflict)
	}
	blobs, err := column(ctx, tx, `SELECT DISTINCT blob FROM drive_versions WHERE file_id=?`, id)
	if err != nil {
		return nil, err
	}
	for _, q := range []string{
		`DELETE FROM drive_editor_saves WHERE key IN (SELECT key FROM drive_editor_documents WHERE file_id=?)`,
		`DELETE FROM drive_editor_documents WHERE file_id=?`,
		`DELETE FROM drive_editor_sessions WHERE file_id=?`,
		`DELETE FROM drive_versions WHERE file_id=?`,
		`DELETE FROM drive_files WHERE id=? AND trashed=true`,
	} {
		if _, err = tx.ExecContext(ctx, q, id); err != nil {
			return nil, err
		}
	}
	return unreferenced(ctx, tx, blobs)
}

func purgeVersion(ctx context.Context, tx *sql.Tx, id string, revision, current, at int64) ([]string, error) {
	if revision >= current {
		return nil, ErrInvalid
	}
	var open int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_editor_sessions WHERE file_id=? AND revision=? AND revoked=false AND expires>?`, id, revision, at).Scan(&open); err != nil {
		return nil, err
	}
	if open > 0 {
		return nil, fmt.Errorf("%w: version open in editor", ErrConflict)
	}
	var blob string
	if err := tx.QueryRowContext(ctx, `SELECT blob FROM drive_versions WHERE file_id=? AND revision=?`, id, revision).Scan(&blob); err != nil {
		return nil, ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drive_versions WHERE file_id=? AND revision=?`, id, revision); err != nil {
		return nil, err
	}
	return unreferenced(ctx, tx, []string{blob})
}

func (s *Store) PurgeFile(ctx context.Context, user, id string) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace string
		var trashed bool
		if err := tx.QueryRowContext(ctx, `SELECT workspace,trashed FROM drive_files WHERE id=?`, id).Scan(&workspace, &trashed); err != nil {
			return ErrDenied
		}
		if err := authorize(ctx, tx, user, workspace, 3); err != nil {
			return err
		}
		if !trashed {
			return fmt.Errorf("%w: move to trash first", ErrConflict)
		}
		var err error
		if blobs, err = purgeFile(ctx, tx, id, time.Now().Unix()); err != nil {
			return err
		}
		return event(ctx, tx, user, "file.purged", id)
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}

func (s *Store) PurgeFolder(ctx context.Context, user, id string) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace string
		var trashed bool
		if err := tx.QueryRowContext(ctx, `SELECT workspace,trashed FROM drive_folders WHERE id=?`, id).Scan(&workspace, &trashed); err != nil {
			return ErrDenied
		}
		if err := authorize(ctx, tx, user, workspace, 3); err != nil {
			return err
		}
		if !trashed {
			return fmt.Errorf("%w: move to trash first", ErrConflict)
		}
		tree, err := subtree(ctx, tx, id, `d.trashed=true`)
		if err != nil {
			return err
		}
		at := time.Now().Unix()
		for _, folder := range tree {
			var live int
			if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM drive_files WHERE parent=? AND trashed=false)+(SELECT COUNT(*) FROM drive_folders WHERE parent=? AND trashed=false)`, folder, folder).Scan(&live); err != nil {
				return err
			}
			if live > 0 {
				return fmt.Errorf("%w: folder has live items", ErrConflict)
			}
			files, err := column(ctx, tx, `SELECT id FROM drive_files WHERE parent=?`, folder)
			if err != nil {
				return err
			}
			for _, f := range files {
				b, err := purgeFile(ctx, tx, f, at)
				if err != nil {
					return err
				}
				blobs = append(blobs, b...)
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM drive_folders WHERE id=?`, folder); err != nil {
				return err
			}
		}
		return event(ctx, tx, user, "folder.purged", id)
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}

func (s *Store) PurgeVersion(ctx context.Context, user, id string, revision int64) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace string
		var current int64
		if err := tx.QueryRowContext(ctx, `SELECT workspace,revision FROM drive_files WHERE id=?`, id).Scan(&workspace, &current); err != nil {
			return ErrDenied
		}
		if err := authorize(ctx, tx, user, workspace, 3); err != nil {
			return err
		}
		var err error
		if blobs, err = purgeVersion(ctx, tx, id, revision, current, time.Now().Unix()); err != nil {
			return err
		}
		return event(ctx, tx, user, "version.purged", fmt.Sprintf("%s:%d", id, revision))
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/drive`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/drive
git commit -m "drive: permanently delete trashed files, folders and old versions"
```

---

### Task 7: Opt-in retention policy and hourly worker

**Files:**
- Create: `internal/drive/retention.go`
- Create: `internal/drive/retention_test.go`
- Modify: `internal/drive/store.go` (`Workspace` struct `:23-30`, `Workspaces` query/scan `:173-185`)
- Create: `internal/api/drive_retention.go`
- Modify: `cmd/server/main.go:184-226` (start and wait for the worker)

**Interfaces:**
- Consumes: `purgeFile`, `purgeVersion` (Task 6).
- Produces:
  - `Workspace` gains `TrashDays int \`json:"trash_days"\`` and `KeepVersions int \`json:"keep_versions"\``
  - `func (s *Store) SetRetention(ctx context.Context, user, workspace string, trashDays, keepVersions int) error`: admin only
  - `func (s *Store) ApplyRetention(ctx context.Context, at time.Time) ([]string, error)`: system actor `system`, at most 500 files and 500 versions per call, one transaction per item, skips items that return `ErrConflict`
  - `func (s *Server) RunRetention(ctx context.Context)`: runs now, then hourly

- [ ] **Step 1: Write the failing tests**

```go
// internal/drive/retention_test.go
package drive_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func TestRetentionIsAdminSetAndBounded(t *testing.T) {
	d, w, _ := managerFixture(t)
	ctx := context.Background()
	if err := d.SetRetention(ctx, "worker", w.ID, 30, 5); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("manager set retention: %v", err)
	}
	for _, c := range [][2]int{{-1, 0}, {3651, 0}, {0, 1001}} {
		if err := d.SetRetention(ctx, "admin", w.ID, c[0], c[1]); !errors.Is(err, drive.ErrInvalid) {
			t.Fatalf("%v accepted: %v", c, err)
		}
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 30, 5); err != nil {
		t.Fatal(err)
	}
	ws, _ := d.Workspaces(ctx, "worker", false)
	if ws[0].TrashDays != 30 || ws[0].KeepVersions != 5 {
		t.Fatalf("not saved: %+v", ws[0])
	}
}

func TestRetentionEmptiesOldTrash(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if err := d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if blobs, err := d.ApplyRetention(ctx, time.Now().Add(365*24*time.Hour)); err != nil || len(blobs) != 0 {
		t.Fatalf("retention off by default: %v %v", blobs, err)
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 30, 0); err != nil {
		t.Fatal(err)
	}
	if blobs, _ := d.ApplyRetention(ctx, time.Now().Add(29*24*time.Hour)); len(blobs) != 0 {
		t.Fatalf("purged early: %v", blobs)
	}
	blobs, err := d.ApplyRetention(ctx, time.Now().Add(31*24*time.Hour))
	if err != nil || len(blobs) != 1 {
		t.Fatalf("not purged: %v %v", blobs, err)
	}
}

func TestRetentionSkipsLiveEditorVersions(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "v1"), 0)
	f, _ = d.Publish(ctx, "worker", f, blob(t, root, "v2"), 1)
	if _, err := d.EditorSession(ctx, "worker", f.ID, "token"); err != nil { // based on revision 2
		t.Fatal(err)
	}
	f, _ = d.Publish(ctx, "worker", f, blob(t, root, "v3"), 2)
	if _, err := d.Publish(ctx, "worker", f, blob(t, root, "v4"), 3); err != nil {
		t.Fatal(err)
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 0, 2); err != nil {
		t.Fatal(err)
	}
	blobs, err := d.ApplyRetention(ctx, time.Now())
	if err != nil || len(blobs) != 1 {
		t.Fatalf("expected only revision 1 purged: %v %v", blobs, err)
	}
	vs, _ := d.Versions(ctx, "worker", f.ID)
	if len(vs) != 3 || vs[2].Revision != 2 {
		t.Fatalf("versions left: %+v", vs)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/drive -run Retention -v`
Expected: FAIL to compile: `d.SetRetention undefined`.

- [ ] **Step 3: Implement the domain**

`store.go`: add the two `Workspace` fields; in `Workspaces`, select `w.trash_days,w.keep_versions` after `w.quota` and scan into `&w.TrashDays, &w.KeepVersions`.

`internal/drive/retention.go`:

```go
package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SetRetention configures automatic purging for one workspace; 0 turns a rule off.
func (s *Store) SetRetention(ctx context.Context, user, workspace string, trashDays, keepVersions int) error {
	if trashDays < 0 || trashDays > 3650 || keepVersions < 0 || keepVersions > 1000 {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, user); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE drive_workspaces SET trash_days=?,keep_versions=? WHERE id=?`, trashDays, keepVersions, workspace)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrInvalid
		}
		return event(ctx, tx, user, "workspace.retention_changed", fmt.Sprintf("%s:trash_days=%d:keep_versions=%d", workspace, trashDays, keepVersions))
	})
}

// ApplyRetention purges what workspace policies have expired as of at. Each item commits on
// its own, so one document still open in an editor does not hold back the rest.
func (s *Store) ApplyRetention(ctx context.Context, at time.Time) ([]string, error) {
	stamp, unix := at.UTC().Format(time.RFC3339Nano), at.Unix()
	var blobs []string
	each := func(action, resource string, purge func(*sql.Tx) ([]string, error)) error {
		var b []string
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			var err error
			if b, err = purge(tx); err != nil {
				return err
			}
			return event(ctx, tx, "system", action, resource)
		})
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) {
			return nil
		}
		if err == nil {
			blobs = append(blobs, b...)
		}
		return err
	}
	files, err := column(ctx, s.db, `SELECT f.id FROM drive_files f JOIN drive_workspaces w ON w.id=f.workspace WHERE f.trashed=true AND w.trash_days>0 AND julianday(?)-julianday(f.trashed_at)>w.trash_days LIMIT 500`, stamp)
	if err != nil {
		return blobs, err
	}
	for _, id := range files {
		if err = each("file.retention_purged", id, func(tx *sql.Tx) ([]string, error) { return purgeFile(ctx, tx, id, unix) }); err != nil {
			return blobs, err
		}
	}
	// Leaf folders first; a deeper tree empties over successive runs of this loop.
	for {
		res, err := s.db.ExecContext(ctx, `DELETE FROM drive_folders WHERE id IN (SELECT d.id FROM drive_folders d JOIN drive_workspaces w ON w.id=d.workspace WHERE d.trashed=true AND w.trash_days>0 AND julianday(?)-julianday(d.trashed_at)>w.trash_days AND NOT EXISTS(SELECT 1 FROM drive_files f WHERE f.parent=d.id) AND NOT EXISTS(SELECT 1 FROM drive_folders c WHERE c.parent=d.id) LIMIT 500)`, stamp)
		if err != nil {
			return blobs, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			break
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT v.file_id,v.revision,f.revision FROM drive_versions v JOIN drive_files f ON f.id=v.file_id JOIN drive_workspaces w ON w.id=f.workspace WHERE w.keep_versions>0 AND v.revision<=f.revision-w.keep_versions LIMIT 500`)
	if err != nil {
		return blobs, err
	}
	type old struct {
		id                string
		revision, current int64
	}
	var versions []old
	for rows.Next() {
		var v old
		if err = rows.Scan(&v.id, &v.revision, &v.current); err != nil {
			rows.Close()
			return blobs, err
		}
		versions = append(versions, v)
	}
	rows.Close()
	for _, v := range versions {
		if err = each("version.retention_purged", fmt.Sprintf("%s:%d", v.id, v.revision), func(tx *sql.Tx) ([]string, error) {
			return purgeVersion(ctx, tx, v.id, v.revision, v.current, unix)
		}); err != nil {
			return blobs, err
		}
	}
	return blobs, nil
}
```

Folder deletion here has no event row per folder: the files inside were already audited. If review wants one, write a `folder.retention_purged` event per deleted ID inside a transaction.

- [ ] **Step 4: Run the domain tests**

Run: `go test -race ./internal/drive -run Retention -v`
Expected: PASS.

- [ ] **Step 5: Add the worker and wire it**

`internal/api/drive_retention.go`:

```go
package api

import (
	"context"
	"log"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// RunRetention applies workspace retention now and then hourly. Blobs are removed after
// their metadata commits; a failed removal leaves an unreferenced file, never a dangling version.
func (s *Server) RunRetention(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		blobs, err := s.store.Drive().ApplyRetention(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			log.Printf("retention: %v", err)
		}
		if err = drive.RemoveBlobs(s.blobRoot(), blobs); err != nil {
			log.Printf("retention blob cleanup: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
```

`cmd/server/main.go`, after the `editorDone` goroutine:

```go
	retentionDone := make(chan struct{})
	go func() { defer close(retentionDone); srv.RunRetention(ctx) }()
```

and after the existing `editorDone` select:

```go
	select {
	case <-retentionDone:
	case <-waitCtx.Done():
		log.Print("Retention shutdown timed out")
	}
```

- [ ] **Step 6: Run everything**

Run: `go vet ./... && go test -race ./internal/drive ./internal/api ./cmd/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/drive internal/api/drive_retention.go cmd/server/main.go
git commit -m "drive: opt-in workspace retention with an hourly worker"
```

---

### Task 8: HTTP routes

**Files:**
- Modify: `internal/api/drive_handlers.go:22-49` (routes) and add handlers
- Create: `internal/api/drive_files_test.go`

**Interfaces:**
- Consumes: Tasks 2–7.
- Produces routes (all JSON, `decodeDrive` bodies, errors through `driveError`):

| Method | Path | Body | Success |
|---|---|---|---|
| PUT | `/api/drive/files/{id}/location` | `{revision, workspace, parent, name}` | 200 file |
| POST | `/api/drive/files/{id}/copy` | `{workspace, parent, name}` | 201 file |
| DELETE | `/api/drive/files/{id}` | none | 200 `{"purged":true}` |
| DELETE | `/api/drive/files/{id}/versions/{revision}` | none | 200 `{"purged":true}` |
| PUT | `/api/drive/folders/{id}/location` | `{parent, name}` | 200 folder |
| PUT | `/api/drive/folders/{id}/trash` | `{trashed}` | 200 `{"applied":true}` |
| DELETE | `/api/drive/folders/{id}` | none | 200 `{"purged":true}` |
| PUT | `/api/drive/workspaces/{id}/retention` | `{trash_days, keep_versions}` | 200 `{"applied":true}`; `requireAdmin` |
| GET | `/api/drive/workspaces/{id}/folders?trash=true` | none | trashed folders (from Task 2) |

- [ ] **Step 1: Write the failing test**

```go
// internal/api/drive_files_test.go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func send(t *testing.T, srv *api.Server, method, path, body string, cookie *http.Cookie, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
		req.Header.Set(auth.HeaderCSRF, "test-csrf")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestFileManagementRoutes(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	boss := loginAs(t, srv, st, "boss", "admin")
	mover := loginAs(t, srv, st, "mover", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "movers", DisplayName: "Movers", Members: []string{"usr_mover"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_boss", "Team", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_boss", w.ID, "movers", "manager"); err != nil {
		t.Fatal(err)
	}
	blobCount := func() int {
		entries, _ := os.ReadDir(filepath.Join(cfg.Database.DataDir, "blobs"))
		n := 0
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				n++
			}
		}
		return n
	}
	var file struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Revision int64  `json:"revision"`
	}
	out := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=a.txt&revision=0", "hello", mover, "")
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &file) != nil {
		t.Fatalf("upload %d %s", out.Code, out.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"revision": 1, "workspace": w.ID, "parent": "", "name": "renamed.txt"})
	if out = send(t, srv, "PUT", "/api/drive/files/"+file.ID+"/location", string(body), mover, ""); out.Code != 200 || !strings.Contains(out.Body.String(), "renamed.txt") {
		t.Fatalf("rename %d %s", out.Code, out.Body.String())
	}
	body, _ = json.Marshal(map[string]any{"workspace": w.ID, "parent": "", "name": "copy.txt"})
	if out = send(t, srv, "POST", "/api/drive/files/"+file.ID+"/copy", string(body), mover, ""); out.Code != 201 {
		t.Fatalf("copy %d %s", out.Code, out.Body.String())
	}
	if out = send(t, srv, "DELETE", "/api/drive/files/"+file.ID, "", mover, ""); out.Code != 409 {
		t.Fatalf("purge of a live file: %d", out.Code)
	}
	if out = send(t, srv, "PUT", "/api/drive/files/"+file.ID+"/trash", `{"revision":1,"trashed":true}`, mover, ""); out.Code != 200 {
		t.Fatalf("trash %d", out.Code)
	}
	token := strings.Repeat("m", 64)
	if _, err = st.Drive().CreateServiceToken(ctx, "usr_mover", w.ID, "Automation", "editor", token); err != nil {
		t.Fatal(err)
	}
	if out = send(t, srv, "DELETE", "/api/drive/files/"+file.ID, "", nil, token); out.Code != 403 {
		t.Fatalf("service token purged: %d", out.Code)
	}
	if out = send(t, srv, "DELETE", "/api/drive/files/"+file.ID, "", mover, ""); out.Code != 200 {
		t.Fatalf("purge %d %s", out.Code, out.Body.String())
	}
	if n := blobCount(); n != 1 {
		t.Fatalf("shared blob removed while the copy still uses it: %d", n)
	}
	var folder struct{ ID string }
	out = send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/folders", `{"parent":"","name":"Old"}`, mover, "")
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &folder) != nil {
		t.Fatalf("folder %d %s", out.Code, out.Body.String())
	}
	if out = send(t, srv, "PUT", "/api/drive/folders/"+folder.ID+"/location", `{"parent":"","name":"Archive"}`, mover, ""); out.Code != 200 {
		t.Fatalf("folder rename %d", out.Code)
	}
	if out = send(t, srv, "PUT", "/api/drive/folders/"+folder.ID+"/trash", `{"trashed":true}`, mover, ""); out.Code != 200 {
		t.Fatalf("folder trash %d", out.Code)
	}
	if out = send(t, srv, "GET", "/api/drive/workspaces/"+w.ID+"/folders?trash=true", "", mover, ""); !strings.Contains(out.Body.String(), "Archive") {
		t.Fatalf("trashed folders %s", out.Body.String())
	}
	if out = send(t, srv, "DELETE", "/api/drive/folders/"+folder.ID, "", mover, ""); out.Code != 200 {
		t.Fatalf("folder purge %d", out.Code)
	}
	if out = send(t, srv, "PUT", "/api/drive/workspaces/"+w.ID+"/retention", `{"trash_days":30,"keep_versions":10}`, mover, ""); out.Code != 403 {
		t.Fatalf("non-admin set retention: %d", out.Code)
	}
	if out = send(t, srv, "PUT", "/api/drive/workspaces/"+w.ID+"/retention", `{"trash_days":30,"keep_versions":10}`, boss, ""); out.Code != 200 {
		t.Fatalf("retention %d %s", out.Code, out.Body.String())
	}
	if out = send(t, srv, "PUT", "/api/drive/workspaces/"+w.ID+"/retention", `{"trash_days":30,"keep_versions":10,"extra":1}`, boss, ""); out.Code != 400 {
		t.Fatalf("unknown field accepted: %d", out.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api -run TestFileManagementRoutes -v`
Expected: FAIL: the rename returns the SPA fallback or 405, not 200.

- [ ] **Step 3: Register routes and add handlers**

In `routes` next to the existing drive routes:

```go
	s.mux.HandleFunc("PUT /api/drive/files/{id}/location", s.requireAuthenticated(s.driveMoveFile))
	s.mux.HandleFunc("POST /api/drive/files/{id}/copy", s.requireAuthenticated(s.driveCopyFile))
	s.mux.HandleFunc("DELETE /api/drive/files/{id}", s.requireAuthenticated(s.drivePurgeFile))
	s.mux.HandleFunc("DELETE /api/drive/files/{id}/versions/{revision}", s.requireAuthenticated(s.drivePurgeVersion))
	s.mux.HandleFunc("PUT /api/drive/folders/{id}/location", s.requireAuthenticated(s.driveMoveFolder))
	s.mux.HandleFunc("PUT /api/drive/folders/{id}/trash", s.requireAuthenticated(s.driveFolderTrash))
	s.mux.HandleFunc("DELETE /api/drive/folders/{id}", s.requireAuthenticated(s.drivePurgeFolder))
	s.mux.HandleFunc("PUT /api/drive/workspaces/{id}/retention", s.requireAdmin(s.driveRetention))
```

Handlers (add `log` to the imports if it is missing):

```go
func (s *Server) driveMoveFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision  int64  `json:"revision"`
		Workspace string `json:"workspace"`
		Parent    string `json:"parent"`
		Name      string `json:"name"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().MoveFile(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Revision, drive.Location{Workspace: in.Workspace, Parent: in.Parent, Name: in.Name})
	if err != nil {
		s.driveError(w, err)
		return
	}
	// A move to another workspace can take away someone's access to an open document.
	if _, err := s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
func (s *Server) driveCopyFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Workspace string `json:"workspace"`
		Parent    string `json:"parent"`
		Name      string `json:"name"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().CopyFile(r.Context(), s.driveUser(r).ID, r.PathValue("id"), drive.Location{Workspace: in.Workspace, Parent: in.Parent, Name: in.Name})
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, out)
}
func (s *Server) driveMoveFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().MoveFolder(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Parent, in.Name)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
func (s *Server) driveFolderTrash(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Trashed bool `json:"trashed"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	if err := s.store.Drive().SetFolderTrash(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Trashed); err != nil {
		s.driveError(w, err)
		return
	}
	if _, err := s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"applied": true})
}
func (s *Server) drivePurgeFile(w http.ResponseWriter, r *http.Request) {
	blobs, err := s.store.Drive().PurgeFile(r.Context(), s.driveUser(r).ID, r.PathValue("id"))
	s.purged(w, blobs, err)
}
func (s *Server) drivePurgeFolder(w http.ResponseWriter, r *http.Request) {
	blobs, err := s.store.Drive().PurgeFolder(r.Context(), s.driveUser(r).ID, r.PathValue("id"))
	s.purged(w, blobs, err)
}
func (s *Server) drivePurgeVersion(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseInt(r.PathValue("revision"), 10, 64)
	if err != nil || revision < 1 {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	blobs, err := s.store.Drive().PurgeVersion(r.Context(), s.driveUser(r).ID, r.PathValue("id"), revision)
	s.purged(w, blobs, err)
}

// purged removes blobs only after their metadata is gone; a failed removal costs disk space,
// never a version without bytes.
func (s *Server) purged(w http.ResponseWriter, blobs []string, err error) {
	if err != nil {
		s.driveError(w, err)
		return
	}
	if err = drive.RemoveBlobs(s.blobRoot(), blobs); err != nil {
		log.Printf("purged blob cleanup: %v", err)
	}
	s.writeJSON(w, 200, map[string]bool{"purged": true})
}
func (s *Server) driveRetention(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TrashDays    int `json:"trash_days"`
		KeepVersions int `json:"keep_versions"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	if err := s.store.Drive().SetRetention(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.TrashDays, in.KeepVersions); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"applied": true})
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/api ./internal/drive && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Smoke check**

Run: `go build -o .browser/server ./cmd/server && scripts/smoke-test.sh`
Expected: PASS (unchanged boundaries).

- [ ] **Step 6: Commit**

```bash
git add internal/api
git commit -m "api: routes for move, copy, folder trash, purge and retention"
```

---

### Task 9: Drive client module, folder rows and breadcrumbs

**Files:**
- Create: `web/src/pages/drive/client.ts`
- Create: `web/src/pages/drive/client.test.ts`
- Modify: `web/src/pages/Drive.tsx` (import helpers from `client.ts`; folder rows; breadcrumb; folder actions)
- Modify: `web/src/styles/theme.css` (`.drive-breadcrumb`, `.drive-folder-name`)

**Interfaces:**
- Produces from `client.ts`: the types `Workspace` (adds `trash_days`, `keep_versions`), `DriveFile`, `Folder` (adds `trashed`), `Grant`, `Group`, `Version`; the guards `workspace`, `file`, `folder`, `grant`, `group`, `version`, `record`; `response`, `list`, `change`, `remove(url): Promise<unknown>` (DELETE through `secureFetch`), `bytes`; and `breadcrumb(folders: Folder[], id: string): Folder[]` (root first, stops on a cycle).
- `Drive.tsx` re-exports `Workspace` so `App.tsx` imports keep working.

- [ ] **Step 1: Write the failing test**

```ts
// web/src/pages/drive/client.test.ts
import { describe, expect, it } from 'vitest';
import { breadcrumb, workspace, type Folder } from './client';

const f = (id: string, parent: string): Folder => ({ id, parent, name: id.toUpperCase(), trashed: false });

describe('breadcrumb', () => {
  it('walks from the root to the folder', () => {
    expect(breadcrumb([f('a', ''), f('b', 'a'), f('c', 'b')], 'c').map(x => x.id)).toEqual(['a', 'b', 'c']);
  });
  it('is empty at the workspace root and for unknown folders', () => {
    expect(breadcrumb([f('a', '')], '')).toEqual([]);
    expect(breadcrumb([f('a', '')], 'missing')).toEqual([]);
  });
  it('stops on a cycle instead of looping', () => {
    expect(breadcrumb([f('a', 'b'), f('b', 'a')], 'a').length).toBeLessThanOrEqual(2);
  });
});

describe('workspace guard', () => {
  it('requires retention fields', () => {
    const base = { id: 'w', name: 'W', role: 'manager', kind: 'shared', quota: 1, used: 0 };
    expect(workspace(base)).toBe(false);
    expect(workspace({ ...base, trash_days: 0, keep_versions: 0 })).toBe(true);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/pages/drive/client.test.ts`
Expected: FAIL: `Failed to resolve import "./client"`.

- [ ] **Step 3: Create `client.ts`**

Move lines 4–20 of `Drive.tsx` (types, guards, `response`, `list`, `change`, `bytes`) into `web/src/pages/drive/client.ts`, export each, change the import to `import { secureFetch } from '../../api';`, and then:

```ts
export type Workspace = { id: string; name: string; role: string; kind: 'personal' | 'shared'; quota: number; used: number; trash_days: number; keep_versions: number };
export type Folder = { id: string; name: string; parent: string; trashed: boolean };
// workspace guard additionally checks: typeof v.trash_days === 'number' && typeof v.keep_versions === 'number'
// folder guard additionally checks: typeof v.trashed === 'boolean'

export async function remove(url: string): Promise<unknown> { return response(await secureFetch(url, { method: 'DELETE' })); }

export function breadcrumb(folders: Folder[], id: string): Folder[] {
  const byID = new Map(folders.map(f => [f.id, f]));
  const out: Folder[] = [];
  const seen = new Set<string>();
  for (let at = byID.get(id); at && !seen.has(at.id); at = byID.get(at.parent)) { seen.add(at.id); out.unshift(at); }
  return out;
}
```

In `Drive.tsx`: `import { ... } from './drive/client'` and `export type { Workspace } from './drive/client';`.

- [ ] **Step 4: Replace the folder `<select>` with a breadcrumb and folder rows**

Replace the `drive-folder-filter` label with:

```tsx
<nav className="drive-breadcrumb" aria-label="Folder path">
  <button className="btn-link" onClick={() => setParent('')} aria-current={parent === '' ? 'page' : undefined}>{current.name}</button>
  {breadcrumb(folders, parent).map(f => <span key={f.id}> / <button className="btn-link" onClick={() => setParent(f.id)} aria-current={f.id === parent ? 'page' : undefined}>{f.name}</button></span>)}
</nav>
```

In the table body, before file rows and only when `!trash`, render one row per `folders.filter(d => d.parent === parent)`:

```tsx
<tr key={`folder-${d.id}`}>
  <td><button className="drive-folder-name btn-link" onClick={() => setParent(d.id)}>{d.name}/</button></td>
  <td>Folder</td><td>—</td>
  <td>{canEdit && <details className="drive-file-menu"><summary className="btn-secondary" aria-label={`Actions for folder ${d.name}`}>Actions</summary><div className="drive-file-actions">
    <button className="btn-secondary" onClick={() => setDialog({ mode: 'rename', item: { kind: 'folder', folder: d } })}>Rename</button>
    <button className="btn-secondary" onClick={() => setDialog({ mode: 'move', item: { kind: 'folder', folder: d } })}>Move</button>
    <button className="btn-secondary" disabled={busy} onClick={() => void run(() => change(`/api/drive/folders/${d.id}/trash`, 'PUT', { trashed: true }))}>Move to trash</button>
  </div></details>}</td>
</tr>
```

`setDialog` comes from Task 10. Until then, render only the trash button and add Rename/Move in Task 10. The empty-state check must count folders too: `files.filter(...).length + visibleFolders.length === 0`.

Add to `theme.css`, next to `.drive-file-menu`:

```css
.drive-breadcrumb { display: flex; flex-wrap: wrap; gap: .25rem; align-items: center; }
.drive-breadcrumb [aria-current="page"] { font-weight: 600; }
.drive-folder-name { font-weight: 600; }
```

- [ ] **Step 5: Run tests and build**

Run: `cd web && npm test && npm run build`
Expected: PASS; build succeeds with no unused-import errors.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "web: drive client module, folder rows and breadcrumb navigation"
```

---

### Task 10: Rename, move and copy dialog

**Files:**
- Create: `web/src/pages/drive/MoveDialog.tsx`
- Create: `web/src/pages/drive/MoveDialog.test.tsx`
- Modify: `web/src/pages/Drive.tsx` (dialog state; file and folder menu items)

**Interfaces:**
- Consumes: `client.ts` (Task 9); routes from Task 8.
- Produces:

```ts
export type Item = { kind: 'file'; file: DriveFile } | { kind: 'folder'; folder: Folder };
export type Mode = 'rename' | 'move' | 'copy';
export function MoveDialog(props: { mode: Mode; item: Item; workspaceID: string; workspaces: Workspace[]; onClose: () => void; onDone: () => void }): JSX.Element;
```

Folders move only within their workspace, so the workspace picker is hidden for folders. Targets are workspaces where the role is `editor` or `manager`. A folder can't be moved into itself or a descendant; the UI hides those options and the server enforces it.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/pages/drive/MoveDialog.test.tsx
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MoveDialog } from './MoveDialog';

const workspaces = [
  { id: 'w1', name: 'My files', role: 'manager', kind: 'personal' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0 },
  { id: 'w2', name: 'Team', role: 'editor', kind: 'shared' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0 },
  { id: 'w3', name: 'Readonly', role: 'reader', kind: 'shared' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0 },
];
const file = { id: 'f1', name: 'a.docx', parent: '', revision: 3, size: 1, trashed: false };

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe('MoveDialog', () => {
  it('moves a file into a folder of another workspace', async () => {
    const calls: [string, RequestInit | undefined][] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push([url, init]);
      const body = url.endsWith('/w2/folders') ? [{ id: 'd2', name: 'Reports', parent: '', trashed: false }] : url.endsWith('/w1/folders') ? [] : { ...file, workspace: 'w2', parent: 'd2' };
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }));
    const done = vi.fn();
    render(<MoveDialog mode="move" item={{ kind: 'file', file }} workspaceID="w1" workspaces={workspaces} onClose={() => {}} onDone={done} />);
    expect(screen.queryByRole('option', { name: 'Readonly' })).toBeNull();
    fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: 'w2' } });
    await screen.findByRole('option', { name: 'Reports' });
    fireEvent.change(screen.getByLabelText('Folder'), { target: { value: 'd2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Move' }));
    await waitFor(() => expect(done).toHaveBeenCalled());
    const [url, init] = calls[calls.length - 1];
    expect(url).toBe('/api/drive/files/f1/location');
    expect(init?.method).toBe('PUT');
    expect(JSON.parse(String(init?.body))).toEqual({ revision: 3, workspace: 'w2', parent: 'd2', name: 'a.docx' });
  });

  it('hides a folder and its descendants as move targets', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify([
      { id: 'a', name: 'A', parent: '', trashed: false },
      { id: 'b', name: 'B', parent: 'a', trashed: false },
      { id: 'c', name: 'C', parent: '', trashed: false },
    ]), { status: 200 })));
    render(<MoveDialog mode="move" item={{ kind: 'folder', folder: { id: 'a', name: 'A', parent: '', trashed: false } }} workspaceID="w1" workspaces={workspaces} onClose={() => {}} onDone={() => {}} />);
    await screen.findByRole('option', { name: 'C' });
    expect(screen.queryByRole('option', { name: 'B' })).toBeNull();
    expect(screen.queryByLabelText('Workspace')).toBeNull();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/pages/drive/MoveDialog.test.tsx`
Expected: FAIL: `Failed to resolve import "./MoveDialog"`.

- [ ] **Step 3: Implement**

```tsx
// web/src/pages/drive/MoveDialog.tsx
import { useEffect, useRef, useState } from 'react';
import { change, folder, list, type DriveFile, type Folder, type Workspace } from './client';

export type Item = { kind: 'file'; file: DriveFile } | { kind: 'folder'; folder: Folder };
export type Mode = 'rename' | 'move' | 'copy';

function descendants(folders: Folder[], root: string): Set<string> {
  const out = new Set([root]);
  for (let grew = true; grew;) { grew = false; for (const f of folders) if (!out.has(f.id) && out.has(f.parent)) { out.add(f.id); grew = true; } }
  return out;
}

export function MoveDialog({ mode, item, workspaceID, workspaces, onClose, onDone }: { mode: Mode; item: Item; workspaceID: string; workspaces: Workspace[]; onClose: () => void; onDone: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const original = item.kind === 'file' ? item.file : item.folder;
  const [name, setName] = useState(original.name);
  const [target, setTarget] = useState(workspaceID);
  const [parent, setParent] = useState(original.parent);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  // jsdom lacks showModal; opening by attribute keeps the form reachable in unit tests.
  useEffect(() => { const d = ref.current; if (d && !d.open) { if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', ''); } }, []);
  useEffect(() => { if (mode === 'rename') return; void list(`/api/drive/workspaces/${target}/folders`, folder).then(setFolders).catch(e => setError(e instanceof Error ? e.message : 'Folders unavailable')); }, [mode, target]);
  const hidden = item.kind === 'folder' ? descendants(folders, item.folder.id) : new Set<string>();
  const verb = { rename: 'Rename', move: 'Move', copy: 'Copy' }[mode];
  async function submit() {
    setBusy(true); setError('');
    try {
      if (item.kind === 'folder') await change(`/api/drive/folders/${item.folder.id}/location`, 'PUT', { parent, name });
      else if (mode === 'copy') await change(`/api/drive/files/${item.file.id}/copy`, 'POST', { workspace: target, parent, name });
      else await change(`/api/drive/files/${item.file.id}/location`, 'PUT', { revision: item.file.revision, workspace: target, parent, name });
      onDone();
    } catch (e) { setError(e instanceof Error ? e.message : `${verb} failed`); } finally { setBusy(false); }
  }
  return <dialog ref={ref} className="drive-document-dialog" aria-labelledby="move-title" onCancel={onClose}>
    <form onSubmit={e => { e.preventDefault(); void submit(); }}>
      <h2 id="move-title">{verb} {original.name}</h2>
      <label>Name<input autoFocus required value={name} onChange={e => setName(e.target.value)} /></label>
      {mode !== 'rename' && item.kind === 'file' && <label>Workspace<select value={target} onChange={e => { setTarget(e.target.value); setParent(''); }}>{workspaces.filter(w => w.role === 'editor' || w.role === 'manager').map(w => <option key={w.id} value={w.id}>{w.name}</option>)}</select></label>}
      {mode !== 'rename' && <label>Folder<select value={parent} onChange={e => setParent(e.target.value)}><option value="">Workspace root</option>{folders.filter(f => !hidden.has(f.id)).map(f => <option key={f.id} value={f.id}>{f.name}</option>)}</select></label>}
      {error && <p role="alert">{error}</p>}
      <div className="drive-dialog-actions"><button type="button" className="btn-secondary" onClick={onClose}>Cancel</button><button disabled={busy || !name.trim()}>{verb}</button></div>
    </form>
  </dialog>;
}
```

In `Drive.tsx`: `const [dialog, setDialog] = useState<{ mode: Mode; item: Item } | null>(null);` and render `{dialog && current && <MoveDialog {...dialog} workspaceID={current.id} workspaces={workspaces} onClose={() => setDialog(null)} onDone={() => { setDialog(null); void run(reload); }} />}`. In the file action menu, add (for `canEdit && !f.trashed`) Rename, Move and Copy buttons calling `setDialog({ mode, item: { kind: 'file', file: f } })`. Add Copy for readers too: they can copy into a workspace they edit. Add Rename and Move to the folder menu from Task 9.

- [ ] **Step 4: Run tests and build**

Run: `cd web && npm test && npm run build`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "web: rename, move and copy dialog for files and folders"
```

---

### Task 11: Trash view, permanent delete and retention settings

**Files:**
- Modify: `web/src/pages/Drive.tsx`
- Create: `web/browser/file-management.spec.mjs`

**Interfaces:**
- Consumes: `remove`, routes from Task 8.
- UI contract:
  - The trash view lists trashed folders (`/folders?trash=true`) and trashed files.
  - Every row has Restore.
  - Managers also get Delete permanently (`window.confirm`, then DELETE) and an Empty trash button. Empty trash purges top-level trashed folders and every trashed file whose parent isn't a trashed folder, in sequence; it stops on the first error and shows it.
  - Version history gives managers Delete version on non-current versions.
  - Admins see a Retention form in Workspace settings: Empty trash after N days and Keep the newest N versions, with "0 keeps everything" helper text, sent as `PUT /retention`.

- [ ] **Step 1: Write the failing browser test**

```js
// web/browser/file-management.spec.mjs
import { test, expect } from '@playwright/test';

async function signIn(page) {
  await page.goto('/');
  expect((await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'BrowserUpdated456!' } })).ok()).toBe(true);
  const csrf = (await page.context().cookies()).find(c => c.name === 'ky_csrf').value;
  return { 'X-CSRF-Token': csrf };
}

test('rename, move, trash a folder, restore it and delete it permanently', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  await page.goto('/');
  const spaces = await (await page.request.get('/api/drive/workspaces')).json();
  const mine = spaces.find(w => w.kind === 'personal');
  const tag = testInfo.project.name.replace(/\W/g, '');
  const folderName = `Box ${tag}`;
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/folders`, { headers, data: { parent: '', name: folderName } })).ok()).toBe(true);
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/documents`, { headers, data: { name: `note ${tag}`, kind: 'markdown', parent: '' } })).ok()).toBe(true);
  await page.reload();

  await page.getByRole('button', { name: `Actions for note ${tag}.md` }).click();
  await page.getByRole('button', { name: 'Move', exact: true }).click();
  await page.getByLabel('Name').fill(`moved ${tag}.md`);
  await page.getByLabel('Folder').selectOption({ label: folderName });
  await page.getByRole('dialog').getByRole('button', { name: 'Move', exact: true }).click();
  await page.getByRole('button', { name: `${folderName}/` }).click();
  await expect(page.getByRole('link', { name: `moved ${tag}.md` })).toBeVisible();

  await page.getByRole('navigation', { name: 'Folder path' }).getByRole('button', { name: mine.name }).click();
  await page.getByRole('button', { name: `Actions for folder ${folderName}` }).click();
  await page.getByRole('button', { name: 'Move to trash' }).click();
  await expect(page.getByRole('button', { name: `${folderName}/` })).toHaveCount(0);

  await page.getByRole('button', { name: 'Trash', exact: true }).click();
  await page.getByRole('row', { name: new RegExp(folderName) }).getByRole('button', { name: 'Restore', exact: true }).click();
  await page.getByRole('button', { name: 'Back to files' }).click();
  await expect(page.getByRole('button', { name: `${folderName}/` })).toBeVisible();

  await page.getByRole('button', { name: `Actions for folder ${folderName}` }).click();
  await page.getByRole('button', { name: 'Move to trash' }).click();
  await page.getByRole('button', { name: 'Trash', exact: true }).click();
  page.once('dialog', d => d.accept());
  await page.getByRole('row', { name: new RegExp(folderName) }).getByRole('button', { name: 'Delete permanently' }).click();
  await expect(page.getByRole('row', { name: new RegExp(folderName) })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go build -o .browser/server ./cmd/server && cd web && npm run build && npx playwright test browser/file-management.spec.mjs`
Expected: FAIL: no `Restore` button on a folder row in trash.

- [ ] **Step 3: Implement the trash view**

In `reload`, load trashed folders when `trash` is on: `list(\`/api/drive/workspaces/${selected}/folders?trash=${trash}\`, folder)`. When `trash` is on, render one row per trashed folder (name, "Folder", "—", actions), then all trashed files (no parent filter, as today). Actions:

```tsx
const canPurge = current?.role === 'manager';
const purge = (url: string, label: string) => { if (window.confirm(`Delete ${label} permanently? This cannot be undone.`)) void run(() => remove(url)); };
// folder row actions in trash:
<button className="btn-secondary" disabled={busy} onClick={() => void run(() => change(`/api/drive/folders/${d.id}/trash`, 'PUT', { trashed: false }))}>Restore</button>
{canPurge && <button className="btn-secondary" disabled={busy} onClick={() => purge(`/api/drive/folders/${d.id}`, d.name)}>Delete permanently</button>}
// file row actions in trash: keep "Restore from trash" (rename its label to "Restore"), add:
{canPurge && f.trashed && <button className="btn-secondary" disabled={busy} onClick={() => purge(`/api/drive/files/${f.id}`, f.name)}>Delete permanently</button>}
```

Empty trash (managers, trash view toolbar):

```tsx
{trash && canPurge && <button className="btn-secondary" disabled={busy} onClick={() => {
  if (!window.confirm('Empty the trash? Everything in it is deleted permanently.')) return;
  const trashedFolders = new Set(folders.map(d => d.id));
  void run(async () => {
    for (const d of folders.filter(d => !trashedFolders.has(d.parent))) await remove(`/api/drive/folders/${d.id}`);
    for (const f of files.filter(f => !trashedFolders.has(f.parent))) await remove(`/api/drive/files/${f.id}`);
  });
}}>Empty trash</button>}
```

In the version list: `{canPurge && v.revision !== history.file.revision && <button disabled={busy} onClick={() => { if (window.confirm(\`Delete version ${v.revision} permanently?\`)) void run(async () => { await remove(\`/api/drive/files/${history.file.id}/versions/${v.revision}\`); setHistory({ ...history, versions: history.versions.filter(x => x.revision !== v.revision) }); }); }}>Delete version</button>}`.

Retention form (admin, inside Workspace settings, under the quota form), with state initialised from `current`:

```tsx
const [trashDays, setTrashDays] = useState(0);
const [keepVersions, setKeepVersions] = useState(0);
useEffect(() => { setTrashDays(current?.trash_days ?? 0); setKeepVersions(current?.keep_versions ?? 0); }, [current?.id, current?.trash_days, current?.keep_versions]);
// ...
{admin && <form className="drive-toolbar" onSubmit={e => { e.preventDefault(); void run(() => change(`/api/drive/workspaces/${selected}/retention`, 'PUT', { trash_days: trashDays, keep_versions: keepVersions })); }}>
  <h3>Retention</h3><p>0 keeps everything. Deleted items cannot be recovered from the drive, only from backups.</p>
  <label>Empty trash after (days)<input type="number" min="0" max="3650" required value={trashDays} onChange={e => setTrashDays(e.target.valueAsNumber)} /></label>
  <label>Keep newest versions<input type="number" min="0" max="1000" required value={keepVersions} onChange={e => setKeepVersions(e.target.valueAsNumber)} /></label>
  <button disabled={busy}>Save retention</button>
</form>}
```

Update the empty-trash text: "Deleted items appear here until restored or deleted permanently."

- [ ] **Step 4: Run all web checks**

Run: `cd web && npm test && npm run build && cd .. && go build -o .browser/server ./cmd/server && cd web && npm run test:browser`
Expected: PASS across all Playwright projects (light/dark, 390px/1280px).

- [ ] **Step 5: Commit**

```bash
git add web
git commit -m "web: trash view with folders, permanent delete and retention settings"
```

---

### Task 12: Contracts, docs and full verification

**Files:**
- Modify: `AGENTS.md` (Local Contracts retention line; User Preferences)
- Modify: `internal/drive/AGENTS.md`, `internal/api/AGENTS.md`, `web/AGENTS.md`, `docs/AGENTS.md`
- Modify: `README.md` (operator note on retention and purge), `docs/ACCEPTANCE.md` Boundaries line on blob collection
- Modify: `../DRIVE_IMPLEMENTATION_PLAN.md` if it still says reclamation is out of scope

- [ ] **Step 1: Find stale statements**

Run: `grep -rn -i -e "garbage collection" -e "pruning" -e "automatic blob" -e "Retain all referenced" --include='*.md' . ..`
Expected: the hits to rewrite (KyDrive `AGENTS.md` Local Contracts, `docs/ACCEPTANCE.md` Boundaries, possibly README).

- [ ] **Step 2: Rewrite the contracts**

- `AGENTS.md` Local Contracts: replace "Retain all referenced versions/blobs and Restic snapshots. No automatic destructive garbage collection or bulk pruning." with: "Versions and blobs are removed only by an explicit manager purge of trashed items or old versions, or by an admin-enabled per-workspace retention policy (off by default); both are audited. Metadata commits before blob removal, and blobs are removed only when the purging transaction found them unreferenced. Restic snapshots are never pruned automatically."
- `internal/drive/AGENTS.md` Local Contracts, add:
  - "`schema.go` is an append-only migration list recorded in `drive_schema`; never edit a shipped entry."
  - "Trash is batched: trashing a folder trashes its live subtree with one `trash_batch`; restore brings back that batch only; an item whose parent is still trashed returns to the root. Trashed folders cannot receive items."
  - "Moves never create versions; cross-workspace file moves need editor rights on both sides and target quota for every version; folder moves stay in their workspace. Copies share the immutable blob and count against target quota."
  - "Purge needs rank 3 and a trashed item (or a non-current version) with no undelivered editor session. Copy and restore read reused blobs inside their publishing transaction."
  - "Retention: `trash_days` and `keep_versions` per workspace, 0 = off, admin-set, applied hourly by the `system` actor, skipping items still open in an editor."
  - Verification: add `schema_test.go`, `trash_test.go`, `move_test.go`, `purge_test.go`, `retention_test.go`.
- `internal/api/AGENTS.md`: add the Task 8 route table rows and "`RunRetention` is a background loop like `RunEditorRevocations`; `cmd/server` waits for both before closing the store."
- `web/AGENTS.md`: replace the folder-select wording with breadcrumb and folder rows; add the move/copy/rename dialog, the trash view with folders, manager-only permanent delete and empty trash, and the admin retention form; add `drive/client.test.ts`, `drive/MoveDialog.test.tsx` and `browser/file-management.spec.mjs` under Verification.
- `docs/AGENTS.md` Ownership: "`superpowers/plans/` holds dated implementation plans; the roadmap file lists milestone decisions."
- `README.md`: a short "Retention and permanent delete" section stating defaults (off), who can purge, that backups still hold purged bytes until their own retention, and the known backup-run race from this plan's Review Focus.

- [ ] **Step 3: Full verification**

Run each and confirm it passes:

```bash
PATH=$HOME/.local/bin:$PATH go test -race ./...
go vet ./...
go mod verify
cd web && npm ci && npm test && npm run build && cd ..
go build -o .browser/server ./cmd/server && (cd web && npm run test:browser)
scripts/smoke-test.sh
python -m unittest discover -s deploy -p '*_test.py'
docker build --platform linux/amd64 -t kydrive-server:local .
```

Restic-backed tests need the `restic` executable on `PATH`; if it is missing, say so in the PR instead of claiming those tests passed.

- [ ] **Step 4: Upgrade rehearsal on a copy of pilot-shaped data**

Run: copy a disposable database produced by the previous release (for example, a backup-drill scratch database from `kydrive-server backup-drill`), start the new binary against the copy, and confirm `drive_schema` reaches version 1, existing folders and trash remain, and `GET /api/drive/workspaces` returns `trash_days: 0` everywhere. Do not run this against live pilot data.

- [ ] **Step 5: Commit**

```bash
git add AGENTS.md internal/*/AGENTS.md web/AGENTS.md docs README.md
git commit -m "docs: record file management, purge and retention contracts"
```

---

## Not in M1

- Folder copy and cross-workspace folder moves (M6 handover adds the whole-tree move).
- A retention default for personal workspaces (the admin UI shows retention for shared workspaces only).
- Step-up re-authentication for purge: the server has no step-up mechanism yet. Purge is manager-only, confirmed in the UI, audited, and recoverable from backups.
