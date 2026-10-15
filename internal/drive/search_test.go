package drive_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func hitNames(hits []drive.Hit) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, h.Workspace+":"+h.Path+"/"+h.Name)
	}
	return out
}

func TestSearchRespectsAccess(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "private", DisplayName: "Private", ExternalID: "external-private", Members: []string{"worker"}}, true); err != nil {
		t.Fatal(err)
	}
	secret, err := d.CreateWorkspace(ctx, "admin", "Secret", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Grant(ctx, "admin", secret.ID, "private", "editor"); err != nil {
		t.Fatal(err)
	}
	reports, err := d.AddFolder(ctx, "worker", w.ID, "", "Reports")
	if err != nil {
		t.Fatal(err)
	}
	year, err := d.AddFolder(ctx, "worker", w.ID, reports.ID, "2026 Budget")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []drive.File{{Workspace: w.ID, Parent: year.ID, Name: "Q1 budget.xlsx"}, {Workspace: w.ID, Name: "notes.txt"}, {Workspace: secret.ID, Name: "BUDGET-secret.xlsx"}} {
		if _, err = d.Publish(ctx, "worker", f, blob(t, root, "x"), 0); err != nil {
			t.Fatal(err)
		}
	}
	search := func(ctx context.Context, user string) []string {
		t.Helper()
		hits, _, err := d.Search(ctx, user, "Budget", 200)
		if err != nil {
			t.Fatal(err)
		}
		return hitNames(hits)
	}
	want := fmt.Sprint([]string{w.ID + ":Reports/2026 Budget", secret.ID + ":/BUDGET-secret.xlsx", w.ID + ":Reports/2026 Budget/Q1 budget.xlsx"})
	if got := fmt.Sprint(search(ctx, "worker")); got != want {
		t.Fatalf("worker sees %s, want %s", got, want)
	}
	if hits := search(ctx, "stranger"); len(hits) != 0 {
		t.Fatalf("outsider sees %v", hits)
	}
	if hits := search(ctx, "admin"); len(hits) != 0 {
		t.Fatalf("admin bypass: %v", hits)
	}
	scoped := drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: secret.ID, Rank: 1})
	if hits := search(scoped, "worker"); fmt.Sprint(hits) != fmt.Sprint([]string{secret.ID + ":/BUDGET-secret.xlsx"}) {
		t.Fatalf("service token sees %v", hits)
	}
	if err = st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", ExternalID: "external-team"}, false); err != nil {
		t.Fatal(err)
	}
	if hits := search(ctx, "worker"); fmt.Sprint(hits) != fmt.Sprint([]string{secret.ID + ":/BUDGET-secret.xlsx"}) {
		t.Fatalf("revoked member sees %v", hits)
	}
}

func TestSearchOrderAndPaths(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	a, _ := d.AddFolder(ctx, "worker", w.ID, "", "a")
	b, _ := d.AddFolder(ctx, "worker", w.ID, a.ID, "b")
	for _, f := range []drive.File{{Workspace: w.ID, Parent: b.ID, Name: "Plan.txt"}, {Workspace: w.ID, Name: "plan.txt"}, {Workspace: w.ID, Parent: a.ID, Name: "a plan.txt"}} {
		if _, err := d.Publish(ctx, "worker", f, blob(t, root, "x"), 0); err != nil {
			t.Fatal(err)
		}
	}
	hits, truncated, err := d.Search(ctx, "worker", "PLAN", 200)
	if err != nil || truncated {
		t.Fatal(err, truncated)
	}
	// Case-insensitive by name, then by path.
	if got, want := fmt.Sprint(hitNames(hits)), fmt.Sprint([]string{w.ID + ":a/a plan.txt", w.ID + ":/plan.txt", w.ID + ":a/b/Plan.txt"}); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if hits[2].Kind != "file" || hits[2].Parent != b.ID {
		t.Fatalf("hit %+v", hits[2])
	}
}

func TestSearchExcludesTrash(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	folder, _ := d.AddFolder(ctx, "worker", w.ID, "", "Old reports")
	if _, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: folder.ID, Name: "report.txt"}, blob(t, root, "x"), 0); err != nil {
		t.Fatal(err)
	}
	if err := d.SetFolderTrash(ctx, "worker", folder.ID, true); err != nil {
		t.Fatal(err)
	}
	if hits, _, err := d.Search(ctx, "worker", "report", 200); err != nil || len(hits) != 0 {
		t.Fatalf("%v %v", hitNames(hits), err)
	}
}

func TestSearchCapsResults(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	for i := range 5 {
		if _, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: fmt.Sprintf("doc-%d", i)}, blob(t, root, "x"), 0); err != nil {
			t.Fatal(err)
		}
	}
	hits, truncated, err := d.Search(ctx, "worker", "doc", 3)
	if err != nil || !truncated || len(hits) != 3 || hits[0].Name != "doc-0" {
		t.Fatalf("%v %v %v", hitNames(hits), truncated, err)
	}
}

func TestSearchRejectsBadQueries(t *testing.T) {
	st, _, _ := fixture(t)
	for _, q := range []string{"", "a", "  b  ", string(make([]rune, 101))} {
		if _, _, err := st.Drive().Search(context.Background(), "worker", q, 200); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("%q: %v", q, err)
		}
	}
}

// Names are decrypted to match, so cost grows with the corpus. Pin it at pilot scale.
func TestSearchScale(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 50 000 files")
	}
	st, w, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	if err := drive.SeedFiles(ctx, d, w.ID, 50000); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	hits, truncated, err := d.Search(ctx, "worker", "file-4999", 200)
	elapsed := time.Since(start)
	if err != nil || len(hits) != 11 || truncated {
		t.Fatalf("%d hits truncated=%v err=%v", len(hits), truncated, err)
	}
	t.Logf("searched 50000 names in %s", elapsed)
	if elapsed > 2*time.Second {
		t.Fatalf("search took %s", elapsed)
	}
}
