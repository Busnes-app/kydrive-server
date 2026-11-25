package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
)

// reportableOrg is the org ID form POST /api/suite/storage accepts. KyIdentity's org IDs
// always are; any other registry row would poison its batch.
var reportableOrg = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// storagePoolTick is how often due and unanswered storage reports are sent; every
// storageFullEvery ticks (about 60 s) every org is checked for changed usage and renewal.
const (
	storagePoolTick  = 10 * time.Second
	storageFullEvery = 6
)

// RunStoragePool reports each org's drive bytes to KyIdentity and records its grant
// (POST /api/suite/storage, tenancy design "Quotas") until ctx ends. cmd/server runs it on the
// elected worker; a single-tenant install has no pool.
func (s *Server) RunStoragePool(ctx context.Context) {
	if !s.multi() {
		return
	}
	tick := time.NewTicker(storagePoolTick)
	defer tick.Stop()
	for i := 0; ; i++ {
		if err := s.reportStorage(ctx, i%storageFullEvery == 0); err != nil && ctx.Err() == nil {
			log.Printf("storage pool: %v", err) // suitedir errors carry no secret
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// reportStorage sends the frozen reports in batches and applies each answer. A failed request
// changes nothing: its reports stay unanswered and are sent again unchanged.
func (s *Server) reportStorage(ctx context.Context, full bool) error {
	client, ok := s.suiteClient()
	if !ok {
		return errNoSuite
	}
	d := s.store.Drive()
	var errs []error
	for after := ""; ; {
		reports, err := d.BeginReports(ctx, full, after)
		if err != nil {
			errs = append(errs, err) // some orgs failed; the frozen reports still go
		}
		if len(reports) == 0 {
			return errors.Join(errs...)
		}
		batch := make([]suitedir.StorageReport, 0, len(reports))
		for _, r := range reports {
			if !reportableOrg.MatchString(r.OrgID) { // KyIdentity refuses a whole batch for one
				errs = append(errs, fmt.Errorf("org %s: not a lowercase UUID; not reported", r.OrgID))
				continue
			}
			batch = append(batch, r.StorageReport)
		}
		if len(batch) > 0 {
			results, err := client.ReportStorage(ctx, batch)
			if err != nil {
				return errors.Join(append(errs, err)...)
			}
			stale, err := d.ApplyReports(ctx, results)
			for _, org := range stale {
				log.Printf("ALERT storage pool: KyIdentity holds a newer report sequence for org %s; it keeps its grant until expiry and is not reported again until kydrive-server storage-seq advances it", org)
			}
			if err != nil {
				return errors.Join(append(errs, err)...)
			}
		}
		if len(reports) < 500 {
			return errors.Join(errs...)
		}
		after = reports[len(reports)-1].OrgID
	}
}
