package drive_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// rank is the highest rank Authorize grants user on workspace, 0 for none.
func rank(ctx context.Context, d *drive.Store, user, workspace string) int {
	for need := 3; need >= 1; need-- {
		if d.Authorize(ctx, user, workspace, need) == nil {
			return need
		}
	}
	return 0
}

// Pins every rule of the access definition so it can be refactored (and later extended) without
// drifting: authorize, the role Workspaces reports, and what PendingRevocations keeps.
func TestAccessDefinitionPinsCurrentRules(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	for _, u := range []*store.User{
		{ID: "reader", Username: "reader", Email: "reader@local.test", Role: "user", Status: "active", SSOProvider: "scim"},
		{ID: "boss", Username: "boss", Email: "boss@local.test", Role: "user", Status: "active", SSOProvider: "scim"},
		{ID: "gone", Username: "gone", Email: "gone@local.test", Role: "user", Status: "inactive", SSOProvider: "scim"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []*store.Group{
		{ID: "readers", DisplayName: "Readers", ExternalID: "x-readers", Members: []string{"reader", "worker"}},
		{ID: "bosses", DisplayName: "Bosses", ExternalID: "x-bosses", Members: []string{"boss"}},
		{ID: "team", DisplayName: "Team", ExternalID: "external-team", Members: []string{"worker", "gone"}},
	} {
		if err := st.Groups().ReplaceGroup(ctx, g, g.ID != "team"); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range [][2]string{{"readers", "reader"}, {"bosses", "manager"}} {
		if err := d.Grant(ctx, "admin", w.ID, g[0], g[1]); err != nil {
			t.Fatal(err)
		}
	}
	personal, err := d.EnsurePersonalWorkspace(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	// A grant row on a personal workspace (Grant refuses one) must give nothing.
	if err = drive.ExecSQL(ctx, d, `INSERT INTO drive_grants(workspace,group_id,role) VALUES(?,?,'manager')`, personal, "bosses"); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		user, workspace string
		want            int
	}{
		{"worker", w.ID, 2}, // editor via team beats reader via readers
		{"reader", w.ID, 1},
		{"boss", w.ID, 3},
		{"stranger", w.ID, 0},
		{"gone", w.ID, 0},  // inactive member
		{"admin", w.ID, 0}, // administration never grants content
		{"worker", personal, 3},
		{"boss", personal, 0}, // the grant on a personal workspace is ignored
		{"admin", personal, 0},
		{"missing-user", w.ID, 0},
	} {
		if got := rank(ctx, d, c.user, c.workspace); got != c.want {
			t.Errorf("rank(%s, %s) = %d, want %d", c.user, c.workspace, got, c.want)
		}
	}

	// A service token never exceeds its scope or rank.
	scoped := drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: w.ID, Rank: 1})
	if got := rank(scoped, d, "worker", w.ID); got != 1 {
		t.Errorf("scoped token rank %d, want 1", got)
	}
	if got := rank(scoped, d, "worker", personal); got != 0 {
		t.Errorf("token outside its workspace: rank %d", got)
	}

	roles := func(ctx context.Context, user string, administration bool) string {
		t.Helper()
		list, err := d.Workspaces(ctx, user, administration)
		if err != nil {
			t.Fatal(err)
		}
		out := ""
		for _, x := range list {
			out += fmt.Sprintf("%s:%s:%s ", x.Kind, x.Name, x.Role)
		}
		return out
	}
	for _, c := range []struct {
		user           string
		administration bool
		want           string
	}{
		{"worker", false, "personal:My files:manager shared:Organization:editor "},
		{"reader", false, "shared:Organization:reader "},
		{"boss", false, "shared:Organization:manager "},
		{"gone", false, ""},
		{"stranger", false, ""},
		{"admin", true, "shared:Organization: "},
	} {
		if got := roles(ctx, c.user, c.administration); got != c.want {
			t.Errorf("Workspaces(%s, %v) = %q, want %q", c.user, c.administration, got, c.want)
		}
	}
	if got := roles(scoped, "worker", false); got != "shared:Organization:editor " {
		t.Errorf("scoped Workspaces = %q", got)
	}

	// PendingRevocations keeps a read-only session on a reader grant and an editable one on an
	// editor grant, and revokes each once that grant no longer covers it.
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "pinned.docx"}, blob(t, root, "x"), 0)
	if err != nil {
		t.Fatal(err)
	}
	editing, err := d.EditorSession(ctx, "worker", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	viewing, err := d.EditorSession(ctx, "reader", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.PendingRevocations(ctx); err != nil || revoked(t, d, editing.ID) || revoked(t, d, viewing.ID) {
		t.Fatalf("live sessions revoked: %v", err)
	}
	// Worker drops to reader (team removed): editing must go, viewing must stay.
	if err = st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", ExternalID: "external-team", Members: []string{"gone"}}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PendingRevocations(ctx); err != nil || !revoked(t, d, editing.ID) || revoked(t, d, viewing.ID) {
		t.Fatalf("editing revoked=%v viewing revoked=%v err=%v", revoked(t, d, editing.ID), revoked(t, d, viewing.ID), err)
	}
	if err = d.Grant(ctx, "admin", w.ID, "readers", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PendingRevocations(ctx); err != nil || !revoked(t, d, viewing.ID) {
		t.Fatalf("viewing kept without any grant: %v", err)
	}
}
