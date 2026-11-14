package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// KyPost pairing and its signed routes (docs/KYPOST.md, contract section C). KyDrive only
// receives: KyPost asks for upload tickets, links and status, and uploads with a ticket.

// Event types, one per route.
const (
	kypostEventTicket = "kypost.drive.ticket"
	kypostEventLink   = "kypost.drive.link"
	kypostEventStatus = "kypost.drive.status"
	kypostMaxBody     = 64 << 10
	ticketPath        = "/api/suite/upload/"
)

// Timing of the KyIdentity lookup. Variables so tests can shorten them.
var (
	kypostFirstWait = 5 * time.Second
	kypostRefresh   = time.Hour   // a working link is fetched again this often
	kypostRetry     = time.Minute // first retry after a failure, doubling up to kypostRefresh
	kypostMinGap    = time.Minute // a kick (a refused signature) waits this long after the last lookup
)

// Why there is no KyPost link (kypostView.Reason).
const (
	kypostPending      = "pending"
	kypostNoKyIdentity = "no_kyidentity" // KyIdentity sign-in is not configured
	kypostNoPeer       = "no_peer"       // KyIdentity has not paired KyPost with KyDrive
	kypostUnauthorized = "unauthorized"  // KyIdentity refused KyDrive's client credentials
	kypostUnreachable  = "unreachable"
	kypostInvalidPeer  = "invalid_peer" // KyIdentity's KyPost address breaks the URL rule
)

// Where a link came from.
const (
	kypostSourceEnvironment = "environment"
	kypostSourceKyIdentity  = "kyidentity"
)

// kypostPeer is the paired KyPost. The secret lives only here, in memory.
type kypostPeer struct {
	URL    string
	Secret []byte
	Source string
}

type kypostLink struct {
	peer    atomic.Pointer[kypostPeer]
	kick    chan struct{}
	replay  syncauth.Replay
	mu      sync.Mutex
	reason  string
	checked time.Time
}

func (k *kypostLink) set(p *kypostPeer, reason string) {
	k.peer.Store(p)
	k.mu.Lock()
	k.reason, k.checked = reason, time.Now()
	k.mu.Unlock()
}

func (k *kypostLink) state() (string, time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.reason, k.checked
}

// kickKyPost asks for a lookup soon: KyPost's signature failed and the pair secret may have rotated.
func (s *Server) kickKyPost() {
	select {
	case s.kypost.kick <- struct{}{}:
	default:
	}
}

func (s *Server) newKyPost() {
	s.kypost.kick = make(chan struct{}, 1)
	s.kypost.replay = syncauth.NewMemoryReplay(syncauth.DefaultWindow, 10000)
	s.kypost.reason = kypostPending
	if d := s.config.Drive; d.KyPostURL != "" {
		s.kypost.peer.Store(&kypostPeer{URL: d.KyPostURL, Secret: d.KyPostSecret, Source: kypostSourceEnvironment})
		s.kypost.reason = ""
	}
}

// StartKyPost pairs KyPost through KyIdentity when the environment names none, then keeps the
// link fresh until ctx ends. cmd/server calls it before serving.
func (s *Server) StartKyPost(ctx context.Context) {
	if s.config.Drive.KyPostURL != "" {
		return
	}
	first, cancel := context.WithTimeout(ctx, kypostFirstWait)
	ok := s.resolveKyPost(first)
	cancel()
	go s.kypostLoop(ctx, ok)
}

func (s *Server) kypostLoop(ctx context.Context, ok bool) {
	last, failures := time.Now(), 0
	if !ok {
		failures = 1
	}
	for {
		wait := kypostRefresh
		if failures > 0 {
			wait = min(kypostRetry<<min(failures-1, 16), kypostRefresh)
		}
		next := last.Add(wait)
	sleep:
		for {
			t := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
				break sleep
			case <-s.kypost.kick:
				t.Stop()
				// Refused signatures cannot hammer KyIdentity: no sooner than minGap after the last lookup.
				if soon := last.Add(kypostMinGap); soon.Before(next) {
					next = soon
				}
			}
		}
		last = time.Now()
		if s.resolveKyPost(ctx) {
			failures = 0
		} else {
			failures++
		}
	}
}

// resolveKyPost asks KyIdentity for KyPost and links it, reporting whether the lookup worked.
// A refusal unlinks; an unreachable KyIdentity keeps the current link. The secret is never logged.
func (s *Server) resolveKyPost(ctx context.Context) bool {
	sso := s.config.SSO
	if !sso.Enabled || sso.KySignOnIssuer == "" || sso.KySignOnClientID == "" || sso.KySignOnSecret == "" {
		return s.unlinkKyPost(kypostNoKyIdentity, nil)
	}
	dir := suitedir.Client{Issuer: sso.KySignOnIssuer, ClientID: sso.KySignOnClientID, ClientSecret: sso.KySignOnSecret}
	peer, err := dir.Peer(ctx, "kypost")
	switch {
	case errors.Is(err, suitedir.ErrNoPeer):
		return s.unlinkKyPost(kypostNoPeer, nil)
	case errors.Is(err, suitedir.ErrUnauthorized):
		return s.unlinkKyPost(kypostUnauthorized, err)
	case err != nil:
		if s.kypost.peer.Load() != nil {
			log.Printf("[KYPOST] KyIdentity lookup failed, keeping the current link: %v", err)
			return false
		}
		return s.unlinkKyPost(kypostUnreachable, err)
	}
	u, err := config.KyPostURL(peer.URL)
	if err != nil {
		return s.unlinkKyPost(kypostInvalidPeer, err)
	}
	if cur := s.kypost.peer.Load(); cur == nil || cur.URL != u || string(cur.Secret) != string(peer.Secret) {
		log.Printf("[KYPOST] linked through KyIdentity: %s", u)
	}
	s.kypost.set(&kypostPeer{URL: u, Secret: peer.Secret, Source: kypostSourceKyIdentity}, "")
	return true
}

func (s *Server) unlinkKyPost(reason string, err error) bool {
	if prev, _ := s.kypost.state(); prev != reason {
		if err != nil {
			log.Printf("[KYPOST] not linked through KyIdentity (%s): %v", reason, err)
		} else {
			log.Printf("[KYPOST] not linked through KyIdentity (%s)", reason)
		}
	}
	s.kypost.set(nil, reason)
	return false
}

// kypostEnabled is the user's switch: their stored choice, else on when KyIdentity paired the
// apps and off when the environment did.
func (s *Server) kypostEnabled(ctx context.Context, user string, p *kypostPeer) (bool, error) {
	on, set, err := s.store.Drive().PeerSetting(ctx, user)
	if err != nil || set {
		return on, err
	}
	return p.Source == kypostSourceKyIdentity, nil
}

// kypostReply is a signed route's answer; code set means a refusal.
type kypostReply struct {
	status int
	code   string
	body   any
}

func refuse(status int, code string) kypostReply { return kypostReply{status: status, code: code} }

// kypostUser names who a signed request speaks for.
type kypostUser struct {
	Sub    string `json:"sub"`
	Issuer string `json:"issuer"`
}

// kypostSigned verifies a KyPost request under event and resolves its user before h runs:
// unpaired 404 kypost_off, a bad signature or another event type 401, a verified replay 200
// duplicate, a malformed body 400, no active everyday account 404 no_user, then the user's
// rate limit and, except for status, their switch. Success and refusal are audited by IDs only.
func (s *Server) kypostSigned(event string, limit int, h func(r *http.Request, user *store.User, p *kypostPeer, body []byte) kypostReply) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, id, out := s.kypostVerify(r, event, limit, h)
		if out.status == http.StatusTooManyRequests || out.status >= 500 {
			// Verified, not acted on: KyPost's retry under the same ID must be applied.
			if f, ok := s.kypost.replay.(syncauth.Forgetter); ok && id != "" {
				f.Forget(id)
			}
			if out.status == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "60")
			}
		}
		rec := &store.AuditRecord{UserID: cmp.Or(user, "kypost"), Action: "kypost." + strings.TrimPrefix(event, "kypost.drive."), Resource: "event:" + id, Details: "outcome=ok", IPAddress: s.requestIP(r)}
		if out.code != "" && out.code != "duplicate" {
			rec.Details = "refused=" + out.code
			out.body = map[string]string{"error": http.StatusText(out.status), "code": out.code}
		}
		// Only verified events are audited: an unsigned caller must not fill the log.
		if id != "" && out.code != "duplicate" {
			s.audit(r.Context(), rec)
		}
		s.writeJSON(w, out.status, out.body)
	}
}

func (s *Server) kypostVerify(r *http.Request, event string, limit int, h func(*http.Request, *store.User, *kypostPeer, []byte) kypostReply) (string, string, kypostReply) {
	if !s.allowPerIP(r, "kypost-signed", 600, time.Minute) {
		return "", "", refuse(429, "rate_limited")
	}
	p := s.kypost.peer.Load()
	if p == nil {
		return "", "", refuse(404, "kypost_off")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, kypostMaxBody+1))
	hd := syncauth.FromRequest(r)
	if err != nil || len(body) > kypostMaxBody || hd.EventType != event {
		return "", "", refuse(401, "bad_signature")
	}
	if _, err = syncauth.Verify(p.Secret, hd, body, syncauth.Options{Replay: s.kypost.replay}); err != nil {
		if errors.Is(err, syncauth.ErrReplay) {
			return "", hd.EventID, kypostReply{status: 200, code: "duplicate", body: map[string]string{"outcome": "duplicate"}}
		}
		if p.Source == kypostSourceKyIdentity && errors.Is(err, syncauth.ErrBadSignature) {
			s.kickKyPost()
		}
		return "", "", refuse(401, "bad_signature")
	}
	var who kypostUser
	if json.Unmarshal(body, &who) != nil || who.Sub == "" || who.Issuer == "" {
		return "", hd.EventID, refuse(400, "bad_body")
	}
	user, err := s.kypostAccount(r.Context(), who)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", hd.EventID, refuse(404, "no_user")
		}
		log.Printf("kypost %s: account lookup: %v", event, err)
		return "", hd.EventID, refuse(503, "unavailable")
	}
	if !s.allowAttempt("kypost:"+event+":"+user.ID, limit, time.Minute) {
		return user.ID, hd.EventID, refuse(429, "rate_limited")
	}
	if event != kypostEventStatus {
		on, err := s.kypostEnabled(r.Context(), user.ID, p)
		if err != nil {
			log.Printf("kypost %s: switch: %v", event, err)
			return user.ID, hd.EventID, refuse(503, "unavailable")
		}
		if !on {
			return user.ID, hd.EventID, refuse(409, "sharing_off")
		}
	}
	out := h(r.WithContext(drive.WithPeer(r.Context(), p.Source == kypostSourceKyIdentity)), user, p, body)
	return user.ID, hd.EventID, out
}

// kypostAccount is the active everyday account linked to the subject at this KyDrive's
// KyIdentity issuer. Dedicated installer administrators are not everyday accounts.
func (s *Server) kypostAccount(ctx context.Context, who kypostUser) (*store.User, error) {
	if strings.TrimSuffix(who.Issuer, "/") != strings.TrimSuffix(s.config.SSO.KySignOnIssuer, "/") || s.config.SSO.KySignOnIssuer == "" {
		return nil, store.ErrNotFound
	}
	user, err := store.DirectorySubject(ctx, s.store.Users(), who.Sub)
	if err != nil {
		return nil, err
	}
	if user.Status != "active" {
		return nil, store.ErrNotFound
	}
	dedicated, err := s.store.InstallerAdministrator(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if dedicated {
		return nil, store.ErrNotFound
	}
	return user, nil
}

func (s *Server) kypostTicket(r *http.Request, user *store.User, _ *kypostPeer, body []byte) kypostReply {
	var in struct {
		Name string `json:"name"`
		Size *int64 `json:"size"`
	}
	if json.Unmarshal(body, &in) != nil || in.Size == nil {
		return refuse(400, "bad_body")
	}
	token, t, err := s.store.Drive().IssueTicket(r.Context(), user.ID, in.Name, *in.Size, s.config.Drive.MaxFileSize)
	switch {
	case errors.Is(err, drive.ErrPeerOff):
		return refuse(409, "sharing_off")
	case errors.Is(err, drive.ErrInvalid):
		return refuse(400, "bad_body")
	case errors.Is(err, drive.ErrTooLarge):
		return refuse(413, "too_large")
	case errors.Is(err, drive.ErrQuota):
		return refuse(507, "quota")
	case errors.Is(err, drive.ErrTicketLimit):
		return refuse(429, "too_many_tickets")
	case errors.Is(err, drive.ErrDenied):
		return refuse(404, "no_user")
	case err != nil:
		log.Printf("kypost ticket: %v", err)
		return refuse(503, "unavailable")
	}
	return kypostReply{status: 201, body: map[string]any{
		"upload_url": s.config.Server.AppURL + ticketPath + token,
		"expires":    time.Unix(t.Expires, 0).UTC().Format(time.RFC3339),
		"max_chunk":  chunkLimit,
	}}
}

func (s *Server) kypostLinkRoute(r *http.Request, user *store.User, _ *kypostPeer, body []byte) kypostReply {
	var in struct {
		FileID string `json:"file_id"`
		Days   *int   `json:"days"`
	}
	if json.Unmarshal(body, &in) != nil || in.FileID == "" {
		return refuse(400, "bad_body")
	}
	days := 30
	if in.Days != nil {
		days = *in.Days
	}
	var expires int64
	switch days {
	case 0:
	case 1, 7, 30:
		expires = s.now().Add(time.Duration(days) * 24 * time.Hour).Unix()
	default:
		return refuse(400, "bad_body")
	}
	_, token, err := s.store.Drive().CreateLink(r.Context(), user.ID, "file", in.FileID, "reader", expires)
	switch {
	case errors.Is(err, drive.ErrPeerOff):
		return refuse(409, "sharing_off")
	case errors.Is(err, drive.ErrDenied), errors.Is(err, drive.ErrInvalid), errors.Is(err, drive.ErrConflict), errors.Is(err, drive.ErrNotFound):
		return refuse(404, "not_found")
	case err != nil:
		log.Printf("kypost link: %v", err)
		return refuse(503, "unavailable")
	}
	out := map[string]any{"url": s.config.Server.AppURL + "/l/#" + token, "expires": nil}
	if expires != 0 {
		out["expires"] = time.Unix(expires, 0).UTC().Format(time.RFC3339)
	}
	return kypostReply{status: 201, body: out}
}

func (s *Server) kypostStatusRoute(r *http.Request, user *store.User, p *kypostPeer, _ []byte) kypostReply {
	on, err := s.kypostEnabled(r.Context(), user.ID, p)
	var free int64
	if err == nil && on {
		free, err = s.store.Drive().PeerFree(r.Context(), user.ID)
	}
	if err != nil {
		log.Printf("kypost status: %v", err)
		return refuse(503, "unavailable")
	}
	return kypostReply{status: 200, body: map[string]any{"enabled": on, "free_bytes": free, "max_file_bytes": s.config.Drive.MaxFileSize}}
}

// ticketRefuse answers a ticket upload refusal; nothing about the ticket reaches a log.
func (s *Server) ticketRefuse(w http.ResponseWriter, status int, code string) {
	s.writeJSON(w, status, map[string]string{"error": http.StatusText(status), "code": code})
}

// handleTicketUpload takes the bytes of one ticket: the whole file, or Content-Range chunks of
// at most chunkLimit in order. The ticket in the path is the only credential; ServeHTTP has
// already answered CORS for the paired KyPost origin alone.
func (s *Server) handleTicketUpload(w http.ResponseWriter, r *http.Request, p *kypostPeer) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT, OPTIONS")
		s.ticketRefuse(w, http.StatusMethodNotAllowed, "bad_method")
		return
	}
	if !s.allowPerIP(r, "ticket-upload", 240, time.Minute) {
		w.Header().Set("Retry-After", "60")
		s.ticketRefuse(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	if p == nil {
		s.ticketRefuse(w, http.StatusNotFound, "kypost_off")
		return
	}
	token := strings.TrimPrefix(r.URL.Path, ticketPath)
	if len(token) != 43 || strings.Trim(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		s.ticketRefuse(w, http.StatusNotFound, "not_found")
		return
	}
	d := s.store.Drive()
	t, err := d.Ticket(r.Context(), token)
	if errors.Is(err, drive.ErrNotFound) {
		s.ticketRefuse(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		log.Printf("ticket upload: lookup: %v", err)
		s.ticketRefuse(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if !s.allowAttempt("ticket-upload:"+t.Owner, 600, time.Minute) {
		w.Header().Set("Retry-After", "60")
		s.ticketRefuse(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	if on, err := s.kypostEnabled(r.Context(), t.Owner, p); err != nil || !on {
		s.ticketRefuse(w, http.StatusConflict, "sharing_off")
		return
	}
	start, length := int64(0), t.Size
	if h := r.Header.Get("Content-Range"); h != "" {
		first, last, total, ok := contentRange(h)
		if !ok || total != t.Size {
			s.ticketRefuse(w, http.StatusBadRequest, "bad_range")
			return
		}
		start, length = first, last-first+1
	}
	if length > chunkLimit {
		s.ticketRefuse(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	if r.ContentLength != length {
		s.ticketRefuse(w, http.StatusBadRequest, "size_mismatch")
		return
	}
	if start+length == t.Size {
		// The last bytes re-seal the whole file, which can outlast the write timeout.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	}
	received, f, err := d.TicketUpload(drive.WithPeer(r.Context(), p.Source == kypostSourceKyIdentity), s.stagingRoot(), s.blobRoot(), s.config.Security.EncryptionKey, t, start, length, http.MaxBytesReader(w, r.Body, chunkLimit+1))
	switch {
	case errors.Is(err, drive.ErrPeerOff):
		s.ticketRefuse(w, http.StatusConflict, "sharing_off")
	case errors.Is(err, drive.ErrConflict):
		w.Header().Set("Upload-Offset", strconv.FormatInt(received, 10))
		s.ticketRefuse(w, http.StatusConflict, "conflict")
	case errors.Is(err, drive.ErrInvalid):
		s.ticketRefuse(w, http.StatusBadRequest, "size_mismatch")
	case errors.Is(err, drive.ErrQuota):
		s.ticketRefuse(w, http.StatusInsufficientStorage, "quota")
	case errors.Is(err, drive.ErrDenied):
		s.ticketRefuse(w, http.StatusNotFound, "not_found")
	case err != nil:
		log.Printf("ticket upload %s: %v", t.ID, err)
		s.ticketRefuse(w, http.StatusServiceUnavailable, "unavailable")
	case f == nil:
		w.Header().Set("Upload-Offset", strconv.FormatInt(received, 10))
		w.WriteHeader(http.StatusNoContent)
	default:
		s.audit(r.Context(), &store.AuditRecord{UserID: t.Owner, Action: "kypost.file_saved", Resource: "file:" + f.ID, Details: "ticket=" + t.ID, IPAddress: s.requestIP(r)})
		s.writeJSON(w, http.StatusCreated, map[string]any{"file_id": f.ID, "name": f.Name, "size": f.Size})
	}
}

// kypostView is the admin screen's KyPost status. It never carries the secret.
type kypostView struct {
	Connected bool       `json:"connected"`
	Source    string     `json:"source,omitempty"`
	URL       string     `json:"url,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

func (s *Server) handleKyPostStatus(w http.ResponseWriter, r *http.Request) {
	var v kypostView
	reason, checked := s.kypost.state()
	if !checked.IsZero() {
		v.CheckedAt = &checked
	}
	if p := s.kypost.peer.Load(); p != nil {
		v.Connected, v.Source, v.URL = true, p.Source, p.URL
	} else {
		v.Reason = reason
	}
	s.writeJSON(w, http.StatusOK, v)
}

// handleKyPostSetting reads or sets the signed-in user's switch. paired false hides it.
func (s *Server) handleKyPostSetting(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r)
	if r.Method == http.MethodPut {
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if decodeDrive(r, &in) != nil || in.Enabled == nil {
			s.driveError(w, drive.ErrInvalid)
			return
		}
		if err := s.store.Drive().SetPeerSetting(r.Context(), user.ID, *in.Enabled); err != nil {
			s.driveError(w, err)
			return
		}
		s.audit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "kypost.switch", Resource: "user:" + user.ID, Details: "enabled=" + map[bool]string{true: "true", false: "false"}[*in.Enabled], IPAddress: s.requestIP(r)})
	}
	p := s.kypost.peer.Load()
	out := map[string]bool{"paired": p != nil, "enabled": false}
	if p != nil {
		on, err := s.kypostEnabled(r.Context(), user.ID, p)
		if err != nil {
			s.driveError(w, err)
			return
		}
		out["enabled"] = on
	}
	s.writeJSON(w, http.StatusOK, out)
}
