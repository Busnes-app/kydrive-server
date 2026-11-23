package drive_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
)

// ticket issues one and returns a function reading it afresh, as each request does.
func (e *peerEnv) ticket(t *testing.T, user, name string, size int64) func() drive.Ticket {
	t.Helper()
	token, _, err := e.d.IssueTicket(e.peer, user, name, size, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return func() drive.Ticket {
		t.Helper()
		tk, err := e.d.Ticket(e.ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
}

func (e *peerEnv) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	n, err := drive.QueryInt(e.ctx, e.d, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *peerEnv) put(tk drive.Ticket, start int64, body string) (int64, *drive.File, error) {
	return e.d.TicketUpload(e.peer, objects.DirTree(e.staging), objects.Dir(e.blobs), testKey, tk, start, int64(len(body)), strings.NewReader(body))
}

func TestTicketIssueRefusals(t *testing.T) {
	e := newPeerEnv(t)
	d, p := e.d, e.peer
	cases := map[string]struct {
		user, name string
		size, max  int64
		want       error
	}{
		"bad name":        {"worker", "a/b", 1, 10, drive.ErrInvalid},
		"empty name":      {"worker", "", 1, 10, drive.ErrInvalid},
		"negative size":   {"worker", "a.txt", -1, 10, drive.ErrInvalid},
		"over the limit":  {"worker", "a.txt", 11, 10, drive.ErrTooLarge},
		"over the quota":  {"worker", "a.txt", 11 << 30, 20 << 30, drive.ErrQuota},
		"inactive user":   {"gone", "a.txt", 1, 10, drive.ErrDenied},
		"name too long n": {"worker", strings.Repeat("a", 252) + ".x", 1, 10, drive.ErrInvalid},
	}
	if err := drive.ExecSQL(e.ctx, d, `INSERT INTO users(id,username,email,role,status,sso_provider,password_hash,created_at,updated_at) VALUES('gone','gone','gone@local.test','user','inactive','scim','',?,?)`, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for what, c := range cases {
		if _, _, err := d.IssueTicket(p, c.user, c.name, c.size, c.max); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", what, err, c.want)
		}
	}
	for range 8 {
		e.ticket(t, "stranger", "a.txt", 1)
	}
	if _, _, err := d.IssueTicket(p, "stranger", "a.txt", 1, 10); !errors.Is(err, drive.ErrTicketLimit) {
		t.Errorf("ninth live ticket: %v", err)
	}
}

func TestTicketLookupRefusals(t *testing.T) {
	e := newPeerEnv(t)
	fresh := e.ticket(t, "worker", "a.txt", 3)
	tk := fresh()
	if _, err := e.d.Ticket(e.ctx, "not-a-ticket"); !errors.Is(err, drive.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	// Spent: one complete upload, then the same token is gone.
	token, _, err := e.d.IssueTicket(e.peer, "worker", "spent.txt", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	spent, _ := e.d.Ticket(e.ctx, token)
	if _, f, err := e.put(spent, 0, "x"); err != nil || f == nil {
		t.Fatal(f, err)
	}
	if _, err := e.d.Ticket(e.ctx, token); !errors.Is(err, drive.ErrNotFound) {
		t.Errorf("spent: %v", err)
	}
	// Expired: gone, and its partial bytes go with the session.
	if _, _, err := e.put(tk, 0, "ab"); err != nil {
		t.Fatal(err)
	}
	tk = fresh()
	if err := drive.ExecSQL(e.ctx, e.d, `UPDATE drive_peer_tickets SET expires=1; UPDATE drive_upload_sessions SET expires=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.Ticket(e.ctx, "anything"); !errors.Is(err, drive.ErrNotFound) {
		t.Errorf("expired: %v", err)
	}
	if _, _, err := e.put(tk, 2, "c"); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("chunk after expiry: %v", err)
	}
	if err := e.d.ExpireTickets(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := e.d.ExpireUploads(e.ctx, objects.DirTree(e.staging)); err != nil || n != 1 {
		t.Errorf("partial bytes not discarded: %d %v", n, err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM drive_peer_tickets`); n != 0 {
		t.Errorf("%d tickets left", n)
	}
}

// Chunks arrive in order; a wrong offset, too many bytes or a late first chunk are refused.
func TestTicketChunks(t *testing.T) {
	e := newPeerEnv(t)
	fresh := e.ticket(t, "worker", "big.bin", 6)
	tk := fresh()
	if _, _, err := e.put(tk, 3, "def"); !errors.Is(err, drive.ErrConflict) {
		t.Errorf("first chunk not at 0: %v", err)
	}
	got, f, err := e.put(tk, 0, "abc")
	if err != nil || f != nil || got != 3 {
		t.Fatalf("first chunk: %d %v %v", got, f, err)
	}
	tk = fresh()
	if _, _, err := e.put(tk, 0, "abc"); !errors.Is(err, drive.ErrConflict) {
		t.Errorf("repeated chunk: %v", err)
	}
	if _, _, err := e.put(tk, 3, "defg"); !errors.Is(err, drive.ErrInvalid) {
		t.Errorf("past the size: %v", err)
	}
	got, f, err = e.put(tk, 3, "def")
	if err != nil || f == nil || got != 6 || f.Size != 6 || f.Parent != e.attachments {
		t.Fatalf("last chunk: %d %+v %v", got, f, err)
	}
	if body := readFile(t, e.blobs, *f); body != "abcdef" {
		t.Errorf("stored %q", body)
	}
}

// A size mismatch while streaming leaves nothing behind.
func TestTicketExactSize(t *testing.T) {
	e := newPeerEnv(t)
	fresh := e.ticket(t, "worker", "a.txt", 3)
	if _, _, err := e.d.TicketUpload(e.peer, objects.DirTree(e.staging), objects.Dir(e.blobs), testKey, fresh(), 0, 3, strings.NewReader("ab")); err == nil {
		t.Error("short body accepted")
	}
	if _, f, err := e.put(fresh(), 0, "abc"); err != nil || f == nil {
		t.Fatalf("retry: %v %v", f, err)
	}
}

func TestTicketNamesAndFolder(t *testing.T) {
	e := newPeerEnv(t)
	second := e.save(t, "worker", "mail.pdf", "again")
	if second.Name != "mail (2).pdf" || second.Parent != e.attachments {
		t.Errorf("collision: %q in %s", second.Name, second.Parent)
	}
	// Taken between the first byte and the last: numbered again at completion.
	race := e.ticket(t, "worker", "race.txt", 2)
	if _, _, err := e.put(race(), 0, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.Publish(e.ctx, "worker", drive.File{Workspace: e.home, Parent: e.attachments, Name: "race.txt"}, blob(t, e.blobs, "x"), 0); err != nil {
		t.Fatal(err)
	}
	if _, f, err := e.put(race(), 1, "b"); err != nil || f == nil || f.Name != "race (2).txt" {
		t.Errorf("late collision: %+v %v", f, err)
	}
	// Renamed: still used. Trashed: a new one is made, beside a file holding the name.
	if _, err := e.d.MoveFolder(e.ctx, "worker", e.attachments, "", "Post"); err != nil {
		t.Fatal(err)
	}
	if f := e.save(t, "worker", "after-rename.txt", "x"); f.Parent != e.attachments {
		t.Errorf("renamed folder not used")
	}
	if err := e.d.SetFolderTrash(e.ctx, "worker", e.attachments, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.Publish(e.ctx, "worker", drive.File{Workspace: e.home, Name: drive.PeerFolderName}, blob(t, e.blobs, "x"), 0); err != nil {
		t.Fatal(err)
	}
	f := e.save(t, "worker", "after-delete.txt", "x")
	if f.Parent == e.attachments {
		t.Fatal("trashed folder reused")
	}
	path, err := e.d.FolderPath(e.ctx, "worker", e.home, f.Parent)
	if err != nil || strings.Join(path, "/") != "Mail attachments (2)" {
		t.Errorf("recreated as %q %v", path, err)
	}
	// The saved file is an ordinary file of its owner: listed, versioned, in the change feed.
	if _, err := e.d.Versions(e.ctx, "worker", f.ID); err != nil {
		t.Error(err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM drive_events WHERE user_id='worker' AND action='kypost.file_saved' AND resource LIKE ?`, f.ID+" peer=kypost%"); n != 1 {
		t.Errorf("%d saved events", n)
	}
}

// Quota is checked again when the file publishes.
func TestTicketQuotaAtCompletion(t *testing.T) {
	e := newPeerEnv(t)
	tk := e.ticket(t, "worker", "a.txt", 3)()
	if err := drive.ExecSQL(e.ctx, e.d, `UPDATE drive_workspaces SET quota=? WHERE id=?`, 10, e.home); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.put(tk, 0, "abc"); !errors.Is(err, drive.ErrQuota) {
		t.Errorf("over quota: %v", err)
	}
	if free, err := e.d.PeerFree(e.ctx, "worker"); err != nil || free != 2 { // mail.pdf and root.txt hold 8
		t.Errorf("free %d %v", free, err)
	}
	if free, err := e.d.PeerFree(e.ctx, "nobody"); err != nil || free != 10<<30 {
		t.Errorf("free without My files %d %v", free, err)
	}
}

func TestPeerSetting(t *testing.T) {
	e := newPeerEnv(t)
	if _, set, err := e.d.PeerSetting(e.ctx, "worker"); err != nil || set {
		t.Fatalf("unset: %v %v", set, err)
	}
	if err := e.d.SetPeerSetting(e.ctx, "worker", false); err != nil {
		t.Fatal(err)
	}
	if on, set, err := e.d.PeerSetting(e.ctx, "worker"); err != nil || !set || on {
		t.Fatalf("off: %v %v %v", on, set, err)
	}
}

// An empty file is one request with no bytes.
func TestTicketEmptyFile(t *testing.T) {
	e := newPeerEnv(t)
	_, f, err := e.put(e.ticket(t, "worker", "empty.txt", 0)(), 0, "")
	if err != nil || f == nil || f.Size != 0 || f.Parent != e.attachments {
		t.Fatalf("empty: %+v %v", f, err)
	}
}

// flipReader turns the owner's switch off while its bytes are being read.
type flipReader struct {
	e    *peerEnv
	t    *testing.T
	r    *strings.Reader
	done bool
}

func (f *flipReader) Read(p []byte) (int, error) {
	if !f.done {
		f.done = true
		if err := f.e.d.SetPeerSetting(f.e.ctx, "worker", false); err != nil {
			f.t.Fatal(err)
		}
	}
	return f.r.Read(p)
}

// The switch is read in the publishing transaction: turned off while the bytes arrive, the
// file is not published and the staged bytes and ticket are gone.
func TestTicketSwitchOffMidUpload(t *testing.T) {
	e := newPeerEnv(t)
	tk := e.ticket(t, "worker", "late.txt", 3)()
	before := e.count(t, `SELECT COUNT(*) FROM drive_files`)
	_, f, err := e.d.TicketUpload(e.peer, objects.DirTree(e.staging), objects.Dir(e.blobs), testKey, tk, 0, 3, &flipReader{e: e, t: t, r: strings.NewReader("abc")})
	if !errors.Is(err, drive.ErrPeerOff) || f != nil {
		t.Fatalf("published after the switch went off: %+v %v", f, err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM drive_files`); n != before {
		t.Errorf("%d files, want %d", n, before)
	}
	if n := e.count(t, `SELECT (SELECT COUNT(*) FROM drive_upload_sessions)+(SELECT COUNT(*) FROM drive_peer_tickets)`); n != 0 {
		t.Errorf("%d sessions and tickets left", n)
	}
	// Off also refuses new tickets and links, and an explicit default off refuses without a row.
	if _, _, err := e.d.IssueTicket(e.peer, "worker", "a.txt", 1, 10); !errors.Is(err, drive.ErrPeerOff) {
		t.Errorf("ticket while off: %v", err)
	}
	if _, _, err := e.d.CreateLink(e.peer, "worker", "file", e.mail.ID, "reader", 0); !errors.Is(err, drive.ErrPeerOff) {
		t.Errorf("link while off: %v", err)
	}
	if _, _, err := e.d.IssueTicket(drive.WithPeer(e.ctx, drive.Peer{Live: func() bool { return true }, Pairing: "pairing-1"}), "stranger", "a.txt", 1, 10); !errors.Is(err, drive.ErrPeerOff) {
		t.Errorf("default off: %v", err)
	}
}

// A pairing that ends while the bytes arrive publishes nothing.
func TestTicketUnpairedMidUpload(t *testing.T) {
	e := newPeerEnv(t)
	tk := e.ticket(t, "worker", "late.txt", 3)()
	live := true
	p := drive.WithPeer(e.ctx, drive.Peer{SwitchDefault: true, Live: func() bool { return live }, Pairing: "pairing-1"})
	if _, _, err := e.d.TicketUpload(p, objects.DirTree(e.staging), objects.Dir(e.blobs), testKey, tk, 0, 1, strings.NewReader("a")); err != nil {
		t.Fatal(err)
	}
	live = false
	tk = drive.Ticket{ID: tk.ID, Owner: tk.Owner, Name: tk.Name, Size: tk.Size, Expires: tk.Expires}
	if err := drive.QueryRow(e.ctx, e.d, `SELECT session FROM drive_peer_tickets WHERE id=?`, tk.ID).Scan(&tk.Session); err != nil {
		t.Fatal(err)
	}
	if _, f, err := e.d.TicketUpload(p, objects.DirTree(e.staging), objects.Dir(e.blobs), testKey, tk, 1, 2, strings.NewReader("bc")); !errors.Is(err, drive.ErrPeerGone) || f != nil {
		t.Fatalf("unpaired: %+v %v", f, err)
	}
	if n := e.count(t, `SELECT (SELECT COUNT(*) FROM drive_upload_sessions)+(SELECT COUNT(*) FROM drive_peer_tickets)`); n != 0 {
		t.Errorf("%d sessions and tickets left", n)
	}
}

// A ticket works only under the pairing that issued it: after a rotation, neither an unused
// nor a partly uploaded one publishes, and both are removed with their bytes.
func TestTicketBoundToItsPairing(t *testing.T) {
	e := newPeerEnv(t)
	unused := e.ticket(t, "worker", "unused.txt", 1)()
	partial := e.ticket(t, "worker", "partial.txt", 2)
	if _, _, err := e.put(partial(), 0, "a"); err != nil {
		t.Fatal(err)
	}
	rotated := drive.WithPeer(e.ctx, drive.Peer{SwitchDefault: true, Live: func() bool { return true }, Pairing: "pairing-2"})
	if _, f, err := e.d.TicketUpload(rotated, objects.DirTree(e.staging), objects.Dir(e.blobs), testKey, unused, 0, 1, strings.NewReader("x")); !errors.Is(err, drive.ErrPeerGone) || f != nil {
		t.Errorf("unused ticket after rotation: %+v %v", f, err)
	}
	if _, f, err := e.d.TicketUpload(rotated, objects.DirTree(e.staging), objects.Dir(e.blobs), testKey, partial(), 1, 1, strings.NewReader("b")); !errors.Is(err, drive.ErrPeerGone) || f != nil {
		t.Errorf("partial ticket after rotation: %+v %v", f, err)
	}
	if n := e.count(t, `SELECT (SELECT COUNT(*) FROM drive_upload_sessions)+(SELECT COUNT(*) FROM drive_peer_tickets)`); n != 0 {
		t.Errorf("%d sessions and tickets left", n)
	}
	if _, _, err := e.d.IssueTicket(drive.WithPeer(e.ctx, drive.Peer{SwitchDefault: true, Live: func() bool { return true }}), "worker", "a.txt", 1, 10); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("ticket without a pairing: %v", err)
	}
}

// KyPost reads only what the user picked, while the pick lasts, the file is live and the switch is on.
func TestPeerFetch(t *testing.T) {
	e := newPeerEnv(t)
	d := e.d
	if _, err := d.PeerFetch(e.peer, "worker", e.root.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("unpicked: %v", err)
	}
	if err := d.RecordPicks(e.peer, "worker", []string{e.root.ID}); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("KyPost recorded its own pick: %v", err)
	}
	if err := d.RecordPicks(e.ctx, "worker", []string{e.root.ID, e.team.ID}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []drive.File{e.root, e.team} {
		if got, err := d.PeerFetch(e.peer, "worker", f.ID); err != nil || got.ID != f.ID {
			t.Fatalf("picked %s: %v", f.Name, err)
		}
	}
	if _, err := d.PeerFetch(e.ctx, "worker", e.root.ID); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("outside a peer context: %v", err)
	}
	if _, err := d.PeerFetch(e.peer, "admin", e.root.ID); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("another user's pick: %v", err)
	}
	// The pick grants nothing else: an ordinary read in a peer context is still refused.
	if _, err := d.File(e.peer, "worker", e.root.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("File in a peer context: %v", err)
	}
	if err := d.SetTrash(e.ctx, "worker", e.team.ID, e.team.Revision, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PeerFetch(e.peer, "worker", e.team.ID); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("trashed: %v", err)
	}
	if err := d.SetPeerSetting(e.ctx, "worker", false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PeerFetch(e.peer, "worker", e.root.ID); !errors.Is(err, drive.ErrPeerOff) {
		t.Errorf("switch off: %v", err)
	}
	if err := d.SetPeerSetting(e.ctx, "worker", true); err != nil {
		t.Fatal(err)
	}
	if err := drive.ExecSQL(e.ctx, d, `UPDATE drive_peer_picks SET expires=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PeerFetch(e.peer, "worker", e.root.ID); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("expired pick: %v", err)
	}
	if err := d.ExpireTickets(e.ctx); err != nil || e.count(t, `SELECT COUNT(*) FROM drive_peer_picks`) != 0 {
		t.Errorf("expired picks kept: %v", err)
	}
}
