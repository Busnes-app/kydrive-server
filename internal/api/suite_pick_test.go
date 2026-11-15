package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// pickWorld is a KyDrive whose KyIdentity lists KyPost, KyCalendar and an http app for every
// user, with KyPost paired through KyIdentity.
type pickWorld struct {
	*kypostWorld
	issuer string
}

func newPickWorld(t *testing.T) *pickWorld {
	t.Helper()
	w := newKyPostWorld(t, "kyidentity")
	idp := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(rw).Encode(map[string]any{"apps": []map[string]string{
			{"id": "kypost", "name": "KyPost", "url": kypostOrigin + "/login"},
			{"id": "kycalendar", "name": "KyCalendar", "url": "https://cal.example/"},
			{"id": "plain", "name": "Plain", "url": "http://plain.example/"},
		}})
	}))
	t.Cleanup(idp.Close)
	w.cfg.SSO.Enabled, w.cfg.SSO.KySignOnIssuer, w.cfg.SSO.KySignOnClientID, w.cfg.SSO.KySignOnSecret = true, idp.URL, "kydrive", "suite-secret"
	return &pickWorld{kypostWorld: w, issuer: idp.URL}
}

func pickCheck(t *testing.T, w *pickWorld, c *http.Cookie, origin, mode string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, w.srv, http.MethodGet, "/api/drive/pick?origin="+url.QueryEscape(origin)+"&mode="+mode, c)
}

func pick(t *testing.T, w *pickWorld, c *http.Cookie, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	return send(t, w.srv, http.MethodPost, "/api/drive/pick", string(raw), c, "")
}

func homeFile(t *testing.T, w *pickWorld, user string) string {
	t.Helper()
	home, err := w.st.Drive().EnsurePersonalWorkspace(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	w.seq++
	ctx, d := context.Background(), w.st.Drive()
	u, err := d.StartUpload(ctx, user, drive.UploadSession{Workspace: home, Name: fmt.Sprintf("f%d.txt", w.seq), Size: 1}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.AppendUpload(ctx, w.cfg.Database.DataDir+"/staging", w.cfg.Security.EncryptionKey, user, u.ID, 0, 1, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	f, err := d.CompleteUpload(ctx, w.cfg.Database.DataDir+"/staging", w.cfg.Database.DataDir+"/blobs", w.cfg.Security.EncryptionKey, user, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

func TestPickOrigins(t *testing.T) {
	w := newPickWorld(t)
	dana := loginWithProvider(t, w.srv, w.st, "dana", "kysignon", "sub-dana")
	f := homeFile(t, w, "usr_dana")

	if rec := pickCheck(t, w, nil, kypostOrigin, "link"); rec.Code != 401 {
		t.Errorf("signed out: %d", rec.Code)
	}
	if rec := send(t, w.srv, http.MethodPost, "/api/drive/pick", `{"origin":"`+kypostOrigin+`","files":["`+f+`"]}`, nil, ""); rec.Code != 401 {
		t.Errorf("signed out pick: %d", rec.Code)
	}
	if rec := pickCheck(t, w, dana, kypostOrigin, "link"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"app":"KyPost"`) {
		t.Errorf("listed origin: %d %s", rec.Code, rec.Body.String())
	}
	if rec := pickCheck(t, w, dana, "https://cal.example", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"mode":"link"`) {
		t.Errorf("default mode: %d %s", rec.Code, rec.Body.String())
	}
	for _, origin := range []string{
		"https://evil.example",      // foreign
		"http://plain.example",      // listed, but http
		"https://plain.example",     // the http app's host over https is still not its origin
		"https://post.example/",     // not exactly an origin
		"https://post.example:443",  // not exactly the app's origin string
		"https://POST.example",      // case differs
		"https://user@post.example", // userinfo
		"https://drive.example",     // KyDrive itself is not in this list
		"null", "", "post.example",
	} {
		code(t, pickCheck(t, w, dana, origin, "link"), 403, "bad_origin")
		code(t, pick(t, w, dana, map[string]any{"origin": origin, "files": []string{f}}), 403, "bad_origin")
	}
	code(t, pickCheck(t, w, dana, kypostOrigin, "move"), 400, "bad_mode")
	// The confirming POST is a session write: no CSRF token, no link.
	req := httptest.NewRequest(http.MethodPost, "/api/drive/pick", strings.NewReader(`{"origin":"`+kypostOrigin+`","files":["`+f+`"]}`))
	req.AddCookie(dana)
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "/l/#") {
		t.Errorf("pick without CSRF: %d %s", rec.Code, rec.Body.String())
	}
	// copy only for the paired KyPost.
	code(t, pickCheck(t, w, dana, "https://cal.example", "copy"), 403, "copy_not_allowed")
	code(t, pick(t, w, dana, map[string]any{"origin": "https://cal.example", "mode": "copy", "files": []string{f}}), 403, "copy_not_allowed")
	api.SetKyPostForTest(w.srv, "", nil, "")
	code(t, pickCheck(t, w, dana, kypostOrigin, "copy"), 403, "copy_not_allowed")
	if rec := pickCheck(t, w, dana, kypostOrigin, "link"); rec.Code != 200 {
		t.Errorf("link mode needs no pairing: %d", rec.Code)
	}

	// A local account has no suite list; a dedicated administrator is refused outright.
	local := loginAs(t, w.srv, w.st, "lou", "user")
	code(t, pickCheck(t, w, local, kypostOrigin, "link"), 403, "bad_origin")
	installer := loginWithProvider(t, w.srv, w.st, "inst", "kysignon", "sub-inst")
	if err := w.st.Settings().SetSetting(context.Background(), "installer.user.inst", `{"setupId":"y","issuer":"`+w.issuer+`","subject":"sub-inst","userId":"usr_inst","role":"admin"}`); err != nil {
		t.Fatal(err)
	}
	if rec := pickCheck(t, w, installer, kypostOrigin, "link"); rec.Code != 403 || !strings.Contains(rec.Body.String(), "everyday identity") {
		t.Errorf("dedicated administrator: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPickLinks(t *testing.T) {
	w := newPickWorld(t)
	dana := loginWithProvider(t, w.srv, w.st, "dana", "kysignon", "sub-dana")
	eve := loginWithProvider(t, w.srv, w.st, "eve", "kysignon", "sub-eve")
	a, b := homeFile(t, w, "usr_dana"), homeFile(t, w, "usr_dana")
	others := homeFile(t, w, "usr_eve")
	_ = eve

	rec := pick(t, w, dana, map[string]any{"origin": "https://cal.example", "files": []string{a, b}, "days": 7})
	var out struct {
		Files []map[string]any `json:"files"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Files) != 2 {
		t.Fatalf("pick: %d %s", rec.Code, rec.Body.String())
	}
	for _, f := range out.Files {
		if f["name"] == "" || f["size"] != float64(1) || !strings.HasPrefix(f["url"].(string), "https://drive.example/l/#") || f["expires"] == nil || f["file_id"] != nil {
			t.Errorf("picked file: %v", f)
		}
	}
	// The link is an ordinary organisation reader link, listed with the file's others.
	if rec := do(t, w.srv, http.MethodGet, "/api/drive/files/"+a+"/links", dana); !strings.Contains(rec.Body.String(), `"role":"reader"`) {
		t.Errorf("listed links: %s", rec.Body.String())
	}
	rec = pick(t, w, dana, map[string]any{"origin": "https://cal.example", "files": []string{a}, "days": 0})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"expires":null`) {
		t.Errorf("no expiry: %d %s", rec.Code, rec.Body.String())
	}
	for _, days := range []int{2, -1, 365} {
		code(t, pick(t, w, dana, map[string]any{"origin": "https://cal.example", "files": []string{a}, "days": days}), 400, "bad_body")
	}
	code(t, pick(t, w, dana, map[string]any{"origin": "https://cal.example", "files": []string{}}), 400, "bad_body")
	many := make([]string, 21)
	for i := range many {
		many[i] = a
	}
	code(t, pick(t, w, dana, map[string]any{"origin": "https://cal.example", "files": many}), 400, "bad_body")
	code(t, pick(t, w, dana, map[string]any{"origin": "https://cal.example", "files": []string{a, a}}), 404, "not_found")

	// Someone else's file refuses the whole pick, and nothing is linked.
	before := do(t, w.srv, http.MethodGet, "/api/drive/files/"+b+"/links", dana).Body.String()
	code(t, pick(t, w, dana, map[string]any{"origin": "https://cal.example", "files": []string{b, others}}), 404, "not_found")
	if after := do(t, w.srv, http.MethodGet, "/api/drive/files/"+b+"/links", dana).Body.String(); after != before {
		t.Errorf("refused pick left a link: %s", after)
	}
	if strings.Contains(w.logs.String(), "/l/#") {
		t.Error("a link token reached the log")
	}
}

func TestPickCopy(t *testing.T) {
	w := newPickWorld(t)
	dana := loginWithProvider(t, w.srv, w.st, "dana", "kysignon", "sub-dana")
	f := homeFile(t, w, "usr_dana")
	rec := pick(t, w, dana, map[string]any{"origin": kypostOrigin, "mode": "copy", "files": []string{f}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"file_id":"`+f+`"`) || strings.Contains(rec.Body.String(), `"url"`) {
		t.Fatalf("copy: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, w.srv, http.MethodGet, "/api/drive/files/"+f+"/links", dana); strings.Contains(rec.Body.String(), `"id"`) {
		t.Errorf("copy made a link: %s", rec.Body.String())
	}
	code(t, pick(t, w, dana, map[string]any{"origin": kypostOrigin, "mode": "copy", "files": []string{f}, "days": 7}), 400, "bad_body")
	// The user's KyPost switch off refuses copy.
	if rec := send(t, w.srv, http.MethodPut, "/api/drive/kypost", `{"enabled":false}`, dana, ""); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	code(t, pickCheck(t, w, dana, kypostOrigin, "copy"), 409, "sharing_off")
	code(t, pick(t, w, dana, map[string]any{"origin": kypostOrigin, "mode": "copy", "files": []string{f}}), 409, "sharing_off")
}

func TestPickRateLimit(t *testing.T) {
	w := newPickWorld(t)
	dana := loginWithProvider(t, w.srv, w.st, "dana", "kysignon", "sub-dana")
	f := homeFile(t, w, "usr_dana")
	for i := 0; i < 30; i++ {
		if rec := pick(t, w, dana, map[string]any{"origin": kypostOrigin, "mode": "copy", "files": []string{f}}); rec.Code != 200 {
			t.Fatalf("pick %d: %d", i, rec.Code)
		}
	}
	rec := pick(t, w, dana, map[string]any{"origin": kypostOrigin, "mode": "copy", "files": []string{f}})
	code(t, rec, 429, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}

// /pick is the SPA, under the global headers: no COOP (so window.opener survives) and never framed.
func TestPickPageHeaders(t *testing.T) {
	w := newPickWorld(t)
	rec := do(t, w.srv, http.MethodGet, "/pick?origin="+url.QueryEscape(kypostOrigin), nil)
	h := rec.Header()
	if rec.Code != 200 || h.Get("Cross-Origin-Opener-Policy") != "" || !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") || h.Get("X-Frame-Options") != "DENY" {
		t.Errorf("/pick: %d %v", rec.Code, h)
	}
}
