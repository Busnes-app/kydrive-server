package drive_test

import (
	"context"
	"database/sql"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/qmark"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// The catalogue as either backend records it.

func tables(t *testing.T, db *sql.DB) []string {
	if qmark.Is(db) {
		return strs(t, db, `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE' ORDER BY table_name`)
	}
	return strs(t, db, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
}

// columnTypes are table's columns as "name type", the type as either backend stores it:
// int64, int32, text or bool.
func columnTypes(t *testing.T, db *sql.DB, table string) []string {
	if qmark.Is(db) {
		return strs(t, db, `SELECT column_name||' '||CASE data_type WHEN 'bigint' THEN 'int64' WHEN 'integer' THEN 'int32' WHEN 'boolean' THEN 'bool' ELSE data_type END FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? ORDER BY ordinal_position`, table)
	}
	return strs(t, db, `SELECT name||' '||CASE upper(type) WHEN 'INTEGER' THEN 'int64' WHEN 'BIGINT' THEN 'int64' WHEN 'BOOLEAN' THEN 'bool' ELSE lower(type) END FROM pragma_table_info(?) ORDER BY cid`, table)
}

func columns(t *testing.T, db *sql.DB, table string) []string {
	if qmark.Is(db) {
		return strs(t, db, `SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? ORDER BY ordinal_position`, table)
	}
	return strs(t, db, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
}

// indexes are table's named indexes, not the ones its constraints make.
func indexes(t *testing.T, db *sql.DB, table string) []string {
	if qmark.Is(db) {
		return strs(t, db, `SELECT i.relname FROM pg_index x JOIN pg_class i ON i.oid=x.indexrelid JOIN pg_class c ON c.oid=x.indrelid LEFT JOIN pg_constraint k ON k.conindid=x.indexrelid WHERE c.relname=? AND c.relnamespace=current_schema()::regnamespace AND k.oid IS NULL ORDER BY 1`, table)
	}
	return strs(t, db, `SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=? AND sql IS NOT NULL ORDER BY name`, table)
}

// triggers are the triggers on table ("" for every table) whose names match like.
func triggers(t *testing.T, db *sql.DB, table, like string) []string {
	if qmark.Is(db) {
		return strs(t, db, `SELECT g.tgname FROM pg_trigger g JOIN pg_class c ON c.oid=g.tgrelid WHERE NOT g.tgisinternal AND c.relnamespace=current_schema()::regnamespace AND (?='' OR c.relname=?) AND g.tgname LIKE ? ORDER BY 1`, table, table, like)
	}
	return strs(t, db, `SELECT name FROM sqlite_master WHERE type='trigger' AND (?='' OR tbl_name=?) AND name LIKE ? ORDER BY name`, table, table, like)
}

func dropTrigger(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	q := `DROP TRIGGER ` + name
	if qmark.Is(db) {
		var table string
		if err := db.QueryRow(`SELECT c.relname FROM pg_trigger g JOIN pg_class c ON c.oid=g.tgrelid WHERE g.tgname=? AND c.relnamespace=current_schema()::regnamespace`, name).Scan(&table); err != nil {
			t.Fatal(name, err)
		}
		q += ` ON ` + table
	}
	if _, err := db.Exec(q); err != nil {
		t.Fatal(q, err)
	}
}

// references reports whether child has a foreign key to parent.
func references(t *testing.T, db *sql.DB, child, parent string) bool {
	if testdb.Postgres() {
		return len(strs(t, db, `SELECT 1 FROM pg_constraint WHERE contype='f' AND conrelid=?::regclass AND confrelid=?::regclass`, child, parent)) > 0
	}
	return len(strs(t, db, `SELECT "table" FROM pragma_foreign_key_list(?) WHERE "table"=?`, child, parent)) > 0
}

// orgIDColumn is table's org_id: whether it is NOT NULL, and its default as SQL text.
func orgIDColumn(db *sql.DB, table string) (notNull bool, def string, err error) {
	var d sql.NullString
	if qmark.Is(db) {
		var nullable string
		err = db.QueryRow(`SELECT is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? AND column_name='org_id'`, table).Scan(&nullable, &d)
		return nullable == "NO", strings.TrimSuffix(d.String, "::text"), err
	}
	var n int
	err = db.QueryRow(`SELECT "notnull",dflt_value FROM pragma_table_info(?) WHERE name='org_id'`, table).Scan(&n, &d)
	return n == 1, d.String, err
}

// catalogue is a database's drive tables, each with its columns, indexes and triggers.
func catalogue(t *testing.T, db *sql.DB) map[string][]string {
	out := map[string][]string{}
	for _, table := range tables(t, db) {
		if !strings.HasPrefix(table, "drive_") && !slices.Contains([]string{"users", "groups", "audit_records", "group_members"}, table) {
			continue
		}
		var entry []string
		for _, c := range columnTypes(t, db, table) {
			if strings.HasPrefix(table, "drive_") || strings.HasPrefix(c, "org_id ") {
				entry = append(entry, "column "+c)
			}
		}
		for _, i := range indexes(t, db, table) {
			if strings.HasPrefix(i, "drive_") || i == "idx_users_kysignon_subject" {
				entry = append(entry, "index "+i)
			}
		}
		for _, g := range triggers(t, db, table, "%") {
			entry = append(entry, "trigger "+g)
		}
		slices.Sort(entry)
		out[table] = entry
	}
	return out
}

// postgresOnly are the PostgreSQL forms of what SQLite's one writer gives for free.
var postgresOnly = map[string][]string{"drive_changes": {"trigger drive_changes_in_order"}}

// A PostgreSQL drive has every table, column (of the same type: 64-bit where SQLite's INTEGER
// is), index and trigger a SQLite one has, and nothing else but postgresOnly.
func TestBackendsHaveTheSameColumns(t *testing.T) {
	if !testdb.Postgres() {
		t.Skip("compares a PostgreSQL drive with SQLite's")
	}
	ctx := context.Background()
	open := func(cfg config.DatabaseConfig) *sql.DB {
		st, err := store.Open(ctx, cfg, testdb.Key)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return testdb.Open(t, cfg)
	}
	pg := catalogue(t, open(testdb.Config(t)))
	lite := catalogue(t, open(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "lite.db")}))
	for table, extra := range postgresOnly {
		pg[table] = slices.DeleteFunc(pg[table], func(e string) bool { return slices.Contains(extra, e) })
	}
	delete(lite, "drive_name_scrub") // a SQLite upgrade's marker
	for _, table := range slices.Sorted(maps.Keys(lite)) {
		if !slices.Equal(pg[table], lite[table]) {
			t.Errorf("%s:\n postgres %v\n sqlite   %v", table, pg[table], lite[table])
		}
	}
	for table := range pg {
		if lite[table] == nil {
			t.Errorf("%s exists only on PostgreSQL", table)
		}
	}
}

// A schema entry appended for SQLite needs its PostgreSQL form in schema_postgres.go.
func TestPostgresSchemaIsCurrent(t *testing.T) {
	if drive.PostgresVersion != drive.SchemaVersion {
		t.Fatalf("PostgreSQL schema is at %d, SQLite at %d: add the new entries to schema_postgres.go", drive.PostgresVersion, drive.SchemaVersion)
	}
}

// Processes starting together on an empty PostgreSQL database all open it: one creates the
// schema while the others wait, then find it done.
func TestConcurrentFirstStartsOpen(t *testing.T) {
	if !testdb.Postgres() {
		t.Skip("SQLite opens one file with one writer")
	}
	cfg := testdb.Config(t)
	const n = 4
	errs := make(chan error, n)
	for range n {
		go func() {
			st, err := store.Open(context.Background(), cfg, testdb.Key)
			if err == nil {
				err = st.Close()
			}
			errs <- err
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
