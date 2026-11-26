package drive

import (
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

// Per-org data keys (D14, multi-tenant only). Each org has one random 32-byte key, stored only
// wrapped under a KEK derived from the deployment key, with the org in the additional data so a
// row copied onto another org fails to open. kek 1 is HKDF(deployment key, "kydrive:org-kek").
// Deleting the row (the purge) destroys everything sealed under the key.

// ErrNoOrgKey means the org has no data key: never recorded, or purged.
var ErrNoOrgKey = errors.New("drive: org has no data key")

// orgKeysDDL is multi-tenant only, run by every multi-tenant open after the schema.
const orgKeysDDL = `CREATE TABLE IF NOT EXISTS drive_org_keys(org_id TEXT PRIMARY KEY REFERENCES drive_orgs(id),kek BIGINT NOT NULL,wrapped TEXT NOT NULL,created TEXT NOT NULL)`

// orgKeyLife bounds how long a replica keeps an unwrapped key: a status change or purge
// applied on another replica reaches this one within it (a poll's interval).
const orgKeyLife = time.Minute

// orgKeyCap bounds the cache; past it, the cache starts over.
const orgKeyCap = 4096

type dataKey struct {
	keys  Keys
	names names
	until time.Time
}

// orgKeys is a replica's cache of unwrapped keys, holding active orgs' only.
type orgKeys struct {
	mu sync.Mutex
	m  map[string]dataKey
}

func (c *orgKeys) get(org string) (dataKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.m[org]
	if ok && time.Now().After(k.until) {
		delete(c.m, org)
		ok = false
	}
	return k, ok
}

func (c *orgKeys) put(org string, k dataKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= orgKeyCap {
		c.m = map[string]dataKey{}
	}
	c.m[org] = k
}

func (c *orgKeys) evict(org string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, org)
}

func (s *Store) kek() ([]byte, error) {
	return hkdf.Key(sha256.New, s.key, nil, "kydrive:org-kek", 32)
}

func orgKeyAAD(kek int64, org string) []byte {
	return fmt.Appendf(nil, "kydrive:org-key:%d:%s", kek, org)
}

// createOrgKey gives org a data key unless it has one or is deleted. It runs in the
// transaction that records the org.
func (s *Store) createOrgKey(ctx context.Context, tx execQueryer, org string) error {
	kek, err := s.kek()
	if err != nil {
		return err
	}
	wrapped, err := crypto.EncryptAESGCMAAD(randomKey(), kek, orgKeyAAD(1, org))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO drive_org_keys(org_id,kek,wrapped,created) SELECT ?,1,?,? WHERE EXISTS(SELECT 1 FROM drive_orgs WHERE id=? AND status<>'deleted') ON CONFLICT(org_id) DO NOTHING`, org, wrapped, now(), org)
	return err
}

func randomKey() []byte {
	k := make([]byte, 32)
	rand.Read(k)
	return k
}

// unwrap is org's unwrapped key and name keys, read through q (the caller's transaction, if
// any). Only an active org's key is cached.
func (s *Store) unwrap(ctx context.Context, q queryer, org string) (dataKey, error) {
	if k, ok := s.orgKeys.get(org); ok {
		return k, nil
	}
	var kek int64
	var wrapped, status string
	err := q.QueryRowContext(ctx, `SELECT k.kek,k.wrapped,o.status FROM drive_org_keys k JOIN drive_orgs o ON o.id=k.org_id WHERE k.org_id=?`, org).Scan(&kek, &wrapped, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return dataKey{}, ErrNoOrgKey
	}
	if err != nil {
		return dataKey{}, err
	}
	if kek != 1 {
		return dataKey{}, fmt.Errorf("drive: org %s key is wrapped under unknown KEK %d", org, kek)
	}
	wrap, err := s.kek()
	if err != nil {
		return dataKey{}, err
	}
	key, err := crypto.DecryptAESGCMAAD(wrapped, wrap, orgKeyAAD(kek, org))
	if err != nil || len(key) != 32 {
		return dataKey{}, fmt.Errorf("drive: org %s key does not unwrap under this deployment key", org)
	}
	n, err := newNames(key)
	if err != nil {
		return dataKey{}, err
	}
	k := dataKey{keys: Keys{key: key, org: org}, names: n, until: time.Now().Add(orgKeyLife)}
	if status == "active" {
		s.orgKeys.put(org, k)
	}
	return k, nil
}

// KeysFor is the Keys that seal workspace's blobs and caches: the deployment key on a
// single-tenant install, the workspace's org key on a multi-tenant one.
func (s *Store) KeysFor(ctx context.Context, workspace string) (Keys, error) {
	return s.keysOf(ctx, s.db, workspaceOrg("?"), workspace)
}

// UserKeys is KeysFor user's org: viewing copies are sealed for their owner's.
func (s *Store) UserKeys(ctx context.Context, user string) (Keys, error) {
	return s.keysOf(ctx, s.db, userOrg("?"), user)
}

// keysOf is the Keys of the org orgSQL (userOrg or workspaceOrg) names for arg.
func (s *Store) keysOf(ctx context.Context, q queryer, orgSQL, arg string) (Keys, error) {
	if !s.Multi() {
		return Deployment(s.key), nil
	}
	k, err := s.orgOf(ctx, q, orgSQL, arg)
	return k.keys, err
}

// namesOf is the name keys of the org orgSQL names for arg.
func (s *Store) namesOf(ctx context.Context, q queryer, orgSQL, arg string) (names, error) {
	if !s.Multi() {
		return s.names, nil
	}
	k, err := s.orgOf(ctx, q, orgSQL, arg)
	return k.names, err
}

func (s *Store) orgOf(ctx context.Context, q queryer, orgSQL, arg string) (dataKey, error) {
	var org sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT `+orgSQL, arg).Scan(&org); err != nil {
		return dataKey{}, err
	}
	if !org.Valid {
		return dataKey{}, ErrNotFound
	}
	return s.unwrap(ctx, q, org.String)
}

// wsNames resolves name keys by workspace for one call, querying each workspace's org once.
// Use it only once a query's rows are closed: it may query q.
type wsNames struct {
	s   *Store
	ctx context.Context
	q   queryer
	m   map[string]names
}

func (s *Store) wsNames(ctx context.Context, q queryer) *wsNames {
	return &wsNames{s: s, ctx: ctx, q: q, m: map[string]names{}}
}

func (w *wsNames) of(workspace string) (names, error) {
	if n, ok := w.m[workspace]; ok {
		return n, nil
	}
	n, err := w.s.namesOf(w.ctx, w.q, workspaceOrg("?"), workspace)
	if err == nil {
		w.m[workspace] = n
	}
	return n, err
}

// open decrypts a stored name of a row in workspace.
func (w *wsNames) open(table, id, workspace, stored string) (string, error) {
	n, err := w.of(workspace)
	if err != nil {
		return "", err
	}
	return n.open(table, id, stored)
}

// sealName seals name for a row of table in workspace (names.seal).
func (s *Store) sealName(ctx context.Context, q queryer, table, id, workspace, name string) (string, string, error) {
	n, err := s.namesOf(ctx, q, workspaceOrg("?"), workspace)
	if err != nil {
		return "", "", err
	}
	return n.seal(table, id, name)
}
