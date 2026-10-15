package drive_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

const week = 7 * 24 * time.Hour

func inDays(n int) int64 { return time.Now().Add(time.Duration(n) * 24 * time.Hour).Unix() }

func guestNames(items []drive.GuestItem) string {
	var out []string
	for _, i := range items {
		out = append(out, i.Kind+":"+i.Name)
	}
	return fmt.Sprint(out)
}

func TestGuestLinkCreation(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	tr := layout(t, d, w, blobs)
	// Worker is a workspace editor and editors may not share yet.
	if _, _, err := d.CreateGuestLink(ctx, "worker", "folder", tr.a.ID, inDays(1), "", false, week); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("editor created a guest link: %v", err)
	}
	managers(t, d, w)
	for _, c := range []struct {
		kind, id string
		expires  int64
		secret   string
		upload   bool
	}{
		{"folder", tr.a.ID, 0, "", false},              // expiry is mandatory
		{"folder", tr.a.ID, inDays(-1), "", false},     // in the past
		{"folder", tr.a.ID, inDays(8), "", false},      // beyond the administrator's limit
		{"file", tr.inA.ID, inDays(1), "", true},       // upload-only needs a folder
		{"folder", tr.a.ID, inDays(1), "short", false}, // password too short
	} {
		if _, _, err := d.CreateGuestLink(ctx, "worker", c.kind, c.id, c.expires, c.secret, c.upload, week); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if _, _, err := d.CreateGuestLink(ctx, "stranger", "folder", tr.a.ID, inDays(1), "", false, week); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("outsider created a guest link: %v", err)
	}
	g, token, err := d.CreateGuestLink(ctx, "worker", "folder", tr.a.ID, inDays(1), "correct horse", false, week)
	if err != nil || !g.HasPassword || len(token) != 43 {
		t.Fatalf("%+v %q %v", g, token, err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_links WHERE password_hash LIKE '%correct horse%' OR token_hash=?`, token); n != 0 {
		t.Fatal("password or token stored in clear")
	}
	list, err := d.GuestLinks(ctx, "worker", "folder", tr.a.ID)
	if err != nil || len(list) != 1 || list[0].ID != g.ID {
		t.Fatalf("%+v %v", list, err)
	}
}

// A folder link shows that folder and below, never siblings or parents; a password guards it.
func TestGuestLinkScope(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	tr := layout(t, d, w, blobs)
	_, token, err := d.CreateGuestLink(ctx, "worker", "folder", tr.a.ID, inDays(1), "correct horse", false, week)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"", "wrong password"} {
		if _, err = d.GuestView(ctx, token, secret, ""); !errors.Is(err, drive.ErrPassword) {
			t.Fatalf("password %q: %v", secret, err)
		}
	}
	view, err := d.GuestView(ctx, token, "correct horse", "")
	if err != nil || view.Name != "a" || guestNames(view.Items) != "[folder:b file:inA.txt]" {
		t.Fatalf("root %+v %v", view, err)
	}
	sub, err := d.GuestView(ctx, token, "correct horse", tr.b.ID)
	if err != nil || guestNames(sub.Items) != "[file:inB.txt]" || guestNames(sub.Path) != "[folder:a folder:b]" {
		t.Fatalf("subfolder %+v %v", sub, err)
	}
	for _, outside := range []string{tr.c.ID, "missing"} {
		if _, err = d.GuestView(ctx, token, "correct horse", outside); !errors.Is(err, drive.ErrNotFound) {
			t.Errorf("folder %s outside the link: %v", outside, err)
		}
	}
	if f, err := d.GuestFile(ctx, token, "correct horse", tr.inB.ID); err != nil || f.ID != tr.inB.ID {
		t.Fatalf("download inside: %+v %v", f, err)
	}
	for _, outside := range []string{tr.inC.ID, tr.rootFile} {
		if _, err = d.GuestFile(ctx, token, "correct horse", outside); !errors.Is(err, drive.ErrNotFound) {
			t.Errorf("file %s outside the link: %v", outside, err)
		}
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_events WHERE action='guest_link.downloaded' AND resource=?`, tr.inB.ID); n != 1 {
		t.Fatalf("download not audited: %d", n)
	}
	if _, err = d.GuestUpload(ctx, token, "correct horse", "x.txt", blob(t, blobs, "x")); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("upload through a view link: %v", err)
	}

	// A file link shows that file only.
	_, fileToken, err := d.CreateGuestLink(ctx, "worker", "file", tr.inC.ID, inDays(1), "", false, week)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := d.GuestView(ctx, fileToken, "", tr.a.ID); err != nil || guestNames(v.Items) != "[file:inC.txt]" {
		t.Fatalf("file link view %+v %v", v, err)
	}
	if _, err = d.GuestFile(ctx, fileToken, "", tr.inA.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("other file through a file link: %v", err)
	}
}

// Revocation, expiry, and the creator losing the right to share all end a guest link.
func TestGuestLinkEnds(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	tr := layout(t, d, w, blobs)
	newLink := func() (drive.GuestLink, string) {
		t.Helper()
		g, token, err := d.CreateGuestLink(ctx, "worker", "folder", tr.a.ID, inDays(1), "", false, week)
		if err != nil {
			t.Fatal(err)
		}
		return g, token
	}
	g, token := newLink()
	if err := d.RevokeLink(ctx, "worker", g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GuestView(ctx, token, "", ""); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("revoked: %v", err)
	}
	_, token = newLink()
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_links SET expires=? WHERE scope='guest' AND revoked=0`, time.Now().Add(-time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GuestView(ctx, token, "", ""); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	_, token = newLink()
	if err := d.Grant(ctx, "admin", w.ID, "team", "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GuestView(ctx, token, "", ""); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("link outlived its creator's right to share: %v", err)
	}
	if _, err := d.GuestView(ctx, "not-a-token", "", ""); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
}

// An upload-only link takes files into its folder and shows nothing that is there.
func TestGuestUploadOnly(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	tr := layout(t, d, w, blobs)
	_, token, err := d.CreateGuestLink(ctx, "worker", "folder", tr.a.ID, inDays(1), "", true, week)
	if err != nil {
		t.Fatal(err)
	}
	view, err := d.GuestView(ctx, token, "", tr.b.ID)
	if err != nil || !view.UploadOnly || len(view.Items) != 0 || guestNames(view.Path) != "[folder:a]" {
		t.Fatalf("upload-only view %+v %v", view, err)
	}
	if _, err = d.GuestFile(ctx, token, "", tr.inA.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("download through an upload-only link: %v", err)
	}
	f, err := d.GuestUpload(ctx, token, "", "invoice.pdf", blob(t, blobs, "pdf"))
	if err != nil || f.Parent != tr.a.ID {
		t.Fatalf("upload %+v %v", f, err)
	}
	if got, err := d.File(ctx, "worker", f.ID, 1); err != nil || got.Name != "invoice.pdf" {
		t.Fatalf("uploaded file %+v %v", got, err)
	}
	// A taken name must not show: the second upload lands beside the first under a free name.
	again, err := d.GuestUpload(ctx, token, "", "invoice.pdf", blob(t, blobs, "again"))
	if err != nil || again.ID == f.ID || again.Name != "invoice (2).pdf" {
		t.Fatalf("duplicate name %+v %v", again, err)
	}
	for range 2 {
		again, err = d.GuestUpload(ctx, token, "", ".env", blob(t, blobs, "env"))
	}
	if err != nil || again.Name != ".env (2)" {
		t.Fatalf("duplicate dotfile %+v %v", again, err)
	}
	// A name too long to number is refused whether or not it is taken.
	if _, err = d.GuestUpload(ctx, token, "", strings.Repeat("x", 250), blob(t, blobs, "long")); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("long name: %v", err)
	}
	if _, err = d.GuestUpload(ctx, token, "", "../x", blob(t, blobs, "x")); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_events WHERE action='guest_link.uploaded' AND resource=?`, f.ID); n != 1 {
		t.Fatal("upload not audited")
	}
}

// A folder moved out from under the link between GuestView's scope check and its ancestor query
// leaves a chain that never reaches the link's folder; the walk must end, not spin.
func TestGuestPathEndsOffTheLink(t *testing.T) {
	path, err := drive.GuestPath([]drive.Folder{{ID: "b", Parent: "a", Name: "B"}, {ID: "a", Name: "A"}}, "b", "a")
	if err != nil || guestNames(path) != "[folder:A folder:B]" {
		t.Fatalf("path %v %v", path, err)
	}
	for name, chain := range map[string][]drive.Folder{
		"moved away": {{ID: "b", Parent: "c"}, {ID: "c"}},
		"cycle":      {{ID: "b", Parent: "c"}, {ID: "c", Parent: "b"}},
	} {
		if _, err := drive.GuestPath(chain, "b", "a"); !errors.Is(err, drive.ErrNotFound) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
