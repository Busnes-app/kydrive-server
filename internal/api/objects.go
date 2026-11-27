package api

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/Busnes-app/kydrive-server/internal/objects"
)

func (s *Server) openObjects() {
	if !s.config.ObjectStorage.Configured() {
		return
	}
	if !s.multi() {
		s.storageErr = errors.New("S3 object storage requires multi tenancy")
		return
	}
	s.bucket, s.storageErr = objects.NewBucket(s.config.ObjectStorage.S3(), filepath.Join(s.config.Database.DataDir, "object-spool"))
	if s.storageErr == nil {
		s.store.Drive().SetOrgPurger(s.purgeOrgObjects)
	}
}
func (s *Server) StorageConfigurationError() error { return s.storageErr }
func (s *Server) orgPrefix(ctx context.Context, org string) (string, error) {
	return s.store.Drive().ObjectPrefix(ctx, org)
}
func (s *Server) hostedStore(kind string) objects.Store {
	selectOrg := func(org string) (objects.Store, error) {
		prefix, err := s.orgPrefix(context.Background(), org)
		if err != nil {
			return nil, err
		}
		return s.bucket.Store(prefix + kind + "/"), nil
	}
	return objects.ScopedStore{Select: selectOrg, All: func(ctx context.Context) ([]objects.Store, error) {
		orgs, err := s.store.Drive().StorageOrgs(ctx, false)
		if err != nil {
			return nil, err
		}
		var stores []objects.Store
		for _, org := range orgs {
			st, err := selectOrg(org)
			if err != nil {
				return nil, err
			}
			stores = append(stores, st)
		}
		return stores, nil
	}}
}
func (s *Server) hostedTree(kind string) objects.Tree {
	selectOrg := func(org string) (objects.Tree, error) {
		prefix, err := s.orgPrefix(context.Background(), org)
		if err != nil {
			return nil, err
		}
		return s.bucket.Tree(prefix + kind + "/"), nil
	}
	return objects.ScopedTree{Select: selectOrg, All: func(ctx context.Context) ([]objects.Tree, error) {
		orgs, err := s.store.Drive().StorageOrgs(ctx, false)
		if err != nil {
			return nil, err
		}
		var trees []objects.Tree
		for _, org := range orgs {
			tree, err := selectOrg(org)
			if err != nil {
				return nil, err
			}
			trees = append(trees, tree)
		}
		return trees, nil
	}}
}
func (s *Server) purgeOrgObjects(ctx context.Context, org string) error {
	prefix, err := s.orgPrefix(ctx, org)
	if err != nil {
		return err
	}
	s.searchCache.evict(org)
	return s.bucket.PurgePrefix(ctx, prefix)
}
