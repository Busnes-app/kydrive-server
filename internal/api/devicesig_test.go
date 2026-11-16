package api_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// signer is a linked device holding its key, as the Android and desktop clients do.
type signer struct {
	key    *ecdsa.PrivateKey
	token  string
	origin string
	now    func() time.Time
}

var signedNames = []string{"content-range", "depth", "destination", "if", "if-match", "if-none-match", "lock-token", "overwrite", "timeout", "x-oc-mtime"}

func hexSum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// sign sets the signing headers for r, whose body is body.
func (d *signer) sign(r *http.Request, body []byte) {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	date := strconv.FormatInt(d.now().UnixMilli(), 10)
	n := base64.RawURLEncoding.EncodeToString(nonce)
	var hs strings.Builder
	for _, name := range signedNames {
		if v := r.Header.Values(name); len(v) > 0 {
			hs.WriteString(name + ":" + strings.Join(v, ",") + "\n")
		}
	}
	msg := strings.Join([]string{"kydrive-request-v1", d.origin, r.Method, r.RequestURI, date, n, hexSum([]byte(hs.String())), hexSum(body)}, "|")
	sum := sha256.Sum256([]byte(msg))
	sig, err := ecdsa.SignASN1(rand.Reader, d.key, sum[:])
	if err != nil {
		panic(err)
	}
	r.Header.Set("Authorization", "Bearer "+d.token)
	r.Header.Set("KyDrive-Date", date)
	r.Header.Set("KyDrive-Nonce", n)
	r.Header.Set("KyDrive-Content-SHA256", hexSum(body))
	r.Header.Set("KyDrive-Signature", base64.StdEncoding.EncodeToString(sig))
}

func (d *signer) request(method, target string, body []byte, headers ...string) *http.Request {
	r := httptest.NewRequest(method, target, bytes.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	d.sign(r, body)
	return r
}

func serve(srv *api.Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

// linkSigned links a phone by QR code with a fresh P-256 key.
func linkSigned(t *testing.T, srv *api.Server, cookie *http.Cookie, origin string) *signer {
	t.Helper()
	out := send(t, srv, "POST", "/api/devices/phone-code", `{}`, cookie, "")
	var qr struct{ Link string }
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &qr) != nil {
		t.Fatalf("phone code %d %s", out.Code, out.Body.String())
	}
	u, _ := url.Parse(qr.Link)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	form := url.Values{"grant": {"qr"}, "code": {u.Query().Get("code")}, "name": {"Pixel 9"}, "platform": {"android"}, "publicKey": {base64.StdEncoding.EncodeToString(der)}}
	r := httptest.NewRequest("POST", "/api/devices/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	out = serve(srv, r)
	var got struct{ Token string }
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &got) != nil {
		t.Fatalf("redeem %d %s", out.Code, out.Body.String())
	}
	return &signer{key: key, token: got.Token, origin: origin, now: time.Now}
}

func TestSignedDeviceRequests(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "sigadmin", "admin")
	user := loginAs(t, srv, st, "siguser", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "siggroup", DisplayName: "Sig", Members: []string{"usr_siguser"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_sigadmin", "Team", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_sigadmin", w.ID, "siggroup", "editor"); err != nil {
		t.Fatal(err)
	}
	origin := strings.ToLower(strings.TrimRight(cfg.Server.AppURL, "/"))
	d := linkSigned(t, srv, user, origin)
	expect := func(got *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if got.Code != code {
			t.Fatalf("%s: %d, want %d: %s", what, got.Code, code, got.Body.String())
		}
	}

	expect(serve(srv, d.request("GET", "/api/drive/workspaces", nil)), 200, "signed list")
	expect(serve(srv, d.request("GET", "/api/drive/device", nil)), 200, "signed account")
	folder := []byte(`{"name":"Synced","parent":""}`)
	expect(serve(srv, d.request("POST", "/api/drive/workspaces/"+w.ID+"/folders", folder)), 201, "signed write")

	// The token alone, as a thief holding it would send it, authorises nothing.
	expect(send(t, srv, "GET", "/api/drive/workspaces", "", nil, d.token), 401, "token without signature")
	expect(send(t, srv, "GET", "/api/drive/device", "", nil, d.token), 401, "account without signature")
	bare := httptest.NewRequest("PROPFIND", "/dav/", nil)
	bare.Header.Set("Authorization", "Bearer "+d.token)
	expect(serve(srv, bare), 401, "DAV without signature")

	// A captured request cannot be replayed.
	r := d.request("GET", "/api/drive/workspaces", nil)
	replay := r.Clone(ctx)
	expect(serve(srv, r), 200, "first")
	expect(serve(srv, replay), 401, "replay")

	// Each signed part is bound: path, method, a state-changing header, the body, the date.
	r = d.request("GET", "/api/drive/workspaces", nil)
	r.RequestURI, r.URL.Path = "/api/drive/shared-with-me", "/api/drive/shared-with-me"
	expect(serve(srv, r), 401, "other path")
	r = d.request("POST", "/api/drive/workspaces/"+w.ID+"/folders", folder)
	r.Body = io.NopCloser(strings.NewReader(`{"name":"Other","parent":""}`))
	expect(serve(srv, r), 401, "other body")
	r = d.request("PROPFIND", "/dav/", nil, "Depth", "0")
	r.Header.Set("Depth", "1")
	expect(serve(srv, r), 401, "other Depth")
	expect(serve(srv, d.request("PROPFIND", "/dav/", nil, "Depth", "1")), 207, "signed DAV")
	// A signed header sent twice is refused: the handler would act on the first field only.
	expect(serve(srv, d.request("MKCOL", "/dav/Team/Src/", nil)), 201, "signed MKCOL")
	r = httptest.NewRequest("MOVE", "/dav/Team/Src/", nil)
	r.Header.Set("Destination", "/dav/Team/Other/,/dav/Team/Dst/")
	d.sign(r, nil)
	r.Header.Del("Destination")
	r.Header.Add("Destination", "/dav/Team/Other/")
	r.Header.Add("Destination", "/dav/Team/Dst/")
	expect(serve(srv, r), 401, "split Destination")
	if got := serve(srv, d.request("PROPFIND", "/dav/Team/Src/", nil, "Depth", "0")); got.Code != 207 {
		t.Fatalf("refused MOVE changed files: %d %s", got.Code, got.Body.String())
	}
	old := &signer{key: d.key, token: d.token, origin: origin, now: func() time.Time { return time.Now().Add(-6 * time.Minute) }}
	expect(serve(srv, old.request("GET", "/api/drive/workspaces", nil)), 401, "stale date")
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	thief := &signer{key: other, token: d.token, origin: origin, now: time.Now}
	expect(serve(srv, thief.request("GET", "/api/drive/workspaces", nil)), 401, "another key")
	wrongHost := &signer{key: d.key, token: d.token, origin: "https://evil.example", now: time.Now}
	expect(serve(srv, wrongHost.request("GET", "/api/drive/workspaces", nil)), 401, "another origin")

	// An upload longer than the checked prefix streams, and a wrong one is never published.
	big := bytes.Repeat([]byte("k"), 3<<20)
	target := "/api/drive/workspaces/" + w.ID + "/uploads?parent=&name=big.bin&revision=0"
	r = d.request("POST", target, big)
	tampered := bytes.Clone(big)
	tampered[len(tampered)-1] = 'x'
	r.Body = io.NopCloser(bytes.NewReader(tampered))
	if got := serve(srv, r); got.Code < 400 {
		t.Fatalf("tampered upload accepted: %d %s", got.Code, got.Body.String())
	}
	list := serve(srv, d.request("GET", "/api/drive/workspaces/"+w.ID+"/files", nil))
	if strings.Contains(list.Body.String(), "big.bin") {
		t.Fatalf("tampered upload published: %s", list.Body.String())
	}
	expect(serve(srv, d.request("POST", target, big)), 201, "signed upload")
	r = d.request("PUT", "/dav/Team/dav.bin", big)
	r.Body = io.NopCloser(bytes.NewReader(tampered))
	if got := serve(srv, r); got.Code < 400 {
		t.Fatalf("tampered DAV upload accepted: %d %s", got.Code, got.Body.String())
	}
	expect(serve(srv, d.request("GET", "/dav/Team/dav.bin", nil)), 404, "tampered DAV upload published")
	expect(serve(srv, d.request("PUT", "/dav/Team/dav.bin", big)), 201, "signed DAV upload")

	// Unlinking itself is signed too.
	expect(send(t, srv, "DELETE", "/api/devices/self", "", nil, d.token), 401, "unsigned unlink")
	expect(serve(srv, d.request("DELETE", "/api/devices/self", nil)), 204, "signed unlink")
}

func TestDeviceKeyMustBeP256(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	user := loginAs(t, srv, st, "keyuser", "user")
	out := send(t, srv, "POST", "/api/devices/phone-code", `{}`, user, "")
	var qr struct{ Link string }
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &qr) != nil {
		t.Fatalf("phone code %d %s", out.Code, out.Body.String())
	}
	u, _ := url.Parse(qr.Link)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&p384.PublicKey)
	for _, key := range []string{base64.StdEncoding.EncodeToString(der), "not base64!"} {
		form := url.Values{"grant": {"qr"}, "code": {u.Query().Get("code")}, "name": {"Pixel 9"}, "platform": {"android"}, "publicKey": {key}}
		r := httptest.NewRequest("POST", "/api/devices/token", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got := serve(srv, r); got.Code != 400 {
			t.Fatalf("key %q: %d %s", key[:8], got.Code, got.Body.String())
		}
	}
}
