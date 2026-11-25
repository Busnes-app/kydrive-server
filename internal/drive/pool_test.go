package drive_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// grant gives org a grant up to ceiling bytes, valid for an hour, as an answered report leaves it.
func grant(t *testing.T, db *sql.DB, org string, ceiling int64) {
	t.Helper()
	if _, err := db.Exec(`UPDATE drive_orgs SET grant_ceiling=?,grant_expires=unixepoch()+3600,report_inflight=0 WHERE id=?`, ceiling, org); err != nil {
		t.Fatal(err)
	}
}

// freeze freezes the reports and returns org's.
func (o *orgs) freeze(t *testing.T, full bool, org string) (drive.PoolReport, bool) {
	t.Helper()
	reports, err := o.d.BeginReports(o.ctx, full, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		if r.OrgID == drive.TestStaffOrg {
			t.Fatal("the staff org was reported")
		}
		if r.OrgID == org {
			return r, true
		}
	}
	return drive.PoolReport{}, false
}

func okAnswer(r drive.PoolReport, room int64) suitedir.StorageResult {
	return suitedir.StorageResult{OrgID: r.OrgID, Result: "ok", Seq: r.Seq, CeilingBytes: r.UsedBytes + room, TTLSeconds: drive.GrantTTL}
}

func (o *orgs) apply(t *testing.T, results ...suitedir.StorageResult) []string {
	t.Helper()
	stale, err := o.d.ApplyReports(o.ctx, results)
	if err != nil {
		t.Fatal(err)
	}
	return stale
}

// put uploads size bytes as a new file.
func (o *orgs) put(user, workspace string, size int) error {
	o.puts++
	_, err := o.d.Publish(o.ctx, user, drive.File{Workspace: workspace, Name: fmt.Sprintf("put-%d.bin", o.puts)}, blob(o.t, o.root, strings.Repeat("x", size)), 0)
	return err
}

func wantQuota(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("%s: %v, want ErrQuota", what, err)
	}
}

func noErr(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func poolOrgs(t *testing.T) *orgs {
	o := multiTenant(t)
	o.t = t
	return o
}

// A write that adds bytes needs an unexpired grant with room, checked in its transaction. An
// unanswered report's snapshot is the limit until its answer.
func TestPoolGrantAdmitsGrowth(t *testing.T) {
	o := poolOrgs(t)
	if _, err := o.db.Exec(`UPDATE drive_orgs SET grant_ceiling=NULL,grant_expires=0 WHERE seeded=0`); err != nil {
		t.Fatal(err)
	}
	wantQuota(t, "no grant", o.put("a1", o.teamA.ID, 10))
	r, ok := o.freeze(t, true, "org-a")
	if !ok || r.UsedBytes != 9 || r.Seq != 1 || r.WantBytes != 1<<30 {
		t.Fatalf("report %+v %v", r, ok)
	}
	if _, ok := o.freeze(t, false, "org-b"); !ok {
		t.Fatal("org-b's unanswered report is not sent again")
	}
	if again, _ := o.freeze(t, false, "org-a"); again != r {
		t.Fatalf("an unanswered report changed: %+v, then %+v", r, again)
	}
	o.apply(t, okAnswer(r, 15))
	noErr(t, "within the grant", o.put("a1", o.teamA.ID, 10))
	wantQuota(t, "past the ceiling", o.put("a2", o.teamA.ID, 10))
	noErr(t, "to the ceiling exactly", o.put("a2", o.teamA.ID, 5))
	// A late or repeated answer changes nothing.
	o.apply(t, okAnswer(r, 1000))
	wantQuota(t, "after a repeated answer", o.put("a1", o.teamA.ID, 1))
	// While the next report is unanswered, nothing may pass its snapshot, even with room left.
	grant(t, o.db, "org-a", 1<<30)
	old := r
	r, _ = o.freeze(t, true, "org-a")
	wantQuota(t, "past an unanswered snapshot", o.put("a1", o.teamA.ID, 1))
	o.apply(t, okAnswer(old, 1000)) // a late answer to the earlier sequence
	wantQuota(t, "after a late answer to an earlier report", o.put("a1", o.teamA.ID, 1))
	o.apply(t, okAnswer(r, 100))
	noErr(t, "after the answer", o.put("a1", o.teamA.ID, 1))
	// The other org's pool is its own.
	wantQuota(t, "org-b has no grant", o.put("b1", o.teamB.ID, 1))
}

// A grant expires GrantTTL after its report was frozen, however late the answer: one sent 15
// minutes ago grants nothing. KyIdentity's ttl is never longer.
func TestPoolGrantExpiresFromSend(t *testing.T) {
	o := poolOrgs(t)
	r, _ := o.freeze(t, true, "org-a")
	if _, err := o.db.Exec(`UPDATE drive_orgs SET report_sent=report_sent-? WHERE id='org-a'`, drive.GrantTTL); err != nil {
		t.Fatal(err)
	}
	o.apply(t, okAnswer(r, 1<<20))
	wantQuota(t, "a grant answered 15 minutes after its report", o.put("a1", o.teamA.ID, 1))
	r, _ = o.freeze(t, true, "org-a")
	long := okAnswer(r, 1<<20)
	long.TTLSeconds = drive.GrantTTL + 1
	if _, err := o.d.ApplyReports(o.ctx, []suitedir.StorageResult{long}); err == nil {
		t.Fatal("a ttl over 900 s was accepted")
	}
	o.apply(t, okAnswer(r, 1<<20))
	noErr(t, "a fresh grant", o.put("a1", o.teamA.ID, 1))
	if _, err := o.db.Exec(`UPDATE drive_orgs SET grant_expires=unixepoch() WHERE id='org-a'`); err != nil {
		t.Fatal(err)
	}
	wantQuota(t, "an expired grant", o.put("a1", o.teamA.ID, 1))
}

// Over the pool (grant 0, the downgrade rule), writes that add bytes are refused and nothing
// else is: a move and a handover inside the org, trash and purge keep working.
func TestPoolOverPoolStillMovesAndFrees(t *testing.T) {
	o := poolOrgs(t)
	mine, err := o.d.EnsurePersonalWorkspace(o.ctx, "a1")
	noErr(t, "My files", err)
	f, err := o.d.Publish(o.ctx, "a1", drive.File{Workspace: mine, Name: "mine.txt"}, blob(t, o.root, "mine"), 0)
	noErr(t, "publish", err)
	grant(t, o.db, "org-a", 0)
	wantQuota(t, "upload over the pool", o.put("a1", o.teamA.ID, 1))
	_, err = o.d.MoveFile(o.ctx, "a1", f.ID, f.Revision, drive.Location{Workspace: o.teamA.ID, Name: "mine.txt"})
	noErr(t, "move over the pool", err)
	_, err = o.d.Handover(drive.WithOrg(o.ctx, "org-a"), "staff", o.leaverA, drive.Target{Workspace: o.teamA.ID})
	noErr(t, "handover over the pool", err)
	noErr(t, "trash", o.d.SetTrash(o.ctx, "a1", o.fileA.ID, o.fileA.Revision, true))
	_, err = o.d.PurgeFile(o.ctx, "a1", o.fileA.ID)
	noErr(t, "purge", err)
}

// The staff org holds no pool, and is never reported.
func TestPoolSkipsTheStaffOrg(t *testing.T) {
	o := poolOrgs(t)
	mine, err := o.d.EnsurePersonalWorkspace(o.ctx, "staff")
	noErr(t, "staff My files", err)
	noErr(t, "staff upload without a grant", o.put("staff", mine, 10))
	o.freeze(t, true, "")
	if err := o.d.AdvanceReportSeq(o.ctx, drive.TestStaffOrg, 5); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("staff sequence advanced: %v", err)
	}
}

// A stale answer keeps the grant until it expires and fences the org's reports until the
// operator advances its sequence; unknown_org drops the grant.
func TestPoolStaleAndUnknownAnswers(t *testing.T) {
	o := poolOrgs(t)
	a, _ := o.freeze(t, true, "org-a")
	b, _ := o.freeze(t, false, "org-b")
	stale := o.apply(t,
		suitedir.StorageResult{OrgID: "org-a", Result: "stale", Seq: a.Seq + 5, CeilingBytes: 0, TTLSeconds: 900},
		suitedir.StorageResult{OrgID: "org-b", Result: "unknown_org"})
	if len(stale) != 1 || stale[0] != "org-a" {
		t.Fatalf("stale %v", stale)
	}
	noErr(t, "a stale org keeps its grant", o.put("a1", o.teamA.ID, 1))
	wantQuota(t, "unknown_org drops the grant", o.put("b1", o.teamB.ID, 1))
	if _, ok := o.freeze(t, true, "org-a"); ok {
		t.Fatal("a stale org was reported again")
	}
	if r, ok := o.freeze(t, false, "org-b"); !ok || r.Seq != b.Seq+1 {
		t.Fatalf("org-b's next report %+v %v", r, ok)
	}
	list, err := o.d.StaleReports(o.ctx)
	if err != nil || len(list) != 1 || list[0] != (drive.StaleReport{Org: "org-a", Seq: a.Seq, Stale: a.Seq + 5}) {
		t.Fatalf("stale list %+v %v", list, err)
	}
	if err := o.d.AdvanceReportSeq(o.ctx, "org-a", a.Seq+4); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("advanced below KyIdentity's sequence: %v", err)
	}
	noErr(t, "advance", o.d.AdvanceReportSeq(o.ctx, "org-a", a.Seq+5))
	wantQuota(t, "an advanced org asks again", o.put("a1", o.teamA.ID, 1))
	if r, ok := o.freeze(t, false, "org-a"); !ok || r.Seq != a.Seq+6 {
		t.Fatalf("org-a after advance %+v %v", r, ok)
	}
	if n, err := drive.QueryInt(o.ctx, o.d, `SELECT COUNT(*) FROM drive_events WHERE action='platform.storage_seq_advanced' AND org_id IS NULL`); err != nil || n != 1 {
		t.Fatalf("%d advance events: %v", n, err)
	}
}

// A write that uses half the grant makes the org due at the next fast pass; a reply to a
// changed poll row does too.
func TestPoolHalfGrantIsDue(t *testing.T) {
	o := poolOrgs(t)
	r, _ := o.freeze(t, true, "org-a")
	o.freeze(t, true, "org-b")
	o.apply(t, okAnswer(r, 20))
	noErr(t, "below half", o.put("a1", o.teamA.ID, 9))
	if _, ok := o.freeze(t, false, "org-a"); ok {
		t.Fatal("due below half the grant")
	}
	noErr(t, "half", o.put("a1", o.teamA.ID, 1))
	if _, ok := o.freeze(t, false, "org-a"); !ok {
		t.Fatal("not due at half the grant")
	}
	if _, err := o.d.ApplyOrgs(o.ctx, []suitedir.Org{{ID: "org-b", Status: suitedir.OrgActive, Revision: 9, Enabled: true}}, 9); err != nil {
		t.Fatal(err)
	}
	if n, err := drive.QueryInt(o.ctx, o.d, `SELECT report_due FROM drive_orgs WHERE id='org-b'`); err != nil || n != 1 {
		t.Fatalf("a changed org is due %d: %v", n, err)
	}
}

// Two replicas (independent pools on one PostgreSQL database) race uploads with room for one: exactly one
// lands. With room for all, every one lands (serialization aborts are retried).
func TestPoolRacingReplicasKeepTheGrant(t *testing.T) {
	o := poolOrgs(t)
	d2 := o.d // SQLite has one writer and multi mode requires PostgreSQL
	if testdb.Postgres() {
		var err error
		d2, err = drive.OpenMulti(o.ctx, testdb.Open(t, o.cfg), testdb.Key)
		noErr(t, "second replica", err)
	}
	race := func(n, size int) (landed int) {
		t.Helper()
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for i := range n {
			d := []*drive.Store{o.d, d2}[i%2]
			v := blob(t, o.root, strings.Repeat("y", size))
			wg.Go(func() {
				_, err := d.Publish(o.ctx, "a1", drive.File{Workspace: o.teamA.ID, Name: fmt.Sprintf("race-%d-%d.bin", size, i)}, v, 0)
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			switch {
			case err == nil:
				landed++
			case !errors.Is(err, drive.ErrQuota):
				t.Fatal(err)
			}
		}
		return landed
	}
	grant(t, o.db, "org-a", 9+60) // the fixture holds 9 bytes
	if landed := race(2, 60); landed != 1 {
		t.Fatalf("%d of 2 uploads landed with room for one", landed)
	}
	if landed := race(8, 60); landed != 0 {
		t.Fatalf("%d uploads landed in a full grant", landed)
	}
	grant(t, o.db, "org-a", 1<<30)
	if landed := race(20, 10); landed != 20 {
		t.Fatalf("%d of 20 uploads landed with room for all", landed)
	}
}

// Every path that adds bytes ends in fits: the only inserts into drive_versions and
// drive_view_copies are in publishTX and behind fits in IssueViewTicket.
func TestOnlyFitsAddsBytes(t *testing.T) {
	files, err := os.ReadDir(".")
	noErr(t, "read dir", err)
	re := regexp.MustCompile(`INSERT INTO drive_(versions|view_copies)\b`)
	found := map[string]int{}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(f.Name())
		noErr(t, "read", err)
		for _, m := range re.FindAllString(string(src), -1) {
			found[f.Name()+" "+m]++
		}
	}
	want := map[string]int{"store.go INSERT INTO drive_versions": 1, "viewcopies.go INSERT INTO drive_view_copies": 1}
	if fmt.Sprint(found) != fmt.Sprint(want) {
		t.Fatalf("byte-adding inserts %v, want %v: a new one must call fits", found, want)
	}
}

// One org that cannot be reported (an exhausted sequence) does not stop the others: its
// error comes back beside their reports.
func TestPoolOneBadOrgDoesNotStarveTheRest(t *testing.T) {
	o := poolOrgs(t)
	if _, err := o.db.Exec(`INSERT INTO drive_orgs(id,status,report_seq) VALUES('org-0','active',9223372036854775807)`); err != nil {
		t.Fatal(err)
	}
	reports, err := o.d.BeginReports(o.ctx, true, "")
	if err == nil || !strings.Contains(err.Error(), "org-0") {
		t.Fatalf("no error for the unreportable org: %v", err)
	}
	if len(reports) != 2 || reports[0].OrgID != "org-a" || reports[1].OrgID != "org-b" {
		t.Fatalf("reports %+v", reports)
	}
}
