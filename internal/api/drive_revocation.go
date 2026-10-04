package api

import (
	"context"
	"log"
	"time"
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
			continue
		}
		if err = s.store.Drive().MarkDropped(ctx, e.ID); err != nil {
			log.Printf("editor revocation receipt failed session=%s: %v", e.ID, err)
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
