package drive_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// KyPost acting for a user (a peer context) may add new files to that user's Mail attachments
// folder and put a reader organisation link on one of them. Everything else is refused. Every
// exported Store method is in exactly one list.
var (
	peerAllowed = []string{
		"AppendUpload", "CompleteUpload", "CreateLink", "IssueTicket", "PeerFetch", "PeerFree", "PeerGuestLink", "PeerGuestSetting", "PeerSetting", "Publish", "StartUpload",
		"Ticket", "TicketUpload", "Upload",
	}
	peerRefused = []string{
		"AddFolder", "ApproveLink", "ApprovePhone", "AppPasswords", "Authorize", "AuthorizeItem", "AuthorizeSharedManager", "CancelUpload", "Changes", "Children",
		"CopyFile", "CreateAgentToken", "CreateAppPassword", "CreateGuestLink", "CreateServiceToken", "CreateWorkspace", "DeleteWorkspace",
		"DeletedWorkspaces", "Devices", "EditorSession", "EnsurePersonalWorkspace", "Events", "File", "Files", "FolderChildren", "FolderPath",
		"FolderTree", "Folders", "Grant", "Grants", "GuestLinks", "Handover", "Leavers", "Links", "Lookup", "MoveFile", "MoveFolder",
		"OpenLink", "People", "PurgeFile", "PurgeFolder", "PurgeVersion", "Quota", "RenameWorkspace", "RestoreVersion", "RestoreWorkspace",
		"RevokeAppPassword", "RevokeDevice", "RevokeLink", "RevokeServiceToken", "Search", "SearchContent", "SearchFolder", "SearchFolderContent",
		"SetAgentScope", "SetEditorsShare", "SetFolderTrash", "SetFolderTrashSince", "SetPeerSetting", "SetRetention", "SetTrash", "Share",
		"SharedWithMe", "Shares", "Snapshot", "Unshare", "Versions", "Workspaces", "Colleagues", "RecordPicks", "SetPeerGuestSetting",
	}
	// Credential checks, callbacks, the operator CLI and background workers take no user context.
	peerUnreachable = []string{
		"AgentTokenUser", "AppPasswordUser", "ApplyRetention", "CallbackActor", "CloseEditor", "DeferRevocation", "DeviceUser", "EditorDownload",
		"ExpireAgentTokens", "ExpireTickets", "ExpireUploads", "GuestFile", "GuestUpload", "GuestView", "IndexChanges", "IndexFile", "IndexFiles",
		"MarkDropped", "OperatorHandover", "OperatorLeavers", "PendingRevocations", "PruneChanges", "PruneDevices", "PublishEditor",
		"PurgeDeletedWorkspaces", "RedeemLink", "RedeemPhone", "RevocationStatus", "RevokeSSOEditors", "RevokeUserCredentials", "RevokeUserEditors",
		"RevokeUserEditorsBefore", "SealLegacyBlobs", "ServiceToken", "SweepOrphans", "UnlinkDevice", "UpgradeBlobs",
	}
)

func TestPeerMethodTableCoversTheStore(t *testing.T) {
	seen := map[string]int{}
	for _, list := range [][]string{peerAllowed, peerRefused, peerUnreachable} {
		for _, m := range list {
			seen[m]++
		}
	}
	st := reflect.TypeOf(&drive.Store{})
	for i := range st.NumMethod() {
		name := st.Method(i).Name
		if seen[name] != 1 {
			t.Errorf("%s is in %d peer lists; classify it in exactly one", name, seen[name])
		}
		delete(seen, name)
	}
	for name := range seen {
		t.Errorf("%s is listed but is not a Store method", name)
	}
}

// peerEnv is worker's My files with a Mail attachments file saved through a ticket, a file at
// its root and a shared workspace worker manages.
type peerEnv struct {
	d                *drive.Store
	home, shared     string
	staging, blobs   string
	mail, root, team drive.File
	attachments      string
	ctx, peer        context.Context
}

func newPeerEnv(t *testing.T) *peerEnv {
	t.Helper()
	st, w, blobs := fixture(t)
	e := &peerEnv{d: st.Drive(), shared: w.ID, staging: t.TempDir(), blobs: blobs, ctx: context.Background()}
	e.peer = drive.WithPeer(e.ctx, livePeer)
	managers(t, e.d, w)
	var err error
	if e.home, err = e.d.EnsurePersonalWorkspace(e.ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	e.mail = e.save(t, "worker", "mail.pdf", "mail")
	e.attachments = e.mail.Parent
	if e.root, err = e.d.Publish(e.ctx, "worker", drive.File{Workspace: e.home, Name: "root.txt"}, blob(t, blobs, "root"), 0); err != nil {
		t.Fatal(err)
	}
	if e.team, err = e.d.Publish(e.ctx, "worker", drive.File{Workspace: w.ID, Name: "team.txt"}, blob(t, blobs, "team"), 0); err != nil {
		t.Fatal(err)
	}
	return e
}

// save puts body into user's Mail attachments through a ticket, in one request.
func (e *peerEnv) save(t *testing.T, user, name, body string) drive.File {
	t.Helper()
	token, _, err := e.d.IssueTicket(e.peer, user, name, int64(len(body)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := e.d.Ticket(e.ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	_, f, err := e.d.TicketUpload(e.peer, e.staging, e.blobs, testKey, tk, 0, int64(len(body)), strings.NewReader(body))
	if err != nil || f == nil {
		t.Fatalf("ticket upload: %v %v", f, err)
	}
	return *f
}

// Each refused method fails in a peer context with arguments that succeed without one, on a
// file in Mail attachments where the method takes one: holding the folder grants no read,
// listing, move, trash, purge or share of what is in it.
func TestPeerCannotReachAnythingElse(t *testing.T) {
	e := newPeerEnv(t)
	d, ctx := e.d, e.ctx
	week := time.Now().Add(7 * 24 * time.Hour).Unix()
	fresh := func(c context.Context) drive.File { // a new Mail attachments file per destructive call
		return e.save(t, "worker", "x.txt", "x")
	}
	share, err := d.Share(ctx, "worker", "file", e.mail.ID, "user", "stranger", "reader")
	if err != nil {
		t.Fatal(err)
	}
	orgLink, linkToken, err := d.CreateLink(ctx, "worker", "file", e.mail.ID, "reader", 0)
	if err != nil {
		t.Fatal(err)
	}
	pw, _, err := d.CreateAppPassword(ctx, "worker", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := d.CreateAgentToken(ctx, "worker", "Agent", drive.ScopeMCPRead, "", "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := d.CreateServiceToken(ctx, "worker", e.shared, "Robot", "reader", "svc-peer-access-test")
	if err != nil {
		t.Fatal(err)
	}
	link(t, d, "worker")
	dev := mustDevices(t, d, "worker")[0].ID
	spare := ""
	n := 0
	name := func(p string) string { n++; return p + " " + strings.Repeat("i", n) }

	calls := map[string]func(context.Context) error{
		"AddFolder": func(c context.Context) error {
			_, err := d.AddFolder(c, "worker", e.home, e.attachments, name("sub"))
			return err
		},
		"ApprovePhone": func(c context.Context) error { _, err := d.ApprovePhone(c, "worker"); return err },
		"ApproveLink": func(c context.Context) error {
			_, err := d.ApproveLink(c, "worker", "Laptop", "linux", challengeOf(verifier), "http://127.0.0.1:8080/cb")
			return err
		},
		"AppPasswords": func(c context.Context) error { _, err := d.AppPasswords(c, "worker"); return err },
		"Authorize":    func(c context.Context) error { return d.Authorize(c, "worker", e.home, 1) },
		"AuthorizeItem": func(c context.Context) error {
			return d.AuthorizeItem(c, "worker", e.home, e.attachments, e.mail.ID, 1)
		},
		"AuthorizeSharedManager": func(c context.Context) error { return d.AuthorizeSharedManager(c, "worker", e.shared) },
		"CancelUpload": func(c context.Context) error {
			u, err := d.StartUpload(ctx, "worker", drive.UploadSession{Workspace: e.home, Parent: e.attachments, Name: name("c"), Size: 1}, 10)
			if err != nil {
				return err
			}
			return d.CancelUpload(c, e.staging, "worker", u.ID)
		},
		"Changes":  func(c context.Context) error { _, _, _, err := d.Changes(c, "worker", e.home, 0, 10); return err },
		"Children": func(c context.Context) error { _, _, err := d.Children(c, "worker", e.home, e.attachments); return err },
		"CopyFile": func(c context.Context) error {
			_, err := d.CopyFile(c, "worker", e.mail.ID, drive.Location{Workspace: e.home, Parent: e.attachments, Name: name("copy")})
			return err
		},
		"CreateAgentToken": func(c context.Context) error {
			_, _, err := d.CreateAgentToken(c, "worker", name("Agent"), drive.ScopeMCPWrite, e.home, e.attachments)
			return err
		},
		"CreateAppPassword": func(c context.Context) error {
			_, _, err := d.CreateAppPassword(c, "worker", name("Phone"))
			return err
		},
		"CreateGuestLink": func(c context.Context) error {
			_, _, err := d.CreateGuestLink(c, "worker", "file", e.mail.ID, week, "", false, 30*24*time.Hour)
			return err
		},
		"CreateServiceToken": func(c context.Context) error {
			_, err := d.CreateServiceToken(c, "worker", e.shared, name("Robot"), "reader", name("svc-peer"))
			return err
		},
		"CreateWorkspace": func(c context.Context) error { _, err := d.CreateWorkspace(c, "admin", name("Second"), 10); return err },
		"DeleteWorkspace": func(c context.Context) error {
			if spare == "" {
				x, err := d.CreateWorkspace(ctx, "admin", "Spare", 10)
				if err != nil {
					return err
				}
				spare = x.ID
			}
			return deleteEmpty(d, c, "admin", spare)
		},
		"DeletedWorkspaces":       func(c context.Context) error { _, err := d.DeletedWorkspaces(c, "admin"); return err },
		"Devices":                 func(c context.Context) error { _, err := d.Devices(c, "worker"); return err },
		"EditorSession":           func(c context.Context) error { _, err := d.EditorSession(c, "worker", e.mail.ID); return err },
		"EnsurePersonalWorkspace": func(c context.Context) error { _, err := d.EnsurePersonalWorkspace(c, "worker"); return err },
		"Events":                  func(c context.Context) error { _, err := d.Events(c, "admin"); return err },
		"File":                    func(c context.Context) error { _, err := d.File(c, "worker", e.mail.ID, 1); return err },
		"Files":                   func(c context.Context) error { _, err := d.Files(c, "worker", e.home, false); return err },
		"FolderChildren": func(c context.Context) error {
			_, _, _, err := d.FolderChildren(c, "worker", e.attachments)
			return err
		},
		"FolderPath": func(c context.Context) error { _, err := d.FolderPath(c, "worker", e.home, e.attachments); return err },
		"FolderTree": func(c context.Context) error { _, _, err := d.FolderTree(c, "worker", e.attachments); return err },
		"Folders":    func(c context.Context) error { _, err := d.Folders(c, "worker", e.home, false); return err },
		"Grant":      func(c context.Context) error { return d.Grant(c, "worker", e.shared, "team", "manager") },
		"Grants":     func(c context.Context) error { _, err := d.Grants(c, "worker", e.shared); return err },
		"GuestLinks": func(c context.Context) error { _, err := d.GuestLinks(c, "worker", "file", e.mail.ID); return err },
		"Leavers":    func(c context.Context) error { _, err := d.Leavers(c, "admin"); return err },
		"Colleagues": func(c context.Context) error { _, err := d.Colleagues(c, "admin"); return err },
		"Links":      func(c context.Context) error { _, err := d.Links(c, "worker", "file", e.mail.ID); return err },
		"Lookup": func(c context.Context) error {
			_, err := d.Lookup(c, "worker", e.home, e.attachments, "mail.pdf")
			return err
		},
		"MoveFile": func(c context.Context) error {
			f := fresh(c)
			_, err := d.MoveFile(c, "worker", f.ID, 1, drive.Location{Workspace: e.home, Parent: e.attachments, Name: name("moved")})
			return err
		},
		"MoveFolder": func(c context.Context) error {
			_, err := d.MoveFolder(c, "worker", e.attachments, "", drive.PeerFolderName)
			return err
		},
		"OpenLink": func(c context.Context) error { _, err := d.OpenLink(c, "stranger", linkToken); return err },
		"People":   func(c context.Context) error { _, err := d.People(c, "worker", "stra"); return err },
		"PurgeFile": func(c context.Context) error {
			f := fresh(c)
			if err := d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
				return err
			}
			_, err := d.PurgeFile(c, "worker", f.ID)
			return err
		},
		"PurgeFolder": func(c context.Context) error {
			x, err := d.AddFolder(ctx, "worker", e.home, e.attachments, name("gone"))
			if err == nil {
				err = d.SetFolderTrash(ctx, "worker", x.ID, true)
			}
			if err != nil {
				return err
			}
			_, err = d.PurgeFolder(c, "worker", x.ID)
			return err
		},
		"PurgeVersion": func(c context.Context) error {
			f := fresh(c)
			if _, err := d.Publish(ctx, "worker", f, blob(t, e.blobs, "v2"), 1); err != nil {
				return err
			}
			_, err := d.PurgeVersion(c, "worker", f.ID, 1)
			return err
		},
		"Quota":           func(c context.Context) error { return d.Quota(c, "admin", e.shared, 200) },
		"RenameWorkspace": func(c context.Context) error { return d.RenameWorkspace(c, "admin", e.shared, name("Renamed")) },
		"RestoreVersion": func(c context.Context) error {
			f := fresh(c)
			v2, err := d.Publish(ctx, "worker", f, blob(t, e.blobs, "v2"), 1)
			if err != nil {
				return err
			}
			_, err = d.RestoreVersion(c, "worker", f.ID, 1, v2.Revision)
			return err
		},
		"RestoreWorkspace": func(c context.Context) error {
			x, err := d.CreateWorkspace(ctx, "admin", name("Deleted"), 10)
			if err == nil {
				err = d.Grant(ctx, "admin", x.ID, "team", "manager")
			}
			if err == nil {
				_, err = d.AddFolder(ctx, "worker", x.ID, "", "kept")
			}
			if err == nil {
				_, err = d.DeleteWorkspace(ctx, "admin", x.ID, x.Name)
			}
			if err != nil {
				return err
			}
			return d.RestoreWorkspace(c, "admin", x.ID)
		},
		"RevokeAppPassword":  func(c context.Context) error { return d.RevokeAppPassword(c, "worker", pw.ID) },
		"RevokeDevice":       func(c context.Context) error { return d.RevokeDevice(c, "worker", dev, false) },
		"RevokeLink":         func(c context.Context) error { return d.RevokeLink(c, "worker", orgLink.ID) },
		"RevokeServiceToken": func(c context.Context) error { return d.RevokeServiceToken(c, "worker", svc) },
		"Search":             func(c context.Context) error { _, _, err := d.Search(c, "worker", "mail", 10); return err },
		"SearchContent": func(c context.Context) error {
			_, _, err := d.SearchContent(c, "worker", "mail", "", 10, nil)
			return err
		},
		"SearchFolder": func(c context.Context) error {
			_, _, err := d.SearchFolder(c, "worker", e.home, e.attachments, "mail", 10)
			return err
		},
		"SearchFolderContent": func(c context.Context) error {
			_, _, err := d.SearchFolderContent(c, "worker", e.home, e.attachments, "mail", "", 10, nil)
			return err
		},
		"SetAgentScope":   func(c context.Context) error { return d.SetAgentScope(c, "worker", agent.ID, drive.ScopeMCPWrite) },
		"SetEditorsShare": func(c context.Context) error { return d.SetEditorsShare(c, "worker", e.shared, true) },
		"SetFolderTrash": func(c context.Context) error {
			x, err := d.AddFolder(ctx, "worker", e.home, e.attachments, name("t"))
			if err != nil {
				return err
			}
			return d.SetFolderTrash(c, "worker", x.ID, true)
		},
		"SetFolderTrashSince": func(c context.Context) error {
			x, err := d.AddFolder(ctx, "worker", e.home, e.attachments, name("ts"))
			if err != nil {
				return err
			}
			return d.SetFolderTrashSince(c, "worker", x.ID, true, 1<<40)
		},
		"SetPeerSetting":      func(c context.Context) error { return d.SetPeerSetting(c, "worker", true) },
		"RecordPicks":         func(c context.Context) error { return d.RecordPicks(c, "worker", []string{e.mail.ID}) },
		"SetPeerGuestSetting": func(c context.Context) error { return d.SetPeerGuestSetting(c, "worker", true) },
		"SetRetention":        func(c context.Context) error { return d.SetRetention(c, "admin", e.shared, 30, 0) },
		"SetTrash":            func(c context.Context) error { return d.SetTrash(c, "worker", fresh(c).ID, 1, true) },
		"Share": func(c context.Context) error {
			_, err := d.Share(c, "worker", "file", e.mail.ID, "group", "team", "reader")
			return err
		},
		"SharedWithMe": func(c context.Context) error { _, err := d.SharedWithMe(c, "stranger"); return err },
		"Shares":       func(c context.Context) error { _, err := d.Shares(c, "worker", "file", e.mail.ID); return err },
		"Snapshot":     func(c context.Context) error { _, _, _, _, err := d.Snapshot(c, "worker", e.home, "", 10); return err },
		"Unshare":      func(c context.Context) error { return d.Unshare(c, "worker", share.ID) },
		"Versions":     func(c context.Context) error { _, err := d.Versions(c, "worker", e.mail.ID); return err },
		"Workspaces":   func(c context.Context) error { _, err := d.Workspaces(c, "worker", false); return err },
		// Handover needs a leaver; the refusal comes first, so no success case.
		"Handover": nil,
	}
	for _, m := range peerRefused {
		call, ok := calls[m]
		if !ok {
			t.Errorf("%s has no refusal case", m)
			continue
		}
		if call == nil {
			if _, err := d.Handover(e.peer, "admin", e.home, drive.Target{Workspace: e.shared}); !errors.Is(err, drive.ErrDenied) {
				t.Errorf("Handover by KyPost: %v", err)
			}
			continue
		}
		if err := call(e.peer); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%s by KyPost: %v", m, err)
		}
		if err := call(ctx); err != nil {
			t.Errorf("%s without KyPost: %v", m, err)
		}
	}
}

// The allowed methods are narrower than they look: only a new file in the user's own Mail
// attachments folder, and only a reader organisation link on a file directly in it.
func TestPeerAllowedMethodsStayInTheFolder(t *testing.T) {
	e := newPeerEnv(t)
	d, p := e.d, e.peer
	v := func() drive.Version { return blob(t, e.blobs, "new") }
	refusals := map[string]error{}
	_, refusals["Publish at the root of My files"] = d.Publish(p, "worker", drive.File{Workspace: e.home, Name: "a.txt"}, v(), 0)
	_, refusals["Publish into a shared workspace"] = d.Publish(p, "worker", drive.File{Workspace: e.shared, Name: "a.txt"}, v(), 0)
	_, refusals["Publish a new version"] = d.Publish(p, "worker", e.mail, v(), e.mail.Revision)
	_, refusals["Publish into another user's folder"] = d.Publish(p, "stranger", drive.File{Workspace: e.home, Parent: e.attachments, Name: "a.txt"}, v(), 0)
	_, refusals["StartUpload at the root"] = d.StartUpload(p, "worker", drive.UploadSession{Workspace: e.home, Name: "a.bin", Size: 1}, 10)
	_, refusals["StartUpload into a shared workspace"] = d.StartUpload(p, "worker", drive.UploadSession{Workspace: e.shared, Name: "a.bin", Size: 1}, 10)
	_, refusals["StartUpload of a new version"] = d.StartUpload(p, "worker", drive.UploadSession{Workspace: e.home, File: e.mail.ID, Expected: e.mail.Revision, Size: 1}, 10)
	u, err := d.StartUpload(e.ctx, "worker", drive.UploadSession{Workspace: e.home, Name: "own.bin", Size: 1}, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, refusals["Upload of a session at the root"] = d.Upload(p, "worker", u.ID)
	_, _, refusals["CreateLink editor"] = d.CreateLink(p, "worker", "file", e.mail.ID, "editor", 0)
	_, _, refusals["CreateLink on the folder"] = d.CreateLink(p, "worker", "folder", e.attachments, "reader", 0)
	_, _, refusals["CreateLink on a file at the root"] = d.CreateLink(p, "worker", "file", e.root.ID, "reader", 0)
	_, _, refusals["CreateLink on a shared workspace file"] = d.CreateLink(p, "worker", "file", e.team.ID, "reader", 0)
	_, _, refusals["CreateLink as another user"] = d.CreateLink(p, "stranger", "file", e.mail.ID, "reader", 0)
	_, _, refusals["IssueTicket without a peer context"] = d.IssueTicket(e.ctx, "worker", "a.txt", 1, 10)
	if err := d.SetPeerGuestSetting(e.ctx, "worker", true); err != nil {
		t.Fatal(err)
	}
	week, life := time.Now().Add(7*24*time.Hour).Unix(), 30*24*time.Hour
	_, _, refusals["PeerGuestLink without a peer context"] = d.PeerGuestLink(e.ctx, "worker", e.mail.ID, week, life)
	_, _, refusals["PeerGuestLink on a file at the root"] = d.PeerGuestLink(p, "worker", e.root.ID, week, life)
	_, _, refusals["PeerGuestLink on a shared workspace file"] = d.PeerGuestLink(p, "worker", e.team.ID, week, life)
	_, _, refusals["PeerGuestLink as another user"] = d.PeerGuestLink(p, "stranger", e.mail.ID, week, life)
	for what, err := range refusals {
		if !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%s: %v", what, err)
		}
	}
	if _, _, err := d.CreateLink(p, "worker", "file", e.mail.ID, "reader", 0); err != nil {
		t.Fatalf("reader link in Mail attachments: %v", err)
	}
	if _, _, err := d.PeerGuestLink(p, "worker", e.mail.ID, week, life); err != nil {
		t.Fatalf("guest link in Mail attachments: %v", err)
	}
	if err := d.SetPeerGuestSetting(e.ctx, "worker", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PeerGuestLink(p, "worker", e.mail.ID, week, life); !errors.Is(err, drive.ErrPeerGuestOff) {
		t.Errorf("guest switch off: %v", err)
	}
	if _, err := d.Publish(p, "worker", drive.File{Workspace: e.home, Parent: e.attachments, Name: "direct.txt"}, v(), 0); err != nil {
		t.Fatalf("new file in Mail attachments: %v", err)
	}
}

// livePeer is a KyIdentity pairing that stays paired.
var livePeer = drive.Peer{SwitchDefault: true, Live: func() bool { return true }, Pairing: "pairing-1"}
