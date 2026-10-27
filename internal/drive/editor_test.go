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
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func revoked(t *testing.T, d *drive.Store, id string) bool {
	t.Helper()
	n, err := drive.QueryInt(context.Background(), d, `SELECT COUNT(*) FROM drive_editor_sessions WHERE id=? AND revoked=true`, id)
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestAccountSecurityRevokesEditorSessions(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(store.Store) error{
		"password change": func(st store.Store) error {
			if err := drive.ExecSQL(ctx, st.Drive(), `UPDATE users SET sso_provider='local',password_hash='old',must_change_password=true WHERE id='worker'`); err != nil {
				return err
			}
			return st.Users().CompletePasswordChange(ctx, "worker", "old", "new", "")
		},
		"logout": func(st store.Store) error {
			if err := drive.ExecSQL(ctx, st.Drive(), `INSERT INTO sessions(token_hash,user_id,user_agent,ip_address,created_at,expires_at) VALUES('h','worker','','',CURRENT_TIMESTAMP,'2999-01-01 00:00:00')`); err != nil {
				return err
			}
			return st.Sessions().EndSession(ctx, "h")
		},
		"back-channel logout": func(st store.Store) error {
			if err := drive.ExecSQL(ctx, st.Drive(), `INSERT INTO sessions(token_hash,user_id,user_agent,ip_address,created_at,expires_at,sso_sid) VALUES('h','worker','','',CURRENT_TIMESTAMP,'2999-01-01 00:00:00','sid')`); err != nil {
				return err
			}
			return st.Sessions().EndSSOSession(ctx, "sid", time.Now().Add(time.Minute))
		},
		"subject-wide back-channel logout": func(st store.Store) error {
			if err := drive.ExecSQL(ctx, st.Drive(), `UPDATE users SET sso_provider='kysignon',sso_subject='worker-sub' WHERE id='worker'`); err != nil {
				return err
			}
			if err := drive.ExecSQL(ctx, st.Drive(), `INSERT INTO sessions(token_hash,user_id,user_agent,ip_address,created_at,expires_at) VALUES('h','worker','','','2000-01-01 00:00:00','2999-01-01 00:00:00')`); err != nil {
				return err
			}
			return st.Sessions().EndSSOSubject(ctx, "kysignon", "worker-sub", time.Now(), time.Now().Add(time.Minute))
		},
		"directory disable": func(st store.Store) error {
			_, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: "worker", Username: "worker", Email: "worker@local.test", Active: false})
			return err
		},
		"mfa disable": func(st store.Store) error {
			if err := drive.ExecSQL(ctx, st.Drive(), `UPDATE users SET totp_enabled=true WHERE id='worker'`); err != nil {
				return err
			}
			return st.Users().DisableTOTP(ctx, "worker", "", "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			st, w, root := fixture(t)
			d := st.Drive()
			f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.docx"}, blob(t, root, "a"), 0)
			e, err := d.EditorSession(ctx, "worker", f.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = change(st); err != nil {
				t.Fatal(err)
			}
			if !revoked(t, d, e.ID) {
				t.Fatal("editor session survived")
			}
			if _, err = d.EditorDownload(ctx, e.ID); !errors.Is(err, drive.ErrDenied) {
				t.Fatalf("revoked download: %v", err)
			}
			if pending, err := d.PendingRevocations(ctx); err != nil || len(pending) != 1 || pending[0].Key != e.Key {
				t.Fatalf("drop not queued for the old key: %v %v", pending, err)
			}
			if name == "directory disable" {
				return
			}
			again, err := d.EditorSession(ctx, "worker", f.ID)
			if err != nil {
				t.Fatal(err)
			}
			if again.Key == e.Key {
				t.Fatal("document key not rotated after revocation")
			}
		})
	}
}

func TestExpiredSessionKeepsFinalSaveWhileDocumentOpen(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.docx"}, blob(t, root, "a"), 0)
	e, err := d.EditorSession(ctx, "worker", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = drive.ExecSQL(ctx, d, `UPDATE drive_editor_sessions SET expires=?`, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PendingRevocations(ctx); err != nil || revoked(t, d, e.ID) {
		t.Fatalf("connected editor dropped for age: %v", err)
	}
	file, revision, epoch, _ := drive.ParseDocumentKey(e.Key)
	actor, err := d.CallbackActor(ctx, file, revision, epoch)
	if err != nil {
		t.Fatalf("final save refused for age: %v", err)
	}
	if _, err = d.PublishEditor(ctx, actor, f, blob(t, root, "final"), e.Key, true); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PendingRevocations(ctx); err != nil || !revoked(t, d, e.ID) {
		t.Fatalf("expired session outlived its closed document: %v", err)
	}
	if _, err = d.CallbackActor(ctx, file, revision, epoch); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("revoked session saved: %v", err)
	}
}

func TestExpiredSessionOnStaleOpenDocumentIsRevokedAfterGrace(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.docx"}, blob(t, root, "a"), 0)
	e, _ := d.EditorSession(ctx, "worker", f.ID)
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_editor_sessions SET expires=?`, time.Now().Add(-8*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PendingRevocations(ctx); err != nil || !revoked(t, d, e.ID) {
		t.Fatalf("stale session never revoked: %v", err)
	}
}

func TestForceSavedDocumentKeepsItsKey(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", ExternalID: "external-team", Members: []string{"worker", "stranger"}}, false); err != nil {
		t.Fatal(err)
	}
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.docx"}, blob(t, root, "a"), 0)
	first, err := d.EditorSession(ctx, "worker", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.PublishEditor(ctx, "worker", f, blob(t, root, "force"), first.Key, false); err != nil {
		t.Fatal(err)
	}
	second, err := d.EditorSession(ctx, "stranger", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Key != first.Key {
		t.Fatalf("co-editor got key %s, open document is %s", second.Key, first.Key)
	}
	if got, err := d.EditorDownload(ctx, second.ID); err != nil || got.Revision != 2 {
		t.Fatalf("joined download: %+v %v", got, err)
	}
	file, revision, epoch, _ := drive.ParseDocumentKey(second.Key)
	actor, err := d.CallbackActor(ctx, file, revision, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.PublishEditor(ctx, actor, f, blob(t, root, "force 2"), second.Key, false); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PublishEditor(ctx, actor, f, blob(t, root, "final"), second.Key, true); err != nil {
		t.Fatalf("final save conflicted: %v", err)
	}
	if cur, _ := d.File(ctx, "worker", f.ID, 1); cur.Revision != 4 {
		t.Fatalf("revision %d", cur.Revision)
	}
	if next, _ := d.EditorSession(ctx, "worker", f.ID); next.Key == first.Key {
		t.Fatal("closed document reused")
	}
}

func TestDocumentClosedWithoutChangesOpensAgain(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.docx"}, blob(t, root, "a"), 0)
	e, _ := d.EditorSession(ctx, "worker", f.ID)
	if err := d.CloseEditor(ctx, e.Key); err != nil {
		t.Fatal(err)
	}
	again, _ := d.EditorSession(ctx, "worker", f.ID)
	if again.Key != e.Key {
		t.Fatalf("unchanged document changed key: %s %s", again.Key, e.Key)
	}
	if _, err := d.PublishEditor(ctx, "worker", f, blob(t, root, "edit"), again.Key, true); err != nil {
		t.Fatalf("reopened document refused a save: %v", err)
	}
}

func TestEditorSaveReturningToEarlierContentPublishes(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.docx"}, blob(t, root, "base"), 0)
	e, _ := d.EditorSession(ctx, "worker", f.ID)
	for i, body := range []string{"A", "B", "A"} {
		duplicate, err := d.PublishEditor(ctx, "worker", f, blob(t, root, body), e.Key, i == 2)
		if err != nil || duplicate {
			t.Fatalf("save %d %s: duplicate=%v %v", i, body, duplicate, err)
		}
	}
	cur, _ := d.File(ctx, "worker", f.ID, 1)
	if cur.Revision != 4 || cur.Digest != blob(t, root, "A").Digest {
		t.Fatalf("final A not published: %+v", cur)
	}
	if duplicate, err := d.PublishEditor(ctx, "worker", f, blob(t, root, "A"), e.Key, true); err != nil || !duplicate {
		t.Fatalf("retry of the newest save: %v %v", duplicate, err)
	}
}

func TestFailingDropsDoNotStarveNewRevocations(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.docx"}, blob(t, root, "a"), 0)
	if err := drive.ExecSQL(ctx, d, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<150) INSERT INTO drive_editor_sessions(id,file_id,revision,user_id,token_hash,expires,revoked) SELECT 'old-'||i,?,1,'worker','',0,true FROM n`, f.ID); err != nil {
		t.Fatal(err)
	}
	// Every row fails exactly once; a slow run can bring early rows due again, so skip seen ones.
	failed := map[string]bool{}
	for len(failed) < 150 {
		pending, err := d.PendingRevocations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fresh := 0
		for _, e := range pending {
			if failed[e.ID] {
				continue
			}
			if err = d.DeferRevocation(ctx, e.ID); err != nil {
				t.Fatal(err)
			}
			failed[e.ID] = true
			fresh++
		}
		if fresh == 0 {
			t.Fatalf("only %d of 150 rows came due", len(failed))
		}
	}
	e, _ := d.EditorSession(ctx, "worker", f.ID)
	if err := d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	pending, err := d.PendingRevocations(ctx)
	if err != nil || len(pending) == 0 || pending[0].ID != e.ID {
		t.Fatalf("new revocation starved: %d pending, %v", len(pending), err)
	}
	if err = d.DeferRevocation(ctx, "old-1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT next_attempt-? FROM drive_editor_sessions WHERE id='old-1' AND attempts=2`, time.Now().Unix()); n < 3 || n > 5 {
		t.Fatalf("second failure backs off %ds, want 4", n)
	}
}

func TestSweepOrphansKeepsReferencedAndFreshFiles(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	kept := blob(t, root, "kept")
	if _, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, kept, 0); err != nil {
		t.Fatal(err)
	}
	orphan, fresh := blob(t, root, "orphan"), blob(t, root, "in flight")
	old := time.Now().Add(-2 * time.Hour)
	touch := func(name string, at time.Time) string {
		p := filepath.Join(root, name)
		if _, err := os.Stat(p); err != nil {
			if err = os.WriteFile(p, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	touch(kept.Blob, old)
	touch(orphan.Blob, old)
	touch(".upload-stale", old)
	touch(".upload-fresh", time.Now())
	touch("notes.txt", old)
	n, err := d.SweepOrphans(ctx, root, time.Hour)
	if err != nil || n != 2 {
		t.Fatalf("removed %d: %v", n, err)
	}
	for name, want := range map[string]bool{kept.Blob: true, fresh.Blob: true, ".upload-fresh": true, "notes.txt": true, orphan.Blob: false, ".upload-stale": false} {
		if _, err := os.Stat(filepath.Join(root, name)); (err == nil) != want {
			t.Errorf("%s present=%v, want %v", name, err == nil, want)
		}
	}
}

func TestQuotaUsageUsesWorkspaceIndex(t *testing.T) {
	st, w, _ := fixture(t)
	plan, err := drive.QueryPlan(context.Background(), st.Drive(), drive.UsedQuery, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(plan, "\n"); !strings.Contains(joined, "drive_files_workspace") {
		t.Fatalf("usage scans drive_files:\n%s", joined)
	}
}

func TestGlobalTrashRetention(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	mine, err := d.EnsurePersonalWorkspace(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	trashed := func(workspace, name string) drive.File {
		f, err := d.Publish(ctx, "worker", drive.File{Workspace: workspace, Name: name}, blob(t, root, name), 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
			t.Fatal(err)
		}
		return f
	}
	exists := func(f drive.File) bool {
		n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_files WHERE id=?`, f.ID)
		return n == 1
	}
	day := 24 * time.Hour
	personal := trashed(mine, "p.txt")
	shared := trashed(w.ID, "s.txt")
	if _, err = d.ApplyRetention(ctx, time.Now().Add(365*day), 0); err != nil || !exists(personal) || !exists(shared) {
		t.Fatalf("0 purged: %v", err)
	}
	if _, err = d.ApplyRetention(ctx, time.Now().Add(89*day), 90); err != nil || !exists(personal) {
		t.Fatalf("purged at 89 days: %v", err)
	}
	if err = d.SetRetention(ctx, "admin", w.ID, 200, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = d.ApplyRetention(ctx, time.Now().Add(91*day), 90); err != nil || exists(personal) {
		t.Fatalf("personal trash kept past 90 days: %v", err)
	}
	if !exists(shared) {
		t.Fatal("workspace trash_days 200 did not override the global 90")
	}
	if err = d.SetRetention(ctx, "admin", w.ID, 10, 0); err != nil {
		t.Fatal(err)
	}
	open := trashed(mine, "open.txt")
	if err = drive.ExecSQL(ctx, d, `INSERT INTO drive_editor_sessions(id,file_id,revision,user_id,token_hash,expires,revoked) VALUES('undropped',?,1,'worker','',0,true)`, open.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.ApplyRetention(ctx, time.Now().Add(11*day), 90); err != nil || exists(shared) {
		t.Fatalf("workspace trash_days 10 did not override the global 90: %v", err)
	}
	if _, err = d.ApplyRetention(ctx, time.Now().Add(91*day), 90); err != nil || !exists(open) {
		t.Fatalf("purged before the editor drop: %v", err)
	}
}

// A back-channel logout revokes the editor sessions opened through that sign-in even after
// its browser session expired and was pruned; other sign-ins keep theirs.
func TestBackchannelLogoutRevokesEditorsOfPrunedSessions(t *testing.T) {
	ctx := context.Background()
	loginAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	for name, logout := range map[string]func(store.Store) error{
		"sid": func(st store.Store) error {
			return st.Sessions().EndSSOSession(ctx, "sid-old", time.Now().Add(time.Minute))
		},
		"subject": func(st store.Store) error {
			return st.Sessions().EndSSOSubject(ctx, "kysignon", "worker-sub", loginAt.Add(time.Second), time.Now().Add(time.Minute))
		},
	} {
		t.Run(name, func(t *testing.T) {
			st, w, root := fixture(t)
			d := st.Drive()
			if err := drive.ExecSQL(ctx, d, `UPDATE users SET sso_subject='worker-sub' WHERE id='worker'`); err != nil {
				t.Fatal(err)
			}
			open := func(sid string, at time.Time, name string) drive.EditorSession {
				t.Helper()
				if err := drive.ExecSQL(ctx, d, `INSERT INTO sessions(token_hash,user_id,user_agent,ip_address,created_at,expires_at,sso_sid) VALUES(?,'worker','','',?,?,?)`, "h-"+sid, at, time.Now().Add(time.Hour), sid); err != nil {
					t.Fatal(err)
				}
				f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: name}, blob(t, root, name), 0)
				e, err := d.EditorSession(drive.WithLogin(ctx, drive.Login{TokenHash: "h-" + sid, SSOSID: sid, At: at}), "worker", f.ID)
				if err != nil {
					t.Fatal(err)
				}
				return e
			}
			ended, kept := open("sid-old", loginAt, "a.docx"), open("sid-new", loginAt.Add(time.Second), "b.docx")
			if err := drive.ExecSQL(ctx, d, `DELETE FROM sessions`); err != nil {
				t.Fatal(err)
			}
			if err := logout(st); err != nil {
				t.Fatal(err)
			}
			if !revoked(t, d, ended.ID) || revoked(t, d, kept.ID) {
				t.Fatalf("revoked: ended %v, kept %v", revoked(t, d, ended.ID), revoked(t, d, kept.ID))
			}
			file, revision, epoch, _ := drive.ParseDocumentKey(ended.Key)
			if _, err := d.CallbackActor(ctx, file, revision, epoch); !errors.Is(err, drive.ErrDenied) {
				t.Fatalf("logged-out editor saved: %v", err)
			}
		})
	}
}

// A sign-in whose browser session a logout already ended cannot open an editor afterwards.
func TestEditorSessionNeedsALiveLogin(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.docx"}, blob(t, root, "a"), 0)
	gone := drive.WithLogin(ctx, drive.Login{TokenHash: "h", SSOSID: "sid", At: time.Now()})
	if _, err := d.EditorSession(gone, "worker", f.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("editor opened without a live session: %v", err)
	}
	if err := drive.ExecSQL(ctx, d, `INSERT INTO sessions(token_hash,user_id,user_agent,ip_address,created_at,expires_at,sso_sid) VALUES('h','worker','','',CURRENT_TIMESTAMP,'2999-01-01 00:00:00','sid')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.EditorSession(gone, "worker", f.ID); err != nil {
		t.Fatal(err)
	}
}

// Drive schema 17 revokes, once, the open editor sessions of directory accounts from before
// editor sessions recorded their sign-in; a local account's stay open.
func TestLoginSchemaRevokesUnboundDirectoryEditors(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", ExternalID: "external-team", Members: []string{"worker", "stranger"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := drive.ExecSQL(ctx, d, `UPDATE users SET sso_provider='local' WHERE id='stranger'`); err != nil {
		t.Fatal(err)
	}
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.docx"}, blob(t, root, "a"), 0)
	directory, err := d.EditorSession(ctx, "worker", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	local, err := d.EditorSession(ctx, "stranger", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	// As before schema 17.
	if err = drive.ExecSQL(ctx, d, `DROP INDEX drive_editor_sids; ALTER TABLE drive_editor_sessions DROP COLUMN sso_sid; ALTER TABLE drive_editor_sessions DROP COLUMN login_at; DELETE FROM drive_schema WHERE version=?`, drive.LoginSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err = drive.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	if !revoked(t, d, directory.ID) || revoked(t, d, local.ID) {
		t.Fatalf("revoked: directory %v, local %v", revoked(t, d, directory.ID), revoked(t, d, local.ID))
	}
	if pending, err := d.PendingRevocations(ctx); err != nil || len(pending) != 1 || pending[0].Key != directory.Key {
		t.Fatalf("drop not queued for the old key: %v %v", pending, err)
	}
	again, err := d.EditorSession(ctx, "worker", f.ID)
	if err != nil || again.Key == directory.Key {
		t.Fatalf("document key not rotated: %v", err)
	}
	// It runs once: a later start keeps the new session.
	if err = drive.Migrate(ctx, d); err != nil || revoked(t, d, again.ID) {
		t.Fatalf("second start revoked a later editor session: %v", err)
	}
}
