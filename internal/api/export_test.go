package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// SignCSRFForTest sets the CSRF header that the request's own session cookie earns. Test-only.
func SignCSRFForTest(s *Server, r *http.Request) {
	r.Header.Set(auth.HeaderCSRF, s.sessions.CSRFToken(s.sessions.SessionToken(r)))
}

// SetRecoveryClientForTest replaces the KyRecovery pairing client. Test-only: this file is not
// part of the package build.
func SetRecoveryClientForTest(s *Server, p recoveryClient) { s.recovery = p }

// SetStoreForTest replaces the handlers' store, so a test can break one write. Test-only.
func SetStoreForTest(s *Server, st store.Store) { s.store = st }

// AttemptsCapForTest is the limiter's hard bound on distinct keys.
const AttemptsCapForTest = attemptsCap

// AttemptKeysForTest returns the limiter's live keys. Test-only: this file is not part of the
// package build.
func AttemptKeysForTest(s *Server) []string {
	s.attemptsMu.Lock()
	defer s.attemptsMu.Unlock()
	keys := make([]string, 0, len(s.attempts))
	for k := range s.attempts {
		keys = append(keys, k)
	}
	return keys
}

// AllowAttemptForTest drives the limiter directly so a test can fill it without paying for
// 10 000 HTTP requests. Test-only.
func AllowAttemptForTest(s *Server, key string, limit int, window time.Duration) bool {
	return s.allowAttempt(key, limit, window)
}

// RegisterDetachedForTest registers one detached handler and returns its unregister func, so a
// test can drive the counter without an HTTP request. Test-only.
func RegisterDetachedForTest(s *Server) func() {
	s.detached.add()
	return s.detached.done
}

// SetClockForTest replaces the clock /mcp signs agents in by, so a test can age a token. Test-only.
func SetClockForTest(s *Server, now func() time.Time) { s.now = now }

// Tool errors and limits the MCP tests compare against. Test-only.
const (
	MCPReadOnlyForTest   = msgReadOnly
	MCPBadPathForTest    = msgBadPath
	MCPNotFoundForTest   = msgNotFound
	MCPDeniedForTest     = msgDenied
	MCPExistsForTest     = msgExists
	MCPReplaceForTest    = msgReplace
	MCPChangedForTest    = msgChanged
	MCPNotTextForTest    = msgNotText
	MCPTooLargeForTest   = msgTooLarge
	MCPFixedForTest      = msgFixed
	MCPRootGoneForTest   = msgRootGone
	MCPReadLimitForTest  = mcpReadLimit
	MCPWriteLimitForTest = mcpWriteLimit
	MCPListLimitForTest  = mcpListLimit
	MCPReadNoticeForTest = readNotice
	MCPNoPDFForTest      = msgNoPDF
	MCPDamagedForTest    = msgDamaged
	MCPSearchInForTest   = msgSearchIn
)

// IndexSearchForTest brings the content search index up to date now: changes queued, the
// drive reconciled, every queued file indexed, debounce ignored. Tests call it instead of
// waiting for the worker. Test-only.
func IndexSearchForTest(s *Server) error { return s.indexIdle(context.Background()) }

// SearchRootForTest is the content search index directory. Test-only.
func SearchRootForTest(s *Server) string { return s.searchRoot() }

// TextRootForTest is where document text is cached. Test-only.
func TextRootForTest(s *Server) string { return s.textRoot() }
