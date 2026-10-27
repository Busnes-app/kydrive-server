package sso

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"

	josejson "github.com/go-jose/go-jose/v4/json"
)

// strongJWKSTransport serves only the OIDC verifier's JWKS fetches. Discovery,
// caching, rotation, signatures and claims stay in coreos/go-oidc.
type strongJWKSTransport struct{ base http.RoundTripper }

func (t strongJWKSTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, errors.New("OIDC JWKS redirects are refused")
	}
	if resp.StatusCode != http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	const maxJWKSBytes = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxJWKSBytes {
		return nil, errors.New("OIDC JWKS exceeds 1 MiB")
	}
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, errors.New("invalid OIDC JWKS")
	}
	for _, rawKey := range set.Keys {
		// go-oidc's envelope is case-insensitive encoding/json; go-jose reads JWK
		// members case-sensitively. Match both so this checks the verifier's modulus.
		var key struct {
			Type string `json:"kty"`
			N    string `json:"n"`
		}
		if err := josejson.Unmarshal(rawKey, &key); err != nil {
			return nil, errors.New("invalid OIDC JWK")
		}
		if key.Type != "RSA" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil || new(big.Int).SetBytes(n).BitLen() < 2048 {
			return nil, errors.New("OIDC JWKS RSA keys must be at least 2048 bits")
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}
