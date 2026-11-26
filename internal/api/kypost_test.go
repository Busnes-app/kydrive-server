package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

const (
	kypostOrigin = "https://post.example"
	kypostIssuer = "https://id.example"
)

var kypostSecret = bytes.Repeat([]byte("k"), 32)

type kypostWorld struct {
	t   *testing.T
	srv *api.Server
	st  store.Store
	cfg *config.Config
	seq int
	// logs holds everything the server logged; no ticket may appear in it.
	logs *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newKyPostWorld(t *testing.T, source string) *kypostWorld {
	t.Helper()
	logs := &syncBuffer{}
	prev := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(prev) })
	srv, st, cfg := setupTestServer(t)
	cfg.SSO.KySignOnIssuer = kypostIssuer
	cfg.Server.AppURL = "https://drive.example"
	api.SetKyPostForTest(srv, kypostOrigin, kypostSecret, source)
	w := &kypostWorld{t: t, srv: srv, st: st, cfg: cfg, logs: logs}
	for _, u := range []struct{ id, provider, sub, status string }{
		{"alice", "kysignon", "sub-alice", "active"},
		{"bob", "scim", "sub-bob", "active"},
		{"gone", "kysignon", "sub-gone", "inactive"},
		{"installer", "kysignon", "sub-installer", "active"},
	} {
		if err := st.Users().CreateUser(context.Background(), &store.User{ID: u.id, Username: u.id, Email: u.id + "@example.com", Role: "user", Status: u.status, SSOProvider: u.provider, SSOSubject: u.sub}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Settings().SetSetting(context.Background(), "installer.user.test", `{"setupId":"x","issuer":"`+kypostIssuer+`","subject":"sub-installer","userId":"installer","role":"admin"}`); err != nil {
		t.Fatal(err)
	}
	return w
}

// signed posts body to path signed under event; id "" takes a fresh event ID.
func (w *kypostWorld) signed(path, event, id string, body any, key []byte) *httptest.ResponseRecorder {
	w.t.Helper()
	raw, _ := json.Marshal(body)
	if id == "" {
		w.seq++
		id = fmt.Sprintf("evt-%d", w.seq)
	}
	h, err := syncauth.Sign(key, time.Now(), event, id, raw)
	if err != nil {
		w.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	h.Apply(req)
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, req)
	return rec
}

func (w *kypostWorld) ticket(sub string, name string, size int64) *httptest.ResponseRecorder {
	return w.signed("/api/suite/kypost/upload-tickets", "kypost.drive.ticket", "", map[string]any{"sub": sub, "issuer": kypostIssuer, "name": name, "size": size}, kypostSecret)
}

func (w *kypostWorld) uploadURL(sub, name string, size int64) string {
	w.t.Helper()
	rec := w.ticket(sub, name, size)
	var out struct {
		URL      string `json:"upload_url"`
		Expires  string `json:"expires"`
		MaxChunk int64  `json:"max_chunk"`
	}
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		w.t.Fatalf("ticket: %d %s", rec.Code, rec.Body.String())
	}
	exp, err := time.Parse(time.RFC3339, out.Expires)
	if err != nil || exp.Before(time.Now().Add(14*time.Minute)) || exp.After(time.Now().Add(16*time.Minute)) || out.MaxChunk != 64<<20 {
		w.t.Fatalf("ticket body %s", rec.Body.String())
	}
	if !strings.HasPrefix(out.URL, "https://drive.example/api/suite/upload/") {
		w.t.Fatalf("upload url %q", out.URL)
	}
	return strings.TrimPrefix(out.URL, "https://drive.example")
}

func (w *kypostWorld) put(path, origin, contentRange, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if contentRange != "" {
		req.Header.Set("Content-Range", contentRange)
	}
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, req)
	return rec
}

func (w *kypostWorld) setSwitch(user string, on bool) {
	w.t.Helper()
	if err := w.st.Drive().SetPeerSetting(context.Background(), user, on); err != nil {
		w.t.Fatal(err)
	}
}

func code(t *testing.T, rec *httptest.ResponseRecorder, status int, want string) {
	t.Helper()
	var out struct{ Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != status || out.Code != want {
		t.Errorf("got %d %q (%s), want %d %q", rec.Code, out.Code, rec.Body.String(), status, want)
	}
}

func TestKyPostSignedRefusals(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	routes := map[string]string{
		"/api/suite/kypost/upload-tickets": "kypost.drive.ticket",
		"/api/suite/kypost/links":          "kypost.drive.link",
		"/api/suite/kypost/status":         "kypost.drive.status",
		"/api/suite/kypost/fetch":          "kypost.drive.fetch",
		"/api/suite/kypost/view-tickets":   "kypost.drive.view",
	}
	alice := map[string]any{"sub": "sub-alice", "issuer": kypostIssuer, "name": "a.txt", "size": 1, "file_id": "x", "max_bytes": 10}
	for path, event := range routes {
		t.Run(path, func(t *testing.T) {
			code(t, w.signed(path, event, "", alice, bytes.Repeat([]byte("x"), 32)), 401, "bad_signature")
			for _, other := range routes {
				if other != event {
					code(t, w.signed(path, other, "", alice, kypostSecret), 401, "bad_signature")
				}
			}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			w.srv.ServeHTTP(rec, req)
			code(t, rec, 401, "bad_signature")
			code(t, w.signed(path, event, "", map[string]any{"pad": strings.Repeat("x", 70<<10)}, kypostSecret), 401, "bad_signature")
			code(t, w.signed(path, event, "", map[string]any{"issuer": kypostIssuer}, kypostSecret), 400, "bad_body")
			for _, who := range []map[string]any{
				{"sub": "sub-alice", "issuer": "https://other.example"},
				{"sub": "sub-nobody", "issuer": kypostIssuer},
				{"sub": "sub-gone", "issuer": kypostIssuer},
				{"sub": "sub-installer", "issuer": kypostIssuer},
			} {
				code(t, w.signed(path, event, "", who, kypostSecret), 404, "no_user")
			}
			// A verified replay is acknowledged and not acted on again.
			first := w.signed(path, event, "replayed-"+event, map[string]any{"sub": "sub-bob", "issuer": kypostIssuer}, kypostSecret)
			again := w.signed(path, event, "replayed-"+event, map[string]any{"sub": "sub-bob", "issuer": kypostIssuer}, kypostSecret)
			if first.Code == 200 && strings.Contains(first.Body.String(), "duplicate") || again.Code != 200 || strings.TrimSpace(again.Body.String()) != `{"outcome":"duplicate"}` {
				t.Errorf("replay: %d %s then %d %s", first.Code, first.Body.String(), again.Code, again.Body.String())
			}
		})
	}
	// Unpaired: every route, the upload included, is kypost_off.
	api.SetKyPostForTest(w.srv, "", nil, "")
	for path, event := range routes {
		code(t, w.signed(path, event, "", alice, kypostSecret), 404, "kypost_off")
	}
	code(t, w.put("/api/suite/upload/"+strings.Repeat("a", 43), "", "", "x"), 404, "kypost_off")
}

// The switch: on by default when KyIdentity paired the apps, off when the environment did; a
// stored choice wins; status reports it rather than refusing.
func TestKyPostSwitch(t *testing.T) {
	for _, source := range []string{"kyidentity", "environment"} {
		w := newKyPostWorld(t, source)
		on := source == "kyidentity"
		status := func() map[string]any {
			rec := w.signed("/api/suite/kypost/status", "kypost.drive.status", "", map[string]any{"sub": "sub-alice", "issuer": kypostIssuer}, kypostSecret)
			var out map[string]any
			if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
				t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
			}
			return out
		}
		if got := status(); got["enabled"] != on || got["max_file_bytes"] != float64(w.cfg.Drive.MaxFileSize) {
			t.Errorf("%s default: %v", source, got)
		}
		if !on {
			code(t, w.ticket("sub-alice", "a.txt", 1), 409, "sharing_off")
		}
		w.setSwitch("alice", true)
		if got := status(); got["enabled"] != true || got["free_bytes"] != float64(10<<30) {
			t.Errorf("%s on: %v", source, got)
		}
		path := w.uploadURL("sub-alice", "a.txt", 1)
		w.setSwitch("alice", false)
		code(t, w.ticket("sub-alice", "a.txt", 1), 409, "sharing_off")
		code(t, w.signed("/api/suite/kypost/links", "kypost.drive.link", "", map[string]any{"sub": "sub-alice", "issuer": kypostIssuer, "file_id": "x"}, kypostSecret), 409, "sharing_off")
		code(t, w.put(path, "", "", "x"), 409, "sharing_off")
		if got := status(); got["enabled"] != false || got["free_bytes"] != float64(0) {
			t.Errorf("%s off: %v", source, got)
		}
	}
}

func TestKyPostTicketRefusals(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	w.cfg.Drive.MaxFileSize = 1 << 20
	code(t, w.ticket("sub-alice", "a.txt", 1<<20+1), 413, "too_large")
	code(t, w.ticket("sub-alice", "a/b", 1), 400, "bad_body")
	code(t, w.ticket("sub-alice", "", 1), 400, "bad_body")
	code(t, w.signed("/api/suite/kypost/upload-tickets", "kypost.drive.ticket", "", map[string]any{"sub": "sub-alice", "issuer": kypostIssuer, "name": "a.txt"}, kypostSecret), 400, "bad_body")
	w.cfg.Drive.MaxFileSize = 20 << 30
	code(t, w.ticket("sub-alice", "a.txt", 11<<30), 507, "quota")
	for range 8 {
		w.uploadURL("sub-bob", "a.txt", 1)
	}
	code(t, w.ticket("sub-bob", "a.txt", 1), 429, "too_many_tickets")
	// Per-user rate limit, with Retry-After; the refused event is forgotten, so its retry counts afresh.
	var rec *httptest.ResponseRecorder
	for i := 0; i < 130 && (rec == nil || rec.Code != 429); i++ {
		rec = w.signed("/api/suite/kypost/status", "kypost.drive.status", fmt.Sprintf("limit-%d", i), map[string]any{"sub": "sub-alice", "issuer": kypostIssuer}, kypostSecret)
	}
	code(t, rec, 429, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
	code(t, w.signed("/api/suite/kypost/status", "kypost.drive.status", fmt.Sprintf("limit-%d", 120), map[string]any{"sub": "sub-alice", "issuer": kypostIssuer}, kypostSecret), 429, "rate_limited")
}

func TestKyPostTicketUpload(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	// One request, then a collision numbered, then chunks.
	path := w.uploadURL("sub-alice", "report.pdf", 5)
	if rec := w.put(path, "", "bytes 0-4/6", "hello"); rec.Code != 400 {
		t.Errorf("range total not the size: %d", rec.Code)
	}
	code(t, w.put(path, "", "", "hell"), 400, "size_mismatch")
	rec := w.put(path, kypostOrigin, "", "hello")
	var saved struct {
		FileID string `json:"file_id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
	}
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &saved) != nil || saved.Name != "report.pdf" || saved.Size != 5 || saved.FileID == "" {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != kypostOrigin || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("CORS on the upload: %q credentials %q", got, rec.Header().Get("Access-Control-Allow-Credentials"))
	}
	code(t, w.put(path, "", "", "hello"), 404, "not_found") // spent
	path2 := w.uploadURL("sub-alice", "report.pdf", 6)
	if rec := w.put(path2, "", "bytes 0-2/6", "abc"); rec.Code != 204 || rec.Header().Get("Upload-Offset") != "3" {
		t.Fatalf("first chunk: %d %s", rec.Code, rec.Body.String())
	}
	if rec := w.put(path2, "", "bytes 0-2/6", "abc"); rec.Code != 409 || rec.Header().Get("Upload-Offset") != "3" {
		t.Errorf("repeated chunk: %d %v", rec.Code, rec.Header())
	}
	rec = w.put(path2, "", "bytes 3-5/6", "def")
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"name":"report (2).pdf"`) {
		t.Fatalf("last chunk: %d %s", rec.Code, rec.Body.String())
	}
	var second struct {
		FileID string `json:"file_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &second)
	// An ordinary file of Alice's: listed in her My files, audited with the ticket noted.
	f, err := w.st.Drive().File(context.Background(), "alice", second.FileID, 1)
	if err != nil || f.Size != 6 {
		t.Fatalf("saved file: %+v %v", f, err)
	}
	if _, err := w.st.Drive().File(context.Background(), "bob", second.FileID, 1); err == nil {
		t.Error("another user reads the saved file")
	}
	recs, _, err := w.st.Audit().ListAuditRecords(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	saves := 0
	for _, r := range recs {
		if r.Action == "kypost.file_saved" && r.UserID == "alice" {
			saves++
		}
	}
	if saves != 2 {
		t.Errorf("%d kypost.file_saved audit records", saves)
	}
	// Bad tickets and origins.
	code(t, w.put("/api/suite/upload/short", "", "", "x"), 404, "not_found")
	code(t, w.put("/api/suite/upload/"+strings.Repeat("A", 43), "", "", "x"), 404, "not_found")
	code(t, w.put(w.uploadURL("sub-alice", "x.txt", 1), "https://evil.example", "", "x"), 403, "bad_origin")
	code(t, w.put(w.uploadURL("sub-alice", "x.txt", 1), w.cfg.Server.AppURL, "", "x"), 403, "bad_origin")
	big := w.uploadURL("sub-alice", "big.bin", 65<<20)
	code(t, w.put(big, "", "", "x"), 413, "too_large")
	// The ticket never reaches a log or the audit trail.
	recs, _, _ = w.st.Audit().ListAuditRecords(context.Background(), 0, 500)
	for _, p := range []string{path, path2, big} {
		token := strings.TrimPrefix(p, "/api/suite/upload/")
		if strings.Contains(w.logs.String(), token) {
			t.Errorf("ticket in the log")
		}
		for _, r := range recs {
			if strings.Contains(r.Resource+r.Details, token) {
				t.Errorf("ticket in audit %s", r.Action)
			}
		}
	}
}

// Only the ticket route answers the paired KyPost origin; preflight grants PUT without credentials.
func TestKyPostCORSIsTheTicketRouteOnly(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	pre := httptest.NewRequest(http.MethodOptions, "/api/suite/upload/"+strings.Repeat("a", 43), nil)
	pre.Header.Set("Origin", kypostOrigin)
	pre.Header.Set("Access-Control-Request-Method", "PUT")
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, pre)
	h := rec.Header()
	if rec.Code != 204 || h.Get("Access-Control-Allow-Origin") != kypostOrigin || h.Get("Access-Control-Allow-Methods") != "PUT, OPTIONS" ||
		h.Get("Access-Control-Allow-Headers") != "Content-Type, Content-Range" || h.Get("Access-Control-Allow-Credentials") != "" || !strings.Contains(h.Get("Vary"), "Origin") {
		t.Errorf("preflight: %d %v", rec.Code, h)
	}
	for _, rt := range []struct{ method, path string }{
		{"OPTIONS", "/api/suite/kypost/upload-tickets"}, {"POST", "/api/suite/kypost/status"}, {"GET", "/api/drive/workspaces"},
		{"OPTIONS", "/api/drive/workspaces"}, {"GET", "/"}, {"OPTIONS", "/api/suite/uploads"}, {"PUT", "/api/drive/upload-sessions/x"},
	} {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		req.Header.Set("Origin", kypostOrigin)
		rec := httptest.NewRecorder()
		w.srv.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s %s grants KyPost's origin: %q", rt.method, rt.path, got)
		}
	}
}

func TestKyPostLink(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	rec := w.put(w.uploadURL("sub-alice", "a.txt", 1), "", "", "a")
	var saved struct {
		FileID string `json:"file_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &saved)
	link := func(sub, file string, days any) *httptest.ResponseRecorder {
		body := map[string]any{"sub": sub, "issuer": kypostIssuer, "file_id": file}
		if days != nil {
			body["days"] = days
		}
		return w.signed("/api/suite/kypost/links", "kypost.drive.link", "", body, kypostSecret)
	}
	for days, want := range map[any]time.Duration{nil: 30 * 24 * time.Hour, 1: 24 * time.Hour, 7: 7 * 24 * time.Hour, 30: 30 * 24 * time.Hour} {
		rec := link("sub-alice", saved.FileID, days)
		var out struct{ URL, Expires string }
		if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !strings.HasPrefix(out.URL, "https://drive.example/l/#") {
			t.Fatalf("link %v: %d %s", days, rec.Code, rec.Body.String())
		}
		exp, err := time.Parse(time.RFC3339, out.Expires)
		if err != nil || exp.Sub(time.Now().Add(want)).Abs() > time.Minute {
			t.Errorf("days %v: expires %q", days, out.Expires)
		}
	}
	if rec := link("sub-alice", saved.FileID, 0); rec.Code != 201 || !strings.Contains(rec.Body.String(), `"expires":null`) {
		t.Errorf("no expiry: %d %s", rec.Code, rec.Body.String())
	}
	code(t, link("sub-alice", saved.FileID, 2), 400, "bad_body")
	code(t, link("sub-alice", "", nil), 400, "bad_body")
	code(t, link("sub-bob", saved.FileID, nil), 404, "not_found") // not Bob's
	code(t, link("sub-alice", "no-such-file", nil), 404, "not_found")
	// A file Alice owns outside Mail attachments is not KyPost's to share.
	home, err := w.st.Drive().EnsurePersonalWorkspace(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	f := uploadSmall(t, w, "alice", home)
	code(t, link("sub-alice", f, nil), 404, "not_found")
}

func uploadSmall(t *testing.T, w *kypostWorld, user, workspace string) string {
	t.Helper()
	ctx := context.Background()
	d := w.st.Drive()
	u, err := d.StartUpload(ctx, user, drive.UploadSession{Workspace: workspace, Name: "own.txt", Size: 1}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.AppendUpload(ctx, objects.DirTree(w.cfg.Database.DataDir+"/staging"), user, u.ID, 0, 1, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	f, err := d.CompleteUpload(ctx, objects.DirTree(w.cfg.Database.DataDir+"/staging"), objects.Dir(w.cfg.Database.DataDir+"/blobs"), user, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

func TestKyPostSettingsAndAdminStatus(t *testing.T) {
	w := newKyPostWorld(t, "environment")
	carol := loginWithProvider(t, w.srv, w.st, "carol", "kysignon", "sub-carol")
	get := func() string {
		return strings.TrimSpace(do(t, w.srv, http.MethodGet, "/api/drive/kypost", carol).Body.String())
	}
	if got := get(); got != `{"enabled":false,"guest_links":false,"guest_policy":false,"paired":true}` {
		t.Errorf("environment default: %s", got)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/drive/kypost", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(carol)
	api.SignCSRFForTest(w.srv, req)
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, req)
	if rec.Code != 200 || get() != `{"enabled":true,"guest_links":false,"guest_policy":false,"paired":true}` {
		t.Errorf("set: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, w.srv, http.MethodGet, "/api/settings/kypost", carol); rec.Code != 403 {
		t.Errorf("status for a member: %d", rec.Code)
	}
	admin := loginAs(t, w.srv, w.st, "root", "admin")
	rec = do(t, w.srv, http.MethodGet, "/api/settings/kypost", admin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"connected":true`) || !strings.Contains(rec.Body.String(), `"source":"environment"`) ||
		strings.Contains(rec.Body.String(), base64.StdEncoding.EncodeToString(kypostSecret)) || strings.Contains(rec.Body.String(), string(kypostSecret)) {
		t.Errorf("admin status: %d %s", rec.Code, rec.Body.String())
	}
	api.SetKyPostForTest(w.srv, "", nil, "")
	if got := get(); got != `{"enabled":false,"guest_links":false,"guest_policy":false,"paired":false}` {
		t.Errorf("unpaired: %s", got)
	}
}

// KyIdentity pairs and unpairs; an unreachable KyIdentity keeps the current link.
func TestKyPostResolveThroughKyIdentity(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	api.SetKyPostForTest(w.srv, "", nil, "")
	var mu sync.Mutex
	answer := http.StatusOK
	peerURL := kypostOrigin + "/"
	idp := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if id, secret, ok := r.BasicAuth(); r.URL.Path != "/api/suite/peer" || r.URL.Query().Get("client") != "kypost" || !ok || id != "kydrive" || secret != "suite-secret" {
			http.Error(rw, "no", 401)
			return
		}
		if answer != http.StatusOK {
			http.Error(rw, `{"error":"no_peer"}`, answer)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]string{"client": "kypost", "url": peerURL, "secret": base64.StdEncoding.EncodeToString(kypostSecret)})
	}))
	t.Cleanup(idp.Close)
	w.cfg.SSO.Enabled, w.cfg.SSO.KySignOnIssuer, w.cfg.SSO.KySignOnClientID, w.cfg.SSO.KySignOnSecret = true, idp.URL, "kydrive", "suite-secret"
	ctx := context.Background()
	if !api.ResolveKyPostForTest(w.srv, ctx) {
		t.Fatal("not linked")
	}
	admin := loginAs(t, w.srv, w.st, "root", "admin")
	if body := do(t, w.srv, http.MethodGet, "/api/settings/kypost", admin).Body.String(); !strings.Contains(body, `"source":"kyidentity"`) || !strings.Contains(body, `"url":"https://post.example"`) {
		t.Errorf("linked: %s", body)
	}
	answer = http.StatusInternalServerError
	if api.ResolveKyPostForTest(w.srv, ctx) || !strings.Contains(do(t, w.srv, http.MethodGet, "/api/settings/kypost", admin).Body.String(), `"connected":true`) {
		t.Error("an unreachable KyIdentity dropped the link")
	}
	answer = http.StatusNotFound
	if api.ResolveKyPostForTest(w.srv, ctx) || !strings.Contains(do(t, w.srv, http.MethodGet, "/api/settings/kypost", admin).Body.String(), `"reason":"no_peer"`) {
		t.Error("an unpaired KyPost stayed linked")
	}
	answer, peerURL = http.StatusOK, "https://post.example/path"
	if api.ResolveKyPostForTest(w.srv, ctx) {
		t.Error("a KyPost URL with a path was linked")
	}
	if strings.Contains(w.logs.String(), base64.StdEncoding.EncodeToString(kypostSecret)) || strings.Contains(w.logs.String(), string(kypostSecret)) {
		t.Error("pair secret logged")
	}
}

// flipBody turns the owner's switch off while the request body is being read.
type flipBody struct {
	w    *kypostWorld
	r    *strings.Reader
	done bool
}

func (f *flipBody) Read(p []byte) (int, error) {
	if !f.done {
		f.done = true
		f.w.setSwitch("alice", false)
	}
	return f.r.Read(p)
}

func TestKyPostSwitchOffDuringUpload(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	path := w.uploadURL("sub-alice", "late.txt", 3)
	req := httptest.NewRequest(http.MethodPut, path, &flipBody{w: w, r: strings.NewReader("abc")})
	req.ContentLength = 3
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, req)
	code(t, rec, 409, "sharing_off")
	home, err := w.st.Drive().EnsurePersonalWorkspace(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if files, err := w.st.Drive().Files(context.Background(), "alice", home, false); err != nil || len(files) != 0 {
		t.Errorf("published after the switch went off: %v %v", files, err)
	}
	code(t, w.put(path, "", "", "abc"), 404, "not_found")
}

// unpairBody unpairs KyPost while the request body is being read.
type unpairBody struct {
	srv  *api.Server
	r    *strings.Reader
	done bool
}

func (u *unpairBody) Read(p []byte) (int, error) {
	if !u.done {
		u.done = true
		api.SetKyPostForTest(u.srv, "", nil, "")
	}
	return u.r.Read(p)
}

func TestKyPostUnpairDuringUpload(t *testing.T) {
	for _, repair := range []bool{false, true} {
		w := newKyPostWorld(t, "kyidentity")
		path := w.uploadURL("sub-alice", "late.txt", 3)
		req := httptest.NewRequest(http.MethodPut, path, &unpairBody{srv: w.srv, r: strings.NewReader("abc")})
		req.ContentLength = 3
		if repair {
			// Re-paired under another secret before the bytes finish: the old pairing is gone too.
			req = httptest.NewRequest(http.MethodPut, path, &repairBody{srv: w.srv, r: strings.NewReader("abc")})
			req.ContentLength = 3
		}
		rec := httptest.NewRecorder()
		w.srv.ServeHTTP(rec, req)
		code(t, rec, 404, "kypost_off")
		home, err := w.st.Drive().EnsurePersonalWorkspace(context.Background(), "alice")
		if err != nil {
			t.Fatal(err)
		}
		if files, err := w.st.Drive().Files(context.Background(), "alice", home, false); err != nil || len(files) != 0 {
			t.Errorf("published after unpairing (repair %v): %v %v", repair, files, err)
		}
	}
}

type repairBody struct {
	srv  *api.Server
	r    *strings.Reader
	done bool
}

func (u *repairBody) Read(p []byte) (int, error) {
	if !u.done {
		u.done = true
		api.SetKyPostForTest(u.srv, kypostOrigin, bytes.Repeat([]byte("n"), 32), "kyidentity")
	}
	return u.r.Read(p)
}

// Tickets from a previous pairing do not publish after the secret rotates.
func TestKyPostTicketsDieWithTheirPairing(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	unused := w.uploadURL("sub-alice", "unused.txt", 1)
	partial := w.uploadURL("sub-alice", "partial.txt", 2)
	if rec := w.put(partial, "", "bytes 0-0/2", "a"); rec.Code != 204 {
		t.Fatalf("first chunk: %d", rec.Code)
	}
	api.SetKyPostForTest(w.srv, kypostOrigin, bytes.Repeat([]byte("r"), 32), "kyidentity")
	code(t, w.put(unused, "", "", "x"), 404, "kypost_off")
	code(t, w.put(partial, "", "bytes 1-1/2", "b"), 404, "kypost_off")
	code(t, w.put(unused, "", "", "x"), 404, "not_found") // removed
	home, err := w.st.Drive().EnsurePersonalWorkspace(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if files, err := w.st.Drive().Files(context.Background(), "alice", home, false); err != nil || len(files) != 0 {
		t.Errorf("published under a rotated pairing: %v %v", files, err)
	}
}
