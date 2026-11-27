package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/search"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

type hostedWorld struct {
	s                       *Server
	st                      store.Store
	db                      *sql.DB
	cfg                     *config.Config
	orgs, users, workspaces []string
	cookies                 []*http.Cookie
}

func newHostedWorld(t *testing.T) *hostedWorld {
	t.Helper()
	endpoint := os.Getenv("KY_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires KY_TEST_S3_* (CI postgres job)")
	}
	ctx := context.Background()
	cfg := &config.Config{Database: testdb.Config(t)}
	cfg.Database.DataDir = t.TempDir()
	cfg.Database.Tenancy = "multi"
	cfg.Database.StaffOrg = uuid.NewString()
	cfg.Database.Cells = []string{"e2e-1", "e2e-2"}
	cfg.Security.EncryptionKey = testdb.Key
	cfg.Security.SessionSecret = strings.Repeat("s", 32)
	cfg.Server.AppURL = "https://drive.e2e.test"
	cfg.SSO.KySignOnIssuer = "https://identity.e2e.test"
	cfg.Drive.ContentSearch = true
	cfg.Drive.MaxFileSize = 8 << 20
	cfg.Drive.MaxZipSize = 8 << 20
	cfg.ObjectStorage = config.ObjectStorageConfig{Endpoint: endpoint, Region: "us-east-1", Bucket: os.Getenv("KY_TEST_S3_BUCKET"), AccessKey: os.Getenv("KY_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("KY_TEST_S3_SECRET_KEY"), PathStyle: true}
	client := s3.New(s3.Options{BaseEndpoint: aws.String(endpoint), Region: "us-east-1", UsePathStyle: true, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: cfg.ObjectStorage.AccessKey, SecretAccessKey: cfg.ObjectStorage.SecretKey}, nil
	})})
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.ObjectStorage.Bucket)})
	if err != nil && !strings.Contains(err.Error(), "BucketAlready") {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, cfg.Database, cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Drive().ConfigureCells(ctx); err != nil {
		t.Fatal(err)
	}
	w := &hostedWorld{st: st, cfg: cfg, db: testdb.Open(t, cfg.Database)}
	for i, home := range []string{"e2e-1", "e2e-2", "e2e-1"} {
		org := uuid.NewString()
		w.orgs = append(w.orgs, org)
		if _, err := st.Drive().ApplyOrgs(ctx, []suitedir.Org{{ID: org, HomeRegion: home, Status: suitedir.OrgActive, Enabled: true, Revision: int64(i + 1)}}, int64(i+1)); err != nil {
			t.Fatal(err)
		}
		if _, err := w.db.Exec(`UPDATE drive_orgs SET grant_ceiling=1099511627776,grant_expires=99999999999 WHERE id=?`, org); err != nil {
			t.Fatal(err)
		}
		u, err := st.Users().ApplyDirectoryUser(ctx, &store.DirectoryUser{Provider: "kysignon", Subject: "Exact:Subject:" + org, Username: "member" + string(rune('a'+i)), Active: true, OrgID: org})
		if err != nil {
			t.Fatal(err)
		}
		w.users = append(w.users, u.ID)
		workspace, err := st.Drive().EnsurePersonalWorkspace(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		w.workspaces = append(w.workspaces, workspace)
	}
	w.s = NewServer(cfg, st)
	if err := w.s.StorageConfigurationError(); err != nil {
		t.Fatal(err)
	}
	for _, id := range w.users {
		u, err := st.Users().GetUserByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		_, _, err = w.s.sessions.IssueSession(ctx, rec, httptest.NewRequest("GET", "/", nil), u)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range rec.Result().Cookies() {
			if strings.HasSuffix(c.Name, "session") {
				w.cookies = append(w.cookies, c)
				break
			}
		}
	}
	if len(w.cookies) != len(w.users) {
		t.Fatal("session cookies missing")
	}
	t.Cleanup(func() {
		for _, org := range w.orgs {
			if err := w.s.purgeOrgObjects(ctx, org); err != nil {
				t.Error(err)
			}
		}
	})
	return w
}
func (w *hostedWorld) request(t *testing.T, s *Server, i int, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(w.cookies[i])
	if method != "GET" {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", s.sessions.CSRFToken(w.cookies[i].Value))
	}
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	return out
}
func (w *hostedWorld) publish(t *testing.T, i int, text string) drive.File {
	t.Helper()
	ctx := context.Background()
	key, err := w.st.Drive().KeysFor(ctx, w.workspaces[i])
	if err != nil {
		t.Fatal(err)
	}
	v, err := drive.WriteBlob(w.s.blobRoot(), key, strings.NewReader(text), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	f, err := w.st.Drive().Publish(ctx, w.users[i], drive.File{Workspace: w.workspaces[i], Name: uuid.NewString() + ".txt"}, v, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func readStored(t *testing.T, st objects.Store, name string) []byte {
	t.Helper()
	o, err := st.Open(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	b, err := io.ReadAll(io.NewSectionReader(o, 0, o.Size()))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestHostedSearchS3IsolationReplicaAndErasure(t *testing.T) {
	w := newHostedWorld(t)
	ctx := context.Background()
	files := []drive.File{w.publish(t, 0, "alphaonly sharedword privatealpha"), w.publish(t, 1, "betaonly sharedword privatebeta"), w.publish(t, 2, "gammaonly sharedword privategamma")}
	if err := w.s.indexIdle(ctx); err != nil {
		t.Fatal(err)
	}
	for i, org := range w.orgs {
		prefix, err := w.st.Drive().ObjectPrefix(ctx, org)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(prefix, "cells/") {
			t.Fatal(prefix)
		}
		p, err := w.st.Drive().SearchPointer(ctx, org)
		if err != nil || p.Object == "" || p.Version < 1 {
			t.Fatal(p, err)
		}
		raw := readStored(t, w.s.bucket.Store(prefix+"search/"), p.Object)
		for _, plain := range []string{"SQLite format", "privatealpha", "privatebeta", "privategamma"} {
			if bytes.Contains(raw, []byte(plain)) {
				t.Fatalf("unencrypted index contains %q", plain)
			}
		}
		keys, _ := w.st.Drive().OrgKeys(ctx, org)
		key, _ := keys.SearchKey()
		snapshot, err := crypto.DecryptAESGCMAAD(string(raw), key, searchAAD(org, p.Object, p.Version))
		if err != nil {
			t.Fatal(err)
		}
		x, err := search.OpenMemory(ctx, key, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		docs, err := x.Docs(ctx)
		x.Close()
		if err != nil || len(docs) != 1 {
			t.Fatalf("org %d contains other docs: %d %v", i, len(docs), err)
		}
		if _, err := crypto.DecryptAESGCMAAD(string(raw), key, searchAAD(org, p.Object, p.Version+1)); err == nil {
			t.Fatal("wrong version opened")
		}
		if _, err := crypto.DecryptAESGCMAAD(string(raw), key, searchAAD(w.orgs[(i+1)%3], p.Object, p.Version)); err == nil {
			t.Fatal("wrong org opened")
		}
		// Whole and ranged reads resolve only this org's cell namespace.
		got := w.request(t, w.s, i, "GET", "/api/drive/files/"+files[i].ID+"/download", "")
		if got.Code != 200 || !strings.Contains(got.Body.String(), "sharedword") {
			t.Fatalf("download: %d %s", got.Code, got.Body)
		}
	}
	// A separate replica has no local index and loads the committed pointer from S3.
	replicaCfg := *w.cfg
	replicaCfg.Database = w.cfg.Database
	replicaCfg.Database.DataDir = t.TempDir()
	replica := NewServer(&replicaCfg, w.st)
	for i := range w.orgs {
		hit := w.request(t, replica, i, "GET", "/api/drive/search?q=sharedword", "")
		if hit.Code != 200 || !strings.Contains(hit.Body.String(), files[i].ID) {
			t.Fatalf("replica search: %d %s", hit.Code, hit.Body)
		}
		for j := range files {
			if j != i && strings.Contains(hit.Body.String(), files[j].ID) {
				t.Fatal("cross-org search hit")
			}
		}
	}
	for _, root := range []string{w.cfg.Database.DataDir, replicaCfg.Database.DataDir} {
		if _, err := os.Stat(filepath.Join(root, "search", search.File)); !os.IsNotExist(err) {
			t.Fatal("host index.db exists", err)
		}
	}
	org := w.orgs[0]
	// A key already loaded in another replica cannot reopen a purged organization.
	p, _ := w.st.Drive().SearchPointer(ctx, org)
	if _, err := w.st.Drive().ApplyOrgs(ctx, []suitedir.Org{{ID: org, HomeRegion: "e2e-1", Status: suitedir.OrgDeleted, Enabled: false, Revision: 50}}, 50); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.st.Drive().PurgeOrgs(ctx); err != nil {
		t.Fatal(err)
	}
	if p2, _ := w.st.Drive().SearchPointer(ctx, org); p2.Object != "" {
		t.Fatal("purge kept pointer")
	}
	prefix, _ := w.st.Drive().ObjectPrefix(ctx, org)
	if _, err := w.s.bucket.Store(prefix+"search/").Open(ctx, p.Object); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("purge kept search object", err)
	}
	if err := w.st.Drive().PublishSearch(ctx, org, p, uuid.NewString(), nil); !errors.Is(err, drive.ErrConflict) {
		t.Fatal("purge allowed stale publish", err)
	}
	denied := w.request(t, replica, 0, "GET", "/api/drive/search?q=sharedword", "")
	if denied.Code == 200 {
		t.Fatal("purged org searched cached index")
	}
	// All prefixes, including blobs, text caches, abandoned staging and old search versions, go.
	var leftovers int
	if err := w.s.bucket.Tree(prefix).List(ctx, func(string, time.Time) error { leftovers++; return nil }); err != nil || leftovers != 0 {
		t.Fatal("org objects remain", leftovers, err)
	}
	for i := 1; i < len(w.orgs); i++ {
		if got := w.request(t, replica, i, "GET", "/api/drive/search?q=sharedword", ""); got.Code != 200 || !strings.Contains(got.Body.String(), files[i].ID) {
			t.Fatal("neighbor erased", got.Code, got.Body)
		}
	}
}

func TestHostedSearchConcurrentPublishAndCellGuard(t *testing.T) {
	w := newHostedWorld(t)
	ctx := context.Background()
	w.publish(t, 0, "concurrentword")
	// Racing indexers only acknowledge work from the winner's snapshot.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { errs <- w.s.indexIdle(ctx) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, drive.ErrConflict) {
			t.Fatal(err)
		}
	}
	if err := w.s.indexIdle(ctx); err != nil {
		t.Fatal(err)
	}
	// Removing a served cell at restart blocks every old credential immediately.
	dropped := w.cfg.Database
	dropped.Cells = []string{"e2e-1"}
	st, err := store.Open(ctx, dropped, w.cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Drive().ConfigureCells(ctx); err != nil {
		t.Fatal(err)
	}
	if _, active, err := st.Drive().OrgActive(ctx, w.orgs[1]); err != nil || active {
		t.Fatal("dropped cell active", active, err)
	}
	if active, err := st.Drive().ActivePrincipal(ctx, w.users[1]); err != nil || active {
		t.Fatal("dropped cell principal active", active, err)
	}
	if got := w.request(t, w.s, 1, "GET", "/api/drive/device-policy", ""); got.Code == 200 {
		t.Fatal("old session served dropped cell")
	}
	// Home is immutable even when a newer directory listing tries to rehome it.
	_, err = st.Drive().ApplyOrgs(ctx, []suitedir.Org{{ID: w.orgs[0], HomeRegion: "e2e-2", Status: suitedir.OrgActive, Enabled: true, Revision: 90}}, 90)
	if err == nil {
		t.Fatal("org rehomed")
	}
	home, _ := st.Drive().OrgHomeRegion(ctx, w.orgs[0])
	if home != "e2e-1" {
		t.Fatal(home)
	}
	// Restore served cells for the independent cleanup owner.
	restored, err := store.Open(ctx, w.cfg.Database, w.cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Drive().ConfigureCells(ctx); err != nil {
		t.Fatal(err)
	}
	restored.Close()
}

func TestHostedStorageStagingAndFailedIndexPublish(t *testing.T) {
	w := newHostedWorld(t)
	ctx := context.Background()
	d := w.st.Drive()
	u, err := d.StartUpload(ctx, w.users[0], drive.UploadSession{Workspace: w.workspaces[0], Name: "staged.txt", Size: 14}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AppendUpload(ctx, w.s.stagingRoot(), w.users[0], u.ID, 0, 7, strings.NewReader("staged ")); err != nil {
		t.Fatal(err)
	}
	prefix, _ := d.ObjectPrefix(ctx, w.orgs[0])
	count := 0
	if err := w.s.bucket.Tree(prefix+"staging/").Sub(u.ID).List(ctx, func(name string, _ time.Time) error {
		count++
		raw := readStored(t, w.s.bucket.Tree(prefix+"staging/").Sub(u.ID), name)
		if bytes.Contains(raw, []byte("staged ")) {
			t.Error("unsealed staging")
		}
		return nil
	}); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err := d.AppendUpload(ctx, w.s.stagingRoot(), w.users[0], u.ID, 7, 7, strings.NewReader("content")); err != nil {
		t.Fatal(err)
	}
	f, err := d.CompleteUpload(ctx, w.s.stagingRoot(), w.s.blobRoot(), w.users[0], u.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := d.KeysFor(ctx, w.workspaces[0])
	if err != nil {
		t.Fatal(err)
	}
	r, err := drive.OpenBlob(w.s.blobRoot(), f.Blob, key, true, f.Size)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if string(got) != "staged content" {
		t.Fatal(string(got))
	}
	// Different cells and orgs can hold the same cache name without replacing each other.
	same := uuid.NewString()
	for i := range w.orgs {
		k, err := d.KeysFor(ctx, w.workspaces[i])
		if err != nil {
			t.Fatal(err)
		}
		if err := drive.WriteThumb(w.s.thumbRoot(), k, same, []byte(w.orgs[i])); err != nil {
			t.Fatal(err)
		}
	}
	for i := range w.orgs {
		k, _ := d.KeysFor(ctx, w.workspaces[i])
		got, err := drive.ReadThumb(w.s.thumbRoot(), k, same)
		if err != nil || string(got) != w.orgs[i] {
			t.Fatal("cache namespace collision", err)
		}
	}
	original := w.s.bucket
	bad := w.cfg.ObjectStorage.S3()
	bad.SecretKey = "wrong-test-secret"
	w.s.bucket, err = objects.NewBucket(bad, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = w.s.indexIdle(ctx)
	w.s.bucket = original
	if err == nil {
		t.Fatal("failed S3 publish reported success")
	}
	p, _ := d.SearchPointer(ctx, w.orgs[0])
	if p.Version != 0 || p.Object != "" {
		t.Fatal("failed publish moved pointer", p)
	}
	pending, err := d.SearchDue(ctx, w.orgs[0], time.Now(), true, 20)
	if err != nil || len(pending) == 0 {
		t.Fatal("failed publish lost shared queue", pending, err)
	}
	if err := w.s.indexIdle(ctx); err != nil {
		t.Fatal(err)
	}
	old, _ := d.SearchPointer(ctx, w.orgs[0])
	oldRaw := readStored(t, w.s.bucket.Store(prefix+"search/"), old.Object)
	w.publish(t, 0, "newrevisionword")
	if err := w.s.indexIdle(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := d.SearchPointer(ctx, w.orgs[0])
	st := w.s.bucket.Store(prefix + "search/")
	currentRaw := readStored(t, st, current.Object)
	// Replaying old ciphertext at a newer committed object/version cannot authenticate.
	draft, err := st.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	draft.Write(oldRaw)
	if err := draft.Publish(current.Object); err != nil {
		t.Fatal(err)
	}
	fresh := NewServer(w.cfg, w.st)
	if err := fresh.withHostedIndex(ctx, w.orgs[0], func(*search.Index, drive.SearchPointer, drive.Keys) error { return nil }); err == nil {
		t.Fatal("old snapshot replay opened")
	}
	draft, err = st.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	draft.Write(currentRaw)
	if err := draft.Publish(current.Object); err != nil {
		t.Fatal(err)
	}
}

func TestHostedPeerUsesOrgCellWithoutFallback(t *testing.T) {
	w := newHostedWorld(t)
	ctx := context.Background()
	p := &kypostPeer{URL: "https://wrong-default.e2e.test", Source: kypostSourceKyIdentity, Secret: bytes.Repeat([]byte{9}, 32), Regions: map[string]string{"e2e-1": "https://mail-one.e2e.test", "e2e-2": "https://mail-two.e2e.test", "foreign": "https://foreign.e2e.test"}}
	w.s.kypost.set(p, "")
	for i, home := range []string{"e2e-1", "e2e-2", "e2e-1"} {
		selected, err := w.s.kypostFor(ctx, w.users[i], p)
		if err != nil || selected.URL != p.Regions[home] || selected.Region != home {
			t.Fatal("wrong peer cell", selected, err)
		}
		if _, _, err := w.st.Drive().IssueTicket(w.s.peerContext(ctx, selected), w.users[i], "attachment.txt", 3, 1<<20); err != nil {
			t.Fatal("cell peer not live", err)
		}
	}
	if w.s.kypostOrigin(p, p.URL) || w.s.kypostOrigin(p, p.Regions["foreign"]) {
		t.Fatal("CORS accepted unserved/default cell")
	}
	selected, err := w.s.kypostFor(ctx, w.users[1], p)
	if err != nil {
		t.Fatal(err)
	}
	oldContext := w.s.peerContext(ctx, selected)
	missing := *p
	missing.Regions = map[string]string{"e2e-1": p.Regions["e2e-1"]}
	w.s.kypost.set(&missing, "")
	if _, err := w.s.kypostFor(ctx, w.users[1], &missing); err == nil {
		t.Fatal("missing cell fell back")
	}
	if _, _, err := w.st.Drive().IssueTicket(oldContext, w.users[1], "attachment.txt", 3, 1<<20); !errors.Is(err, drive.ErrPeerGone) {
		t.Fatal("removed cell peer stayed live", err)
	}
}
