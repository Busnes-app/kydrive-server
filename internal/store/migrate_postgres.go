package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/qmark"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sys/unix"
)

// migrationTables is every table that holds data, in foreign-key order. Add a new table here;
// a migration refuses a schema with a table it does not list.
var migrationTables = []string{
	"drive_orgs", "drive_key", "server_settings",
	"users", "groups", "group_members", "sessions", "audit_records", "mfa_challenges",
	"sso_logouts", "sso_logout_tokens", "sso_subject_logouts",
	"drive_workspaces", "drive_personal_workspaces", "drive_grants", "drive_suite_groups",
	"drive_folders", "drive_files", "drive_versions", "drive_changes", "drive_change_floor",
	"drive_events", "drive_shares", "drive_links", "drive_link_users", "drive_service_tokens",
	"drive_editor_sessions", "drive_editor_documents", "drive_editor_saves",
	"drive_upload_sessions", "drive_upload_segments", "drive_blob_kyb1", "drive_blob_retire",
	"drive_app_passwords", "drive_devices", "drive_device_codes", "drive_view_copies",
	"drive_peer_settings", "drive_peer_folders", "drive_peer_tickets", "drive_peer_picks",
	"drive_peer_guest",
}

// schemaTables record schema versions; each side has its own, at the same versions.
var schemaTables = []string{"schema_migrations", "drive_schema", "drive_name_scrub"}

// openedTables are the ones a store open fills on an empty database. The copy replaces
// their rows with the source's.
var openedTables = []string{"drive_orgs", "drive_key", "server_settings"}

// MigrationReport is the rows copied per table: counts, never contents.
type MigrationReport struct {
	Rows      map[string]int64 `json:"rows"`
	CheckOnly bool             `json:"check_only"`
}

// LockSQLite takes the lock a server holds on its SQLite database for as long as it runs,
// so migrate-postgres never copies a database a server is writing. Closing the file
// (or the process exiting) releases it.
func LockSQLite(path string) (*os.File, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("another KyDrive process is using %s", path)
		}
		return nil, err
	}
	return f, nil
}

// MigrateToPostgres copies the single-tenant SQLite database at source into the empty
// PostgreSQL database dsn names, then checks every row arrived. key is the deployment key;
// the source must have been made with it. checkOnly does all of it and rolls the copy back.
// The source file is never written.
func MigrateToPostgres(ctx context.Context, source, dsn string, key []byte, checkOnly bool) (MigrationReport, error) {
	report := MigrationReport{Rows: map[string]int64{}, CheckOnly: checkOnly}
	lock, err := LockSQLite(source)
	if err != nil {
		return report, fmt.Errorf("stop the KyDrive server first: %w", err)
	}
	defer lock.Close()
	src, dir, err := upgradedSnapshot(ctx, source, key)
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(dir)
	defer src.Close()

	db, err := qmark.Open(dsn)
	if err != nil {
		return report, errors.New("cannot open the PostgreSQL target: check KY_MIGRATE_POSTGRES_DSN")
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return report, errors.New("cannot connect to the PostgreSQL target; check connectivity and permissions")
	}
	defer conn.Close()
	// A server starting on the target finishes first; checkTarget then finds what it wrote.
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(?)`, startLock); err != nil {
		return report, err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(?)`, startLock)
	if err := checkTarget(ctx, db); err != nil {
		return report, err
	}
	if _, err := newSQLStore(ctx, db, "postgres", key, drive.Tenancy{Mode: drive.TenancySingle}); err != nil {
		return report, fmt.Errorf("create the PostgreSQL schema: %w", err)
	}
	if err := copyAll(ctx, src.db, db, &report); err != nil {
		return report, err
	}
	return report, nil
}

// upgradedSnapshot is a private copy of source, brought to this release's schema, in dir
// beside it. The caller removes dir after closing the store.
func upgradedSnapshot(ctx context.Context, source string, key []byte) (_ *SQLStore, _ string, err error) {
	path, err := filepath.Abs(source)
	if err != nil {
		return nil, "", err
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("no SQLite database at %s", path)
	}
	// mode=ro: a mistyped path is refused, not created.
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	original, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, "", err
	}
	defer original.Close()
	var integrity string
	if err := original.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return nil, "", errors.Join(errors.New("the SQLite database failed its integrity check"), err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(path), ".postgres-migration-")
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(tmp))
		}
	}()
	snapshot := filepath.Join(tmp, "snapshot.db")
	if err := recoveryclient.SQLiteSnapshot(ctx, original, snapshot); err != nil {
		return nil, "", errors.New("cannot snapshot the SQLite database")
	}
	st, err := openSQLite(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: snapshot}, key)
	if err != nil {
		return nil, "", fmt.Errorf("upgrade the snapshot: %w", err)
	}
	return st.(*SQLStore), tmp, nil
}

// checkTarget refuses a target that holds anything but this schema, or any row a store
// open did not write: a server has used it.
func checkTarget(ctx context.Context, db *sql.DB) error {
	tables, err := names(ctx, db, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema()`)
	if err != nil {
		return errors.New("cannot read the PostgreSQL target's tables")
	}
	for _, table := range tables {
		if slices.Contains(schemaTables, table) || slices.Contains(openedTables, table) {
			continue
		}
		if !slices.Contains(migrationTables, table) {
			return fmt.Errorf("the PostgreSQL target has a table KyDrive does not know (%s); use an empty database", table)
		}
		var used bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+quote(table)+`)`).Scan(&used); err != nil || used {
			return errors.Join(fmt.Errorf("the PostgreSQL target already has data (%s); use an empty database", table), err)
		}
	}
	return nil
}

func copyAll(ctx context.Context, src, db *sql.DB, report *MigrationReport) error {
	for side, q := range map[*sql.DB]string{
		src: `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`,
		db:  `SELECT tablename FROM pg_tables WHERE schemaname=current_schema()`,
	} {
		tables, err := names(ctx, side, q)
		if err != nil {
			return err
		}
		tables = slices.DeleteFunc(tables, func(t string) bool { return slices.Contains(schemaTables, t) })
		if missing := setDiff(tables, migrationTables); len(missing) > 0 {
			return fmt.Errorf("migrate-postgres does not know tables %v: this release cannot migrate them", missing)
		}
		if missing := setDiff(migrationTables, tables); len(missing) > 0 {
			return fmt.Errorf("tables %v are missing", missing)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	quoted := make([]string, len(migrationTables))
	for i, t := range migrationTables {
		quoted[i] = quote(t)
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE `+strings.Join(quoted, ",")+` IN ACCESS EXCLUSIVE MODE NOWAIT`); err != nil {
		return errors.New("the PostgreSQL target is in use; stop its servers and retry")
	}
	for _, t := range slices.Backward(openedTables) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+quote(t)); err != nil {
			return err
		}
	}
	// Change rows keep their seq: a desktop client's cursor is one.
	if _, err := tx.ExecContext(ctx, `ALTER TABLE drive_changes DISABLE TRIGGER drive_changes_in_order`); err != nil {
		return err
	}
	for _, table := range migrationTables {
		columns, err := targetColumns(ctx, tx, table)
		if err != nil {
			return err
		}
		if err := sameColumns(ctx, src, table, columns); err != nil {
			return err
		}
		n, sent, err := copyTable(ctx, src, tx, table, columns)
		if err != nil {
			return err
		}
		m, got, err := digest(ctx, tx, table, columns)
		if err != nil || n != m || sent.Cmp(got) != 0 {
			return errors.Join(fmt.Errorf("%s did not arrive intact", table), err)
		}
		report.Rows[table] = n
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE drive_changes ENABLE TRIGGER drive_changes_in_order`); err != nil {
		return err
	}
	if err := advanceSequences(ctx, tx); err != nil {
		return err
	}
	if report.CheckOnly {
		return nil
	}
	return tx.Commit()
}

// advanceSequences sets every identity and serial column's next value past the copied rows.
func advanceSequences(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT table_name,column_name FROM information_schema.columns WHERE table_schema=current_schema() AND (is_identity='YES' OR column_default LIKE 'nextval(%')`)
	if err != nil {
		return err
	}
	var seqs [][2]string
	for rows.Next() {
		var s [2]string
		if err := rows.Scan(&s[0], &s[1]); err != nil {
			rows.Close()
			return err
		}
		seqs = append(seqs, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range seqs {
		if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence(?,?),COALESCE(MAX(`+quote(s[1])+`),1),COUNT(*)>0) FROM `+quote(s[0]), quote(s[0]), s[1]); err != nil {
			return err
		}
	}
	return nil
}

type column struct{ name, kind string }

func targetColumns(ctx context.Context, tx *sql.Tx, table string) ([]column, error) {
	rows, err := tx.QueryContext(ctx, `SELECT column_name,data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []column
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.name, &c.kind); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func sameColumns(ctx context.Context, src *sql.DB, table string, columns []column) error {
	rows, err := src.QueryContext(ctx, `SELECT * FROM `+quote(table)+` LIMIT 0`)
	if err != nil {
		return err
	}
	have, err := rows.Columns()
	rows.Close()
	if err != nil {
		return err
	}
	want := make([]string, len(columns))
	for i, c := range columns {
		want[i] = c.name
	}
	if d := append(setDiff(have, want), setDiff(want, have)...); len(d) > 0 {
		return fmt.Errorf("%s: columns %v are on one side only", table, d)
	}
	return nil
}

func copyTable(ctx context.Context, src *sql.DB, tx *sql.Tx, table string, columns []column) (int64, *big.Int, error) {
	list := columnList(columns)
	rows, err := src.QueryContext(ctx, `SELECT `+list+` FROM `+quote(table))
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO `+quote(table)+`(`+list+`) VALUES(`+strings.Repeat("?,", len(columns)-1)+`?)`)
	if err != nil {
		return 0, nil, err
	}
	defer stmt.Close()
	sum := new(big.Int)
	var n int64
	for rows.Next() {
		values, err := scanRow(rows, columns)
		if err != nil {
			return 0, nil, fmt.Errorf("%s row %d: %w", table, n+1, err)
		}
		if _, err := stmt.ExecContext(ctx, values...); err != nil {
			// Not err itself: its detail can quote the row.
			msg := "rejected"
			var pe *pgconn.PgError
			if errors.As(err, &pe) {
				msg = pe.Message
			}
			return 0, nil, fmt.Errorf("%s row %d: %s", table, n+1, msg)
		}
		addRow(sum, values)
		n++
	}
	return n, sum, rows.Err()
}

func digest(ctx context.Context, tx *sql.Tx, table string, columns []column) (int64, *big.Int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+columnList(columns)+` FROM `+quote(table))
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	sum := new(big.Int)
	var n int64
	for rows.Next() {
		values, err := scanRow(rows, columns)
		if err != nil {
			return 0, nil, err
		}
		addRow(sum, values)
		n++
	}
	return n, sum, rows.Err()
}

// addRow adds the row's hash to sum: a total that ignores row order and holds no row.
func addRow(sum *big.Int, values []any) {
	b, _ := json.Marshal(values)
	h := sha256.Sum256(b)
	sum.Add(sum, new(big.Int).SetBytes(h[:]))
}

// scanRow reads a row as the values PostgreSQL stores for columns, from either side.
func scanRow(rows *sql.Rows, columns []column) ([]any, error) {
	values := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	for i, c := range columns {
		v, err := normalize(values[i], c.kind)
		if err != nil {
			return nil, fmt.Errorf("column %s: %w", c.name, err)
		}
		values[i] = v
	}
	return values, nil
}

func normalize(v any, kind string) (any, error) {
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	if v == nil {
		return nil, nil
	}
	switch kind {
	case "text", "character varying":
		if _, ok := v.(string); ok {
			return v, nil
		}
	case "bigint", "integer":
		if _, ok := v.(int64); ok {
			return v, nil
		}
	case "boolean":
		switch b := v.(type) {
		case bool:
			return b, nil
		case int64:
			if b == 0 || b == 1 {
				return b == 1, nil
			}
		}
	case "timestamp with time zone":
		t, ok := v.(time.Time)
		if s, isText := v.(string); isText {
			t, ok = parseTime(s)
		}
		if ok {
			// PostgreSQL keeps microseconds.
			return t.UTC().Truncate(time.Microsecond), nil
		}
	default:
		return nil, fmt.Errorf("type %s is not migrated", kind)
	}
	return nil, fmt.Errorf("%T is not a %s", v, kind)
}

func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func names(ctx context.Context, db *sql.DB, q string) ([]string, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// setDiff is a's members not in b.
func setDiff(a, b []string) []string {
	m := map[string]bool{}
	for _, s := range a {
		m[s] = !slices.Contains(b, s)
	}
	maps.DeleteFunc(m, func(_ string, keep bool) bool { return !keep })
	return slices.Sorted(maps.Keys(m))
}

func columnList(columns []column) string {
	q := make([]string, len(columns))
	for i, c := range columns {
		q[i] = quote(c.name)
	}
	return strings.Join(q, ",")
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
