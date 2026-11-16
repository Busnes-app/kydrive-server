package api

import (
	"context"
	"log"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// suiteGroupsPoll is how often KyIdentity's suite groups are read. A member removed there
// loses the workspace within one poll plus one reconcile pass.
const suiteGroupsPoll = 60 * time.Second

// RunSuiteGroups reconciles KyIdentity's suite groups into workspaces every suiteGroupsPoll
// until ctx ends. cmd/server starts it.
func (s *Server) RunSuiteGroups(ctx context.Context) {
	tick := time.NewTicker(suiteGroupsPoll)
	defer tick.Stop()
	for {
		s.syncSuiteGroups(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// syncSuiteGroups is one pass. Without KySignOn it does nothing; when the list cannot be read
// it changes nothing, so a failed or refused call never archives anything.
func (s *Server) syncSuiteGroups(ctx context.Context) {
	sso := s.config.SSO
	if !sso.Enabled || sso.KySignOnIssuer == "" || sso.KySignOnClientID == "" || sso.KySignOnSecret == "" {
		return
	}
	client := suitedir.Client{Issuer: sso.KySignOnIssuer, ClientID: sso.KySignOnClientID, ClientSecret: sso.KySignOnSecret}
	groups, err := client.Groups(ctx)
	if err != nil {
		// suitedir errors carry neither the secret nor any member subject.
		if ctx.Err() == nil {
			log.Printf("suite groups: %v", err)
		}
		return
	}
	r, err := s.store.Drive().ReconcileSuiteGroups(ctx, groups)
	if err != nil && ctx.Err() == nil {
		log.Printf("suite groups: %v", err) // group and resource IDs only
	}
	if r != (drive.SuiteReport{}) {
		log.Printf("suite groups: created=%d renamed=%d restored=%d archived=%d members_changed=%d", r.Created, r.Renamed, r.Restored, r.Archived, r.MembersChanged)
	}
}
