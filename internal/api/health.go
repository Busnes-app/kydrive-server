package api

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ReadyDrain is how long /readyz answers 503 before the server stops accepting connections, so
// a load balancer probing every second takes the replica out before any request is refused.
const ReadyDrain = 5 * time.Second

// readiness is /readyz's state: a database ping cached for one second, behind a lock so any
// number of probes cost at most one ping a second, and the drain flag, which never clears.
type readiness struct {
	draining atomic.Bool
	mu       sync.Mutex
	at       time.Time
	ok       bool
}

// handleLivez answers whether the process is alive, without touching the database: for
// restarts only.
func (s *Server) handleLivez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz answers whether this replica should receive traffic: not draining, and the
// database answered a ping within one second.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.ready.draining.Load() && s.databaseReady(r.Context()) {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unready"})
}

func (s *Server) databaseReady(ctx context.Context) bool {
	rd := &s.ready
	rd.mu.Lock()
	defer rd.mu.Unlock()
	if now := s.now(); now.Sub(rd.at) >= time.Second || now.Before(rd.at) {
		ping, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		rd.ok = s.store.Ping(ping) == nil
		cancel()
		rd.at = s.now()
	}
	return rd.ok
}

// Drain turns /readyz to 503 for good: the server is stopping. Every other route keeps
// serving until the HTTP server shuts down.
func (s *Server) Drain() { s.ready.draining.Store(true) }
