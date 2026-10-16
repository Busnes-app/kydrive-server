package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

// fakeDocumentServer converts the way Euro-Office does: it fetches the source with an outbox
// token signed for exactly that URL and offers the PNG under /cache/.
type fakeDocumentServer struct {
	secret []byte
	mu     sync.Mutex
	output []byte
	source string // last source URL
	got    []byte // last source bytes
}

func (d *fakeDocumentServer) sign(t *testing.T, claims any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: d.secret}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (d *fakeDocumentServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cache/out.png" {
			d.mu.Lock()
			defer d.mu.Unlock()
			w.Write(d.output)
			return
		}
		var req struct {
			Token string `json:"token"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		parsed, err := jwt.ParseSigned(req.Token, []jose.SignatureAlgorithm{jose.HS256})
		var claims struct {
			URL string `json:"url"`
		}
		if err != nil || parsed.Claims(d.secret, &claims) != nil {
			w.Write([]byte(`{"error":-8}`))
			return
		}
		get, _ := http.NewRequest("GET", claims.URL, nil)
		get.Header.Set("Authorization", "Bearer "+d.sign(t, map[string]any{"payload": map[string]string{"url": claims.URL}, "exp": time.Now().Add(time.Minute).Unix()}))
		resp, err := http.DefaultClient.Do(get)
		if err != nil || resp.StatusCode != 200 {
			w.Write([]byte(`{"error":-4}`))
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		d.mu.Lock()
		d.source, d.got = claims.URL, body
		d.mu.Unlock()
		w.Write([]byte(`{"endConvert":true,"fileType":"png","fileUrl":"http://` + r.Host + `/cache/out.png","percent":100}`))
	}
}

func TestOfficeThumbnailsThroughTheDocumentServer(t *testing.T) {
	ds := &fakeDocumentServer{secret: []byte(strings.Repeat("s", 32))}
	editorServer := httptest.NewServer(ds.handler(t))
	defer editorServer.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	drive := "http://" + ln.Addr().String()
	t.Setenv("KYDRIVE_EDITOR_URL", editorServer.URL)
	t.Setenv("KYDRIVE_EDITOR_DRIVE_URL", drive)
	t.Setenv("KYDRIVE_EDITOR_SECRET", string(ds.secret))
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	live := httptest.NewUnstartedServer(srv)
	live.Listener.Close()
	live.Listener = ln
	live.Start()
	defer live.Close()

	ctx := context.Background()
	owner := loginAs(t, srv, st, "writer", "user")
	loginAs(t, srv, st, "officeadmin", "admin")
	if err = st.Groups().ReplaceGroup(ctx, &store.Group{ID: "writers", DisplayName: "Writers", Members: []string{"usr_writer"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_officeadmin", "Docs", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_officeadmin", w.ID, "writers", "editor"); err != nil {
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
	var page bytes.Buffer
	png.Encode(&page, image.NewRGBA(image.Rect(0, 0, 300, 200)))
	ds.output = page.Bytes()

	report := upload("report.docx", "PK docx bytes")
	got := send(t, srv, "GET", "/api/drive/files/"+report+"/thumbnail", "", owner, "")
	img, err := png.Decode(got.Body)
	if got.Code != 200 || err != nil || img.Bounds().Dx() != 256 || img.Bounds().Dy() != 170 {
		t.Fatalf("office thumbnail %d %v %s", got.Code, err, got.Body.String())
	}
	if string(ds.got) != "PK docx bytes" || !strings.HasPrefix(ds.source, drive+"/api/editor/convert/") {
		t.Fatalf("document server fetched %q from %q", ds.got, ds.source)
	}

	// The ticket served one conversion: afterwards, even a correctly signed fetch finds nothing.
	fetch := func(url, token string) int {
		r, _ := http.NewRequest("GET", url, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	signed := ds.sign(t, map[string]any{"payload": map[string]string{"url": ds.source}})
	if code := fetch(ds.source, signed); code != http.StatusNotFound {
		t.Fatalf("spent ticket: %d", code)
	}
	if code := fetch(ds.source, ds.sign(t, map[string]any{"payload": map[string]string{"url": drive + "/api/editor/convert/other"}})); code != http.StatusUnauthorized {
		t.Fatalf("token for another URL: %d", code)
	}

	// The converter's output is untrusted: it goes through the same bounded decoder.
	ds.output = []byte("<html>not a png</html>")
	if got := send(t, srv, "GET", "/api/drive/files/"+upload("slides.pptx", "PK pptx")+"/thumbnail", "", owner, ""); got.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("bad converter output: %d", got.Code)
	}
	// Formats the server does not convert, such as Markdown, still get no thumbnail.
	if got := send(t, srv, "GET", "/api/drive/files/"+upload("notes.md", "# notes")+"/thumbnail", "", owner, ""); got.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("markdown thumbnail: %d", got.Code)
	}
}
