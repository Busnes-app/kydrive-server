package drive_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// docx is the shortest body that sniffs as a zip.
var docx = "PK\x03\x04" + strings.Repeat("x", 60)

// storeView reserves and stores a viewing copy of docx for user through KyPost.
func storeView(t *testing.T, d *drive.Store, peer context.Context, user, root string) drive.ViewCopy {
	t.Helper()
	token, _, err := d.IssueViewTicket(peer, user, "report.docx", int64(len(docx)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.ViewTicket(context.Background(), token)
	if err == nil {
		err = d.StoreViewCopy(peer, root, testKey, c, token, strings.NewReader(docx))
	}
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestViewCopyLifecycle(t *testing.T) {
	st, _, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	peer := drive.WithPeer(ctx, livePeer)
	before, err := d.PeerFree(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := d.IssueViewTicket(peer, "worker", "Report.DOCX", int64(len(docx)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// Counted against My files from the moment it is reserved.
	if free, _ := d.PeerFree(ctx, "worker"); free != before-int64(len(docx)) {
		t.Fatalf("free %d, want %d", free, before-int64(len(docx)))
	}
	c, err := d.ViewTicket(ctx, token)
	if err != nil || c.Name != "Report.DOCX" || c.Owner != "worker" {
		t.Fatalf("ticket %+v %v", c, err)
	}
	if _, err = d.ViewCopy(ctx, "worker", c.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("copy readable before its bytes: %v", err)
	}
	if err = d.StoreViewCopy(peer, root, testKey, c, token, strings.NewReader(docx)); err != nil {
		t.Fatal(err)
	}
	// The ticket is spent: neither found again nor usable again.
	if _, err = d.ViewTicket(ctx, token); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("spent ticket found: %v", err)
	}
	if err = d.StoreViewCopy(peer, root, testKey, c, token, strings.NewReader(docx)); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("ticket replay: %v", err)
	}
	got, err := d.ViewCopy(ctx, "worker", c.ID)
	if err != nil || got.Blob == "" || got.Size != int64(len(docx)) {
		t.Fatalf("owner open %+v %v", got, err)
	}
	if _, err = d.ViewCopy(ctx, "stranger", c.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("another user opened it: %v", err)
	}
	if _, err = d.ViewCopy(drive.WithServiceAccess(ctx, drive.ServiceAccess{}), "worker", c.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("service context opened it: %v", err)
	}
	if _, err = d.ViewDownload(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	// Not a file: no listing, search or lookup sees it.
	if hits, _, err := d.Search(ctx, "worker", "report", 10); err != nil || len(hits) != 0 {
		t.Fatalf("search found %v %v", hits, err)
	}
	if blob, err := d.CloseViewCopy(ctx, "stranger", c.ID); err != nil || blob != "" {
		t.Fatalf("stranger closed it: %q %v", blob, err)
	}
	blob, err := d.CloseViewCopy(ctx, "worker", c.ID)
	if err != nil || blob != got.Blob {
		t.Fatalf("close %q %v", blob, err)
	}
	if _, err = d.ViewDownload(ctx, c.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("closed copy served: %v", err)
	}
	if free, _ := d.PeerFree(ctx, "worker"); free != before {
		t.Fatalf("quota not returned: %d", free)
	}
}

func TestViewCopyRefusals(t *testing.T) {
	st, _, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	peer := drive.WithPeer(ctx, livePeer)
	issue := func(c context.Context, name string, size, max int64) error {
		_, _, err := d.IssueViewTicket(c, "worker", name, size, max)
		return err
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		file string
		size int64
		max  int64
		want error
	}{
		{"not KyPost", ctx, "a.docx", 5, 1 << 20, drive.ErrDenied},
		{"script", peer, "a.exe", 5, 1 << 20, drive.ErrInvalid},
		{"html", peer, "a.html", 5, 1 << 20, drive.ErrInvalid},
		{"no extension", peer, "docx", 5, 1 << 20, drive.ErrInvalid},
		{"empty", peer, "a.pdf", 0, 1 << 20, drive.ErrInvalid},
		{"over the drive limit", peer, "a.pdf", 11, 10, drive.ErrTooLarge},
		{"over the attachment limit", peer, "a.pdf", drive.ViewMaxBytes + 1, 1 << 40, drive.ErrTooLarge},
	} {
		if err := issue(tc.ctx, tc.file, tc.size, tc.max); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	// Bytes that are not what the name says, or not exactly the size.
	for _, tc := range []struct {
		file, body string
		size       int64
		want       error
	}{
		{"a.docx", "<html><script>x</script></html>", 31, drive.ErrUnsupported},
		{"a.pdf", docx, int64(len(docx)), drive.ErrUnsupported},
		{"a.csv", "<!DOCTYPE html><p>x", 19, drive.ErrUnsupported},
		{"a.docx", docx[:20], int64(len(docx)), drive.ErrInvalid},
		{"a.docx", docx + "extra", int64(len(docx)), drive.ErrInvalid},
	} {
		token, _, err := d.IssueViewTicket(peer, "worker", tc.file, tc.size, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := d.ViewTicket(ctx, token)
		if err = d.StoreViewCopy(peer, root, testKey, c, token, strings.NewReader(tc.body)); !errors.Is(err, tc.want) {
			t.Errorf("%s %q: %v, want %v", tc.file, tc.body, err, tc.want)
		}
		d.CloseViewCopy(ctx, "worker", c.ID)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("refused bytes left on disk: %v", entries)
	}
	// Another pairing cannot spend this one's ticket.
	token, _, _ := d.IssueViewTicket(peer, "worker", "a.pdf", 8, 1<<20)
	c, _ := d.ViewTicket(ctx, token)
	other := drive.WithPeer(ctx, drive.Peer{SwitchDefault: true, Live: func() bool { return true }, Pairing: "pairing-2"})
	if err := d.StoreViewCopy(other, root, testKey, c, token, strings.NewReader("%PDF-1.7")); !errors.Is(err, drive.ErrPeerGone) {
		t.Errorf("other pairing: %v", err)
	}
	if err := d.StoreViewCopy(peer, root, testKey, c, token, strings.NewReader("%PDF-1.7")); err != nil {
		t.Errorf("pdf: %v", err)
	}
	// Four live per person.
	for range 3 {
		if err := issue(peer, "b.csv", 3, 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	if err := issue(peer, "b.csv", 3, 1<<20); !errors.Is(err, drive.ErrTicketLimit) {
		t.Errorf("fifth: %v", err)
	}
	// The person's switch.
	if err := d.SetPeerSetting(ctx, "worker", false); err != nil {
		t.Fatal(err)
	}
	if err := issue(peer, "c.csv", 3, 1<<20); !errors.Is(err, drive.ErrPeerOff) {
		t.Errorf("switch off: %v", err)
	}
}

func TestViewCopyExpiry(t *testing.T) {
	st, _, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	peer := drive.WithPeer(ctx, livePeer)
	kept := storeView(t, d, peer, "worker", root)
	old := storeView(t, d, peer, "worker", root)
	unused, _, err := d.IssueViewTicket(peer, "worker", "late.pdf", 8, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	late, _ := d.ViewTicket(ctx, unused)
	got, _ := d.ViewCopy(ctx, "worker", old.ID)
	if err = drive.AgeViewCopy(d, old.ID, drive.ViewLife); err != nil {
		t.Fatal(err)
	}
	if err = drive.AgeViewCopy(d, late.ID, drive.TicketLife); err != nil {
		t.Fatal(err)
	}
	// Access ends at expiry, before any sweep.
	if _, err = d.ViewCopy(ctx, "worker", old.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("expired copy opened: %v", err)
	}
	if _, err = d.ViewTicket(ctx, unused); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("expired ticket found: %v", err)
	}
	blobs, err := d.ExpireViewCopies(ctx)
	if err != nil || len(blobs) != 1 || blobs[0] != got.Blob {
		t.Fatalf("expired %v %v, want [%s]", blobs, err, got.Blob)
	}
	if err = drive.RemoveBlobs(root, blobs); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, got.Blob)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired bytes remain: %v", err)
	}
	if _, err = d.ViewCopy(ctx, "worker", kept.ID); err != nil {
		t.Fatalf("live copy swept: %v", err)
	}
	// Euro-Office closing the document deletes it too.
	if blob, err := d.ViewClosed(ctx, kept.ID); err != nil || blob == "" {
		t.Fatalf("closed %q %v", blob, err)
	}
}

// A copy is bound to the sign-in that opened it: ending that sign-in revokes it at once and
// queues a Euro-Office drop, as for editor sessions.
func TestViewCopyEndsWithItsSignIn(t *testing.T) {
	st, _, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	peer := drive.WithPeer(ctx, livePeer)
	c := storeView(t, d, peer, "worker", root)
	other := storeView(t, d, peer, "worker", root)
	if _, err := d.ViewDownload(ctx, c.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("an unopened copy was served: %v", err)
	}
	login := drive.WithLogin(ctx, drive.Login{SSOSID: "sid-1", At: time.Now().Add(-time.Minute)})
	if _, err := d.ViewCopy(login, "worker", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ViewCopy(drive.WithLogin(ctx, drive.Login{SSOSID: "sid-2", At: time.Now()}), "worker", other.ID); err != nil {
		t.Fatal(err)
	}
	// Bound to its first sign-in: the same one reopens it, another (even the same user) cannot.
	if _, err := d.ViewCopy(login, "worker", c.ID); err != nil {
		t.Fatalf("same sign-in reopen: %v", err)
	}
	for _, l := range []drive.Login{{SSOSID: "sid-2", At: time.Now()}, {At: time.Now().Add(time.Second)}} {
		if _, err := d.ViewCopy(drive.WithLogin(ctx, l), "worker", c.ID); !errors.Is(err, drive.ErrNotFound) {
			t.Fatalf("another sign-in %+v opened it: %v", l, err)
		}
	}
	if err := st.Sessions().EndSSOSession(ctx, "sid-1", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ViewDownload(ctx, c.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("revoked copy served: %v", err)
	}
	if _, err := d.ViewCopy(login, "worker", c.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("revoked copy reopened: %v", err)
	}
	if _, err := d.ViewDownload(ctx, other.ID); err != nil {
		t.Fatalf("another sign-in's copy revoked: %v", err)
	}
	drops, err := d.PendingViewDrops(ctx)
	if err != nil || len(drops) != 1 || drops[0].ID != c.ID || drops[0].Owner != "worker" {
		t.Fatalf("drops %+v %v", drops, err)
	}
	// A failed drop retries later; a done one lets the sweep remove the bytes.
	if err = d.MarkViewDropped(ctx, c.ID, false); err != nil {
		t.Fatal(err)
	}
	if drops, _ = d.PendingViewDrops(ctx); len(drops) != 0 {
		t.Fatalf("retry not backed off: %+v", drops)
	}
	if blobs, _ := d.ExpireViewCopies(ctx); len(blobs) != 0 {
		t.Fatalf("swept before its drop: %v", blobs)
	}
	if err = d.MarkViewDropped(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if blobs, _ := d.ExpireViewCopies(ctx); len(blobs) != 1 {
		t.Fatalf("dropped copy not swept: %v", blobs)
	}
}

// Closing an opened copy keeps it, revoked and without bytes, until Euro-Office drops it, so a
// second tab still showing it is disconnected; a later logout cannot lose that drop.
func TestClosedViewCopyKeepsItsDrop(t *testing.T) {
	st, _, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	peer := drive.WithPeer(ctx, livePeer)
	c := storeView(t, d, peer, "worker", root)
	unopened := storeView(t, d, peer, "worker", root)
	before, _ := d.PeerFree(ctx, "worker")
	if _, err := d.ViewCopy(drive.WithLogin(ctx, drive.Login{SSOSID: "sid-1", At: time.Now()}), "worker", c.ID); err != nil {
		t.Fatal(err)
	}
	blob, err := d.CloseViewCopy(ctx, "worker", c.ID)
	if err != nil || blob == "" {
		t.Fatalf("close %q %v", blob, err)
	}
	if free, _ := d.PeerFree(ctx, "worker"); free != before+int64(len(docx)) {
		t.Fatalf("closed copy still counted: %d", free)
	}
	if err = st.Sessions().EndSSOSession(ctx, "sid-1", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if blobs, _ := d.ExpireViewCopies(ctx); len(blobs) != 0 {
		t.Fatalf("swept before its drop: %v", blobs)
	}
	drops, err := d.PendingViewDrops(ctx)
	if err != nil || len(drops) != 1 || drops[0].ID != c.ID {
		t.Fatalf("drops %+v %v", drops, err)
	}
	if _, err = d.ViewDownload(ctx, c.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("closed copy served: %v", err)
	}
	if err = d.MarkViewDropped(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if drops, _ = d.PendingViewDrops(ctx); len(drops) != 0 {
		t.Fatalf("drop still pending: %+v", drops)
	}
	// Never opened: nothing to drop, deleted at once.
	if blob, err = d.CloseViewCopy(ctx, "worker", unopened.ID); err != nil || blob == "" {
		t.Fatalf("close unopened %q %v", blob, err)
	}
	if drops, _ = d.PendingViewDrops(ctx); len(drops) != 0 {
		t.Fatalf("unopened copy queued a drop: %+v", drops)
	}
}
