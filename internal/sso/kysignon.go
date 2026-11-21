package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"golang.org/x/oauth2"
)

// KySignOnClient manages interactions with the central KySignOn identity provider.
type KySignOnClient struct {
	config config.SSOConfig
	store  store.Store
	flow   *oauthFlow
}

func NewKySignOnClient(cfg config.SSOConfig, st store.Store) *KySignOnClient {
	return &KySignOnClient{
		config: cfg,
		store:  st,
		flow:   newOAuthFlow(cfg.KySignOnIssuer, cfg.KySignOnClientID, cfg.KySignOnSecret),
	}
}

// BuildAuthURL generates the authorization code URL with PKCE for KySignOn. A step-up sends
// max_age=0 so KyIdentity makes the user sign in again rather than reuse its session.
func (k *KySignOnClient) BuildAuthURL(ctx context.Context, redirectURI, state, verifier, nonce string, stepUp bool) (string, error) {
	if stepUp {
		return k.flow.authCodeURL(ctx, redirectURI, state, verifier, nonce, oauth2.SetAuthURLParam("max_age", "0"))
	}
	return k.flow.authCodeURL(ctx, redirectURI, state, verifier, nonce)
}

// ExchangeCode exchanges the authorization code and verifier for identity claims.
func (k *KySignOnClient) ExchangeCode(ctx context.Context, code, verifier, redirectURI, expectedNonce string) (*IdentityClaims, error) {
	claims, err := k.flow.exchange(ctx, code, verifier, redirectURI, expectedNonce)
	if err != nil {
		return nil, err
	}
	claims.Provider = "kysignon"
	return claims, nil
}

// VerifyLogoutToken verifies a back-channel logout token signed by KySignOn for this client.
func (k *KySignOnClient) VerifyLogoutToken(ctx context.Context, raw string) (*LogoutClaims, error) {
	return k.flow.verifyLogout(ctx, raw)
}

// directoryUser is the SCIM user resource KyIdentity's suite webhook sends. Its roles, if
// any, are ignored: application administration is an operator grant.
type directoryUser struct {
	ID          string `json:"id"`
	UserName    string `json:"userName"`
	DisplayName string `json:"displayName"`
	Name        *struct {
		Formatted string `json:"formatted"`
	} `json:"name"`
	Emails []struct {
		Value   string `json:"value"`
		Primary bool   `json:"primary"`
	} `json:"emails"`
	Active bool `json:"active"`
	Meta   *struct {
		Version string `json:"version"`
	} `json:"meta"`
}

// revisionPattern is the weak ETag carrying KyIdentity's per-account revision. Zero would turn
// ordering off in the store, so only positive revisions are accepted.
var revisionPattern = regexp.MustCompile(`^W/"([1-9][0-9]{0,17})"$`)

// ErrWebhookUnauthorized is a missing secret, bad signature or out-of-window timestamp.
var ErrWebhookUnauthorized = errors.New("webhook signature or timestamp rejected")

// ErrWebhookMalformed is a signed body that is not a usable event.
var ErrWebhookMalformed = errors.New("malformed webhook payload")

// HandleSyncWebhook applies one syncauth-signed user event from KyIdentity's suite webhook.
// Events apply only when their revision is newer than the account's last applied one; a
// redelivery or an out-of-order older event is acknowledged without effect.
func (k *KySignOnClient) HandleSyncWebhook(ctx context.Context, headers syncauth.Headers, body []byte) error {
	if len(k.config.KySignOnHMACSecret) < crypto.MinHMACKeyBytes {
		return ErrWebhookUnauthorized
	}
	event, err := syncauth.Verify([]byte(k.config.KySignOnHMACSecret), headers, body, syncauth.Options{})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWebhookUnauthorized, err)
	}

	var user directoryUser
	if err := json.Unmarshal(body, &user); err != nil {
		return fmt.Errorf("%w: %v", ErrWebhookMalformed, err)
	}
	if user.ID == "" || user.Meta == nil {
		return ErrWebhookMalformed
	}
	match := revisionPattern.FindStringSubmatch(user.Meta.Version)
	if match == nil {
		return ErrWebhookMalformed
	}
	revision, _ := strconv.ParseInt(match[1], 10, 64)

	existing, err := store.DirectorySubject(ctx, k.store.Users(), user.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	switch event.Type {
	case "user.created", "user.updated":
		if existing == nil && !user.Active {
			return nil // nothing local to disable
		}
		d := &store.DirectoryUser{Provider: "kysignon", Subject: user.ID, Username: user.UserName, Email: user.email(),
			DisplayName: user.displayName(), Active: user.Active, Revision: revision}
		if existing != nil {
			d.ID = existing.ID
		}
		_, err = k.store.Users().ApplyDirectoryUser(ctx, d)
	case "user.deleted":
		if existing == nil {
			return nil
		}
		err = k.store.Users().DeleteDirectoryUser(ctx, existing.ID, revision)
	case "user.mfa_reset":
		// The person's second factor may have been lost or taken. Whatever was signed in or
		// minted with it ends, whatever the revision: revoking is restrictive and repeatable.
		if existing == nil {
			return nil
		}
		err = k.store.Users().RevokeDirectoryCredentials(ctx, existing.ID)
	default:
		return nil // acknowledge events this version does not consume
	}
	if errors.Is(err, store.ErrStale) {
		return nil
	}
	if errors.Is(err, store.ErrInvalidUsername) {
		return fmt.Errorf("%w: %v", ErrWebhookMalformed, err)
	}
	return err
}

// email is the primary address, else the first.
func (u directoryUser) email() string {
	for _, e := range u.Emails {
		if e.Primary {
			return e.Value
		}
	}
	if len(u.Emails) > 0 {
		return u.Emails[0].Value
	}
	return ""
}

func (u directoryUser) displayName() string {
	if u.DisplayName == "" && u.Name != nil {
		return u.Name.Formatted
	}
	return u.DisplayName
}
