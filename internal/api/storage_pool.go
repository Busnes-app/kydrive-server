package api

import (
	"context"
	"log"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
)

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
	for after := ""; ; {
		reports, err := d.BeginReports(ctx, full, after)
		if err != nil || len(reports) == 0 {
			return err
		}
		batch := make([]suitedir.StorageReport, len(reports))
		for i, r := range reports {
			batch[i] = r.StorageReport
		}
		results, err := client.ReportStorage(ctx, batch)
		if err != nil {
			return err
		}
		stale, err := d.ApplyReports(ctx, results)
		for _, org := range stale {
			log.Printf("ALERT storage pool: KyIdentity holds a newer report sequence for org %s; it keeps its grant until expiry and is not reported again until kydrive-server storage-seq advances it", org)
		}
		if err != nil || len(reports) < 500 {
			return err
		}
		after = reports[len(reports)-1].OrgID
	}
}
