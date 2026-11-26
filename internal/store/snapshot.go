package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/qmark"
)

// querier is what the copy reads from: a database or one transaction's snapshot of it.
type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// RecordedTenancy is the tenancy a KyDrive database records (single when it records none).
func RecordedTenancy(ctx context.Context, q querier) (drive.Tenancy, error) {
	t := drive.Tenancy{Mode: drive.TenancySingle}
	for key, dst := range map[string]*string{"tenancy_mode": &t.Mode, "staff_org": &t.StaffOrg} {
		err := q.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key=?`, key).Scan(dst)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return t, err
		}
	}
	return t, nil
}

// snapshotHook runs inside the snapshot transaction once its snapshot is fixed (tests only).
var snapshotHook func()

// SnapshotPostgres writes one point-in-time copy of the KyDrive PostgreSQL database dsn
// names into a new SQLite file at dest, in its recorded tenancy and this release's schema.
// Every table is read in one REPEATABLE READ READ ONLY transaction on a connection of its
// own; each copied table is checked (row count and summed row digests), then the file by
// SQLite's integrity and foreign-key checks. key is the deployment key. The caller owns
// dest's directory and removes it.
func SnapshotPostgres(ctx context.Context, dsn, dest string, key []byte) error {
	db, err := qmark.Open(dsn)
	if err != nil {
		return errors.New("cannot open the PostgreSQL database") // its error can quote the DSN
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	src, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer src.Rollback()
	// The first query fixes the snapshot every later read sees.
	tenancy, err := RecordedTenancy(ctx, src)
	if err != nil {
		return err
	}
	if snapshotHook != nil {
		snapshotHook()
	}
	opened, err := openSQLite(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: dest, Tenancy: tenancy.Mode, StaffOrg: tenancy.StaffOrg}, key)
	if err != nil {
		return err
	}
	out := opened.(*SQLStore)
	defer out.Close()
	migrationTables := copiedTables(tenancy.Mode == drive.TenancyMulti)
	for side, q := range map[querier]string{
		src:    `SELECT tablename FROM pg_tables WHERE schemaname=current_schema()`,
		out.db: `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`,
	} {
		tables, err := names(ctx, side, q)
		if err != nil {
			return err
		}
		tables = slices.DeleteFunc(tables, uncopied)
		if d := append(setDiff(tables, migrationTables), setDiff(migrationTables, tables)...); len(d) > 0 {
			return fmt.Errorf("the PostgreSQL and SQLite schemas differ in tables %v: back up with the release that runs the database", d)
		}
	}
	if err := copyIntoSQLite(ctx, src, out.db, migrationTables); err != nil {
		return err
	}
	var integrity string
	if err := out.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return errors.Join(errors.New("the snapshot failed SQLite's integrity check"), err)
	}
	var orphan bool
	if err := out.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_foreign_key_check)`).Scan(&orphan); err != nil || orphan {
		return errors.Join(errors.New("the snapshot failed SQLite's foreign-key check"), err)
	}
	// One self-contained file: fold the WAL in before it is read and sealed.
	if _, err := out.db.ExecContext(ctx, `PRAGMA journal_mode=DELETE`); err != nil {
		return err
	}
	return out.Close()
}

// copyIntoSQLite replaces dst's rows with src's, table by table, in one transaction. The
// triggers are dropped for the copy and recreated after it: they check new rows, and these
// rows passed them when PostgreSQL took them (an org deleted but not yet purged keeps rows
// no trigger would admit now).
func copyIntoSQLite(ctx context.Context, src querier, dst *sql.DB, migrationTables []string) error {
	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT name,sql FROM sqlite_master WHERE type='trigger'`)
	if err != nil {
		return err
	}
	var triggers [][2]string
	for rows.Next() {
		var t [2]string
		if err := rows.Scan(&t[0], &t[1]); err != nil {
			rows.Close()
			return err
		}
		triggers = append(triggers, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range triggers {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER `+quote(t[0])); err != nil {
			return err
		}
	}
	for _, t := range slices.Backward(migrationTables) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+quote(t)); err != nil {
			return err
		}
	}
	for _, table := range migrationTables {
		columns, err := targetColumns(ctx, src, table)
		if err != nil {
			return err
		}
		if err := sameColumns(ctx, tx, table, columns); err != nil {
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
	}
	for _, t := range triggers {
		if _, err := tx.ExecContext(ctx, t[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}
