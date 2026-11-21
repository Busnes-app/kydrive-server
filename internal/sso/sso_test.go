package sso_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/sso"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/coreos/go-oidc/v3/oidc"
)

func TestOAuthAuthorizationURLUsesDiscoveryAndPKCE(t *testing.T) {
	var issuer string
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
			"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
		})
	}))
	defer idp.Close()
	issuer = idp.URL

	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnIssuer: issuer, KySignOnClientID: "client"}, nil)
	authURL, err := client.BuildAuthURL(oidc.ClientContext(context.Background(), idp.Client()), "https://app.example/callback", "state", "verifier", "nonce", false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != "/authorize" || query.Get("state") != "state" || query.Get("nonce") != "nonce" || query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		t.Fatalf("unexpected authorization URL: %s", authURL)
	}
}

const hmacSecret = "webhook-secret-999"

func webhookClient(t *testing.T) (*sso.KySignOnClient, store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t), testdb.Key)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: hmacSecret}, st), st
}

// scimUser is a suite webhook body as KyIdentity sends it, at revision rev.
func scimUser(id, username string, active bool, rev int64) map[string]any {
	return map[string]any{
		"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, "id": id, "externalId": id,
		"userName": username, "active": active, "roles": []any{}, "meta": map[string]any{"resourceType": "User", "version": fmt.Sprintf(`W/"%d"`, rev)},
	}
}

// deliver signs body with syncauth as KyIdentity does, at time at.
func deliver(t *testing.T, c *sso.KySignOnClient, secret, eventType string, body []byte, at time.Time) error {
	t.Helper()
	h, err := syncauth.Sign([]byte(secret), at, eventType, "evt-"+crypto.RandomHex(4), body)
	if err != nil {
		t.Fatal(err)
	}
	return c.HandleSyncWebhook(context.Background(), h, body)
}

func send(t *testing.T, c *sso.KySignOnClient, eventType string, user map[string]any) error {
	t.Helper()
	body, _ := json.Marshal(user)
	return deliver(t, c, hmacSecret, eventType, body, time.Now())
}

func TestKySignOnWebhookSync(t *testing.T) {
	client, st := webhookClient(t)
	bob := scimUser("ext-usr-456", "bob", true, 1)
	bob["displayName"] = "Bob Engineer"
	bob["emails"] = []map[string]any{{"value": "old@busnes.app"}, {"value": "bob@busnes.app", "primary": true}}
	if err := send(t, client, "user.created", bob); err != nil {
		t.Fatalf("create: %v", err)
	}
	created, err := st.Users().GetUserBySSO(context.Background(), "kysignon", "ext-usr-456")
	if err != nil || created.Username != "bob" || created.DisplayName != "Bob Engineer" || created.Email != "bob@busnes.app" || created.Status != "active" {
		t.Fatalf("unexpected created user: %+v %v", created, err)
	}
	// KyIdentity deactivates with user.updated and active false.
	if err := send(t, client, "user.updated", scimUser("ext-usr-456", "bob", false, 2)); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	updated, _ := st.Users().GetUserBySSO(context.Background(), "kysignon", "ext-usr-456")
	if updated.Status != "inactive" || updated.Username != "bob" {
		t.Errorf("deactivation: %+v", updated)
	}
	if err := send(t, client, "user.deleted", scimUser("ext-usr-456", "bob", false, 3)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.Users().GetUserByID(context.Background(), created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete kept the account: %v", err)
	}
}

func TestKySignOnWebhookNeverSetsTheApplicationRole(t *testing.T) {
	client, st := webhookClient(t)
	ctx := context.Background()
	mallory := scimUser("sub-1", "mallory", true, 1)
	mallory["roles"] = []map[string]any{{"value": "admin", "primary": true}}
	if err := send(t, client, "user.created", mallory); err != nil {
		t.Fatal(err)
	}
	u, err := st.Users().GetUserBySSO(ctx, "kysignon", "sub-1")
	if err != nil || u.Role != "user" {
		t.Fatalf("directory payload chose the role: %+v %v", u, err)
	}
	mallory["meta"] = map[string]any{"version": `W/"2"`}
	if err := send(t, client, "user.updated", mallory); err != nil {
		t.Fatal(err)
	}
	if u, _ = st.Users().GetUserByID(ctx, u.ID); u.Role != "user" {
		t.Fatal("update escalated the role")
	}
	if err := st.Users().SetDirectoryRole(ctx, "sub-1", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := send(t, client, "user.updated", scimUser("sub-1", "mallory2", true, 3)); err != nil {
		t.Fatal(err)
	}
	if u, _ = st.Users().GetUserByID(ctx, u.ID); u.Role != "admin" || u.Username != "mallory2" {
		t.Fatalf("role-less update demoted the operator grant: %+v", u)
	}
}

func TestKySignOnWebhookRejectsForgedStaleAndMalformedRequests(t *testing.T) {
	client, st := webhookClient(t)
	body, _ := json.Marshal(scimUser("sub-x", "eve", true, 1))
	for name, err := range map[string]error{
		"bad signature":     deliver(t, client, "wrong-secret-16-bytes", "user.created", body, time.Now()),
		"expired timestamp": deliver(t, client, hmacSecret, "user.created", body, time.Now().Add(-10*time.Minute)),
		"future timestamp":  deliver(t, client, hmacSecret, "user.created", body, time.Now().Add(10*time.Minute)),
		// The pre-syncauth format: a bare hex HMAC of the body.
		"legacy signature": client.HandleSyncWebhook(context.Background(), syncauth.Headers{Signature: crypto.ComputeHMACSHA256(body, hmacSecret)}, body),
	} {
		if !errors.Is(err, sso.ErrWebhookUnauthorized) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A signed body changed in flight.
	h, _ := syncauth.Sign([]byte(hmacSecret), time.Now(), "user.created", "evt-1", body)
	tampered, _ := json.Marshal(scimUser("sub-x", "eve-admin", true, 1))
	if err := client.HandleSyncWebhook(context.Background(), h, tampered); !errors.Is(err, sso.ErrWebhookUnauthorized) {
		t.Errorf("tampered body: %v", err)
	}
	noMeta := scimUser("sub-x", "eve", true, 1)
	delete(noMeta, "meta")
	for name, user := range map[string]map[string]any{
		"invalid username": scimUser("sub-x", "bad name", true, 1),
		"no id":            scimUser("", "eve", true, 1),
		"no revision":      noMeta,
		"revision zero":    scimUser("sub-x", "eve", true, 0),
		"negative":         scimUser("sub-x", "eve", true, -1),
	} {
		if err := send(t, client, "user.created", user); !errors.Is(err, sso.ErrWebhookMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := deliver(t, client, hmacSecret, "user.created", []byte("not json"), time.Now()); !errors.Is(err, sso.ErrWebhookMalformed) {
		t.Errorf("not json: %v", err)
	}
	if _, err := st.Users().GetUserBySSO(context.Background(), "kysignon", "sub-x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("rejected webhook created a user", err)
	}
}

func TestKySignOnWebhookAppliesOnlyNewerRevisions(t *testing.T) {
	client, st := webhookClient(t)
	ctx := context.Background()
	if err := send(t, client, "user.created", scimUser("sub-2", "carol", true, 3)); err != nil {
		t.Fatal(err)
	}
	if err := send(t, client, "user.updated", scimUser("sub-2", "carol", false, 5)); err != nil {
		t.Fatal(err)
	}
	// An older "active" update delivered late, or retried with a fresh timestamp, must not
	// reactivate the account.
	if err := send(t, client, "user.updated", scimUser("sub-2", "carol", true, 4)); err != nil {
		t.Fatalf("stale event should be acknowledged: %v", err)
	}
	u, _ := st.Users().GetUserBySSO(ctx, "kysignon", "sub-2")
	if u.Status != "inactive" {
		t.Fatal("out-of-order event reactivated the account")
	}
	// The deactivation's own revision, activating, is acknowledged but not applied.
	if err := send(t, client, "user.updated", scimUser("sub-2", "carol-replay", true, 5)); err != nil {
		t.Fatal(err)
	}
	if u, _ = st.Users().GetUserByID(ctx, u.ID); u.Status != "inactive" || u.Username != "carol" {
		t.Fatalf("replayed revision applied: %+v", u)
	}
	if err := send(t, client, "user.deleted", scimUser("sub-2", "carol", false, 4)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Users().GetUserByID(ctx, u.ID); err != nil {
		t.Fatal("stale delete removed the account", err)
	}
	if err := send(t, client, "user.updated", scimUser("sub-2", "carol", true, 6)); err != nil {
		t.Fatal(err)
	}
	if u, _ = st.Users().GetUserByID(ctx, u.ID); u.Status != "active" {
		t.Fatal("newer event not applied")
	}
	// Events this version does not consume are acknowledged.
	if err := send(t, client, "group.updated", scimUser("sub-2", "carol", true, 7)); err != nil {
		t.Fatal(err)
	}
}

func TestKySignOnWebhookUpdatesTheSCIMAccount(t *testing.T) {
	client, st := webhookClient(t)
	ctx := context.Background()
	scimRow, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "scim", Subject: "sub-3", Username: "dave", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	dave := scimUser("sub-3", "dave", true, 1)
	dave["name"] = map[string]any{"formatted": "Dave"}
	if err := send(t, client, "user.created", dave); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Users().GetUserBySSO(ctx, "kysignon", "sub-3"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("webhook duplicated the SCIM account")
	}
	if u, _ := st.Users().GetUserByID(ctx, scimRow.ID); u.DisplayName != "Dave" {
		t.Fatalf("SCIM account not updated: %+v", u)
	}
}

func TestKySignOnWebhookMFAResetSignsOut(t *testing.T) {
	client, st := webhookClient(t)
	ctx := context.Background()
	if err := send(t, client, "user.created", scimUser("sub-4", "erin", true, 2)); err != nil {
		t.Fatal(err)
	}
	u, _ := st.Users().GetUserBySSO(ctx, "kysignon", "sub-4")
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "erin", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "", false); err != nil {
		t.Fatal(err)
	}
	// Any revision, older ones included: revoking is never stale.
	if err := send(t, client, "user.mfa_reset", scimUser("sub-4", "erin", true, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sessions().GetSession(ctx, "erin"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("MFA reset kept the session", err)
	}
	if u, _ = st.Users().GetUserByID(ctx, u.ID); u.Status != "active" {
		t.Fatalf("MFA reset disabled the account: %+v", u)
	}
	// A subject with no account yet is fenced: a first login still holding a pre-reset ID
	// token cannot finish, while a login after the reset can.
	if err := send(t, client, "user.mfa_reset", scimUser("sub-new", "frank", true, 1)); err != nil {
		t.Fatal(err)
	}
	f, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "kysignon", Subject: "sub-new", Username: "frank", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "frank-old", UserID: f.ID, CreatedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)}, "", false); !errors.Is(err, store.ErrSSOSessionEnded) {
		t.Fatalf("pre-reset first login: %v", err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "frank-new", UserID: f.ID, CreatedAt: time.Now().Add(2 * time.Second), ExpiresAt: time.Now().Add(time.Hour)}, "", false); err != nil {
		t.Fatalf("post-reset login: %v", err)
	}
}
