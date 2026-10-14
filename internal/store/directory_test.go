package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestApplyDirectoryUserOwnsNoRoleAndRevokesOnDisable(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "scim", Subject: "s-1", Username: "erin@example.com", Active: true})
	if err != nil || u.Role != "user" || u.Status != "active" || u.SSOProvider != "scim" {
		t.Fatalf("create: %+v %v", u, err)
	}
	if err := st.Users().SetDirectoryRole(ctx, "s-1", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "live", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "", false); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateMFAChallenge(ctx, &store.MFAChallenge{TokenHash: "mfa", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
	if u, err = st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: u.ID, Username: "erin@example.com", Active: false}); err != nil || u.Role != "admin" || u.Status != "inactive" {
		t.Fatalf("disable: %+v %v", u, err)
	}
	if _, err := st.Sessions().GetSession(ctx, "live"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("disable kept the session", err)
	}
	if _, _, err := st.Sessions().ConsumeMFAChallenge(ctx, "mfa"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("disable kept the MFA challenge", err)
	}
}

func TestApplyDirectoryUserRefusesBadInputAndLocalAccounts(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "local", Username: "Admin", Role: "admin", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "has space", "tab\t", "ünïcode", string(make([]byte, 129))} {
		if _, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "scim", Username: name, Active: true}); !errors.Is(err, store.ErrInvalidUsername) {
			t.Fatalf("username %q: %v", name, err)
		}
	}
	if _, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "scim", Username: "admin", Active: true}); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("case-variant of the local admin: %v", err)
	}
	if _, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "local", Username: "x-local", Active: true}); err == nil {
		t.Fatal("created a local account")
	}
	if _, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: "local", Username: "Admin", Active: false}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("updated the local account: %v", err)
	}
	if err := st.Users().DeleteDirectoryUser(ctx, "local", 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted the local account: %v", err)
	}
	if u, err := st.Users().GetUserByID(ctx, "local"); err != nil || u.Status != "active" {
		t.Fatal("local account changed", u, err)
	}
}

func TestCleanExpiredRows(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "u", Username: "u", Role: "user", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	past, future := time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Hour)
	for hash, exp := range map[string]time.Time{"old": past, "new": future} {
		if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: hash, UserID: "u", CreatedAt: past, ExpiresAt: exp}, "", false); err != nil {
			t.Fatal(err)
		}
		if err := st.Sessions().CreateMFAChallenge(ctx, &store.MFAChallenge{TokenHash: hash, UserID: "u", ExpiresAt: exp}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Sessions().CleanExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CleanExpiredMFAChallenges(ctx); err != nil {
		t.Fatal(err)
	}
	// An expired row still present reads as ErrSessionExpired; a deleted one as ErrNotFound.
	if _, err := st.Sessions().GetSession(ctx, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("expired session kept", err)
	}
	if _, _, err := st.Sessions().ConsumeMFAChallenge(ctx, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("expired MFA challenge kept", err)
	}
	if _, err := st.Sessions().GetSession(ctx, "new"); err != nil {
		t.Fatal("live session removed", err)
	}
	if _, _, err := st.Sessions().ConsumeMFAChallenge(ctx, "new"); err != nil {
		t.Fatal("live MFA challenge removed", err)
	}
}

func TestRecoveryCodeRedemptionIsSingleUseUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "rc", Username: "rc", Role: "user", Status: "active", RecoveryCodesHash: `["a","b"]`}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = st.Users().UpdateRecoveryCodes(ctx, "rc", `["a","b"]`, `["","b"]`)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, store.ErrAlreadyExists) {
			t.Fatal(err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d concurrent redemptions of one code succeeded", ok)
	}
	if err := st.Users().UpdateRecoveryCodes(ctx, "rc", `["a","b"]`, `["","b"]`); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatal("spent code redeemed again", err)
	}
}

func TestDirectoryTimestampTieFavoursTheRestrictiveEvent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	const at = 1_800_000_000
	u, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "kysignon", Subject: "s", Username: "tie", Active: true, EventTime: at - 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: u.ID, Username: "tie", Active: true, EventTime: at}); err != nil {
		t.Fatal(err)
	}
	if err = st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tie", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "", false); err != nil {
		t.Fatal(err)
	}
	if u, err = st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: u.ID, Username: "tie", Active: false, EventTime: at}); err != nil || u.Status != "inactive" {
		t.Fatalf("same-second deactivation: %+v %v", u, err)
	}
	if _, err = st.Sessions().GetSession(ctx, "tie"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("same-second deactivation kept the session", err)
	}
	if _, err = st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: u.ID, Username: "tie", Active: true, EventTime: at}); !errors.Is(err, store.ErrStale) {
		t.Fatalf("same-second reactivation applied: %v", err)
	}
	if err = st.Users().DeleteDirectoryUser(ctx, u.ID, at); err != nil {
		t.Fatalf("same-second delete: %v", err)
	}
}

func TestDirectoryDeactivatesLegacyUsername(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "legacy", Username: "jürgen", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "j"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "legacy", UserID: "legacy", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "", false); err != nil {
		t.Fatal(err)
	}
	if u, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: "legacy", Username: "jürgen", Active: false}); err != nil || u.Status != "inactive" || u.Username != "jürgen" {
		t.Fatalf("legacy deactivation: %+v %v", u, err)
	}
	if _, err := st.Sessions().GetSession(ctx, "legacy"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("legacy deactivation kept the session", err)
	}
	if _, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{ID: "legacy", Username: "jürgen", Active: true}); !errors.Is(err, store.ErrInvalidUsername) {
		t.Fatalf("legacy reactivation skipped validation: %v", err)
	}
}

// A password login that read the account before MFA was enabled must not get a session after it.
func TestSessionRefusedWhenMFAChangedSinceLogin(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "m", Username: "m", PasswordHash: "h", Role: "user", Status: "active", TOTPSecretEnc: "sealed", RecoveryCodesHash: "[]"}); err != nil {
		t.Fatal(err)
	}
	seen, err := st.Users().GetUserByID(ctx, "m") // the login's view: MFA off
	if err != nil || seen.TOTPEnabled {
		t.Fatal(seen, err)
	}
	if err = st.Users().EnableTOTP(ctx, "m", "sealed", "[]", "", ""); err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{TokenHash: "late", UserID: "m", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err = st.Sessions().CreateSession(ctx, sess, seen.PasswordHash, seen.TOTPEnabled); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("password-only session issued after MFA was enabled: %v", err)
	}
	if err = st.Sessions().CreateSession(ctx, sess, "h", true); err != nil {
		t.Fatalf("MFA-verified session refused: %v", err)
	}
}
