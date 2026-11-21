package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// Disabling a directory account ends its credentials, so enabling it again revives none.
func TestDirectoryDisableEndsCredentials(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	u, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "scim", Subject: "s-cred", Username: "bob@example.com", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	_, password, err := st.Drive().CreateAppPassword(ctx, u.ID, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := st.Drive().CreateAgentToken(ctx, u.ID, "Agent", drive.ScopeMCPWrite, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, active := range []bool{false, true} {
		if _, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: u.ID, Username: "bob@example.com", Active: active}); err != nil {
			t.Fatal(err)
		}
	}
	if id, err := st.Drive().AppPasswordUser(ctx, "bob@example.com", password); err == nil {
		t.Fatalf("WebDAV password revived for %s", id)
	}
	if id, _, err := st.Drive().AgentTokenUser(ctx, token, time.Now()); err == nil {
		t.Fatalf("agent token revived for %s", id)
	}
}

// An MFA reset ends sessions and credentials but leaves the account active; a local account
// is refused.
func TestRevokeDirectoryCredentials(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	u, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "kysignon", Subject: "s-mfa", Username: "carol@example.com", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "mfa", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "", false); err != nil {
		t.Fatal(err)
	}
	_, password, err := st.Drive().CreateAppPassword(ctx, u.ID, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := st.Drive().CreateAgentToken(ctx, u.ID, "Agent", drive.ScopeMCPWrite, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.Drive().EnsurePersonalWorkspace(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.Drive().Publish(ctx, u.ID, drive.File{Workspace: ws, Name: "notes.docx"}, drive.Version{Blob: "blob", Size: 4, Digest: strings.Repeat("a", 64), Sealed: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	editor, err := st.Drive().EditorSession(ctx, u.ID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	id, revision, epoch, err := drive.ParseDocumentKey(editor.Key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drive().CallbackActor(ctx, id, revision, epoch); err != nil {
		t.Fatalf("editor not open: %v", err)
	}
	if err := st.Users().RevokeDirectoryCredentials(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drive().CallbackActor(ctx, id, revision, epoch); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("open editor kept saving: %v", err)
	}
	if _, err := st.Sessions().GetSession(ctx, "mfa"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session kept", err)
	}
	if id, err := st.Drive().AppPasswordUser(ctx, "carol@example.com", password); err == nil {
		t.Fatalf("WebDAV password kept for %s", id)
	}
	if id, _, err := st.Drive().AgentTokenUser(ctx, token, time.Now()); err == nil {
		t.Fatalf("agent token kept for %s", id)
	}
	if got, _ := st.Users().GetUserByID(ctx, u.ID); got.Status != "active" {
		t.Fatalf("account disabled: %+v", got)
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "local-mfa", Username: "admin2", Role: "admin", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().RevokeDirectoryCredentials(ctx, "local-mfa"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("local account: %v", err)
	}
}

// An SSO callback already holding a pre-reset ID token cannot sign in after the MFA reset; a
// login issued after it can.
func TestRevokeDirectoryCredentialsFencesPendingLogins(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	u, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "kysignon", Subject: "s-fence", Username: "dave@example.com", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	verified := time.Now().Add(-time.Minute) // the paused callback's ID token iat
	if err := st.Users().RevokeDirectoryCredentials(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	session := func(hash string, iat time.Time) error {
		return st.Sessions().CreateSession(ctx, &store.Session{TokenHash: hash, UserID: u.ID, CreatedAt: iat, ExpiresAt: time.Now().Add(time.Hour), SSOSID: hash}, "", false)
	}
	if err := session("pending", verified); !errors.Is(err, store.ErrSSOSessionEnded) {
		t.Fatalf("pre-reset callback: %v", err)
	}
	if _, err := st.Sessions().GetSession(ctx, "pending"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("pre-reset session kept", err)
	}
	if err := session("fresh", time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("post-reset login: %v", err)
	}
}
