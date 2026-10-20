package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/dav"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/editor"
	"github.com/Busnes-app/kydrive-server/internal/scim"
	"github.com/Busnes-app/kydrive-server/internal/sso"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/web"
)

// recoveryClient is the KyRecovery client as the handlers use it, narrowed so tests can stand
// in a fake without reaching the network.
type recoveryClient interface {
	ClaimPairing(ctx context.Context, serverURL, pairingCode, serviceName, appName string) (recoveryclient.PairingResult, error)
	recoveryclient.Depositor
}

type Server struct {
	davLocks   *dav.Locks   // one per server: WebDAV locks are single-instance
	mcp        http.Handler // /mcp, for AI agents
	now        func() time.Time
	editor     *editor.Client
	editorErr  error
	revokeMu   sync.Mutex
	config     *config.Config
	store      store.Store
	sessions   *auth.SessionManager
	kysignon   *sso.KySignOnClient
	scim       *scim.Server
	recovery   recoveryClient
	mux        *http.ServeMux
	attemptsMu sync.Mutex
	ticketsMu  sync.Mutex
	tickets    map[string]convertTicket // Document Server fetches for thumbnails
	attempts   map[string]attemptWindow
	powSpent   auth.PoWSpent
	// detached counts the requests running on a context deliberately separated from their
	// connection. http.Server.Shutdown does not know about them, so runServer waits on this
	// before the store closes.
	detached detachedCounter
	// collecting outlives requests but not shutdown: a detached deposit still collecting
	// has sent nothing, so StopBackupCollection may abandon it.
	collecting     context.Context
	stopCollecting context.CancelFunc
}

// detachedCounter is a WaitGroup that tolerates a registration arriving while the wait is
// already running. sync.WaitGroup panics on an Add from zero concurrent with Wait, and there is
// no barrier that rules that out here: Shutdown returns when its own timeout expires, with
// requests still in flight, so a second admin request can register just as the first finishes
// and drops the count to zero. A counter under a condition variable has no such rule.
type detachedCounter struct {
	once sync.Once
	mu   sync.Mutex
	cond *sync.Cond
	n    int
}

// signal builds the condition variable on first use, so the zero value of Server works.
func (d *detachedCounter) signal() *sync.Cond {
	d.once.Do(func() { d.cond = sync.NewCond(&d.mu) })
	return d.cond
}

func (d *detachedCounter) add() {
	c := d.signal()
	c.L.Lock()
	d.n++
	c.L.Unlock()
}

func (d *detachedCounter) done() {
	c := d.signal()
	c.L.Lock()
	d.n--
	c.L.Unlock()
	c.Broadcast()
}

// tracked counts a request as detached for as long as h runs. It wraps the auth middleware
// rather than the handler: requireAdmin authenticates against the store before the handler is
// reached, ReadTimeout (15s) outlasts cmd/server's shutdownTimeout (5s), and a SIGTERM landing
// during that lookup would otherwise leave the counter at zero, WaitDetached returning and the
// store closing under a request about to pin a key.
//
// The window before ServeHTTP is entered -- while net/http is still reading the request line
// and headers -- cannot be covered by any counter: there is no handler goroutine to register
// yet. Shutdown's own drain is all that covers it, which is why shutdownTimeout is spent first.
func (s *Server) tracked(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.detached.add()
		defer s.detached.done()
		h(w, r)
	}
}

// WaitDetached blocks until every request that detached from its connection has finished. It is
// called after http.Server.Shutdown and before the store is closed: pairing, the key pin and a
// deposit all keep writing after their connection is gone, and a closed store under them leaves
// a key pinned on disk with no row recording it.
func (s *Server) WaitDetached() {
	c := s.detached.signal()
	c.L.Lock()
	defer c.L.Unlock()
	for s.detached.n > 0 {
		c.Wait()
	}
}

type attemptWindow struct {
	count int
	reset time.Time
}

// attemptsCap bounds the limiter map. Unauthenticated callers influence the keys, so the map
// is itself attack surface. At the cap we evict, never refuse: refusing every unknown key
// would let one caller fill the map and lock every new client out of login.
//
// The trade-off: memory is bounded, but an attacker who fills the map shortens other clients'
// windows, since an evicted counter starts again from zero. That weakens throttling while the
// attack runs; it never locks anyone out, which is the failure mode worth avoiding.
//
// Eviction is deliberately blind to how much of a window is left. Picking the entry nearest to
// expiry would always sacrifice the shortest windows first, so a caller minting keys with a
// long window could keep the one-minute login counter from ever reaching its limit. Every key
// is therefore equally likely to go. The real defence is that no key carries caller-supplied
// bytes, so filling the map costs an attacker one slot per IP.
const attemptsCap = 10000

func NewServer(cfg *config.Config, st store.Store) *Server {
	sessions := auth.NewSessionManager(st, cfg.Security)
	kysignon := sso.NewKySignOnClient(cfg.SSO, st)
	scimSrv := scim.NewServer(st, cfg.SCIM, cfg.Server.AppURL)
	recovery := recoveryclient.NewClient(recoveryclient.Options{AllowPrivate: cfg.Backup.AllowPrivateRecovery})

	s := &Server{
		davLocks: dav.NewLocks(),
		config:   cfg,
		store:    st,
		sessions: sessions,
		kysignon: kysignon,
		scim:     scimSrv,
		recovery: recovery,
		mux:      http.NewServeMux(),
		attempts: make(map[string]attemptWindow),
		tickets:  make(map[string]convertTicket),
		now:      time.Now,
	}
	s.collecting, s.stopCollecting = context.WithCancel(context.Background())

	s.editor, s.editorErr = editor.FromEnv(cfg.Server.AppURL, cfg.Server.Environment)
	s.mcp = s.newMCPHandler()
	s.routes()
	return s
}

// StopBackupCollection cancels detached deposits still collecting their payload, so shutdown
// waits only for those sealing or uploading.
func (s *Server) StopBackupCollection() { s.stopCollecting() }

func (s *Server) allowAttempt(key string, limit int, window time.Duration) bool {
	now := time.Now()
	s.attemptsMu.Lock()
	defer s.attemptsMu.Unlock()
	if _, known := s.attempts[key]; !known && len(s.attempts) >= attemptsCap {
		s.makeRoom(now)
	}
	entry := bumpWindow(s.attempts[key], now, window)
	s.attempts[key] = entry
	return entry.count <= limit
}

// attemptsExceeded reports, without counting, whether key already reached limit in its window.
func (s *Server) attemptsExceeded(key string, limit int) bool {
	s.attemptsMu.Lock()
	defer s.attemptsMu.Unlock()
	w, ok := s.attempts[key]
	return ok && time.Now().Before(w.reset) && w.count >= limit
}

// refundAttempt returns one attempt allowAttempt counted that the request did not spend.
func (s *Server) refundAttempt(key string) {
	s.attemptsMu.Lock()
	defer s.attemptsMu.Unlock()
	if w, ok := s.attempts[key]; ok && w.count > 0 {
		w.count--
		s.attempts[key] = w
	}
}

func (s *Server) resetAttempts(key string) {
	s.attemptsMu.Lock()
	delete(s.attempts, key)
	s.attemptsMu.Unlock()
}

// makeRoom frees a slot for a new key: it drops every expired window, and if the map is still
// full it drops one live entry chosen at random, never the one nearest expiry. Caller holds
// attemptsMu. The scan is O(attemptsCap) and only runs for a new key while the map is full;
// 10 000 entries is microseconds.
func (s *Server) makeRoom(now time.Time) {
	for candidate, w := range s.attempts {
		if now.After(w.reset) {
			delete(s.attempts, candidate)
		}
	}
	if len(s.attempts) >= attemptsCap {
		// Go randomises map iteration, so the first entry is an unbiased victim.
		for candidate := range s.attempts {
			delete(s.attempts, candidate)
			break
		}
	}
}

func bumpWindow(entry attemptWindow, now time.Time, window time.Duration) attemptWindow {
	if now.After(entry.reset) {
		entry = attemptWindow{reset: now.Add(window)}
	}
	entry.count++
	return entry
}

// requestIP is the limiter's key for unauthenticated routes. It resolves to the same address
// a session is bound to, and honours X-Forwarded-For only from a configured trusted proxy:
// keying on a caller-supplied header would make every limit here bypassable.
func (s *Server) requestIP(r *http.Request) string {
	return auth.ClientIP(r, s.config.Security.TrustedProxies)
}

func (s *Server) routes() {
	if s.store.Drive() != nil {
		s.driveRoutes()
	}
	// Auth
	s.mux.HandleFunc("/api/auth/pow-challenge", s.handlePoWChallenge)
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/mfa/totp", s.handleMFATOTP)
	s.mux.HandleFunc("/api/auth/mfa/recovery-code", s.handleMFARecovery)
	s.mux.HandleFunc("/api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("/api/auth/me", s.handleMe)
	s.mux.HandleFunc("/api/auth/change-password", s.handleChangePassword)
	s.mux.HandleFunc("PUT /api/auth/preferences", s.requireAuthenticated(s.handleSetPreferences))
	// Session routes: CSRF-checked, unlike the pre-session MFA login routes above.
	s.mux.HandleFunc("POST /api/auth/mfa/enroll", s.requireAuthenticated(s.handleMFAEnroll))
	s.mux.HandleFunc("POST /api/auth/mfa/enroll/confirm", s.requireAuthenticated(s.handleMFAEnrollConfirm))
	s.mux.HandleFunc("POST /api/auth/mfa/disable", s.requireAuthenticated(s.handleMFADisable))
	s.mux.HandleFunc("GET /api/auth/app-passwords", s.requireAuthenticated(s.handleAppPasswords))
	s.mux.HandleFunc("POST /api/auth/app-passwords", s.requireAuthenticated(s.handleCreateAppPassword))
	s.mux.HandleFunc("PATCH /api/auth/app-passwords/{id}", s.requireAuthenticated(s.handleSetAgentScope))
	s.mux.HandleFunc("DELETE /api/auth/app-passwords/{id}", s.requireAuthenticated(s.handleRevokeAppPassword))
	// The agent skill holds no user or instance data.
	s.mux.HandleFunc("GET /agent/skills/kydrive/SKILL.md", s.handleAgentSkill)

	// SSO
	s.mux.HandleFunc("/api/sso/kysignon/login", s.handleKySignOnLogin)
	s.mux.HandleFunc("/api/sso/kysignon/callback", s.handleKySignOnCallback)
	s.mux.HandleFunc("/api/sso/kysignon/sync", s.handleKySignOnSyncWebhook)

	// Feature 0 KyBackup & Restore Drills. Capsules carry site data and keys: admins only.
	// Method patterns: only the declared method reaches a handler. Export is a POST so the
	// CSRF check covers a download that carries the whole instance.
	s.mux.HandleFunc("POST /api/backup/drill", s.requireAdmin(s.handleBackupDrill))
	s.mux.HandleFunc("POST /api/backup/export-capsule", s.requireAdmin(s.handleExportCapsule))
	s.mux.HandleFunc("POST /api/backup/pair-remote", s.tracked(s.requireAdmin(s.handlePairRemoteRecovery)))
	s.mux.HandleFunc("POST /api/backup/deposit", s.tracked(s.requireAdmin(s.handleRunBackup)))
	s.mux.HandleFunc("DELETE /api/backup/pairing", s.requireAdmin(s.handleUnpair))
	s.mux.HandleFunc("POST /api/backup/pin-key", s.tracked(s.requireAdmin(s.handlePinKey)))
	s.mux.HandleFunc("PUT /api/backup/schedule", s.requireAdmin(s.handleSetSchedule))
	s.mux.HandleFunc("PUT /api/backup/bulk-retention", s.requireAdmin(s.handleSetBulkRetention))
	s.mux.HandleFunc("GET /api/backup/status", s.requireAdmin(s.handleBackupStatus))

	// Settings & Theme. The read endpoint tiers its own payload by role.
	s.mux.HandleFunc("/api/settings", s.handleGetSettings)
	s.mux.HandleFunc("/api/settings/theme", s.requireAdmin(s.handleSetTheme))

	// SCIM 2.0 routes
	s.scim.RegisterRoutes(s.mux)

	// CJK whiteboard fonts (13 MB) stay out of the binary; without a directory they are absent.
	fonts := http.NotFoundHandler()
	if dir := s.config.Server.ExcalidrawFontsDir; dir != "" {
		fonts = http.StripPrefix("/excalidraw/fonts/Xiaolai", http.FileServerFS(os.DirFS(filepath.Join(dir, "Xiaolai"))))
	}
	s.mux.HandleFunc("GET /excalidraw/fonts/Xiaolai/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // no directory listings
			return
		}
		fonts.ServeHTTP(w, r)
	})

	// Unknown API paths are JSON 404s, not the SPA shell.
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, http.StatusNotFound, "Not found")
	})

	// Embedded React PWA Frontend
	s.mux.Handle("/", web.Handler())
}

// requireAdmin rejects requests without a valid session, or with a non-admin one.
func (s *Server) requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _, err := s.sessions.AuthenticateRequest(r)
		if err != nil {
			if errors.Is(err, auth.ErrPasswordChangeRequired) {
				s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Change your password before continuing", "code": "password_change_required"})
			} else {
				s.writeError(w, http.StatusUnauthorized, "Authentication required")
			}
			return
		}
		if user.Role != "admin" {
			s.writeError(w, http.StatusForbidden, "Administrator role required")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), driveUserKey{}, user)))
	}
}

func (s *Server) requireAuthenticated(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/drive/") && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && s.store.Drive() != nil {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if strings.HasPrefix(token, drive.DevicePrefix) {
				if user, ok := s.deviceUser(w, r, token); ok {
					h(w, r.WithContext(context.WithValue(drive.WithDevice(r.Context()), driveUserKey{}, user)))
				}
				return
			}
			id, access, err := s.store.Drive().ServiceToken(r.Context(), token)
			if err != nil {
				s.writeError(w, 401, "Invalid service credential")
				return
			}
			user, err := s.store.Users().GetUserByID(r.Context(), id)
			if err != nil {
				s.writeError(w, 401, "Service account unavailable")
				return
			}
			ctx := drive.WithServiceAccess(r.Context(), access)
			if !s.allowInstallerActor(w, r, user) {
				return
			}
			h(w, r.WithContext(context.WithValue(ctx, driveUserKey{}, user)))
			return
		}
		user, _, err := s.sessions.AuthenticateRequest(r)
		if err != nil {
			if errors.Is(err, auth.ErrPasswordChangeRequired) {
				s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Change your password before continuing", "code": "password_change_required"})
			} else {
				s.writeError(w, http.StatusUnauthorized, "Authentication required")
			}
			return
		}
		if !s.allowInstallerActor(w, r, user) {
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), driveUserKey{}, user)))
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/scim/") {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if guestPath(r.URL.Path) {
		// Guest pages and their API never pass a referrer, are never indexed or cached.
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	if s.config.Security.CookieSecure {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}

	// AI agents authenticate every request themselves and are never browsers: no CORS grant, and
	// a preflight reaches withMCPAuth, which refuses it.
	if r.URL.Path == "/mcp" {
		s.mcp.ServeHTTP(w, r)
		return
	}

	// The only CORS grant KyDrive makes: the app's own origin, reflected, never "*".
	// TestCORSNeverGrantsAnotherOrigin pins that no other code sets these headers.
	origin := r.Header.Get("Origin")
	w.Header().Set("Vary", "Origin")
	if origin != "" && sameOrigin(origin, s.config.Server.AppURL) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, X-KySignOn-Signature")

	// WebDAV authenticates every request itself (app passwords, never cookies) and owns its
	// OPTIONS, methods and body sizes.
	if r.URL.Path == "/dav" || strings.HasPrefix(r.URL.Path, "/dav/") {
		s.serveDAV(w, r)
		return
	}

	if r.Method == http.MethodOptions {
		if origin != "" && !sameOrigin(origin, s.config.Server.AppURL) {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if isUnsafeMethod(r.Method) {
		// The signed directory webhook is server-to-server; every other write must not be cross-site.
		if r.URL.Path != "/api/sso/kysignon/sync" && !s.sameSiteRequest(r) {
			s.writeError(w, http.StatusForbidden, "Cross-site request refused")
			return
		}
		// Login and MFA predate a session; JSON-only keeps HTML forms from posting them.
		if credentialRoute(r.URL.Path) && !isJSON(r) {
			s.writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		if s.sessions.SessionToken(r) != "" && !csrfExempt(r.URL.Path) && !s.sessions.ValidateCSRF(r) {
			s.writeError(w, http.StatusForbidden, "Invalid CSRF token")
			return
		}
	}
	if r.Body != nil {
		limit := int64(1 << 20)
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/drive/workspaces/") && strings.HasSuffix(r.URL.Path, "/uploads") {
			limit = uploadLimit + 1
		}
		if r.Method == "POST" && r.URL.Path == "/api/public/links/upload" {
			limit = uploadLimit + 1
		}
		if r.Method == "PUT" && path.Dir(r.URL.Path) == "/api/drive/upload-sessions" {
			limit = chunkLimit + 1
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}

	// SCIM middleware
	if strings.HasPrefix(r.URL.Path, "/scim/v2") {
		s.scim.AuthMiddleware(s.mux).ServeHTTP(w, r)
		return
	}

	s.mux.ServeHTTP(w, r)
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// credentialRoute is a pre-session sign-in step. Exact paths: MFA enrolment under the same
// prefix is a session route and must keep its CSRF check.
func credentialRoute(path string) bool {
	return path == "/api/auth/login" || path == "/api/auth/mfa/totp" || path == "/api/auth/mfa/recovery-code"
}

// guestPath is a guest link page (/g/) or its public API.
func guestPath(p string) bool {
	return p == "/g" || strings.HasPrefix(p, "/g/") || strings.HasPrefix(p, "/api/public/")
}

// csrfExempt routes never act on a session; /api/public/ and the device token exchange still
// get the same-site check. /api/devices/self takes only a Bearer device token, which a browser
// never sends on its own, so a forged request carries no credential; a stray session cookie
// gets its 401 rather than a CSRF 403.
func csrfExempt(path string) bool {
	return credentialRoute(path) || path == "/api/sso/kysignon/sync" || strings.HasPrefix(path, "/api/public/") || path == "/api/devices/token" || path == "/api/devices/self"
}

func isJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

// sameSiteRequest refuses a write a browser marks as cross-site, or whose Origin is not the
// app's. Non-browser clients send neither header.
func (s *Server) sameSiteRequest(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	// Guest pages send no referrer, so their form posts carry Origin: null; the browser's own
	// Sec-Fetch-Site still proves them same-origin. Sandboxed content reports cross-site.
	if origin == "null" && guestPath(r.URL.Path) && r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		return true
	}
	return origin == "" || sameOrigin(origin, s.config.Server.AppURL)
}

func sameOrigin(origin, appURL string) bool {
	a, err := url.Parse(appURL)
	if err != nil || a.Scheme == "" || a.Host == "" {
		return false
	}
	o, err := url.Parse(origin)
	return err == nil && o.Scheme == a.Scheme && o.Host == a.Host
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("response not written: %v", err)
	}
}

// audit records rec. The action has already happened, so a failed write is logged, not returned.
func (s *Server) audit(ctx context.Context, rec *store.AuditRecord) {
	if err := s.store.Audit().LogAudit(ctx, rec); err != nil {
		log.Printf("audit %s not recorded: %v", rec.Action, err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}
