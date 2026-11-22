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

// syncSuiteGroups is one pass. Without KySignOn it does nothing. A multi-tenant install asks
// per active, enabled org. A list that cannot be read changes nothing in its org, so a failed
// or refused call never archives anything.
func (s *Server) syncSuiteGroups(ctx context.Context) {
	client, ok := s.suiteClient()
	if !ok {
		return
	}
	d := s.store.Drive()
	if !s.multi() {
		s.syncOrgGroups(ctx, drive.DefaultOrg, func() ([]suitedir.Group, error) { return client.Groups(ctx) })
		return
	}
	orgs, err := d.ActiveOrgs(ctx)
	if err != nil {
		log.Printf("suite groups: %v", err)
		return
	}
	for _, org := range orgs {
		if ctx.Err() != nil {
			return
		}
		s.syncOrgGroups(ctx, org, func() ([]suitedir.Group, error) { return client.OrgGroups(ctx, org) })
	}
}

// syncOrgGroups reconciles the groups list returns into org.
func (s *Server) syncOrgGroups(ctx context.Context, org string, list func() ([]suitedir.Group, error)) {
	groups, err := list()
	if err != nil {
		// suitedir errors carry neither the secret nor any member subject.
		if ctx.Err() == nil {
			log.Printf("suite groups: org %s: %v", org, err)
		}
		return
	}
	r, err := s.store.Drive().ReconcileSuiteGroups(ctx, org, groups)
	if err != nil && ctx.Err() == nil {
		log.Printf("suite groups: org %s: %v", org, err) // group and resource IDs only
	}
	if r != (drive.SuiteReport{}) {
		log.Printf("suite groups: org %s: created=%d renamed=%d restored=%d archived=%d members_changed=%d", org, r.Created, r.Renamed, r.Restored, r.Archived, r.MembersChanged)
	}
}
