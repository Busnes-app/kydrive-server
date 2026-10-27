package sso_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

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
	authURL, err := client.BuildAuthURL(oidc.ClientContext(context.Background(), idp.Client()), "https://app.example/callback", "state", "verifier", "nonce")
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

// send signs body with the shared secret. ts 0 means now.
func send(t *testing.T, c *sso.KySignOnClient, event map[string]any, ts int64) error {
	t.Helper()
	if ts == 0 {
		ts = time.Now().Unix()
	}
	event["timestamp"] = ts
	body, _ := json.Marshal(event)
	return c.HandleSyncWebhook(context.Background(), body, crypto.ComputeHMACSHA256(body, hmacSecret))
}

func TestKySignOnWebhookSync(t *testing.T) {
	client, st := webhookClient(t)
	now := time.Now().Unix()
	if err := send(t, client, map[string]any{"event": "user.created", "id": "ext-usr-456", "username": "bob", "email": "bob@busnes.app", "display_name": "Bob Engineer"}, now-10); err != nil {
		t.Fatalf("create: %v", err)
	}
	created, err := st.Users().GetUserBySSO(context.Background(), "kysignon", "ext-usr-456")
	if err != nil || created.Username != "bob" || created.DisplayName != "Bob Engineer" || created.Status != "active" {
		t.Fatalf("unexpected created user: %+v %v", created, err)
	}
	if err := send(t, client, map[string]any{"event": "user.deactivated", "id": "ext-usr-456"}, now-5); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	updated, _ := st.Users().GetUserBySSO(context.Background(), "kysignon", "ext-usr-456")
	if updated.Status != "inactive" || updated.Username != "bob" {
		t.Errorf("deactivation: %+v", updated)
	}
}

func TestKySignOnWebhookNeverSetsTheApplicationRole(t *testing.T) {
	client, st := webhookClient(t)
	ctx := context.Background()
	now := time.Now().Unix()
	if err := send(t, client, map[string]any{"event": "user.created", "id": "sub-1", "username": "mallory", "role": "admin"}, now-20); err != nil {
		t.Fatal(err)
	}
	u, err := st.Users().GetUserBySSO(ctx, "kysignon", "sub-1")
	if err != nil || u.Role != "user" {
		t.Fatalf("directory payload chose the role: %+v %v", u, err)
	}
	if err := send(t, client, map[string]any{"event": "user.updated", "id": "sub-1", "username": "mallory", "role": "admin"}, now-15); err != nil {
		t.Fatal(err)
	}
	if u, _ = st.Users().GetUserByID(ctx, u.ID); u.Role != "user" {
		t.Fatal("update escalated the role")
	}
	if err := st.Users().SetDirectoryRole(ctx, "sub-1", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := send(t, client, map[string]any{"event": "user.updated", "id": "sub-1", "username": "mallory2"}, now-10); err != nil {
		t.Fatal(err)
	}
	if u, _ = st.Users().GetUserByID(ctx, u.ID); u.Role != "admin" || u.Username != "mallory2" {
		t.Fatalf("role-less update demoted the operator grant: %+v", u)
	}
}

func TestKySignOnWebhookRejectsForgedAndStaleRequests(t *testing.T) {
	client, st := webhookClient(t)
	body, _ := json.Marshal(map[string]any{"event": "user.created", "id": "sub-x", "username": "eve", "timestamp": time.Now().Unix()})
	if err := client.HandleSyncWebhook(context.Background(), body, crypto.ComputeHMACSHA256(body, "wrong-secret")); !errors.Is(err, sso.ErrWebhookUnauthorized) {
		t.Fatalf("bad signature: %v", err)
	}
	if err := send(t, client, map[string]any{"event": "user.created", "id": "sub-x", "username": "eve"}, time.Now().Add(-10*time.Minute).Unix()); !errors.Is(err, sso.ErrWebhookUnauthorized) {
		t.Fatalf("expired timestamp: %v", err)
	}
	if err := send(t, client, map[string]any{"event": "user.created", "id": "sub-x", "username": "bad name"}, 0); !errors.Is(err, sso.ErrWebhookMalformed) {
		t.Fatalf("invalid username: %v", err)
	}
	if _, err := st.Users().GetUserBySSO(context.Background(), "kysignon", "sub-x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("rejected webhook created a user", err)
	}
}

func TestKySignOnWebhookAppliesOnlyNewerEvents(t *testing.T) {
	client, st := webhookClient(t)
	ctx := context.Background()
	now := time.Now().Unix()
	created := map[string]any{"event": "user.created", "id": "sub-2", "username": "carol"}
	if err := send(t, client, created, now-30); err != nil {
		t.Fatal(err)
	}
	if err := send(t, client, map[string]any{"event": "user.deactivated", "id": "sub-2"}, now-10); err != nil {
		t.Fatal(err)
	}
	// An older "active" update delivered late must not reactivate the account.
	if err := send(t, client, map[string]any{"event": "user.updated", "id": "sub-2", "username": "carol", "status": "active"}, now-20); err != nil {
		t.Fatalf("stale event should be acknowledged: %v", err)
	}
	u, _ := st.Users().GetUserBySSO(ctx, "kysignon", "sub-2")
	if u.Status != "inactive" {
		t.Fatal("out-of-order event reactivated the account")
	}
	// An exact replay of the deactivation's timestamp is acknowledged but not reapplied.
	if err := send(t, client, map[string]any{"event": "user.updated", "id": "sub-2", "username": "carol-replay", "status": "active"}, now-10); err != nil {
		t.Fatal(err)
	}
	if u, _ = st.Users().GetUserByID(ctx, u.ID); u.Status != "inactive" || u.Username != "carol" {
		t.Fatalf("replayed timestamp applied: %+v", u)
	}
	if err := send(t, client, map[string]any{"event": "user.deleted", "id": "sub-2"}, now-15); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Users().GetUserByID(ctx, u.ID); err != nil {
		t.Fatal("stale delete removed the account", err)
	}
	if err := send(t, client, map[string]any{"event": "user.updated", "id": "sub-2", "username": "carol", "status": "active"}, now-5); err != nil {
		t.Fatal(err)
	}
	if u, _ = st.Users().GetUserByID(ctx, u.ID); u.Status != "active" {
		t.Fatal("newer event not applied")
	}
}

func TestKySignOnWebhookUpdatesTheSCIMAccount(t *testing.T) {
	client, st := webhookClient(t)
	ctx := context.Background()
	scimUser, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "scim", Subject: "sub-3", Username: "dave", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := send(t, client, map[string]any{"event": "user.created", "id": "sub-3", "username": "dave", "display_name": "Dave"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Users().GetUserBySSO(ctx, "kysignon", "sub-3"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("webhook duplicated the SCIM account")
	}
	if u, _ := st.Users().GetUserByID(ctx, scimUser.ID); u.DisplayName != "Dave" {
		t.Fatalf("SCIM account not updated: %+v", u)
	}
}
