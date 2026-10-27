package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/store"
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

// BuildAuthURL generates the authorization code URL with PKCE for KySignOn.
func (k *KySignOnClient) BuildAuthURL(ctx context.Context, redirectURI, state, verifier, nonce string) (string, error) {
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

// KySignOnSyncPayload defines the schema received during automatic directory replication
// webhooks. A role field, if sent, is ignored: application administration is an operator grant.
type KySignOnSyncPayload struct {
	Event       string `json:"event"` // "user.created", "user.updated", "user.deactivated", "user.deleted"
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	Timestamp   int64  `json:"timestamp"`
}

// ErrWebhookUnauthorized is a missing secret, bad signature or out-of-window timestamp.
var ErrWebhookUnauthorized = errors.New("webhook signature or timestamp rejected")

// ErrWebhookMalformed is a signed body that is not a usable event.
var ErrWebhookMalformed = errors.New("malformed webhook payload")

// HandleSyncWebhook processes inbound HMAC-SHA256 signed user updates from KySignOn server.
// Events apply only when newer than the account's last applied event; a replay or an
// out-of-order older event is acknowledged without effect.
func (k *KySignOnClient) HandleSyncWebhook(ctx context.Context, body []byte, signature string) error {
	if len(k.config.KySignOnHMACSecret) < crypto.MinHMACKeyBytes || !crypto.VerifyHMACSHA256(body, k.config.KySignOnHMACSecret, signature) {
		return ErrWebhookUnauthorized
	}

	var payload KySignOnSyncPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("%w: %v", ErrWebhookMalformed, err)
	}
	if payload.Timestamp <= 0 || time.Since(time.Unix(payload.Timestamp, 0)).Abs() > 5*time.Minute {
		return ErrWebhookUnauthorized
	}
	if payload.ID == "" {
		return ErrWebhookMalformed
	}

	existing, err := store.DirectorySubject(ctx, k.store.Users(), payload.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	switch payload.Event {
	case "user.created", "user.updated", "user.deactivated":
		d := &store.DirectoryUser{Provider: "kysignon", Subject: payload.ID, Username: payload.Username, Email: payload.Email, DisplayName: payload.DisplayName,
			Active: payload.Event != "user.deactivated" && (payload.Status == "" || payload.Status == "active"), EventTime: payload.Timestamp}
		if existing != nil {
			d.ID = existing.ID
			if payload.Event == "user.deactivated" {
				d.Username, d.Email, d.DisplayName = existing.Username, existing.Email, existing.DisplayName
			}
		} else if payload.Event == "user.deactivated" {
			return nil // nothing local to disable
		}
		_, err = k.store.Users().ApplyDirectoryUser(ctx, d)
	case "user.deleted":
		if existing == nil {
			return nil
		}
		err = k.store.Users().DeleteDirectoryUser(ctx, existing.ID, payload.Timestamp)
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
