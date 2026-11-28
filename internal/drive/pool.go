package drive

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/google/uuid"
)

// The org storage pool (multi-tenant only, tenancy design D7 "Quotas"; contract KyIdentity's
// POST /api/suite/storage). KyDrive reports each org's drive bytes and receives a ceiling it may
// fill until the grant expires. Every grant figure lives on the org's drive_orgs row, so every
// replica checks the same one inside the write's own transaction.

// ErrPoolRefreshing reports that an org's unexpired storage grant has room for a write, but an
// in-flight storage report to KyIdentity currently caps spending at report_used until the answer
// lands. It wraps ErrQuota so callers that do not retry still treat it as quota exceeded.
var ErrPoolRefreshing = fmt.Errorf("%w: storage grant refreshing", ErrQuota)

var poolRetryDelays = []time.Duration{
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
}

const poolRetryCap = 1500 * time.Millisecond

// RetryPool retries fn while it returns ErrPoolRefreshing, using short bounded backoff up to
// poolRetryCap so concurrent writes wait out an in-flight storage-pool report instead of failing
// with quota exceeded. Never call inside a database transaction; store methods themselves return
// ErrPoolRefreshing immediately without sleeping.
func RetryPool(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(poolRetryCap)
	for attempt := 0; ; attempt++ {
		err := fn()
		if !errors.Is(err, ErrPoolRefreshing) {
			return err
		}
		rem := time.Until(deadline)
		if rem <= 0 {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		wait := min(poolRetryDelays[min(attempt, len(poolRetryDelays)-1)], rem)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// GrantTTL is the longest a grant lives, in seconds (KyIdentity answers ttl_seconds 900). It
// counts from when the report was frozen, by database time, so a slow answer only shortens it.
const GrantTTL = 900

// reportWant is the headroom asked for: 1 GiB, or twice the growth since the last report.
const reportWant = 1 << 30

// maxReportBytes bounds used_bytes and want_bytes (the contract's 2^53).
const maxReportBytes = 1 << 53

// orgBytes, given the org as ?1, is the org's drive_bytes: every version row of its
// workspaces and its live viewing copies, the figure fits checks and the reporter sends.
const orgBytes = `SELECT (SELECT COALESCE(SUM(v.size),0) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id JOIN drive_workspaces w ON w.id=f.workspace WHERE w.org_id=?1)+(SELECT COALESCE(SUM(size),0) FROM drive_view_copies WHERE revoked=0 AND org_id=?1)+(SELECT COALESCE(SUM(size),0) FROM drive_vault_versions WHERE org_id=?1)+(` + vaultCheckpointBytes + `)`

// poolSchema is drive schema 30: the grant and the report state per org, and the indexes the
// org sum reads. report_due is set by a write that used half its grant and by every org poll
// change; a new row starts due, so a new org gets its first grant at the next fast pass.
func poolSchema(d dialect) string {
	integer := "INTEGER"
	if d.pg {
		integer = "BIGINT"
	}
	s := ""
	for _, c := range []string{"grant_ceiling " + integer, "grant_expires " + integer + " NOT NULL DEFAULT 0", "report_seq " + integer + " NOT NULL DEFAULT 0",
		"report_used " + integer + " NOT NULL DEFAULT 0", "report_want " + integer + " NOT NULL DEFAULT 0", "report_sent " + integer + " NOT NULL DEFAULT 0",
		"report_inflight " + integer + " NOT NULL DEFAULT 0", "report_stale " + integer + " NOT NULL DEFAULT 0", "report_due " + integer + " NOT NULL DEFAULT 1"} {
		s += "ALTER TABLE drive_orgs ADD COLUMN " + c + ";\n"
	}
	return s + `CREATE INDEX drive_workspaces_org ON drive_workspaces(org_id);
CREATE INDEX drive_view_copies_org ON drive_view_copies(org_id);`
}

// quotaFits returns ErrQuota unless workspace can take extra more bytes. Single-tenant: the
// workspace quota. Multi-tenant: the org pool, only when adds (a move or handover inside one
// org adds nothing to it) and extra > 0; the per-workspace quota is not enforced there.
func quotaFits(ctx context.Context, q execQueryer, workspace string, extra int64, adds bool) error {
	var multi bool
	var org string
	if err := q.QueryRowContext(ctx, `SELECT `+multiTenantSQL+`,org_id FROM drive_workspaces WHERE id=?`, workspace).Scan(&multi, &org); err != nil {
		return err
	}
	if !multi {
		var used, quota int64
		if err := q.QueryRowContext(ctx, `SELECT quota,COALESCE((`+usedBytes+`),0) FROM drive_workspaces WHERE id=?`, workspace, workspace).Scan(&quota, &used); err != nil {
			return err
		}
		if used > quota || extra > quota-used {
			return ErrQuota
		}
		return nil
	}
	if !adds || extra <= 0 {
		return nil
	}
	return poolFits(ctx, q, org, extra)
}

type execQueryer interface {
	queryer
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// poolFits admits extra > 0 more bytes for org only within an unexpired grant. While a report
// is unanswered its frozen snapshot is the limit, so the grant that answer replaces is never
// spent twice; when the write still fits in the unexpired ceiling, it returns ErrPoolRefreshing
// so write-boundary callers can wait out the report instead of refusing with quota exceeded.
// A write past half the grant marks the org due, committing with the write.
func poolFits(ctx context.Context, q execQueryer, org string, extra int64) error {
	var seeded, inflight bool
	var ceiling sql.NullInt64
	var expires, reported, now int64
	err := q.QueryRowContext(ctx, `SELECT seeded=1,grant_ceiling,grant_expires,report_inflight=1,report_used,unixepoch() FROM drive_orgs WHERE id=?`, org).Scan(&seeded, &ceiling, &expires, &inflight, &reported, &now)
	if err != nil {
		return err
	}
	if seeded { // the staff org holds no customer content and has no pool
		return nil
	}
	if !ceiling.Valid || expires <= now {
		return ErrQuota
	}
	budget := ceiling.Int64
	if inflight {
		budget = min(budget, reported)
	}
	var used int64
	if err := q.QueryRowContext(ctx, orgBytes, org).Scan(&used); err != nil {
		return err
	}
	if extra > budget-used {
		if inflight && extra <= ceiling.Int64-used {
			return ErrPoolRefreshing
		}
		return ErrQuota
	}
	if grant := ceiling.Int64 - reported; used+extra-reported >= (grant+1)/2 {
		_, err = q.ExecContext(ctx, `UPDATE drive_orgs SET report_due=1 WHERE id=? AND report_due=0`, org)
	}
	return err
}

// PoolReport is one frozen report. An unanswered one is sent again unchanged: same sequence,
// bytes and send time, so KyIdentity treats it as a replay.
type PoolReport struct {
	suitedir.StorageReport
	Sent int64
}

// BeginReports freezes up to 500 reports (the contract's batch): every unanswered one, and,
// for orgs not fenced by a stale answer, each one due (report_due) or, when full, each whose
// drive bytes changed, that has no grant, or whose grant has less than half its life left.
// Each org freezes in its own transaction, so a pass never holds many orgs' sums at once.
// Orgs come in ID order after after, so a caller pages with the last report's org. Staff and
// deleted orgs are never reported. An org that fails is skipped and its error returned with
// the reports.
func (s *Store) BeginReports(ctx context.Context, full bool, after string) ([]PoolReport, error) {
	if !s.Multi() {
		return nil, ErrNotFound
	}
	orgs, err := column(ctx, s.db, `SELECT id FROM drive_orgs WHERE seeded=0 AND status<>'deleted' AND report_stale=0 AND (report_inflight=1 OR report_due=1 OR ?) AND id>? ORDER BY id`, full, after)
	if err != nil {
		return nil, err
	}
	var out []PoolReport
	var errs []error
	for _, org := range orgs {
		if len(out) == 500 || ctx.Err() != nil {
			break
		}
		// One org that cannot be reported must not starve the others: its error is
		// returned beside every report frozen, and the caller sends those.
		r, ok, err := s.freezeReport(ctx, org)
		if err != nil {
			errs = append(errs, fmt.Errorf("org %s: %w", org, err))
			continue
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, errors.Join(errs...)
}

// freezeReport returns org's unanswered report, or freezes a new one when it needs one.
func (s *Store) freezeReport(ctx context.Context, org string) (r PoolReport, ok bool, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		r, ok = PoolReport{}, false
		var inflight, due, nogrant bool
		var left, now int64
		r.OrgID = org
		err := tx.QueryRowContext(ctx, `SELECT report_seq,report_used,report_want,report_sent,report_inflight=1,report_due=1,grant_ceiling IS NULL,grant_expires-unixepoch(),unixepoch() FROM drive_orgs WHERE id=? AND seeded=0 AND status<>'deleted' AND report_stale=0`, org).
			Scan(&r.Seq, &r.UsedBytes, &r.WantBytes, &r.Sent, &inflight, &due, &nogrant, &left, &now)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil || inflight {
			ok = err == nil
			return err
		}
		var used int64
		if err := tx.QueryRowContext(ctx, orgBytes, org).Scan(&used); err != nil {
			return err
		}
		if !due && used == r.UsedBytes && !nogrant && left > GrantTTL/2 {
			return nil
		}
		if used > maxReportBytes || r.Seq == 1<<63-1 {
			return fmt.Errorf("drive: org %s cannot be reported", org)
		}
		r.WantBytes = min(max(reportWant, 2*(used-r.UsedBytes)), maxReportBytes)
		r.UsedBytes, r.Sent = used, now
		r.Seq++
		_, err = tx.ExecContext(ctx, `UPDATE drive_orgs SET report_seq=?,report_used=?,report_want=?,report_sent=?,report_inflight=1,report_due=0 WHERE id=?`, r.Seq, r.UsedBytes, r.WantBytes, r.Sent, org)
		ok = err == nil
		return err
	})
	return r, ok, err
}

// ErrStaleReport is a stale answer: KyIdentity holds a higher sequence (a restored database).
// The org keeps its grant until it expires and is not reported again until an operator runs
// AdvanceReportSeq past the sequence KyIdentity holds; KyDrive never jumps to it on its own.
var ErrStaleReport = errors.New("drive: storage report is stale")

// ApplyReports records KyIdentity's answers, each only for the unanswered report it answers
// (same sequence), so a late or repeated answer changes nothing. It returns the orgs answered
// stale.
func (s *Store) ApplyReports(ctx context.Context, results []suitedir.StorageResult) (stale []string, err error) {
	if !s.Multi() {
		return nil, ErrNotFound
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		stale = nil
		for _, r := range results {
			var err error
			switch r.Result {
			case "ok":
				if r.TTLSeconds <= 0 || r.TTLSeconds > GrantTTL {
					return fmt.Errorf("drive: storage grant for %s: ttl %d", r.OrgID, r.TTLSeconds)
				}
				// A ceiling outside [used, used+want] breaks the contract: no grant, fail closed,
				// and the next pass reports again. Over the pool the grant is 0: ceiling=used.
				_, err = tx.ExecContext(ctx, `UPDATE drive_orgs SET grant_ceiling=CASE WHEN report_used<=?1 AND ?1-report_used<=report_want THEN ?1 END,grant_expires=CASE WHEN report_used<=?1 AND ?1-report_used<=report_want THEN report_sent+?2 ELSE 0 END,report_inflight=0,report_due=CASE WHEN report_used<=?1 AND ?1-report_used<=report_want THEN report_due ELSE 1 END WHERE id=?3 AND seeded=0 AND report_inflight=1 AND report_seq=?4`, r.CeilingBytes, r.TTLSeconds, r.OrgID, r.Seq)
			case "stale":
				var res sql.Result
				// The answer carries KyIdentity's sequence, at least the one sent.
				res, err = tx.ExecContext(ctx, `UPDATE drive_orgs SET report_stale=?,report_inflight=0 WHERE id=? AND seeded=0 AND report_inflight=1 AND report_seq<=?`, r.Seq, r.OrgID, r.Seq)
				if err == nil {
					if n, _ := res.RowsAffected(); n == 1 {
						stale = append(stale, r.OrgID)
					}
				}
			case "unknown_org":
				// The answer carries no sequence; only one report per org is ever unanswered.
				_, err = tx.ExecContext(ctx, `UPDATE drive_orgs SET grant_ceiling=NULL,grant_expires=0,report_inflight=0 WHERE id=? AND seeded=0 AND report_inflight=1`, r.OrgID)
			default:
				err = fmt.Errorf("drive: storage answer %q", r.Result)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	return stale, err
}

// StaleReport is an org whose reports wait for AdvanceReportSeq.
type StaleReport struct {
	Org        string
	Seq, Stale int64 // KyDrive's last sequence and the one KyIdentity holds
}

// StaleReports lists the fenced orgs.
func (s *Store) StaleReports(ctx context.Context) ([]StaleReport, error) {
	if !s.Multi() {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,report_seq,report_stale FROM drive_orgs WHERE report_stale>0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StaleReport
	for rows.Next() {
		var r StaleReport
		if err := rows.Scan(&r.Org, &r.Seq, &r.Stale); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AdvanceReportSeq is the operator's recovery from a stale answer (after a restore): the
// org's sequence moves to past, at least the one KyIdentity holds, its grant is dropped and
// it is due at once. Audited in the platform log, as system:operator-cli.
func (s *Store) AdvanceReportSeq(ctx context.Context, org string, past int64) error {
	if !s.Multi() {
		return ErrNotFound
	}
	if past <= 0 || past == 1<<63-1 {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE drive_orgs SET report_seq=?,report_stale=0,report_inflight=0,grant_ceiling=NULL,grant_expires=0,report_due=1
WHERE id=? AND seeded=0 AND status<>'deleted' AND report_seq<=? AND report_stale<=?`, past, org, past, past)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return cmp.Or(err, ErrConflict)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drive_events(id,user_id,action,resource,created,org_id) VALUES(?,?,?,?,?,NULL)`, uuid.NewString(), "system:operator-cli", "platform.storage_seq_advanced", "org:"+org+" past="+strconv.FormatInt(past, 10), now())
		return err
	})
}
