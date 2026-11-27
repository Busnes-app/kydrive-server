package sso

import (
	"context"
	"crypto/fips140"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type oauthFlow struct {
	issuer       string
	clientID     string
	clientSecret string
	mu           sync.Mutex
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
}

func newOAuthFlow(issuer, clientID, clientSecret string) *oauthFlow {
	return &oauthFlow{issuer: strings.TrimRight(issuer, "/"), clientID: clientID, clientSecret: clientSecret}
}

func (f *oauthFlow) getProvider(ctx context.Context) (*oidc.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.provider != nil {
		return f.provider, nil
	}
	if err := requireStrongOIDCHTTPS(f.issuer); err != nil {
		return nil, err
	}
	ctx = strongOIDCContext(ctx)
	provider, err := oidc.NewProvider(ctx, f.issuer)
	if err != nil {
		return nil, err
	}
	var metadata struct {
		Issuer                           string   `json:"issuer"`
		JWKSURI                          string   `json:"jwks_uri"`
		IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, err
	}
	if err := requireStrongOIDCHTTPS(provider.Endpoint().AuthURL, provider.Endpoint().TokenURL, metadata.JWKSURI); err != nil {
		return nil, err
	}
	algs, err := supportedSigningAlgs(metadata.IDTokenSigningAlgValuesSupported)
	if err != nil {
		return nil, err
	}
	jwksClient := *contextClient(ctx)
	if jwksClient.Transport == nil {
		jwksClient.Transport = http.DefaultTransport
	}
	jwksClient.Transport = strongJWKSTransport{base: jwksClient.Transport}
	if jwksClient.Timeout == 0 {
		jwksClient.Timeout = 20 * time.Second
	}
	issuer := metadata.Issuer
	if issuer == "" {
		issuer = f.issuer
	}
	// The verifier keeps key caching and rotation behind the JWKS key floor
	// while retaining RFC 9964 ML-DSA-65 AKP keys that go-oidc v3.21.0 skips.
	keySet := newRemoteJWKSKeySet(metadata.JWKSURI, &jwksClient, algs)
	f.verifier = oidc.NewVerifier(issuer, keySet, &oidc.Config{
		ClientID:             f.clientID,
		SupportedSigningAlgs: algs,
	})
	f.provider = provider
	return provider, nil
}

// allowedOIDCSigningAlgs mirrors coreos/go-oidc's asymmetric allowlist plus RFC 9964 ML-DSA-65.
var allowedOIDCSigningAlgs = map[string]bool{
	"ML-DSA-65": true,
	oidc.RS256:  true,
	oidc.RS384:  true,
	oidc.RS512:  true,
	oidc.ES256:  true,
	oidc.ES384:  true,
	oidc.ES512:  true,
	oidc.PS256:  true,
	oidc.PS384:  true,
	oidc.PS512:  true,
	oidc.EdDSA:  true,
}

func supportedSigningAlgs(advertised []string) ([]string, error) {
	if len(advertised) == 0 {
		return []string{oidc.RS256}, nil
	}
	seen := make(map[string]struct{}, len(advertised))
	out := make([]string, 0, len(advertised))
	for _, alg := range advertised {
		if !allowedOIDCSigningAlgs[alg] {
			continue
		}
		if _, dup := seen[alg]; dup {
			continue
		}
		seen[alg] = struct{}{}
		out = append(out, alg)
	}
	if len(out) == 0 {
		return nil, errors.New("OIDC provider advertises no supported asymmetric signing algorithms")
	}
	return out, nil
}

func (f *oauthFlow) config(ctx context.Context, redirectURI string) (*oauth2.Config, *oidc.Provider, error) {
	if f.issuer == "" || f.clientID == "" {
		return nil, nil, ErrProviderDisabled
	}
	provider, err := f.getProvider(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &oauth2.Config{ClientID: f.clientID, ClientSecret: f.clientSecret, Endpoint: provider.Endpoint(), RedirectURL: redirectURI, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}, provider, nil
}

func (f *oauthFlow) authCodeURL(ctx context.Context, redirectURI, state, verifier, nonce string, extra ...oauth2.AuthCodeOption) (string, error) {
	config, _, err := f.config(ctx, redirectURI)
	if err != nil {
		return "", err
	}
	return config.AuthCodeURL(state, append([]oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce)}, extra...)...), nil
}

func (f *oauthFlow) exchange(ctx context.Context, code, verifier, redirectURI, expectedNonce string) (*IdentityClaims, error) {
	config, _, err := f.config(ctx, redirectURI)
	if err != nil {
		return nil, err
	}
	ctx = strongOIDCContext(ctx)
	token, err := config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, err
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, errors.New("missing id_token in token response")
	}
	idToken, err := f.verifier.Verify(ctx, rawIDToken)
	if err != nil || expectedNonce == "" || idToken.Nonce != expectedNonce {
		return nil, ErrInvalidIDToken
	}
	return claimsFromIDToken(idToken)
}

func contextClient(ctx context.Context) *http.Client {
	if client, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok {
		return client
	}
	return http.DefaultClient
}

// requireStrongOIDCHTTPS keeps the strong-key profile from falling back to
// plaintext: every OIDC endpoint must be HTTPS without URL credentials.
func requireStrongOIDCHTTPS(endpoints ...string) error {
	if !fips140.Enabled() {
		return nil
	}
	for _, raw := range endpoints {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errors.New("strong-crypto mode requires HTTPS OIDC issuer and endpoints without URL credentials")
		}
	}
	return nil
}

// strongOIDCContext keeps the caller's client (roots, timeouts) but refuses
// discovery and token redirects in the strong-key profile.
func strongOIDCContext(ctx context.Context) context.Context {
	if !fips140.Enabled() {
		return ctx
	}
	client := *contextClient(ctx)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("OIDC redirects are refused in strong-crypto mode")
	}
	if client.Timeout == 0 {
		client.Timeout = 20 * time.Second
	}
	return oidc.ClientContext(ctx, &client)
}

func claimsFromIDToken(idToken *oidc.IDToken) (*IdentityClaims, error) {
	var raw struct {
		Sub               string `json:"sub"`
		SID               string `json:"sid"`
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Role              string `json:"role"`
		OrgID             string `json:"org_id"`
		ExportOnly        bool   `json:"export_only"`
		AuthTime          int64  `json:"auth_time"`
	}
	if err := idToken.Claims(&raw); err != nil || raw.Sub == "" {
		return nil, ErrInvalidIDToken
	}
	var authTime time.Time
	if raw.AuthTime > 0 {
		authTime = time.Unix(raw.AuthTime, 0).UTC()
	}
	username := raw.PreferredUsername
	if username == "" {
		username = raw.Email
	}
	if username == "" {
		username = raw.Sub
	}
	return &IdentityClaims{Subject: raw.Sub, Email: raw.Email, Name: raw.Name, PreferredUsername: username, Role: raw.Role, OrgID: raw.OrgID, ExportOnly: raw.ExportOnly, SessionID: raw.SID, IssuedAt: idToken.IssuedAt, AuthTime: authTime}, nil
}
