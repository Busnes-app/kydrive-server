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
