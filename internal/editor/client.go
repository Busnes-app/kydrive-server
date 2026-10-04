package editor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type Client struct {
	InternalOrigin string
	Origin         string
	DriveOrigin    string
	secret         []byte
	HTTP           *http.Client
}

func FromEnv(appURL, environment string) (*Client, error) {
	origin := strings.TrimRight(os.Getenv("KYDRIVE_EDITOR_URL"), "/")
	if origin == "" {
		return nil, nil
	}
	drive := strings.TrimRight(os.Getenv("KYDRIVE_EDITOR_DRIVE_URL"), "/")
	if drive == "" {
		drive = appURL
	}
	internal := strings.TrimRight(os.Getenv("KYDRIVE_EDITOR_INTERNAL_URL"), "/")
	if internal == "" {
		internal = origin
	}
	for _, raw := range []string{origin, drive, internal} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("editor endpoints must be plain origins")
		}
		if u.Scheme != "https" && !(environment == "development" && u.Scheme == "http") {
			return nil, errors.New("editor endpoints require HTTPS outside development")
		}
	}
	secret := []byte(os.Getenv("KYDRIVE_EDITOR_SECRET"))
	if len(secret) < 32 {
		return nil, errors.New("KYDRIVE_EDITOR_SECRET must contain at least 32 bytes")
	}
	return &Client{InternalOrigin: internal, Origin: origin, DriveOrigin: drive, secret: secret, HTTP: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Sign(payload any) (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: c.secret}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", err
	}
	return jwt.Signed(signer).Claims(payload).Serialize()
}
func (c *Client) Verify(token string) (map[string]json.RawMessage, error) {
	if len(token) > 65536 {
		return nil, errors.New("token too large")
	}
	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return nil, err
	}
	var claims map[string]json.RawMessage
	if err = parsed.Claims(c.secret, &claims); err != nil {
		return nil, err
	}
	if raw, ok := claims["exp"]; ok {
		var exp int64
		if json.Unmarshal(raw, &exp) != nil || exp <= time.Now().Unix() {
			return nil, errors.New("token expired")
		}
	}
	return claims, nil
}

type Callback struct {
	Key    string `json:"key"`
	Status int    `json:"status"`
	URL    string `json:"url"`
}

func (c *Client) Callback(token string) (Callback, error) {
	var cb Callback
	claims, err := c.Verify(token)
	if err != nil {
		return cb, err
	}
	raw, ok := claims["payload"]
	if !ok {
		raw, err = json.Marshal(claims)
	}
	if err != nil {
		return cb, err
	}
	err = json.Unmarshal(raw, &cb)
	if cb.Key == "" {
		err = errors.New("missing callback key")
	}
	return cb, err
}
func (c *Client) Fetch(ctx context.Context, raw string) (io.ReadCloser, error) {
	u, err := url.Parse(raw)
	base, _ := url.Parse(c.Origin)
	if err != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, "/cache/") {
		return nil, errors.New("editor output URL rejected")
	}
	inside, _ := url.Parse(c.InternalOrigin)
	u.Scheme = inside.Scheme
	u.Host = inside.Host
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("editor output status %d", resp.StatusCode)
	}
	return resp.Body, nil
}
func (c *Client) Drop(ctx context.Context, key, user string) error {
	payload := map[string]any{"c": "drop", "key": key, "users": []string{user}}
	token, err := c.Sign(payload)
	if err != nil {
		return err
	}
	payload["token"] = token
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.InternalOrigin+"/coauthoring/CommandService.ashx", strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("editor command status %d", resp.StatusCode)
	}
	var result struct {
		Error *int `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result); err != nil {
		return err
	}
	if result.Error == nil {
		return errors.New("editor command response omitted status")
	}
	if *result.Error != 0 && *result.Error != 1 {
		return fmt.Errorf("editor command error %d", *result.Error)
	}
	return nil
}
