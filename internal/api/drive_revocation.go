package api

import (
	"context"
	"log"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// EditorConfigurationError is checked by the CLI before serving requests.
func (s *Server) EditorConfigurationError() error { return s.editorErr }
func (s *Server) revokeEditors(ctx context.Context) {
	if s.editor == nil || s.store.Drive() == nil {
		return
	}
	// One process sends commands; durable rows survive restarts and failed attempts.
	if !s.revokeMu.TryLock() {
		return
	}
	defer s.revokeMu.Unlock()
	pending, err := s.store.Drive().PendingRevocations(ctx)
	if err != nil {
		log.Printf("editor revocation scan failed: %v", err)
		return
	}
	for _, e := range pending {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.editor.Drop(attempt, e.Key, e.User)
		cancel()
		if err != nil {
			log.Printf("editor revocation pending session=%s: %v", e.ID, err)
			if err = s.store.Drive().DeferRevocation(ctx, e.ID); err != nil {
				log.Printf("editor revocation backoff failed session=%s: %v", e.ID, err)
			}
			continue
		}
		if err = s.store.Drive().MarkDropped(ctx, e.ID); err != nil {
			log.Printf("editor revocation receipt failed session=%s: %v", e.ID, err)
		}
	}
	views, err := s.store.Drive().PendingViewDrops(ctx)
	if err != nil {
		log.Printf("viewing copy revocation scan failed: %v", err)
		return
	}
	for _, c := range views {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.editor.Drop(attempt, drive.ViewKeyPrefix+c.ID, c.Owner)
		cancel()
		if err != nil {
			log.Printf("viewing copy revocation pending view=%s: %v", c.ID, err)
		}
		if err = s.store.Drive().MarkViewDropped(ctx, c.ID, err == nil); err != nil {
			log.Printf("viewing copy revocation receipt failed view=%s: %v", c.ID, err)
		}
	}
}
func (s *Server) RunEditorRevocations(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		s.revokeEditors(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
