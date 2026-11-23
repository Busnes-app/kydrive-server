// Package qmark opens PostgreSQL through pgx with SQLite's ? placeholders, so one query text
// serves both backends.
package qmark

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
)

// Open opens dsn, rewriting each query's ? placeholders to $1, $2, ... before pgx sees it.
func Open(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// Every transaction and statement is serializable, so concurrent writers keep the
	// guarantee SQLite's one writer gives: a revocation and a grant cannot both act on what the
	// other has not committed.
	cfg.RuntimeParams["default_transaction_isolation"] = "serializable"
	return sql.OpenDB(connector{stdlib.GetConnector(*cfg, stdlib.OptionAfterConnect(func(_ context.Context, c *pgx.Conn) error {
		c.TypeMap().RegisterType(&pgtype.Type{Name: "int8", OID: pgtype.Int8OID, Codec: intCodec{pgtype.Int8Codec{}}})
		c.TypeMap().RegisterType(&pgtype.Type{Name: "int4", OID: pgtype.Int4OID, Codec: intCodec{pgtype.Int4Codec{}}})
		return nil
	}))}), nil
}

// intCodec binds a Go bool to an integer parameter as 1 or 0, as SQLite does: the shared
// schema keeps SQLite's INTEGER flag columns.
type intCodec struct{ pgtype.Codec }

func (c intCodec) PlanEncode(m *pgtype.Map, oid uint32, format int16, value any) pgtype.EncodePlan {
	if _, ok := value.(bool); ok {
		return boolPlan{c.Codec.PlanEncode(m, oid, format, pgtype.Int8{})}
	}
	return c.Codec.PlanEncode(m, oid, format, value)
}

type boolPlan struct{ int pgtype.EncodePlan }

func (p boolPlan) Encode(value any, buf []byte) ([]byte, error) {
	n := pgtype.Int8{Valid: true}
	if value.(bool) {
		n.Int64 = 1
	}
	return p.int.Encode(n, buf)
}

// Retry runs f, one whole transaction, again after a random pause while it fails with a
// serialization failure or deadlock, up to MaxAttempts runs; f must only touch the database.
// On SQLite neither error occurs, so f runs once.
func Retry(ctx context.Context, f func() error) error {
	for attempt := 1; ; attempt++ {
		err := f()
		if attempt == MaxAttempts || !retryable(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(rand.Int64N(int64(min(attempt, 20)) * int64(time.Millisecond)))):
		}
	}
}

// MaxAttempts bounds the runs of one transaction: enough for dozens of writers to one row.
const MaxAttempts = 50

func retryable(err error) bool {
	msg := fmt.Sprint(err)
	return strings.Contains(msg, "(SQLSTATE 40001)") || strings.Contains(msg, "(SQLSTATE 40P01)")
}

// Is reports whether db was opened by Open.
func Is(db *sql.DB) bool { _, ok := db.Driver().(drv); return ok }

type drv struct{ driver.Driver }

type connector struct{ driver.Connector }

func (c connector) Driver() driver.Driver { return drv{c.Connector.Driver()} }

func (c connector) Connect(ctx context.Context) (driver.Conn, error) {
	cn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: cn.(*stdlib.Conn)}, nil
}

// conn is a pgx connection whose query entry points rebind; every other method is pgx's.
type conn struct {
	*stdlib.Conn
	inTx bool
}

func (c *conn) Prepare(q string) (driver.Stmt, error) { return c.Conn.Prepare(Rebind(q)) }
func (c *conn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.PrepareContext(ctx, Rebind(q))
}

// ExecContext reruns a statement outside a transaction while it fails with a serialization
// failure or deadlock: PostgreSQL rolled it back whole, so a rerun is the same statement once.
// Inside a transaction the error stands, and Retry reruns the transaction.
func (c *conn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	q = Rebind(q)
	if c.inTx {
		return c.Conn.ExecContext(ctx, q, args)
	}
	var res driver.Result
	err := Retry(ctx, func() (err error) {
		res, err = c.Conn.ExecContext(ctx, q, args)
		return err
	})
	return res, err
}
func (c *conn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.QueryContext(ctx, Rebind(q), args)
}

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	t, err := c.Conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	c.inTx = true
	return tx{t, c}, nil
}

// tx marks its connection as outside a transaction again once it ends.
type tx struct {
	driver.Tx
	c *conn
}

func (t tx) Commit() error   { t.c.inTx = false; return t.Tx.Commit() }
func (t tx) Rollback() error { t.c.inTx = false; return t.Tx.Rollback() }

// Rebind numbers q's ? and ?NNN placeholders, skipping string literals, quoted identifiers, comments
// and dollar-quoted bodies.
func Rebind(q string) string {
	if !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		end := i + 1
		switch c := q[i]; {
		case c == '?':
			// ?NNN is parameter NNN; a bare ? is one past the largest so far, as in SQLite.
			j := i + 1
			for j < len(q) && q[j] >= '0' && q[j] <= '9' {
				j++
			}
			if k, err := strconv.Atoi(q[i+1 : j]); err == nil {
				n = max(n, k)
				b.WriteString("$" + q[i+1:j])
			} else {
				n++
				b.WriteString("$" + strconv.Itoa(n))
			}
			i = j - 1
			continue
		case c == '\'' || c == '"':
			end = closing(q, i+1, string(c))
		case strings.HasPrefix(q[i:], "--"):
			end = closing(q, i+2, "\n")
		case strings.HasPrefix(q[i:], "/*"):
			end = closing(q, i+2, "*/")
		case c == '$':
			if tag := dollarTag(q[i:]); tag != "" {
				end = closing(q, i+len(tag), tag)
			}
		}
		b.WriteString(q[i:end])
		i = end - 1
	}
	return b.String()
}

// closing is the index just past the first stop at or after from, else len(q). A doubled quote
// inside a literal is two literals back to back, which this reads the same way.
func closing(q string, from int, stop string) int {
	if j := strings.Index(q[from:], stop); j >= 0 {
		return from + j + len(stop)
	}
	return len(q)
}

// dollarTag is the $tag$ (or $$) opening s, else "". $1 is a parameter, not a tag.
func dollarTag(s string) string {
	for j := 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[:j+1]
		}
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 1 && c >= '0' && c <= '9') {
			return ""
		}
	}
	return ""
}
