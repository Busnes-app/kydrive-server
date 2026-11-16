package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// A KyIdentity that cannot be read, or refuses the credentials, changes nothing: no archive,
// no member removed.
func TestSuiteGroupsWorkerChangesNothingOnFailure(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	ctx := context.Background()
	if st.Drive() == nil {
		t.Skip("drive is SQLite-only")
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "alice", Username: "alice", Email: "alice@local.test", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-alice"}); err != nil {
		t.Fatal(err)
	}
	var status, refused atomic.Int32
	status.Store(http.StatusOK)
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if r.URL.Path != "/api/suite/groups" || !ok || id != "kydrive" || secret != "suite-secret" {
			refused.Add(1)
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		if code := int(status.Load()); code != http.StatusOK {
			http.Error(w, "down", code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"groups":[{"id":"g1","name":"Sales","slug":"sales","state":"active","members":["sub-alice"]}]}`))
	}))
	t.Cleanup(idp.Close)
	cfg.SSO.Enabled, cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.KySignOnSecret = true, idp.URL, "kydrive", "suite-secret"

	start := time.Now()
	api.SyncSuiteGroupsForTest(srv, ctx)
	t.Logf("one poll and reconcile pass: %v", time.Since(start))
	alice := func() []string {
		t.Helper()
		ws, err := st.Drive().Workspaces(ctx, "alice", false)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, w := range ws {
			if w.Kind == "shared" {
				out = append(out, w.Name+":"+w.Role)
			}
		}
		return out
	}
	if got := alice(); len(got) != 1 || got[0] != "Sales:editor" {
		t.Fatalf("after a good pass: %v", got)
	}

	status.Store(http.StatusInternalServerError)
	api.SyncSuiteGroupsForTest(srv, ctx)
	if got := alice(); len(got) != 1 || got[0] != "Sales:editor" {
		t.Fatalf("a 500 changed access: %v", got)
	}

	status.Store(http.StatusOK)
	cfg.SSO.KySignOnSecret = "wrong"
	api.SyncSuiteGroupsForTest(srv, ctx)
	if got := alice(); refused.Load() != 1 || len(got) != 1 || got[0] != "Sales:editor" {
		t.Fatalf("a 401 (%d refused) changed access: %v", refused.Load(), got)
	}

	// The loop stops with its context.
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); srv.RunSuiteGroups(run) }()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunSuiteGroups did not stop")
	}
}
