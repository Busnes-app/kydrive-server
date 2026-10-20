package drive_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

const day = 24 * time.Hour

// An agent token is a Bearer credential for /mcp only: it never signs in as a WebDAV password,
// a WebDAV password never signs in as an agent, and neither works for another user.
func TestAgentTokensAndAppPasswordsNeverCross(t *testing.T) {
	st, _, _ := fixture(t)
	d, ctx, now := st.Drive(), context.Background(), time.Now()
	p, token, err := d.CreateAgentToken(ctx, "worker", "Hermes", drive.ScopeMCPRead, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^kya_[a-z2-7]{32}$`).MatchString(token) || p.Scope != drive.ScopeMCPRead {
		t.Fatalf("%+v %q", p, token)
	}
	if want := now.Add(drive.AgentIdleLimit).Unix(); p.RevokeAfter < want-5 || p.RevokeAfter > want+5 {
		t.Errorf("revoke_after %d, want about %d", p.RevokeAfter, want)
	}
	user, got, err := d.AgentTokenUser(ctx, token, now)
	if err != nil || user != "worker" || got.ID != p.ID || got.Scope != drive.ScopeMCPRead {
		t.Fatalf("agent token: %q %+v %v", user, got, err)
	}
	_, davSecret, err := d.CreateAppPassword(ctx, "worker", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	bare := strings.TrimPrefix(token, drive.AgentTokenPrefix)
	for _, secret := range []string{token, bare, strings.ToUpper(bare)} {
		if _, err := d.AppPasswordUser(ctx, "worker", secret); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("agent token %q signed in over WebDAV: %v", secret, err)
		}
	}
	for _, secret := range []string{davSecret, drive.AgentTokenPrefix + davSecret, drive.AgentTokenPrefix + strings.ReplaceAll(davSecret, "-", ""), bare, drive.AgentTokenPrefix, ""} {
		if _, _, err := d.AgentTokenUser(ctx, secret, now); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%q signed in as an agent: %v", secret, err)
		}
	}
	if id, err := d.AppPasswordUser(ctx, "worker", davSecret); err != nil || id != "worker" {
		t.Fatalf("the WebDAV password itself stopped working: %q %v", id, err)
	}

	// Deactivation stops the token on the next use.
	if err = drive.ExecSQL(ctx, d, `UPDATE users SET status='inactive' WHERE id='worker'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.AgentTokenUser(ctx, token, now); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("inactive user's agent token: %v", err)
	}
}

func TestAgentTokenScopesAndRoots(t *testing.T) {
	st, w, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	for _, c := range []struct{ scope, workspace, folder string }{
		{drive.ScopeDAV, "", ""}, {"mcp:admin", "", ""}, {"", "", ""}, {drive.ScopeMCPRead, "", "some-folder"},
	} {
		if _, _, err := d.CreateAgentToken(ctx, "worker", "Agent", c.scope, c.workspace, c.folder); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	projects, err := d.AddFolder(ctx, "worker", w.ID, "", "Projects")
	if err != nil {
		t.Fatal(err)
	}
	old, err := d.AddFolder(ctx, "worker", w.ID, "", "Old")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "worker", old.ID, true); err != nil {
		t.Fatal(err)
	}
	mine, err := d.EnsurePersonalWorkspace(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	// The root must be a live folder in that workspace that the user can read.
	if _, _, err = d.CreateAgentToken(ctx, "stranger", "Agent", drive.ScopeMCPRead, w.ID, ""); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("a workspace the user cannot read: %v", err)
	}
	for name, folder := range map[string]string{"trashed": old.ID, "missing": "no-such-folder"} {
		if _, _, err = d.CreateAgentToken(ctx, "worker", "Agent", drive.ScopeMCPRead, w.ID, folder); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("%s folder: %v", name, err)
		}
	}
	if _, _, err = d.CreateAgentToken(ctx, "worker", "Agent", drive.ScopeMCPRead, mine, projects.ID); !errors.Is(err, drive.ErrInvalid) {
		t.Errorf("a folder of another workspace: %v", err)
	}
	p, token, err := d.CreateAgentToken(ctx, "worker", "Agent", drive.ScopeMCPRead, w.ID, projects.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, got, err := d.AgentTokenUser(ctx, token, time.Now()); err != nil || got.Workspace != w.ID || got.Folder != projects.ID {
		t.Fatalf("root %+v %v", got, err)
	}
	if path, err := d.FolderPath(ctx, "worker", w.ID, projects.ID); err != nil || strings.Join(path, "/") != "Projects" {
		t.Fatalf("folder path %q %v", path, err)
	}

	// Access moves between read and write; a WebDAV password never becomes an agent token.
	if err = d.SetAgentScope(ctx, "worker", p.ID, drive.ScopeMCPWrite); err != nil {
		t.Fatal(err)
	}
	if _, got, _ := d.AgentTokenUser(ctx, token, time.Now()); got.Scope != drive.ScopeMCPWrite {
		t.Fatalf("scope after widening: %+v", got)
	}
	if err = d.SetAgentScope(ctx, "worker", p.ID, drive.ScopeDAV); !errors.Is(err, drive.ErrInvalid) {
		t.Errorf("agent to WebDAV: %v", err)
	}
	if err = d.SetAgentScope(ctx, "stranger", p.ID, drive.ScopeMCPRead); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("another user's token: %v", err)
	}
	dav, _, err := d.CreateAppPassword(ctx, "worker", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAgentScope(ctx, "worker", dav.ID, drive.ScopeMCPRead); !errors.Is(err, drive.ErrInvalid) {
		t.Errorf("WebDAV to agent: %v", err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_events WHERE action='app_password.scope_changed'`); n != 1 {
		t.Errorf("%d scope_changed events, want 1", n)
	}
	list, err := d.AppPasswords(ctx, "worker")
	if err != nil || len(list) != 2 || list[0].Scope != drive.ScopeMCPWrite || list[0].Folder != projects.ID || list[1].Scope != drive.ScopeDAV || list[1].RevokeAfter != 0 {
		t.Fatalf("list %+v %v", list, err)
	}

	// A trashed root has no path, so the agent sees the folder gone.
	if err = d.SetFolderTrash(ctx, "worker", projects.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = d.FolderPath(ctx, "worker", w.ID, projects.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Errorf("trashed root: %v", err)
	}
}

// An agent token unused for 90 days is revoked: refused and deleted at sign-in even if the
// sweep never ran, and swept otherwise. A never-used token counts from creation, use resets
// the clock, and WebDAV passwords are never swept.
func TestAgentTokensExpireWhenIdle(t *testing.T) {
	st, _, _ := fixture(t)
	d, ctx, now := st.Drive(), context.Background(), time.Now()
	used, usedToken, err := d.CreateAgentToken(ctx, "worker", "Used", drive.ScopeMCPRead, "", "")
	if err != nil {
		t.Fatal(err)
	}
	idle, idleToken, err := d.CreateAgentToken(ctx, "worker", "Idle", drive.ScopeMCPWrite, "", "")
	if err != nil {
		t.Fatal(err)
	}
	never, _, err := d.CreateAgentToken(ctx, "worker", "Never", drive.ScopeMCPRead, "", "")
	if err != nil {
		t.Fatal(err)
	}
	dav, _, err := d.CreateAppPassword(ctx, "worker", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.AgentTokenUser(ctx, usedToken, now.Add(89*day)); err != nil {
		t.Fatalf("89 days unused: %v", err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT last_used FROM drive_app_passwords WHERE id=?`, used.ID); int64(n) != now.Add(89*day).Unix() {
		t.Errorf("use did not reset the clock: last_used %d", n)
	}
	if _, _, err = d.AgentTokenUser(ctx, idleToken, now.Add(91*day)); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("91 days unused: %v", err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_app_passwords WHERE id=?`, idle.ID); n != 0 {
		t.Error("an idle token refused at sign-in is still stored")
	}

	if n, err := d.ExpireAgentTokens(ctx, now.Add(89*day)); err != nil || n != 0 {
		t.Fatalf("sweep at 89 days: %d %v", n, err)
	}
	if n, err := d.ExpireAgentTokens(ctx, now.Add(91*day)); err != nil || n != 1 {
		t.Fatalf("sweep at 91 days: %d %v", n, err)
	}
	for id, want := range map[string]int{never.ID: 0, used.ID: 1, dav.ID: 1} {
		if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_app_passwords WHERE id=?`, id); n != want {
			t.Errorf("token %s: %d rows, want %d", id, n, want)
		}
	}
	if n, err := d.ExpireAgentTokens(ctx, now.Add(10*365*day)); err != nil || n != 1 {
		t.Fatalf("sweep in ten years: %d %v (the WebDAV password must stay)", n, err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_events WHERE action='app_password.expired'`); n != 3 {
		t.Errorf("%d app_password.expired events, want 3", n)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_app_passwords WHERE id=?`, dav.ID); n != 1 {
		t.Error("a WebDAV password was swept")
	}
}

// SearchFolder finds names only beneath its folder, and paths start there.
func TestSearchFolderStaysInsideItsFolder(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	projects, err := d.AddFolder(ctx, "worker", w.ID, "", "Projects")
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := d.AddFolder(ctx, "worker", w.ID, projects.ID, "Alpha report")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []drive.File{{Workspace: w.ID, Parent: alpha.ID, Name: "report.txt"}, {Workspace: w.ID, Name: "report.txt"}, {Workspace: w.ID, Parent: projects.ID, Name: "report.md"}} {
		if _, err := d.Publish(ctx, "worker", f, blob(t, root, "x"), 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = d.AddFolder(ctx, "worker", w.ID, "", "report archive"); err != nil {
		t.Fatal(err)
	}
	hits, more, err := d.SearchFolder(ctx, "worker", w.ID, projects.ID, "report", 50)
	if err != nil || more {
		t.Fatal(err, more)
	}
	got := strings.Join(hitNames(hits), ",")
	if want := w.ID + ":/Alpha report," + w.ID + ":/report.md," + w.ID + ":Alpha report/report.txt"; got != want {
		t.Errorf("hits %s\nwant %s", got, want)
	}
	hits, _, err = d.SearchFolder(ctx, "worker", w.ID, "", "report", 50)
	if err != nil || len(hits) != 5 {
		t.Errorf("whole workspace: %v %v", hitNames(hits), err)
	}
	if _, _, err = d.SearchFolder(ctx, "stranger", w.ID, projects.ID, "report", 50); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("a stranger searched the folder: %v", err)
	}
}
