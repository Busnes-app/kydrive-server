package drive_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// A device token syncs files. It never creates or changes access, and never mints a session or
// credential, so revoking the device leaves nothing behind. Every exported Store method is in
// exactly one list; the guard fails when a new one is in none.
var (
	// Reads, content changes and the device's own unlink.
	deviceAllowed = []string{
		"AddFolder", "AppendUpload", "Authorize", "AuthorizeItem", "CancelUpload", "Changes", "Children", "CompleteUpload",
		"CopyFile", "EnsurePersonalWorkspace", "Events", "File", "Files", "FolderChildren", "FolderTree", "Folders",
		"GuestLinks", "Grants", "Links", "Lookup", "MoveFile", "MoveFolder", "People", "Publish", "PurgeFile",
		"PurgeFolder", "PurgeVersion", "RestoreVersion", "Search", "SetFolderTrash", "SetTrash", "SharedWithMe",
		"Shares", "Snapshot", "StartUpload", "UnlinkDevice", "Upload", "Versions", "Workspaces",
	}
	// Creates or changes access, or mints a session or credential: ErrDenied for a device.
	deviceRefused = []string{
		"ApproveLink", "AuthorizeSharedManager", "CreateAppPassword", "CreateGuestLink", "CreateLink",
		"CreateServiceToken", "CreateWorkspace", "EditorSession", "Grant", "Handover", "OpenLink", "Quota",
		"RevokeAppPassword", "RevokeDevice", "RevokeLink", "RevokeServiceToken", "SetEditorsShare", "SetRetention",
		"Share", "Unshare",
	}
	// Never reached with a device context: credential checks, editor and guest callbacks, the
	// operator CLI, admin-only listings and background workers.
	deviceUnreachable = []string{
		"AppPasswordUser", "AppPasswords", "ApplyRetention", "CallbackActor", "CloseEditor", "Colleagues",
		"DeferRevocation", "DeviceUser", "Devices", "EditorDownload", "ExpireUploads", "GuestFile", "GuestUpload",
		"GuestView", "Leavers", "MarkDropped", "OperatorHandover", "OperatorLeavers", "PendingRevocations",
		"PruneChanges", "PruneDevices", "PublishEditor", "RedeemLink", "RevocationStatus", "RevokeUserEditors", "SealLegacyBlobs",
		"ServiceToken", "SweepOrphans",
	}
)

func TestDeviceMethodTableCoversTheStore(t *testing.T) {
	seen := map[string]int{}
	for _, list := range [][]string{deviceAllowed, deviceRefused, deviceUnreachable} {
		for _, m := range list {
			seen[m]++
		}
	}
	st := reflect.TypeOf(&drive.Store{})
	for i := range st.NumMethod() {
		name := st.Method(i).Name
		if seen[name] != 1 {
			t.Errorf("%s is in %d device lists; classify it in exactly one", name, seen[name])
		}
		delete(seen, name)
	}
	for name := range seen {
		t.Errorf("%s is listed but is not a Store method", name)
	}
}

// Each refused method fails for a device with arguments that succeed without one.
func TestDeviceCannotCreateAccess(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	device := drive.WithDevice(ctx)
	if err := d.Grant(ctx, "admin", w.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "plan.docx"}, blob(t, root, "x"), 0)
	if err != nil {
		t.Fatal(err)
	}
	share, err := d.Share(ctx, "worker", "file", f.ID, "user", "stranger", "reader")
	if err != nil {
		t.Fatal(err)
	}
	orgLink, linkToken, err := d.CreateLink(ctx, "worker", "file", f.ID, "reader", 0)
	if err != nil {
		t.Fatal(err)
	}
	pw, _, err := d.CreateAppPassword(ctx, "worker", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := d.CreateServiceToken(ctx, "worker", w.ID, "Robot", "reader", "svc-device-access-test")
	if err != nil {
		t.Fatal(err)
	}
	link(t, d, "worker")
	dev := mustDevices(t, d, "worker")[0].ID
	week := time.Now().Add(7 * 24 * time.Hour).Unix()

	calls := map[string]func(context.Context) error{
		"EditorSession": func(c context.Context) error { _, err := d.EditorSession(c, "worker", f.ID); return err },
		"Share": func(c context.Context) error {
			_, err := d.Share(c, "worker", "file", f.ID, "group", "team", "reader")
			return err
		},
		"Unshare": func(c context.Context) error { return d.Unshare(c, "worker", share.ID) },
		"CreateLink": func(c context.Context) error {
			_, _, err := d.CreateLink(c, "worker", "file", f.ID, "editor", 0)
			return err
		},
		"OpenLink":   func(c context.Context) error { _, err := d.OpenLink(c, "stranger", linkToken); return err },
		"RevokeLink": func(c context.Context) error { return d.RevokeLink(c, "worker", orgLink.ID) },
		"CreateGuestLink": func(c context.Context) error {
			_, _, err := d.CreateGuestLink(c, "worker", "file", f.ID, week, "", false, 30*24*time.Hour)
			return err
		},
		"SetEditorsShare":        func(c context.Context) error { return d.SetEditorsShare(c, "worker", w.ID, true) },
		"Grant":                  func(c context.Context) error { return d.Grant(c, "worker", w.ID, "team", "manager") },
		"AuthorizeSharedManager": func(c context.Context) error { return d.AuthorizeSharedManager(c, "worker", w.ID) },
		"CreateAppPassword": func(c context.Context) error {
			_, _, err := d.CreateAppPassword(c, "worker", "Phone")
			return err
		},
		"RevokeAppPassword": func(c context.Context) error { return d.RevokeAppPassword(c, "worker", pw.ID) },
		"CreateServiceToken": func(c context.Context) error {
			_, err := d.CreateServiceToken(c, "worker", w.ID, "Robot 2", "reader", "svc-device-access-test-2")
			return err
		},
		"RevokeServiceToken": func(c context.Context) error { return d.RevokeServiceToken(c, "worker", svc) },
		"RevokeDevice":       func(c context.Context) error { return d.RevokeDevice(c, "worker", dev, false) },
		"ApproveLink": func(c context.Context) error {
			_, err := d.ApproveLink(c, "worker", "Laptop", "linux", challengeOf(verifier), "http://127.0.0.1:8080/cb")
			return err
		},
		"CreateWorkspace": func(c context.Context) error { _, err := d.CreateWorkspace(c, "admin", "Second", 10); return err },
		"Quota":           func(c context.Context) error { return d.Quota(c, "admin", w.ID, 200) },
		"SetRetention":    func(c context.Context) error { return d.SetRetention(c, "admin", w.ID, 30, 0) },
		// Handover needs a leaver; the device refusal comes first, so no success case.
		"Handover": nil,
	}
	for _, name := range deviceRefused {
		call, ok := calls[name]
		if !ok {
			t.Errorf("%s has no refusal case", name)
			continue
		}
		if call == nil {
			if _, err := d.Handover(device, "admin", w.ID, drive.Target{Workspace: w.ID}); !errors.Is(err, drive.ErrDenied) {
				t.Errorf("Handover by a device: %v", err)
			}
			continue
		}
		if err := call(device); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%s by a device: %v", name, err)
		}
		if err := call(ctx); err != nil {
			t.Errorf("%s without a device: %v", name, err)
		}
	}
}
