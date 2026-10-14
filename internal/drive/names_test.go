package drive_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

var legacyNames = []string{"reports-plain", "Zeta-plain", "a-plain.txt", "B-plain.txt", "b-plain.txt", "é-plain.txt", "old-plain.txt"}

// legacyDB writes a populated database from before drive_schema, with plaintext names.
func legacyDB(t *testing.T) config.DatabaseConfig {
	t.Helper()
	cfg := testdb.Config(t)
	if cfg.Driver != "sqlite" {
		t.Skip("drive is SQLite-only")
	}
	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE drive_workspaces(id TEXT PRIMARY KEY,name TEXT NOT NULL,quota BIGINT NOT NULL CHECK(quota>0));
CREATE TABLE drive_folders(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,UNIQUE(workspace,parent,name));
CREATE TABLE drive_files(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,revision BIGINT NOT NULL,trashed BOOLEAN NOT NULL DEFAULT false);
CREATE TABLE drive_versions(file_id TEXT NOT NULL REFERENCES drive_files(id),revision BIGINT NOT NULL,blob TEXT NOT NULL,digest TEXT NOT NULL,size BIGINT NOT NULL CHECK(size>=0),created TEXT NOT NULL,PRIMARY KEY(file_id,revision));
INSERT INTO drive_workspaces VALUES('w','Legacy',1048576),('w2','Other',1048576);
INSERT INTO drive_folders VALUES('d','w','','reports-plain'),('z','w','','Zeta-plain'),('e','w2','','reports-plain');
INSERT INTO drive_files VALUES('a','w','','a-plain.txt',1,false),('B','w','','B-plain.txt',1,false),('b','w','','b-plain.txt',1,false),
 ('bd','w','d','b-plain.txt',1,false),('u','w','','é-plain.txt',1,false),('old','w','d','old-plain.txt',1,true),
 ('old2','w','d','old-plain.txt',1,false),('k','w2','','b-plain.txt',1,false);
INSERT INTO drive_versions SELECT id,1,'blob-'||id,'` + strings.Repeat("0", 64) + `',1,'2026-01-01T00:00:00Z' FROM drive_files;`)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// noPlaintext fails if any name survives in the database file or its WAL, including freed pages.
func noPlaintext(t *testing.T, dsn string, plain []string) {
	t.Helper()
	for _, path := range []string{dsn, dsn + "-wal"} {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range plain {
			if bytes.Contains(raw, []byte(name)) {
				t.Errorf("%s holds plaintext %q", path, name)
			}
		}
	}
}

func listNames(t *testing.T, d *drive.Store, workspace string) (files, folders []string) {
	t.Helper()
	ctx := context.Background()
	fs, err := d.Files(ctx, "worker", workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		files = append(files, f.Name)
	}
	ds, err := d.Folders(ctx, "worker", workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range ds {
		folders = append(folders, f.Name)
	}
	return
}

func TestNameMigrationEncryptsPopulatedDatabase(t *testing.T) {
	ctx := context.Background()
	cfg := legacyDB(t)
	var st store.Store
	for range 2 {
		if st != nil {
			st.Close()
		}
		var err error
		if st, err = store.Open(ctx, cfg, testdb.Key); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { st.Close() }()
	d := st.Drive()
	noPlaintext(t, cfg.DSN, legacyNames)
	for _, table := range []string{"drive_files", "drive_folders"} {
		rows, err := drive.RawNames(ctx, d, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			stored, mac, _ := strings.Cut(row, " ")
			if len(mac) != 64 || slices.Contains(legacyNames, stored) {
				t.Errorf("%s row not sealed: %q", table, row)
			}
		}
	}

	for _, u := range []*store.User{{ID: "admin", Username: "admin", Role: "admin", Status: "active"}, {ID: "worker", Username: "worker", Role: "user", Status: "active"}} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", Members: []string{"worker"}}, true); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"w", "w2"} {
		if err := d.Grant(ctx, "admin", w, "team", "editor"); err != nil {
			t.Fatal(err)
		}
	}

	// Bytewise order, as SQLite's BINARY collation gave.
	files, folders := listNames(t, d, "w")
	if want := []string{"B-plain.txt", "a-plain.txt", "b-plain.txt", "b-plain.txt", "old-plain.txt", "é-plain.txt"}; !slices.Equal(files, want) {
		t.Fatalf("files %q, want %q", files, want)
	}
	if want := []string{"Zeta-plain", "reports-plain"}; !slices.Equal(folders, want) {
		t.Fatalf("folders %q, want %q", folders, want)
	}

	root := t.TempDir()
	publish := func(workspace, parent, name string) error {
		_, err := d.Publish(ctx, "worker", drive.File{Workspace: workspace, Parent: parent, Name: name}, blob(t, root, name), 0)
		return err
	}
	for name, err := range map[string]error{
		"publish":    publish("w", "", "a-plain.txt"),
		"folder add": func() error { _, err := d.AddFolder(ctx, "worker", "w", "", "Zeta-plain"); return err }(),
		"move": func() error {
			_, err := d.MoveFile(ctx, "worker", "B", 1, drive.Location{Workspace: "w", Name: "b-plain.txt"})
			return err
		}(),
		"folder move":  func() error { _, err := d.MoveFolder(ctx, "worker", "z", "", "reports-plain"); return err }(),
		"file restore": d.SetTrash(ctx, "worker", "old", 1, false),
	} {
		if !errors.Is(err, drive.ErrConflict) {
			t.Errorf("%s of a taken name: %v", name, err)
		}
	}
	if err := d.SetFolderTrash(ctx, "worker", "z", true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddFolder(ctx, "worker", "w", "", "Zeta-plain"); err != nil {
		t.Fatalf("trashed name not reusable: %v", err)
	}
	if err := d.SetFolderTrash(ctx, "worker", "z", false); !errors.Is(err, drive.ErrConflict) {
		t.Errorf("folder restore onto a taken name: %v", err)
	}
	for name, err := range map[string]error{
		"other parent":    publish("w", "d", "a-plain.txt"),
		"other workspace": publish("w2", "", "a-plain.txt"),
		"other case":      publish("w", "", "A-plain.txt"),
		"folder in child": func() error { _, err := d.AddFolder(ctx, "worker", "w", "d", "Zeta-plain"); return err }(),
	} {
		if err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}

	st.Close()
	if other, err := store.Open(ctx, cfg, bytes.Repeat([]byte{8}, 32)); !errors.Is(err, drive.ErrWrongKey) {
		if err == nil {
			other.Close()
		}
		t.Fatalf("opened under the wrong key: %v", err)
	}
	// Past the open-time check, a row sealed under another key still fails to read.
	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM drive_key`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if st, err = store.Open(ctx, cfg, bytes.Repeat([]byte{8}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Drive().Files(ctx, "worker", "w", false); err == nil {
		t.Fatal("files listed under the wrong key")
	}
	if _, err = st.Drive().Folders(ctx, "worker", "w", false); err == nil {
		t.Fatal("folders listed under the wrong key")
	}
	if _, err = st.Drive().File(ctx, "worker", "a", 1); err == nil {
		t.Fatal("file read under the wrong key")
	}
}

func TestNameMigrationRefusesWithoutKey(t *testing.T) {
	ctx := context.Background()
	cfg := legacyDB(t)
	if st, err := store.Open(ctx, cfg, nil); err == nil {
		st.Close()
		t.Fatal("opened without a deployment key")
	}
	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err = db.QueryRow(`SELECT name FROM drive_files WHERE id='a'`).Scan(&name); err != nil || name != "a-plain.txt" {
		t.Fatalf("names touched without a key: %q %v", name, err)
	}
}

// Every write path that sets a name stores ciphertext only, and readers see plaintext.
func TestNewNamesAreSealed(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	folder, err := d.AddFolder(ctx, "worker", w.ID, "", "secret-folder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.MoveFolder(ctx, "worker", folder.ID, "", "secret-folder-renamed"); err != nil {
		t.Fatal(err)
	}
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "secret-file.txt"}, blob(t, root, "x"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.CopyFile(ctx, "worker", f.ID, drive.Location{Workspace: w.ID, Parent: folder.ID, Name: "secret-copy.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.MoveFile(ctx, "worker", f.ID, 1, drive.Location{Workspace: w.ID, Name: "secret-moved.txt"}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"drive_files", "drive_folders"} {
		rows, err := drive.RawNames(ctx, d, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if strings.Contains(row, "secret") {
				t.Errorf("%s stores a plaintext name: %q", table, row)
			}
		}
	}
	files, folders := listNames(t, d, w.ID)
	if !slices.Equal(files, []string{"secret-copy.txt", "secret-moved.txt"}) || !slices.Equal(folders, []string{"secret-folder-renamed"}) {
		t.Fatalf("read back %q %q", files, folders)
	}
}
