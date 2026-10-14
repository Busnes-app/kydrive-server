package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

const trashDaysSetting = "drive_trash_days"

// trashDays is the global trash purge age in days: unset is 90, 0 is off.
func (s *Server) trashDays(ctx context.Context) (int, error) {
	v, err := s.store.Settings().GetSetting(ctx, trashDaysSetting)
	if errors.Is(err, store.ErrNotFound) {
		return 90, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

func (s *Server) driveTrashRetention(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		var in struct {
			TrashDays *int `json:"trash_days"`
		}
		if decodeDrive(r, &in) != nil || in.TrashDays == nil || *in.TrashDays < 0 || *in.TrashDays > 3650 {
			s.writeError(w, 400, "trash_days must be 0 to 3650")
			return
		}
		if err := s.store.Settings().SetSetting(r.Context(), trashDaysSetting, strconv.Itoa(*in.TrashDays)); err != nil {
			s.driveError(w, err)
			return
		}
		if err := s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: s.driveUser(r).ID, Action: "admin.trash_retention", Resource: "drive", Details: fmt.Sprintf("trash_days=%d", *in.TrashDays), IPAddress: s.requestIP(r)}); err != nil {
			log.Printf("trash retention audit not recorded: %v", err)
		}
	}
	days, err := s.trashDays(r.Context())
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]int{"trash_days": days})
}

// RunRetention applies retention and sweeps orphaned blobs now and then hourly. Blobs are
// removed after their metadata commits; a failed removal leaves an unreferenced file, never a
// dangling version.
func (s *Server) RunRetention(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		days, err := s.trashDays(ctx)
		if err != nil {
			log.Printf("retention: trash purge off, unreadable %s: %v", trashDaysSetting, err)
			days = 0
		}
		blobs, err := s.store.Drive().ApplyRetention(ctx, time.Now(), days)
		if err != nil && ctx.Err() == nil {
			log.Printf("retention: %v", err)
		}
		if err = drive.RemoveBlobs(s.blobRoot(), blobs); err != nil {
			log.Printf("retention blob cleanup: %v", err)
		}
		if n, err := s.store.Drive().SweepOrphans(ctx, s.blobRoot(), time.Hour); err != nil && ctx.Err() == nil {
			log.Printf("orphan sweep: %v", err)
		} else if n > 0 {
			log.Printf("orphan sweep: removed %d files", n)
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
