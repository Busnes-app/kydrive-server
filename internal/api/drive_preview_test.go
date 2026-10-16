package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestPreviewServesOnlyTheAllowlist(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	owner := loginAs(t, srv, st, "viewer", "user")
	outsider := loginAs(t, srv, st, "outsider", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "viewers", DisplayName: "Viewers", Members: []string{"usr_viewer"}}, true); err != nil {
		t.Fatal(err)
	}
	loginAs(t, srv, st, "mediaadmin", "admin")
	w, err := st.Drive().CreateWorkspace(ctx, "usr_mediaadmin", "Media", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_mediaadmin", w.ID, "viewers", "editor"); err != nil {
		t.Fatal(err)
	}
	upload := func(name, body string) string {
		t.Helper()
		out := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name="+name+"&parent=&revision=0", body, owner, "")
		var f struct{ ID string }
		if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &f) != nil {
			t.Fatalf("upload %s: %d %s", name, out.Code, out.Body.String())
		}
		return f.ID
	}
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 64)
	text := strings.Repeat("plain words <b>not markup</b>\n", 4000)
	served := map[string]struct{ name, body, kind string }{
		"png":     {"photo.png", png, "image/png"},
		"pdf":     {"paper.pdf", "%PDF-1.4\n%%EOF\n", "application/pdf"},
		"text":    {"notes.txt", text, "text/plain; charset=utf-8"},
		"mp4":     {"clip.mp4", "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom", "video/mp4"},
		"no name": {"README", "just text", "text/plain; charset=utf-8"},
		// Markup without an HTML or XML signature is text: shown as its source, never rendered.
		"bare svg": {"logo.svg", `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`, "text/plain; charset=utf-8"},
	}
	for what, c := range served {
		id := upload(c.name, c.body)
		got := send(t, srv, "GET", "/api/drive/files/"+id+"/preview", "", owner, "")
		h := got.Header()
		if got.Code != 200 || h.Get("Content-Type") != c.kind || got.Body.String() != c.body {
			t.Fatalf("%s: %d %q", what, got.Code, h.Get("Content-Type"))
		}
		if !strings.HasPrefix(h.Get("Content-Disposition"), "inline") || h.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox;") ||
			!strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'self'") || h.Get("X-Frame-Options") != "SAMEORIGIN" || h.Get("Cross-Origin-Resource-Policy") != "same-origin" || h.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s headers %v", what, h)
		}
	}

	// The bytes decide: active or unknown content never renders, whatever its name.
	refused := map[string][2]string{
		"html":         {"page.html", "<!DOCTYPE html><script>alert(1)</script>"},
		"html as png":  {"cat.png", "<html><script>alert(1)</script></html>"},
		"svg with xml": {"badge.svg", `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`},
		"utf-16 text":  {"wide.txt", "\xfe\xff\x00h\x00i"},
		"zip":          {"bundle.zip", "PK\x03\x04" + strings.Repeat("\x00", 40)},
		"octet stream": {"blob.bin", "\x00\x01\x02\x03"},
	}
	for what, c := range refused {
		id := upload(c[0], c[1])
		if got := send(t, srv, "GET", "/api/drive/files/"+id+"/preview", "", owner, ""); got.Code != http.StatusUnsupportedMediaType || strings.Contains(got.Body.String(), "alert") {
			t.Fatalf("%s: %d %q", what, got.Code, got.Body.String())
		}
	}

	id := upload("long.txt", text)
	// A range on a sealed blob, as a text preview asks for its first part.
	r := httptest.NewRequest("GET", "/api/drive/files/"+id+"/preview", nil)
	r.AddCookie(owner)
	r.Header.Set("Range", "bytes=0-99")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != text[:100] || rec.Header().Get("Content-Range") != "bytes 0-99/"+strconv.Itoa(len(text)) {
		t.Fatalf("range %d %q %q", rec.Code, rec.Header().Get("Content-Range"), rec.Body.String())
	}
	// HEAD tells the client the type without the body.
	head := send(t, srv, "HEAD", "/api/drive/files/"+id+"/preview", "", owner, "")
	if head.Code != 200 || head.Header().Get("Content-Type") != "text/plain; charset=utf-8" || head.Body.Len() != 0 {
		t.Fatalf("head %d %q", head.Code, head.Header().Get("Content-Type"))
	}

	if got := send(t, srv, "GET", "/api/drive/files/"+id+"/preview", "", outsider, ""); got.Code != http.StatusForbidden {
		t.Fatalf("outsider preview: %d", got.Code)
	}
	if got := send(t, srv, "PUT", "/api/drive/files/"+id+"/trash", `{"revision":1,"trashed":true}`, owner, ""); got.Code != 200 {
		t.Fatalf("trash %d %s", got.Code, got.Body.String())
	}
	if got := send(t, srv, "GET", "/api/drive/files/"+id+"/preview", "", owner, ""); got.Code != http.StatusForbidden {
		t.Fatalf("trashed preview: %d", got.Code)
	}
}
