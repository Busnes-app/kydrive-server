package qmark_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/qmark"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// A statement outside a transaction reruns when a concurrent update serializes it out; one
// inside a transaction returns the failure, so the transaction's own Retry reruns it whole.
func TestStatementsOutsideTransactionsRerun(t *testing.T) {
	if !testdb.Postgres() {
		t.Skip("SQLite has one writer")
	}
	ctx := context.Background()
	db, err := qmark.Open(testdb.Config(t).DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE counter(id BIGINT PRIMARY KEY, n BIGINT NOT NULL); INSERT INTO counter VALUES(1,0)`); err != nil {
		t.Fatal(err)
	}
	const writers, each = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for range writers {
		wg.Go(func() {
			for range each {
				if _, err := db.ExecContext(ctx, `UPDATE counter SET n=n+1 WHERE id=?`, 1); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal("autocommit update:", err)
	}
	var n int
	if err := db.QueryRow(`SELECT n FROM counter`).Scan(&n); err != nil || n != writers*each {
		t.Fatalf("n=%d, want %d (%v)", n, writers*each, err)
	}

	a, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Rollback()
	if err := a.QueryRow(`SELECT n FROM counter WHERE id=1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE counter SET n=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(`UPDATE counter SET n=n+1 WHERE id=1`); err == nil || !strings.Contains(err.Error(), "(SQLSTATE 40001)") {
		t.Fatalf("a conflicting update inside a transaction returned %v, want its serialization failure", err)
	}
}
