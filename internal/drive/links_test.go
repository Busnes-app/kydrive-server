package drive_test

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func TestOrganisationLinks(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	tr := layout(t, d, w, blobs)
	if _, _, err := d.CreateLink(ctx, "stranger", "folder", tr.a.ID, "reader", 0); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("outsider created a link: %v", err)
	}
	for _, role := range []string{"manager", "owner"} {
		if _, _, err := d.CreateLink(ctx, "worker", "folder", tr.a.ID, role, 0); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("link role %s: %v", role, err)
		}
	}
	for _, expires := range []int64{time.Now().Add(-time.Minute).Unix(), time.Now().Add(400 * 24 * time.Hour).Unix()} {
		if _, _, err := d.CreateLink(ctx, "worker", "folder", tr.a.ID, "reader", expires); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("expiry %d: %v", expires, err)
		}
	}
	link, token, err := d.CreateLink(ctx, "worker", "folder", tr.a.ID, "reader", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(token) {
		t.Fatalf("token %q", token)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_links WHERE token_hash=?`, token); n != 0 {
		t.Fatal("token stored in clear")
	}

	// Holding the token gives nothing until the link is opened; opening gives its role.
	if _, err = d.File(ctx, "stranger", tr.inB.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("access before opening: %v", err)
	}
	item, err := d.OpenLink(ctx, "stranger", token)
	if err != nil || item.Kind != "folder" || item.Name != "a" || item.Role != "reader" || item.WorkspaceName != "Organization" {
		t.Fatalf("open: %+v %v", item, err)
	}
	if _, err = d.File(ctx, "stranger", tr.inB.ID, 1); err != nil {
		t.Fatalf("subtree after opening: %v", err)
	}
	if _, err = d.File(ctx, "stranger", tr.inB.ID, 2); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("reader link gave edit: %v", err)
	}
	if _, err = d.File(ctx, "stranger", tr.inC.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("sibling through a link: %v", err)
	}
	shared, err := d.SharedWithMe(ctx, "stranger")
	if err != nil || len(shared) != 1 || shared[0].ID != tr.a.ID {
		t.Fatalf("opened link not in Shared with me: %+v %v", shared, err)
	}
	links, err := d.Links(ctx, "worker", "folder", tr.a.ID)
	if err != nil || len(links) != 1 || links[0].ID != link.ID || links[0].Opened != 1 {
		t.Fatalf("links %+v %v", links, err)
	}
	if _, err = d.Links(ctx, "stranger", "folder", tr.a.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("link user listed links: %v", err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_events WHERE action='link.opened' AND resource=?`, link.ID); n != 1 {
		t.Fatalf("open not audited: %d", n)
	}

	// Revoking ends the access and the link alike.
	if err = d.RevokeLink(ctx, "stranger", link.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("link user revoked: %v", err)
	}
	if err = d.RevokeLink(ctx, "worker", link.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.File(ctx, "stranger", tr.inB.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("access after revoke: %v", err)
	}
	for _, tok := range []string{token, "not-a-token", ""} {
		if _, err = d.OpenLink(ctx, "stranger", tok); !errors.Is(err, drive.ErrNotFound) {
			t.Errorf("open %q: %v", tok, err)
		}
	}

	// Expiry ends access without anyone acting.
	_, token2, err := d.CreateLink(ctx, "worker", "file", tr.inC.ID, "editor", time.Now().Add(time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.OpenLink(ctx, "stranger", token2); err != nil {
		t.Fatal(err)
	}
	if _, err = d.File(ctx, "stranger", tr.inC.ID, 2); err != nil {
		t.Fatalf("editor link: %v", err)
	}
	if err = drive.ExecSQL(ctx, d, `UPDATE drive_links SET expires=? WHERE scope='org' AND revoked=0`, time.Now().Add(-time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = d.File(ctx, "stranger", tr.inC.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("access after expiry: %v", err)
	}
	if _, err = d.OpenLink(ctx, "stranger", token2); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("expired link opened: %v", err)
	}
	if _, err = d.OpenLink(drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: w.ID, Rank: 1}), "stranger", token2); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("service token opened a link: %v", err)
	}
}

// A link's editor rights end an open editor when the link is revoked.
func TestLinkRevocationRevokesEditor(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	tr := layout(t, d, w, blobs)
	link, token, err := d.CreateLink(ctx, "worker", "file", tr.inA.ID, "editor", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.OpenLink(ctx, "stranger", token); err != nil {
		t.Fatal(err)
	}
	e, err := d.EditorSession(ctx, "stranger", tr.inA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.RevokeLink(ctx, "worker", link.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PendingRevocations(ctx); err != nil || !revoked(t, d, e.ID) {
		t.Fatalf("editor kept after the link was revoked: %v", err)
	}
}
