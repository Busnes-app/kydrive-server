package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExcalidrawCJKFonts(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Xiaolai"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Xiaolai", "a.woff2"), []byte("font"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	get := func(srv http.Handler, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}

	t.Setenv("KY_EXCALIDRAW_FONTS_DIR", dir)
	srv, _, _ := setupTestServer(t)
	if w := get(srv, "/excalidraw/fonts/Xiaolai/a.woff2"); w.Code != 200 || w.Body.String() != "font" {
		t.Fatalf("font: %d %q", w.Code, w.Body.String())
	}
	for _, path := range []string{"/excalidraw/fonts/Xiaolai/", "/excalidraw/fonts/Xiaolai/missing.woff2", "/excalidraw/fonts/Xiaolai/../secret", "/excalidraw/fonts/Xiaolai/%2e%2e/secret"} {
		if w := get(srv, path); w.Code == 200 || w.Body.String() == "secret" {
			t.Fatalf("%s: %d %q", path, w.Code, w.Body.String())
		}
	}

	// Without the directory the fonts are absent, not answered with the SPA shell.
	t.Setenv("KY_EXCALIDRAW_FONTS_DIR", "")
	srv, _, _ = setupTestServer(t)
	if w := get(srv, "/excalidraw/fonts/Xiaolai/a.woff2"); w.Code != 404 {
		t.Fatalf("unset dir: %d", w.Code)
	}
}
