package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// suiteOrgsPoll is how often a multi-tenant KyIdentity's orgs are read. A suspension there stops
// KyDrive serving the org within one poll.
const suiteOrgsPoll = 60 * time.Second

// orgsAlertAfter is how long org polls may fail before each failure is logged as an alert
// (question 4). drive.OrgsMaxAge later stops serving the orgs.
const orgsAlertAfter = 10 * time.Minute

// suiteClient is KyIdentity's suite directory, or false without KySignOn.
func (s *Server) suiteClient() (suitedir.Client, bool) {
	sso := s.config.SSO
	if !sso.Enabled || sso.KySignOnIssuer == "" || sso.KySignOnClientID == "" || sso.KySignOnSecret == "" {
		return suitedir.Client{}, false
	}
	return suitedir.Client{Issuer: sso.KySignOnIssuer, ClientID: sso.KySignOnClientID, ClientSecret: sso.KySignOnSecret}, true
}

// multi reports whether this install is multi-tenant.
func (s *Server) multi() bool {
	d := s.store.Drive()
	return d != nil && d.Multi()
}

// RunSuiteOrgs mirrors KyIdentity's orgs into the registry every suiteOrgsPoll until ctx ends.
// cmd/server starts it; a single-tenant install has no orgs to read.
func (s *Server) RunSuiteOrgs(ctx context.Context) {
	if !s.multi() {
		return
	}
	tick := time.NewTicker(suiteOrgsPoll)
	defer tick.Stop()
	started := time.Now()
	for {
		if err := s.pollOrgs(ctx); err != nil && ctx.Err() == nil {
			s.orgsMu.Lock()
			last := s.orgsOK
			s.orgsMu.Unlock()
			if last.IsZero() {
				last = started
			}
			if failing := time.Since(last); failing > orgsAlertAfter {
				log.Printf("ALERT suite orgs: no successful poll for %s; orgs stop being served %s after the last one: %v", failing.Round(time.Second), drive.OrgsMaxAge, err)
			} else {
				log.Printf("suite orgs: %v", err) // suitedir errors carry no secret
			}
		}
		s.purgeLifecycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

var errNoSuite = errors.New("KySignOn is not configured")

// purgeLifecycle purges the orgs recorded deleted and carries out recorded erasures, then
// removes the blobs they left. It runs after every org poll, failed ones included, since both
// act on what the registry already holds.
func (s *Server) purgeLifecycle(ctx context.Context) {
	d := s.store.Drive()
	if s.bucket != nil {
		orgs, err := d.StorageOrgs(ctx, true)
		if err != nil {
			log.Printf("org object cleanup: %v", err)
		}
		for _, org := range orgs {
			if err := s.purgeOrgObjects(ctx, org); err != nil {
				log.Printf("org %s object cleanup: %v", org, err)
			}
		}
	}
	for _, p := range []struct {
		what  string
		purge func(context.Context) ([]string, []string, error)
	}{{"org purge", d.PurgeOrgs}, {"member erasure", d.EraseMembers}} {
		what := p.what
		blobs, views, err := p.purge(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("%s: %v", what, err)
		}
		if err := drive.RemoveBlobs(s.blobRoot(), blobs); err != nil {
			log.Printf("%s blob cleanup: %v", what, err)
		}
		if err := drive.RemoveBlobs(s.viewRoot(), views); err != nil {
			log.Printf("%s viewing copy cleanup: %v", what, err)
		}
	}
}

// pollOrgs applies KyIdentity's erasures, then the orgs changed since the last poll. A failed
// call changes nothing. Erasures come first, so a fresh org mirror means they are applied.
func (s *Server) pollOrgs(ctx context.Context) error {
	client, ok := s.suiteClient()
	if !ok {
		return errNoSuite
	}
	s.orgsMu.Lock()
	defer s.orgsMu.Unlock()
	if err := s.applyErased(ctx, &client); err != nil {
		return err
	}
	d := s.store.Drive()
	since, err := d.OrgsSince(ctx)
	if err != nil {
		return err
	}
	orgs, next, err := client.Orgs(ctx, since)
	if err != nil {
		return err
	}
	staffNotActive, err := d.ApplyOrgs(ctx, orgs, next)
	if err == nil {
		s.orgsOK = time.Now()
	}
	if staffNotActive {
		log.Printf("ALERT suite orgs: KyIdentity lists the staff org as not active; KyDrive keeps it active")
	}
	return err
}

// applyErased applies the erasures KyIdentity listed after the last read (all of them after a
// restore, which deletes store.ErasedSince).
func (s *Server) applyErased(ctx context.Context, client *suitedir.Client) error {
	since := int64(0)
	v, err := s.store.Settings().GetSetting(ctx, store.ErasedSince)
	switch {
	case err == nil:
		if since, err = strconv.ParseInt(v, 10, 64); err != nil {
			return fmt.Errorf("%s: %w", store.ErasedSince, err)
		}
	case !errors.Is(err, store.ErrNotFound):
		return err
	}
	subjects, next, err := client.Erased(ctx, since)
	if err != nil {
		return err
	}
	for _, e := range subjects {
		err := s.store.Users().EraseDirectoryUser(ctx, e.Sub, e.OrgID, e.Revision)
		if errors.Is(err, store.ErrOtherOrg) {
			log.Printf("ALERT suite erased: an erasure at revision %d names an account in another org; not applied", e.Revision)
			continue
		}
		if err != nil {
			return err
		}
	}
	if next == since {
		return nil
	}
	return s.store.Settings().SetSetting(ctx, store.ErasedSince, strconv.FormatInt(next, 10))
}

// orgActive reports whether org is registered, active and enabled, polling once for an org not
// yet registered (a sign-in can arrive before the poll that lists a new org).
func (s *Server) orgActive(ctx context.Context, org string) (bool, error) {
	d := s.store.Drive()
	known, active, err := d.OrgActive(ctx, org)
	if err != nil || known {
		return active, err
	}
	if err := s.pollOrgs(ctx); err != nil {
		log.Printf("suite orgs: %v", err)
	}
	_, active, err = d.OrgActive(ctx, org)
	return active, err
}
