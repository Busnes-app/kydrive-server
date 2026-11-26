package drive_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// replica opens a second multi-tenant store on o's database: another replica, with its own
// key cache.
func (o *orgs) replica(t *testing.T) *drive.Store {
	t.Helper()
	r, err := drive.OpenMulti(o.ctx, o.db, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (o *orgs) keys(t *testing.T, d *drive.Store, workspace string) drive.Keys {
	t.Helper()
	k, err := d.KeysFor(o.ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func readBlob(st objects.Store, k drive.Keys, v drive.Version) error {
	r, err := drive.OpenBlob(st, v.Blob, k, true, v.Size)
	if err == nil {
		r.Close()
	}
	return err
}

func TestOrgKeysAreRecordedWithTheirOrg(t *testing.T) {
	o := multiTenant(t)
	if _, err := o.d.ApplyOrgs(o.ctx, []suitedir.Org{
		{ID: "org-c", Status: suitedir.OrgActive, Revision: 1, Enabled: true},
		{ID: "org-d", Status: suitedir.OrgDeleted, Revision: 1, Enabled: true},
	}, 1); err != nil {
		t.Fatal(err)
	}
	for child, parent := range drive.MultiTenantTables {
		if !references(t, o.db, child, parent) {
			t.Errorf("%s has no foreign key to %s", child, parent)
		}
	}
	got := strs(t, o.db, `SELECT org_id||':'||kek FROM drive_org_keys ORDER BY org_id`)
	if want := []string{drive.TestStaffOrg + ":1", "org-a:1", "org-b:1", "org-c:1"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("keys %v, want %v (a deleted org gets none)", got, want)
	}
	// A later poll of the org keeps its key: what it sealed still opens on a new replica.
	v := o.blob(t, o.teamA.ID, "sealed before the poll")
	if _, err := o.d.ApplyOrgs(o.ctx, []suitedir.Org{{ID: "org-a", Status: suitedir.OrgActive, Revision: 9, Enabled: true}}, 3); err != nil {
		t.Fatal(err)
	}
	if err := readBlob(objects.Dir(o.root), o.keys(t, o.replica(t), o.teamA.ID), v); err != nil {
		t.Fatalf("a poll replaced the org's key: %v", err)
	}
	// Two replicas recording the same new org at once agree on one key.
	r := o.replica(t)
	var wg sync.WaitGroup
	for _, d := range []*drive.Store{o.d, r} {
		wg.Go(func() {
			if _, err := d.ApplyOrgs(o.ctx, []suitedir.Org{{ID: "org-e", Status: suitedir.OrgActive, Revision: 1, Enabled: true}}, 2); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	a, err := drive.OrgKeys(o.ctx, o.d, "org-e")
	if err != nil {
		t.Fatal(err)
	}
	b, err := drive.OrgKeys(o.ctx, r, "org-e")
	if err != nil {
		t.Fatal(err)
	}
	v, err = drive.WriteBlob(objects.Dir(o.root), a, strings.NewReader("e"), 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := readBlob(objects.Dir(o.root), b, v); err != nil {
		t.Fatalf("replicas hold different keys for one org: %v", err)
	}
}

func TestOrgSealedFilesOpenOnlyForTheirOrg(t *testing.T) {
	o := multiTenant(t)
	st := objects.Dir(o.root)
	a, b := o.keys(t, o.d, o.teamA.ID), o.keys(t, o.d, o.teamB.ID)
	v := o.blob(t, o.teamA.ID, "plan of org a")
	if err := readBlob(st, a, v); err != nil {
		t.Fatal(err)
	}
	if err := readBlob(st, b, v); err == nil {
		t.Fatal("org A's blob opened under org B's key")
	}
	if err := drive.WriteThumb(st, a, v.Blob, []byte("cache of org a")); err != nil {
		t.Fatal(err)
	}
	if _, err := drive.ReadThumb(st, b, v.Blob); err == nil {
		t.Fatal("org A's cache entry opened under org B's key")
	}
	if got, err := drive.ReadThumb(st, a, v.Blob); err != nil || string(got) != "cache of org a" {
		t.Fatalf("cache entry %q, %v", got, err)
	}
	// The stanza names the org; naming another one opens under neither.
	path, err := drive.BlobPath(o.root, v.Blob)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("-> kydrive-org org-a ")) {
		t.Fatalf("no org stanza in %q", raw[:80])
	}
	if err = os.WriteFile(path, bytes.Replace(raw, []byte("kydrive-org org-a"), []byte("kydrive-org org-b"), 1), 0600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []drive.Keys{a, b} {
		if err := readBlob(st, k, v); err == nil {
			t.Fatal("blob opened with its stanza's org changed")
		}
	}
}

func TestEachModeRefusesTheOtherStanza(t *testing.T) {
	o := multiTenant(t)
	st := objects.Dir(o.root)
	org, single := o.keys(t, o.d, o.teamA.ID), drive.Deployment(testdb.Key)
	write := func(k drive.Keys) drive.Version {
		v, err := drive.WriteBlob(st, k, strings.NewReader("bytes"), 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if err := readBlob(st, org, write(single)); err == nil {
		t.Error("multi-tenant keys opened a kydrive stanza")
	}
	if err := readBlob(st, single, write(org)); err == nil {
		t.Error("the deployment key opened a kydrive-org stanza")
	}
	plain, err := st.Create(o.ctx)
	if err != nil {
		t.Fatal(err)
	}
	plain.Write([]byte("bytes"))
	if err = plain.Publish("00000000-0000-4000-8000-00000000beef"); err != nil {
		t.Fatal(err)
	}
	if _, err := drive.OpenBlob(st, "00000000-0000-4000-8000-00000000beef", org, false, 5); err == nil {
		t.Error("multi-tenant keys opened an unsealed blob")
	}
}

func TestOrgNamesAreKeyedPerOrg(t *testing.T) {
	o := multiTenant(t)
	fa, err := o.d.Publish(o.ctx, "a1", drive.File{Workspace: o.teamA.ID, Name: "same.txt"}, o.blob(t, o.teamA.ID, "a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := o.d.Publish(o.ctx, "b1", drive.File{Workspace: o.teamB.ID, Name: "same.txt"}, o.blob(t, o.teamB.ID, "b"), 0)
	if err != nil {
		t.Fatal(err)
	}
	macs := strs(t, o.db, `SELECT name_hmac FROM drive_files WHERE id IN (?,?)`, fa.ID, fb.ID)
	if len(macs) != 2 || macs[0] == macs[1] {
		t.Fatalf("one name in two orgs has name_hmac %v", macs)
	}
	// A name sealed for A does not open under B's key.
	if _, err := o.db.Exec(`UPDATE drive_files SET name=(SELECT name FROM drive_files WHERE id=?) WHERE id=?`, o.fileA.ID, o.fileB.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := o.d.File(o.ctx, "b1", o.fileB.ID, 1); err == nil || errors.Is(err, drive.ErrDenied) {
		t.Fatalf("org A's sealed name under org B: %v", err)
	}
}

func TestWrappedKeyCopiedToAnotherOrgFails(t *testing.T) {
	o := multiTenant(t)
	if _, err := o.db.Exec(`UPDATE drive_org_keys SET wrapped=(SELECT wrapped FROM drive_org_keys WHERE org_id='org-a') WHERE org_id='org-b'`); err != nil {
		t.Fatal(err)
	}
	r := o.replica(t)
	if _, err := drive.OrgKeys(o.ctx, r, "org-b"); err == nil {
		t.Fatal("org A's wrapped key opened as org B's")
	}
	if _, err := r.Files(o.ctx, "b1", o.teamB.ID, false); err == nil {
		t.Fatal("org B's names read with the copied key")
	}
	if _, err := drive.OrgKeys(o.ctx, r, "org-a"); err != nil {
		t.Fatal(err)
	}
}

// Deleting an org's key leaves its blobs, caches and names unreadable on every replica once
// its cache lets the key go: at once on the replica that purges, within orgKeyLife elsewhere.
func TestDestroyedOrgKeyLeavesNothingReadable(t *testing.T) {
	o := multiTenant(t)
	st := objects.Dir(o.root)
	r := o.replica(t)
	a := o.keys(t, o.d, o.teamA.ID)
	if err := drive.WriteThumb(st, a, o.fileA.Blob, []byte("thumb")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []*drive.Store{o.d, r} { // both replicas hold the key
		if _, err := d.Files(o.ctx, "a1", o.teamA.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o.db.Exec(`UPDATE drive_orgs SET status='deleted' WHERE id='org-a'`); err != nil {
		t.Fatal(err)
	}
	blobs, _, err := o.d.PurgeOrgs(o.ctx)
	if err != nil || len(blobs) == 0 {
		t.Fatal(blobs, err) // left on the storage on purpose
	}
	if n := strs(t, o.db, `SELECT org_id FROM drive_org_keys WHERE org_id='org-a'`); len(n) != 0 {
		t.Fatal("purge kept the org key")
	}
	if _, err := drive.OrgKeys(o.ctx, o.d, "org-a"); !errors.Is(err, drive.ErrNoOrgKey) {
		t.Fatalf("purging replica still has the key: %v", err)
	}
	if _, err := drive.OrgKeys(o.ctx, r, "org-a"); err != nil {
		t.Fatalf("other replica dropped the key before its cache did: %v", err)
	}
	drive.ExpireOrgKeys(r)
	if _, err := drive.OrgKeys(o.ctx, r, "org-a"); !errors.Is(err, drive.ErrNoOrgKey) {
		t.Fatalf("other replica kept the key past its cache: %v", err)
	}
	for _, f := range [][2]string{{"blob", o.fileA.Blob}, {"cache", o.fileA.Blob}} {
		raw, err := os.ReadFile(o.root + "/" + f[1])
		if err != nil || bytes.Contains(raw, []byte("plan")) || bytes.Contains(raw, []byte("thumb")) {
			t.Fatalf("%s left readable: %v", f[0], err)
		}
	}
	// The org is gone; nothing else names its key.
	if _, err := drive.OrgKeys(o.ctx, o.d, "org-b"); err != nil {
		t.Fatal(err)
	}
}
