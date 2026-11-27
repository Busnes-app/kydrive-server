package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/search"
	"github.com/google/uuid"
)

// Cache only immutable ciphertext. Readers deserialize their own in-memory database, so
// searches, the elected writer and different orgs never mutate one SQLite connection.
const maxSearchObject = 48 << 20
const maxSearchCache = 64 << 20

type cachedSearch struct {
	object  string
	version int64
	sealed  string
}
type sealedSearchCache struct {
	mu      sync.Mutex
	entries map[string]cachedSearch
	bytes   int
}

func (c *sealedSearchCache) get(org string, p drive.SearchPointer) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[org]
	return e.sealed, ok && e.object == p.Object && e.version == p.Version
}
func (c *sealedSearchCache) put(org string, p drive.SearchPointer, sealed string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cachedSearch{}
	}
	if old, ok := c.entries[org]; ok {
		c.bytes -= len(old.sealed)
		delete(c.entries, org)
	}
	for c.bytes+len(sealed) > maxSearchCache {
		for id, e := range c.entries {
			c.bytes -= len(e.sealed)
			delete(c.entries, id)
			break
		}
	}
	c.entries[org] = cachedSearch{p.Object, p.Version, sealed}
	c.bytes += len(sealed)
}
func (c *sealedSearchCache) evict(org string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[org]; ok {
		c.bytes -= len(e.sealed)
		delete(c.entries, org)
	}
}
func searchAAD(org, object string, version int64) []byte {
	return fmt.Appendf(nil, "kydrive:search-index:v1:%s:%s:%d", org, object, version)
}
func (s *Server) hostedSearch() bool { return s.bucket != nil && s.config.Drive.ContentSearch }
func (s *Server) withHostedIndex(ctx context.Context, org string, fn func(*search.Index, drive.SearchPointer, drive.Keys) error) error {
	select {
	case s.searchSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.searchSlots }()
	keys, err := s.store.Drive().OrgKeys(ctx, org)
	if err != nil {
		return err
	}
	key, err := keys.SearchKey()
	if err != nil {
		return err
	}
	p, err := s.store.Drive().SearchPointer(ctx, org)
	if err != nil {
		return err
	}
	var snapshot []byte
	if p.Object != "" {
		sealed, ok := s.searchCache.get(org, p)
		if !ok {
			prefix, err := s.orgPrefix(ctx, org)
			if err != nil {
				return err
			}
			obj, err := s.bucket.Store(prefix+"search/").Open(ctx, p.Object)
			if err != nil {
				return err
			}
			if obj.Size() > maxSearchObject {
				obj.Close()
				return errors.New("search snapshot exceeds size limit")
			}
			raw, err := io.ReadAll(io.NewSectionReader(obj, 0, obj.Size()))
			obj.Close()
			if err != nil {
				return err
			}
			sealed = string(raw)
			// Authenticate before a remote object enters the cache.
		}
		snapshot, err = crypto.DecryptAESGCMAAD(sealed, key, searchAAD(org, p.Object, p.Version))
		if err != nil {
			return errors.New("search snapshot failed authentication")
		}
		if len(snapshot) > search.MaxSnapshot {
			return errors.New("search snapshot exceeds expanded size limit")
		}
		if !ok {
			s.searchCache.put(org, p, sealed)
		}
	}
	x, err := search.OpenMemory(ctx, key, snapshot)
	if err != nil {
		return err
	}
	defer x.Close()
	return fn(x, p, keys)
}
func (s *Server) runHostedSearch(ctx context.Context) {
	for {
		if err := s.indexHostedOrgs(ctx, false); err != nil && ctx.Err() == nil {
			log.Printf("org search index: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(indexPoll):
		}
	}
}
func (s *Server) indexHostedOrgs(ctx context.Context, force bool) error {
	orgs, err := s.store.Drive().ActiveOrgs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, org := range orgs {
		home, e := s.store.Drive().OrgHomeRegion(ctx, org)
		if e != nil {
			return e
		}
		if home == "" && org == s.config.Database.StaffOrg {
			continue
		}
		for {
			count := 0
			err := s.withHostedIndex(ctx, org, func(x *search.Index, p drive.SearchPointer, keys drive.Keys) error {
				w := &searchWorker{Server: s, search: x, org: org, keys: keys}
				scoped := drive.WithOrg(ctx, org)
				if err := w.tailSearch(scoped, time.Now()); err != nil {
					return err
				}
				if err := w.reconcileSearch(scoped, time.Now()); err != nil {
					return err
				}
				if err := w.drainSearch(scoped, force, !force); err != nil {
					return err
				}
				count = len(w.done)
				if count == 0 && !w.dirty && p.Object != "" {
					return nil
				}
				snapshot, err := x.Snapshot(ctx)
				if err != nil {
					return err
				}
				if len(snapshot) > search.MaxSnapshot {
					return errors.New("search snapshot exceeds size limit")
				}
				key, err := keys.SearchKey()
				if err != nil {
					return err
				}
				object := uuid.NewString()
				sealed, err := crypto.EncryptAESGCMAAD(snapshot, key, searchAAD(org, object, p.Version+1))
				if err != nil {
					return err
				}
				prefix, err := s.orgPrefix(ctx, org)
				if err != nil {
					return err
				}
				st := s.bucket.Store(prefix + "search/")
				draft, err := st.Create(ctx)
				if err != nil {
					return err
				}
				defer draft.Discard()
				if _, err = draft.Write([]byte(sealed)); err != nil {
					return err
				}
				if err = draft.Publish(object); err != nil {
					return err
				}
				if err = s.store.Drive().PublishSearch(ctx, org, p, object, w.done); err != nil {
					cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
					defer cancel()
					return errors.Join(err, st.Remove(cleanup, object))
				}
				s.searchCache.put(org, drive.SearchPointer{Object: object, Version: p.Version + 1}, sealed)
				if p.Object != "" {
					if err := st.Remove(ctx, p.Object); err != nil {
						log.Printf("search old snapshot cleanup: %v", err)
					}
				}
				return nil
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("org %s: %w", org, err))
				break
			}
			if !force || count == 0 {
				break
			}
		}
	}
	return errors.Join(errs...)
}
func (s *searchWorker) cursor(ctx context.Context) (int64, error) {
	if s.org == "" {
		return s.search.Cursor(ctx)
	}
	p, err := s.store.Drive().SearchPointer(ctx, s.org)
	return p.Cursor, err
}
func (s *searchWorker) enqueue(ctx context.Context, files []string, now time.Time, delay time.Duration, cursor int64) error {
	if s.org == "" {
		return s.search.Enqueue(ctx, files, now, delay, cursor)
	}
	return s.store.Drive().SearchEnqueue(ctx, s.org, files, now, delay, cursor)
}
func (s *searchWorker) due(ctx context.Context, now time.Time, force bool, limit int) ([]string, error) {
	if s.org == "" {
		return s.search.Due(ctx, now, force, limit)
	}
	work, err := s.store.Drive().SearchDue(ctx, s.org, now, force, limit)
	s.done = work
	var files []string
	for _, w := range work {
		files = append(files, w.File)
	}
	return files, err
}
func (s *searchWorker) dequeue(ctx context.Context, file string) error {
	if s.org == "" {
		return s.search.Dequeue(ctx, file)
	}
	return nil // acknowledged together with the published snapshot, by generation
}
func (s *Server) enqueueSnippet(ctx context.Context, org, file string) error {
	if s.hostedSearch() {
		return s.store.Drive().SearchEnqueue(ctx, org, []string{file}, time.Now(), 0, -1)
	}
	return s.search.Enqueue(ctx, []string{file}, time.Now(), 0, -1)
}
