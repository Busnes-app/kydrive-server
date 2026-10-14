package api

import (
	"context"
	"log"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// RunRetention applies workspace retention now and then hourly. Blobs are removed after
// their metadata commits; a failed removal leaves an unreferenced file, never a dangling version.
func (s *Server) RunRetention(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		blobs, err := s.store.Drive().ApplyRetention(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			log.Printf("retention: %v", err)
		}
		if err = drive.RemoveBlobs(s.blobRoot(), blobs); err != nil {
			log.Printf("retention blob cleanup: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// RunBlobSealing encrypts blobs written before sealing existed, now and then hourly. Each
// pass first finishes swaps an earlier run committed but did not clean up.
func (s *Server) RunBlobSealing(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		n, err := s.store.Drive().SealLegacyBlobs(ctx, s.blobRoot(), s.config.Security.EncryptionKey)
		if err != nil && ctx.Err() == nil {
			log.Printf("blob sealing: %v", err)
		}
		if n > 0 {
			log.Printf("blob sealing: sealed %d legacy blobs", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
