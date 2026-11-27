package drive_test

import (
	"context"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/google/uuid"
)

func TestCellsRequireFreshListingAndKeepHomeFixed(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	cfg.Tenancy = "multi"
	cfg.StaffOrg = uuid.NewString()
	cfg.Cells = []string{"e2e-1"}
	st, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Drive().ConfigureCells(ctx); err != nil {
		t.Fatal(err)
	}
	local, remote := uuid.NewString(), uuid.NewString()
	listed := []suitedir.Org{{ID: local, HomeRegion: "e2e-1", Status: suitedir.OrgActive, Enabled: true, Revision: 1}, {ID: remote, HomeRegion: "e2e-2", Status: suitedir.OrgActive, Enabled: true, Revision: 2}}
	if _, err := st.Drive().ApplyOrgs(ctx, listed, 2); err != nil {
		t.Fatal(err)
	}
	if known, active, err := st.Drive().OrgActive(ctx, local); err != nil || !known || !active {
		t.Fatal("local org unavailable", err)
	}
	if known, _, err := st.Drive().OrgActive(ctx, remote); err != nil || known {
		t.Fatal("unknown foreign cell created", err)
	}
	cfg.Cells = []string{"e2e-2", "e2e-1"}
	grown, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer grown.Close()
	if err := grown.Drive().ConfigureCells(ctx); err != nil {
		t.Fatal(err)
	}
	if since, err := grown.Drive().OrgsSince(ctx); err != nil || since != 0 {
		t.Fatal("new cell did not reset cursor", since, err)
	}
	if _, active, err := grown.Drive().OrgActive(ctx, local); err != nil || active {
		t.Fatal("changed cells reused stale listing", err)
	}
	if _, err := grown.Drive().ApplyOrgs(ctx, listed, 2); err != nil {
		t.Fatal(err)
	}
	if known, active, err := grown.Drive().OrgActive(ctx, remote); err != nil || !known || !active {
		t.Fatal("added cell missing", err)
	}
	a, _ := grown.Drive().ObjectPrefix(ctx, local)
	b, _ := grown.Drive().ObjectPrefix(ctx, remote)
	if a == b || a == "" || b == "" {
		t.Fatal("colliding prefixes", a, b)
	}
	listed[0].HomeRegion = "e2e-2"
	listed[0].Revision = 5
	if _, err := grown.Drive().ApplyOrgs(ctx, listed[:1], 5); err == nil {
		t.Fatal("home changed")
	}
	home, _ := grown.Drive().OrgHomeRegion(ctx, local)
	if home != "e2e-1" {
		t.Fatal(home)
	}
	// Reordering an unchanged cell set preserves a fresh mirror and its cursor.
	cfg.Cells = []string{"e2e-1", "e2e-2"}
	same, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer same.Close()
	if err := same.Drive().ConfigureCells(ctx); err != nil {
		t.Fatal(err)
	}
	if since, err := same.Drive().OrgsSince(ctx); err != nil || since != 2 {
		t.Fatal("reorder reset cursor", since, err)
	}
	cfg.Cells = []string{"e2e-1"}
	dropped, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer dropped.Close()
	if err := dropped.Drive().ConfigureCells(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := grown.Drive().ObjectPrefix(ctx, remote); err != drive.ErrDenied {
		t.Fatal("old replica resolved dropped cell", err)
	}
}
