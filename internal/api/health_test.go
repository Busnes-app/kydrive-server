package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/api"
)

func probe(t *testing.T, srv http.Handler, method, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("%s %s Cache-Control %q", method, path, got)
	}
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

// /livez never touches the database; /readyz answers from a ping cached for one second and
// turns 503 for good once the server drains. Both answer HEAD and say only their status.
func TestLivezAndReadyz(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	now := time.Now()
	api.SetClockForTest(srv, func() time.Time { return now })

	for _, c := range []struct{ method, path, body string }{
		{"GET", "/livez", `{"status":"ok"}`},
		{"GET", "/readyz", `{"status":"ready"}`},
		{"HEAD", "/livez", ""},
		{"HEAD", "/readyz", ""},
	} {
		// A recorder keeps a HEAD body, which net/http's server drops; only the code is checked.
		if code, body := probe(t, srv, c.method, c.path); code != 200 || (c.method == "GET" && body != c.body) {
			t.Errorf("%s %s: %d %s", c.method, c.path, code, body)
		}
	}

	// The cached answer holds for a second, then the next probe pings again.
	st.Close()
	if code, _ := probe(t, srv, "GET", "/readyz"); code != 200 {
		t.Errorf("within the cache second: %d", code)
	}
	now = now.Add(time.Second)
	if code, body := probe(t, srv, "GET", "/readyz"); code != 503 || body != `{"status":"unready"}` {
		t.Errorf("database down: %d %s", code, body)
	}
	if code, _ := probe(t, srv, "GET", "/livez"); code != 200 {
		t.Errorf("/livez with the database down: %d", code)
	}
}

func TestDrainTurnsReadyzUnready(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	srv.Drain()
	if code, body := probe(t, srv, "GET", "/readyz"); code != 503 || body != `{"status":"unready"}` {
		t.Fatalf("draining: %d %s", code, body)
	}
	if code, _ := probe(t, srv, "GET", "/livez"); code != 200 {
		t.Fatalf("/livez while draining: %d", code)
	}
}
