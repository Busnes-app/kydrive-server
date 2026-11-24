package store_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

func TestReplicaConfigSingleUnchanged(t *testing.T) {
	st := newTestStore(t)
	for _, accept := range []bool{false, true} {
		if _, err := st.UpdateReplicaConfig(t.Context(), nil, accept); err == nil {
			t.Fatal("single mode accepted replica config")
		}
	}
	if _, err := st.Settings().GetSetting(t.Context(), config.ReplicaConfigSetting); err != store.ErrNotFound {
		t.Fatal("single mode wrote fingerprint", err)
	}
}

func TestReplicaConfigAtomic(t *testing.T) {
	if !testdb.Postgres() {
		t.Skip("multi-mode coordination uses PostgreSQL")
	}
	ctx := context.Background()
	dbcfg := testdb.Config(t)
	dbcfg.Tenancy = "multi"
	dbcfg.StaffOrg = drive.TestStaffOrg
	open := func() store.Store {
		st, err := store.Open(ctx, dbcfg, testdb.Key)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}
	a, b := open(), open()
	cfg := config.Config{Database: dbcfg, Security: config.SecurityConfig{EncryptionKey: testdb.Key, SessionSecret: "first-session-secret"}}
	fp := config.ReplicaFingerprint(&cfg)
	other := maps.Clone(fp)
	other["KY_SESSION_SECRET"] = config.ReplicaField(testdb.Key, "KY_SESSION_SECRET", "other-session-secret")
	// Different first-start configurations race across real independent pools. Exactly one wins.
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, st := range []store.Store{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			current := fp
			if i == 1 {
				current = other
			}
			_, errs[i] = st.UpdateReplicaConfig(ctx, current, false)
		}()
	}
	close(start)
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("first-start race: %v", errs)
	}
	if errs[0] != nil {
		fp, other = other, fp
	}
	before, err := a.Settings().GetSetting(ctx, config.ReplicaConfigSetting)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.UpdateReplicaConfig(ctx, other, false); err == nil || strings.Contains(err.Error(), "session-secret") {
		t.Fatal("mismatch not safely refused", err)
	}
	if after, _ := a.Settings().GetSetting(ctx, config.ReplicaConfigSetting); after != before {
		t.Fatal("refusal changed record")
	}
	changed, err := b.UpdateReplicaConfig(ctx, other, true)
	if err != nil || !slices.Equal(changed, []string{"KY_SESSION_SECRET"}) {
		t.Fatal(changed, err)
	}
	if _, err = a.UpdateReplicaConfig(ctx, fp, false); err == nil {
		t.Fatal("old replica still admitted")
	}
	if _, err = a.UpdateReplicaConfig(ctx, other, false); err != nil {
		t.Fatal(err)
	}
	db := testdb.Open(t, dbcfg)
	var details string
	if err = db.QueryRow(`SELECT details FROM audit_records WHERE action='admin.replica_config_accepted'`).Scan(&details); err != nil || details != "fields=KY_SESSION_SECRET" {
		t.Fatal("audit", details, err)
	}
	// An audit failure must roll back an accepted change, never leave unaudited state.
	if _, err = db.Exec(`ALTER TABLE audit_records ADD CONSTRAINT reject_replica_audit CHECK(action <> 'admin.replica_config_accepted') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.UpdateReplicaConfig(ctx, fp, true); err == nil {
		t.Fatal("audit failure accepted")
	}
	if _, err = a.UpdateReplicaConfig(ctx, other, false); err != nil {
		t.Fatal("audit failure changed record", err)
	}
	if _, err = db.Exec(`ALTER TABLE audit_records DROP CONSTRAINT reject_replica_audit`); err != nil {
		t.Fatal(err)
	}
	// A new field is learned without dropping a future/obsolete field.
	newer := maps.Clone(other)
	newer["KY_NEW_SHARED_FIELD"] = config.ReplicaField(testdb.Key, "KY_NEW_SHARED_FIELD", "value")
	if _, err = a.UpdateReplicaConfig(ctx, newer, false); err != nil {
		t.Fatal(err)
	}
	if _, err = b.UpdateReplicaConfig(ctx, other, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := a.Settings().GetSetting(ctx, config.ReplicaConfigSetting)
	var stored map[string]string
	if json.Unmarshal([]byte(raw), &stored) != nil || stored["KY_NEW_SHARED_FIELD"] != newer["KY_NEW_SHARED_FIELD"] {
		t.Fatal("new field lost")
	}
	for _, field := range []string{config.EncryptionKeyField, "KYDRIVE_IDENTITY_KEY_FILE", "KYDRIVE_STAFF_ORG", "KYDRIVE_TENANCY"} {
		bad := maps.Clone(other)
		bad[field] = config.ReplicaField(testdb.Key, field, "wrong")
		if _, err = a.UpdateReplicaConfig(ctx, bad, true); err == nil {
			t.Fatalf("accepted immutable %s", field)
		}
	}
	// A malformed persisted record cannot be accepted as a back door around key checks.
	for _, bad := range []string{`{broken`, `null`, `{}`, `{"KY_ENCRYPTION_KEY":"bad"}`} {
		if err = a.Settings().SetSetting(ctx, config.ReplicaConfigSetting, bad); err != nil {
			t.Fatal(err)
		}
		if _, err = a.UpdateReplicaConfig(ctx, other, true); err == nil {
			t.Fatal("accepted corrupt record")
		}
	}
}

func TestReplicaConfigPrivateSetting(t *testing.T) {
	st := newTestStore(t)
	if err := st.Settings().SetSetting(t.Context(), config.ReplicaConfigSetting, "private-marker"); err != nil {
		t.Fatal(err)
	}
	all, err := st.Settings().GetAllSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := all[config.ReplicaConfigSetting]; ok {
		t.Fatal("bulk settings expose private fingerprint")
	}
}
