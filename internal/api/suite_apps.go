package api

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
)

// suiteAppsTTL keeps KyIdentity load low; a revoked app may linger in the rail this long.
const suiteAppsTTL = time.Minute

type suiteAppsEntry struct {
	apps    []suitedir.App
	expires time.Time
}

// suiteAppsCache is keyed by local user ID, so one user's list never reaches another.
type suiteAppsCache struct {
	mu sync.Mutex
	m  map[string]suiteAppsEntry
}

// handleSuiteApps lists the suite apps the signed-in directory user may open, for the app
// rail. Local accounts, SSO not configured and any KyIdentity failure answer an empty list.
func (s *Server) handleSuiteApps(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r)
	sso := s.config.SSO
	apps := []suitedir.App{}
	if (user.SSOProvider == "kysignon" || user.SSOProvider == "scim") && user.SSOSubject != "" &&
		sso.Enabled && sso.KySignOnIssuer != "" && sso.KySignOnClientID != "" && sso.KySignOnSecret != "" {
		apps = s.suiteAppsFor(r, user.ID, user.SSOSubject)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
}

func (s *Server) suiteAppsFor(r *http.Request, userID, sub string) []suitedir.App {
	now := s.now()
	c := &s.suiteApps
	c.mu.Lock()
	e, ok := c.m[userID]
	c.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.apps
	}
	sso := s.config.SSO
	client := suitedir.Client{Issuer: sso.KySignOnIssuer, ClientID: sso.KySignOnClientID, ClientSecret: sso.KySignOnSecret}
	apps, err := client.Apps(r.Context(), sub)
	if err != nil {
		// suitedir errors carry neither the secret nor the query holding sub. Failures are
		// not cached, so the rail returns on the next page load.
		log.Printf("suite apps: %v", err)
		return []suitedir.App{}
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = make(map[string]suiteAppsEntry)
	}
	for id, old := range c.m {
		if !now.Before(old.expires) {
			delete(c.m, id)
		}
	}
	c.m[userID] = suiteAppsEntry{apps: apps, expires: now.Add(suiteAppsTTL)}
	c.mu.Unlock()
	return apps
}
