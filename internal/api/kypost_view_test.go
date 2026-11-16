package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/editor"
)

var viewDocx = "PK\x03\x04" + strings.Repeat("d", 60)

// viewWorld is a KyPost pairing with Euro-Office configured and two signed-in directory users.
type viewWorld struct {
	*kypostWorld
	signer      *editor.Client
	ann, ben    *http.Cookie
	driveOrigin string
}

func newViewWorld(t *testing.T) *viewWorld {
	t.Helper()
	signer, origin := editorFixture(t)
	w := &viewWorld{kypostWorld: newKyPostWorld(t, "kyidentity"), signer: signer, driveOrigin: origin}
	if w.st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	for _, name := range []string{"ann", "ben"} {
		c := loginAs(t, w.srv, w.st, name, "user")
		u, err := w.st.Users().GetUserByID(context.Background(), "usr_"+name)
		if err != nil {
			t.Fatal(err)
		}
		u.SSOProvider, u.SSOSubject = "kysignon", "sub-"+name
		if err = w.st.Users().UpdateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		if name == "ann" {
			w.ann = c
		} else {
			w.ben = c
		}
	}
	return w
}

func (w *viewWorld) viewTicket(sub, name string, size int) *httptest.ResponseRecorder {
	return w.signed("/api/suite/kypost/view-tickets", "kypost.drive.view", "", map[string]any{"sub": sub, "issuer": kypostIssuer, "name": name, "size": size}, kypostSecret)
}

// viewUpload reserves and uploads name for sub and returns the copy's ID.
func (w *viewWorld) viewUpload(sub, name, body string) string {
	w.t.Helper()
	rec := w.viewTicket(sub, name, len(body))
	var t struct {
		URL string `json:"upload_url"`
	}
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &t) != nil || !strings.HasPrefix(t.URL, "https://drive.example/api/suite/view/") {
		w.t.Fatalf("view ticket %d %s", rec.Code, rec.Body.String())
	}
	rec = w.put(strings.TrimPrefix(t.URL, "https://drive.example"), "", "", body)
	var out struct {
		URL string `json:"view_url"`
	}
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !strings.HasPrefix(out.URL, "https://drive.example/view.html?copy=") {
		w.t.Fatalf("view upload %d %s", rec.Code, rec.Body.String())
	}
	return strings.TrimPrefix(out.URL, "https://drive.example/view.html?copy=")
}

func (w *viewWorld) callback(key string, status int) *httptest.ResponseRecorder {
	token, err := w.signer.Sign(map[string]any{"key": key, "status": status, "url": "http://editor.test/cache/out.docx"})
	if err != nil {
		w.t.Fatal(err)
	}
	return send(w.t, w.srv, "POST", "/api/editor/callback", `{"token":"`+token+`"}`, nil, "")
}

func TestKyPostViewCopy(t *testing.T) {
	w := newViewWorld(t)
	status := w.signed("/api/suite/kypost/status", "kypost.drive.status", "", map[string]any{"sub": "sub-ann", "issuer": kypostIssuer}, kypostSecret)
	if !strings.Contains(status.Body.String(), `"viewer":true`) {
		t.Fatalf("status %s", status.Body.String())
	}
	id := w.viewUpload("sub-ann", "Q3 plan.docx", viewDocx)
	if rec := send(t, w.srv, "GET", "/view.html?copy="+id, "", w.ann, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `src="/view-bootstrap.js"`) || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("viewer page %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(t, w.srv, "GET", "/view.html?copy="+id, "", nil, ""); rec.Code == 200 {
		t.Fatal("viewer page without a session")
	}

	// Another person, a missing session and a wrong ID see nothing.
	if rec := send(t, w.srv, "POST", "/api/drive/view-copies/"+id+"/editor", "", w.ben, ""); rec.Code != 404 {
		t.Fatalf("another user opened it: %d", rec.Code)
	}
	if rec := send(t, w.srv, "POST", "/api/drive/view-copies/"+id+"/editor", "", nil, ""); rec.Code != 401 {
		t.Fatalf("no session: %d", rec.Code)
	}
	rec := send(t, w.srv, "POST", "/api/drive/view-copies/"+id+"/editor", "", w.ann, "")
	var opened struct {
		Config struct {
			Document struct {
				Key, URL    string
				Permissions map[string]bool
			} `json:"document"`
			EditorConfig struct{ Mode string } `json:"editorConfig"`
			Token        string                `json:"token"`
		} `json:"config"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &opened) != nil {
		t.Fatalf("open %d %s", rec.Code, rec.Body.String())
	}
	cfg := opened.Config
	if cfg.EditorConfig.Mode != "view" || cfg.Document.Key != "view-"+id || cfg.Document.URL != w.driveOrigin+"/api/editor/view/"+id {
		t.Fatalf("config %+v", cfg)
	}
	for _, p := range []string{"edit", "download", "print", "comment", "review", "fillForms"} {
		if on, ok := cfg.Document.Permissions[p]; !ok || on {
			t.Errorf("permission %s = %v %v", p, on, ok)
		}
	}
	// The signed token fixes view mode: a browser cannot flip it.
	claims, err := w.signer.Verify(cfg.Token)
	if err != nil || !strings.Contains(string(claims["editorConfig"]), `"mode":"view"`) || !strings.Contains(string(claims["document"]), `"edit":false`) {
		t.Fatalf("signed config %v %v", claims, err)
	}

	// Euro-Office alone fetches the bytes.
	download := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/editor/view/"+id, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		w.srv.ServeHTTP(rec, req)
		return rec
	}
	signed, _ := w.signer.Sign(map[string]any{"payload": map[string]string{"url": cfg.Document.URL}, "exp": time.Now().Add(time.Minute).Unix()})
	other, _ := w.signer.Sign(map[string]any{"payload": map[string]string{"url": w.driveOrigin + "/api/editor/download/" + id}})
	if got := download(""); got.Code != 401 {
		t.Fatalf("unsigned download %d", got.Code)
	}
	if got := download(other); got.Code != 401 {
		t.Fatalf("download signed for another URL %d", got.Code)
	}
	if got := download(signed); got.Code != 200 || got.Body.String() != viewDocx || strings.Contains(got.Header().Get("Content-Disposition"), "plan") {
		t.Fatalf("download %d %q %q", got.Code, got.Body.String(), got.Header().Get("Content-Disposition"))
	}

	// A view token cannot save, by either save status.
	for _, s := range []int{2, 6} {
		if got := w.callback("view-"+id, s); got.Code != 403 {
			t.Fatalf("save status %d: %d %s", s, got.Code, got.Body.String())
		}
	}
	if got := download(signed); got.Code != 200 {
		t.Fatalf("refused save removed the copy: %d", got.Code)
	}
	// Euro-Office closing the document deletes the copy.
	if got := w.callback("view-"+id, 4); got.Code != 200 {
		t.Fatalf("close callback %d", got.Code)
	}
	if got := download(signed); got.Code != 404 {
		t.Fatalf("closed copy served: %d", got.Code)
	}
	if rec := send(t, w.srv, "POST", "/api/drive/view-copies/"+id+"/editor", "", w.ann, ""); rec.Code != 404 {
		t.Fatalf("closed copy opened: %d", rec.Code)
	}

	// The viewer page closing deletes it, for its owner only.
	id = w.viewUpload("sub-ann", "sheet.csv", "a,b\n1,2\n")
	if rec := send(t, w.srv, "POST", "/api/drive/view-copies/"+id+"/close", "", w.ben, ""); rec.Code != 204 {
		t.Fatalf("close by another %d", rec.Code)
	}
	if rec := send(t, w.srv, "POST", "/api/drive/view-copies/"+id+"/editor", "", w.ann, ""); rec.Code != 200 {
		t.Fatalf("another user's close removed it: %d", rec.Code)
	}
	if rec := send(t, w.srv, "POST", "/api/drive/view-copies/"+id+"/close", "", w.ann, ""); rec.Code != 204 {
		t.Fatalf("close %d", rec.Code)
	}
	if rec := send(t, w.srv, "POST", "/api/drive/view-copies/"+id+"/editor", "", w.ann, ""); rec.Code != 404 {
		t.Fatalf("closed copy opened: %d", rec.Code)
	}
	// Signing out ends the viewer: Euro-Office's download stops and a drop is queued.
	id = w.viewUpload("sub-ann", "deck.pptx", viewDocx)
	if rec := send(t, w.srv, "POST", "/api/drive/view-copies/"+id+"/editor", "", w.ann, ""); rec.Code != 200 {
		t.Fatalf("open %d", rec.Code)
	}
	url := w.driveOrigin + "/api/editor/view/" + id
	signed, _ = w.signer.Sign(map[string]any{"payload": map[string]string{"url": url}, "exp": time.Now().Add(time.Minute).Unix()})
	get := func() int {
		req := httptest.NewRequest("GET", "/api/editor/view/"+id, nil)
		req.Header.Set("Authorization", "Bearer "+signed)
		rec := httptest.NewRecorder()
		w.srv.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := get(); got != 200 {
		t.Fatalf("download before logout %d", got)
	}
	if rec := send(t, w.srv, "POST", "/api/auth/logout", "", w.ann, ""); rec.Code != 200 {
		t.Fatalf("logout %d", rec.Code)
	}
	if got := get(); got != 404 {
		t.Fatalf("download after logout %d", got)
	}
	// Both: the copy closed earlier while opened, and the one this logout revoked.
	if drops, err := w.st.Drive().PendingViewDrops(context.Background()); err != nil || len(drops) != 2 || (drops[0].ID != id && drops[1].ID != id) {
		t.Fatalf("drop not queued: %+v %v", drops, err)
	}
	if strings.Contains(w.logs.String(), "Q3 plan") || strings.Contains(w.logs.String(), "sheet.csv") {
		t.Fatalf("a file name reached the log:\n%s", w.logs.String())
	}
}

func TestKyPostViewUploadRefusals(t *testing.T) {
	w := newViewWorld(t)
	code(t, w.viewTicket("sub-ann", "run.exe", 5), 400, "bad_body")
	code(t, w.viewTicket("sub-ann", "page.html", 5), 400, "bad_body")
	code(t, w.viewTicket("sub-ann", "a.docx", 0), 400, "bad_body")
	code(t, w.viewTicket("sub-ann", "a.docx", 25<<20+1), 413, "too_large")
	ticket := func() string {
		rec := w.viewTicket("sub-ann", "a.docx", len(viewDocx))
		var out struct {
			URL string `json:"upload_url"`
		}
		if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("ticket %d %s", rec.Code, rec.Body.String())
		}
		return strings.TrimPrefix(out.URL, "https://drive.example")
	}
	path := ticket()
	code(t, w.put(path, kypostOrigin, "", viewDocx), 403, "bad_origin")
	code(t, w.put(path, "", "", viewDocx[:10]), 400, "size_mismatch")
	code(t, w.put(path, "", "", "<html>"+strings.Repeat("x", len(viewDocx)-6)), 415, "bad_type")
	code(t, w.put("/api/suite/view/"+strings.Repeat("a", 43), "", "", viewDocx), 404, "not_found")
	if rec := w.put(path, "", "", viewDocx); rec.Code != 201 {
		t.Fatalf("upload %d %s", rec.Code, rec.Body.String())
	}
	code(t, w.put(path, "", "", viewDocx), 404, "not_found") // spent
	// Four live per person.
	ticket()
	ticket()
	ticket()
	code(t, w.viewTicket("sub-ann", "a.docx", 5), 429, "too_many_tickets")
	// The person's switch, then the pairing.
	path = ticket2(t, w, "sub-ben")
	w.setSwitch("usr_ben", false)
	code(t, w.put(path, "", "", viewDocx), 409, "sharing_off")
	code(t, w.viewTicket("sub-ben", "a.docx", 5), 409, "sharing_off")
	w.setSwitch("usr_ben", true)
	path = ticket2(t, w, "sub-ben")
	api.SetKyPostForTest(w.srv, "", nil, "")
	code(t, w.put(path, "", "", viewDocx), 404, "kypost_off")
}

func ticket2(t *testing.T, w *viewWorld, sub string) string {
	t.Helper()
	rec := w.viewTicket(sub, "a.docx", len(viewDocx))
	var out struct {
		URL string `json:"upload_url"`
	}
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("ticket %d %s", rec.Code, rec.Body.String())
	}
	return strings.TrimPrefix(out.URL, "https://drive.example")
}

// Without Euro-Office there is nothing to view with: status says so and tickets are refused.
func TestKyPostViewNeedsEuroOffice(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	status := w.signed("/api/suite/kypost/status", "kypost.drive.status", "", map[string]any{"sub": "sub-alice", "issuer": kypostIssuer}, kypostSecret)
	if !strings.Contains(status.Body.String(), `"viewer":false`) {
		t.Fatalf("status %s", status.Body.String())
	}
	code(t, w.signed("/api/suite/kypost/view-tickets", "kypost.drive.view", "", map[string]any{"sub": "sub-alice", "issuer": kypostIssuer, "name": "a.pdf", "size": 5}, kypostSecret), 409, "editor_off")
}
