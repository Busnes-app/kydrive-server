package drive_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func TestSearchSnapshotCASQueueAndPurgeFence(t *testing.T) {
	o := multiTenant(t)
	d, ctx := o.d, o.ctx
	enqueue := func() {
		t.Helper()
		if err := d.SearchEnqueue(ctx, "org-a", []string{o.fileA.ID}, time.Now(), 0, -1); err != nil {
			t.Fatal(err)
		}
	}
	enqueue()
	done, err := d.SearchDue(ctx, "org-a", time.Now(), true, 20)
	if err != nil || len(done) != 1 {
		t.Fatal(done, err)
	}
	p, err := d.SearchPointer(ctx, "org-a")
	if err != nil {
		t.Fatal(err)
	}
	enqueue() // a newer update arrives while the first batch is extracting
	if err := d.PublishSearch(ctx, "org-a", p, "new-snapshot", done); err != nil {
		t.Fatal(err)
	}
	pending, err := d.SearchDue(ctx, "org-a", time.Now(), true, 20)
	if err != nil || len(pending) != 1 || pending[0].Generation == done[0].Generation {
		t.Fatal("new update acknowledged by old work", pending, err)
	}
	if err := d.PublishSearch(ctx, "org-a", p, "loser-snapshot", pending); !errors.Is(err, drive.ErrConflict) {
		t.Fatal("stale CAS won", err)
	}
	if _, err := o.db.Exec(`UPDATE drive_orgs SET status='deleted' WHERE id='org-a'`); err != nil {
		t.Fatal(err)
	}
	current, _ := d.SearchPointer(ctx, "org-a")
	if err := d.PublishSearch(ctx, "org-a", current, "after-deletion", pending); !errors.Is(err, drive.ErrConflict) {
		t.Fatal("deleted org published", err)
	}
	if err := d.SearchEnqueue(ctx, "org-a", []string{o.fileA.ID}, time.Now(), 0, -1); !errors.Is(err, drive.ErrDenied) {
		t.Fatal("deleted org queued", err)
	}
	if _, _, err := d.PurgeOrgs(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := d.SearchPointer(ctx, "org-a"); p.Object != "" {
		t.Fatal("purge kept pointer")
	}
	if q, _ := d.SearchDue(ctx, "org-a", time.Now(), true, 20); len(q) != 0 {
		t.Fatal("purge kept queue")
	}
}
