package api_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// twoReplicas is setupMultiServer's server and a second replica on the same PostgreSQL
// database, each with its own store, as two hosts run them (tenancy design R4).
func twoReplicas(t *testing.T) (*multiAPI, *api.Server) {
	t.Helper()
	if !testdb.Postgres() {
		t.Skip("PostgreSQL only: replicas share this state there; SQLite keeps it in memory")
	}
	m := setupMultiServer(t)
	st, err := store.Open(context.Background(), m.cfg.Database, m.cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := api.NewServer(m.cfg, st)
	t.Cleanup(func() {
		if err := b.CloseSearch(); err != nil {
			t.Error(err)
		}
	})
	return m, b
}

// Per-IP and per-account sign-in windows count every replica's attempts.
func TestReplicasShareSignInWindows(t *testing.T) {
	m, b := twoReplicas(t)
	replicas := []*api.Server{m.srv, b}
	for i := 1; i <= 20; i++ {
		if code := exemptCall(t, replicas[i%2], "POST", "/api/auth/login", `{}`, "198.51.100.20:1", "", nil).Code; code != http.StatusBadRequest {
			t.Fatalf("attempt %d answered %d, want 400", i, code)
		}
	}
	for i, srv := range replicas {
		if code := exemptCall(t, srv, "POST", "/api/auth/login", `{}`, "198.51.100.20:1", "", nil).Code; code != http.StatusTooManyRequests {
			t.Fatalf("replica %d answered attempt 21 with %d, want 429", i, code)
		}
	}
	login := func(srv *api.Server, ip, pass string) int {
		return exemptCall(t, srv, "POST", "/api/auth/login", `{"username":"a2","password":"`+pass+`"}`, ip, "", nil).Code
	}
	for i := 1; i <= 10; i++ {
		if code := login(replicas[i%2], "198.51.100.21:1", "WrongPass123!"); code != http.StatusUnauthorized {
			t.Fatalf("guess %d answered %d", i, code)
		}
	}
	for i, srv := range replicas {
		if code := login(srv, "198.51.100.21:1", "SuperSecretPass123!"); code != http.StatusUnauthorized {
			t.Fatalf("replica %d let the right password through an exhausted window: %d", i, code)
		}
	}
	if code := login(b, "198.51.100.22:1", "SuperSecretPass123!"); code != http.StatusOK {
		t.Fatalf("the owner from another client answered %d", code)
	}
}

// A CAPTCHA solution spent on one replica is spent on all of them.
func TestReplicasSpendACaptchaOnce(t *testing.T) {
	m, b := twoReplicas(t)
	m.cfg.Captcha.Provider, m.cfg.Captcha.DifficultyPoW = "pow", 2000
	token := solvePoW(t, m.srv)
	body := map[string]string{"username": "nobody", "password": "WrongPassword123!", "captcha_token": token}
	if w := postLogin(m.srv, "198.51.100.23", body); w.Code != http.StatusUnauthorized {
		t.Fatalf("first use: %d %s", w.Code, w.Body)
	}
	if w := postLogin(b, "198.51.100.24", body); w.Code != http.StatusForbidden {
		t.Fatalf("the solution replayed to the other replica answered %d, want 403", w.Code)
	}
}

// A signed device request captured on its way to one replica cannot be replayed to another.
func TestReplicasRefuseAReplayedDeviceSignature(t *testing.T) {
	m, b := twoReplicas(t)
	d := linkSigned(t, m.srv, m.session["a1"], api.DeviceOriginForTest(m.srv))
	r := d.request("GET", "/api/drive/workspaces", nil)
	replay := r.Clone(context.Background())
	if got := serve(m.srv, r); got.Code != http.StatusOK {
		t.Fatalf("first: %d %s", got.Code, got.Body.String())
	}
	if got := serve(b, replay); got.Code != http.StatusUnauthorized {
		t.Fatalf("replay to the other replica: %d, want 401", got.Code)
	}
}

// A KyPost event ID accepted by one replica is a replay on the other, until it is forgotten
// (the 429 and 5xx answers do that), after which the sender's retry is accepted.
func TestReplicasShareKyPostReplays(t *testing.T) {
	m, b := twoReplicas(t)
	ga, gb := api.KyPostReplayForTest(m.srv), api.KyPostReplayForTest(b)
	at := time.Now()
	if err := ga.Check("evt-1", at); err != nil {
		t.Fatal(err)
	}
	if err := gb.Check("evt-1", at); !errors.Is(err, syncauth.ErrReplay) {
		t.Fatalf("the other replica's check: %v, want ErrReplay", err)
	}
	gb.(syncauth.Forgetter).Forget("evt-1")
	if err := ga.Check("evt-1", at); err != nil {
		t.Fatalf("the retry after Forget: %v", err)
	}
}

// A conversion ticket made on one replica is served by any, until it is dropped, and carries
// no file name.
func TestReplicasShareConversionTickets(t *testing.T) {
	m, b := twoReplicas(t)
	f := drive.File{Name: "Board minutes.docx", Blob: "blob-1", Digest: "d1", Size: 9, Sealed: true}
	id := api.NewTicketForTest(m.srv, f)
	got, ok := api.TicketForTest(b, id)
	if !ok || got.Blob != f.Blob || got.Digest != f.Digest || got.Size != f.Size || !got.Sealed || got.Name != "document.docx" {
		t.Fatalf("the other replica's ticket: %+v %v", got, ok)
	}
	api.DropTicketForTest(b, id)
	if _, ok := api.TicketForTest(m.srv, id); ok {
		t.Fatal("a dropped ticket still serves")
	}
}

// When the shared state cannot answer, a limit refuses: it never falls back to a fresh window.
func TestSharedStateFailureRefuses(t *testing.T) {
	m, _ := twoReplicas(t)
	if _, err := m.db.Exec(`DROP TABLE shared_windows`); err != nil {
		t.Fatal(err)
	}
	if code := exemptCall(t, m.srv, "POST", "/api/auth/login", `{}`, "198.51.100.25:1", "", nil).Code; code != http.StatusTooManyRequests {
		t.Fatalf("login with the windows table gone answered %d, want 429", code)
	}
}

// A WebDAV lock taken through one replica holds on the other: a write there without the
// token waits (423), the token one replica signed is honoured by the other, and either
// replica releases it.
func TestReplicasShareDAVLocks(t *testing.T) {
	m, b := twoReplicas(t)
	_, secret, err := m.st.Drive().CreateAppPassword(context.Background(), "a1", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	path := "/dav/Team/Plans/plan.txt"
	lock := davRequest(m.srv, "LOCK", path, "a1", secret, strings.NewReader(`<?xml version="1.0"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`), "Timeout", "Second-60")
	if lock.Code != http.StatusOK {
		t.Fatalf("lock: %d %s", lock.Code, lock.Body.String())
	}
	token := lock.Header().Get("Lock-Token")
	if got := davRequest(b, "PUT", path, "a1", secret, strings.NewReader("no token")); got.Code != http.StatusLocked {
		t.Fatalf("write without the token on the other replica: %d, want 423", got.Code)
	}
	if got := davRequest(b, "PUT", path, "a1", secret, strings.NewReader("v2"), "If", "("+token+")"); got.Code != http.StatusCreated && got.Code != http.StatusNoContent {
		t.Fatalf("write with the token on the other replica: %d %s", got.Code, got.Body.String())
	}
	if got := davRequest(b, "UNLOCK", path, "a1", secret, nil, "Lock-Token", token); got.Code != http.StatusNoContent {
		t.Fatalf("unlock on the other replica: %d", got.Code)
	}
	if got := davRequest(m.srv, "PUT", path, "a1", secret, strings.NewReader("v3")); got.Code != http.StatusCreated && got.Code != http.StatusNoContent {
		t.Fatalf("write after the unlock: %d", got.Code)
	}
}
