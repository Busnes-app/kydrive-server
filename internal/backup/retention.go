package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kydrive-server/internal/config"
)

// BulkPolicy is the admin's opt-in bulk snapshot retention. All zero is off, the default.
type BulkPolicy struct {
	Daily   int `json:"keep_daily"`
	Weekly  int `json:"keep_weekly"`
	Monthly int `json:"keep_monthly"`
}

// BulkPrune records the last attempt, failed or not.
type BulkPrune struct {
	At        time.Time `json:"at"`
	Forgotten int       `json:"forgotten"`
	Error     string    `json:"error,omitempty"`
}

const (
	maxBulkKeep      = 1000
	bulkPolicyKey    = "bulk_retention"
	bulkLastPruneKey = "bulk_last_prune"
)

var (
	ErrBadBulkPolicy = errors.New("backup: each bulk retention count must be between 0 and 1000")
	ErrBulkBusy      = errors.New("backup: another bulk backup operation is running")
)

func (p BulkPolicy) Off() bool { return p == BulkPolicy{} }

func (p BulkPolicy) validate() error {
	for _, n := range []int{p.Daily, p.Weekly, p.Monthly} {
		if n < 0 || n > maxBulkKeep {
			return ErrBadBulkPolicy
		}
	}
	return nil
}

// LoadBulkPolicy returns off when unset; an unreadable row is an error, so nothing is forgotten.
func LoadBulkPolicy(s recoveryclient.Settings) (BulkPolicy, error) {
	var p BulkPolicy
	if ok, err := getJSON(s, bulkPolicyKey, &p); err != nil || !ok {
		return BulkPolicy{}, err
	}
	return p, p.validate()
}

func SetBulkPolicy(s recoveryclient.Settings, p BulkPolicy) error {
	if err := p.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.Set(bulkPolicyKey, string(raw))
}

func LastBulkPrune(s recoveryclient.Settings) (BulkPrune, bool, error) {
	var last BulkPrune
	ok, err := getJSON(s, bulkLastPruneKey, &last)
	return last, ok, err
}

// PruneBulkIfDue runs PruneBulk under the stored policy at most once a day and records the
// attempt, so a repository that refuses prune is retried daily, not after every deposit.
// It does not run (false) when the policy is off, a run was recorded in the last 24 hours,
// or another bulk operation holds the lock (ErrBulkBusy; tried again after the next deposit).
func PruneBulkIfDue(ctx context.Context, cfg *config.Config, s recoveryclient.Settings, now time.Time) (BulkPrune, bool, error) {
	policy, err := LoadBulkPolicy(s)
	if err != nil || policy.Off() {
		return BulkPrune{}, false, err
	}
	last, ok, err := LastBulkPrune(s)
	if err != nil || ok && now.Sub(last.At) < 24*time.Hour {
		return BulkPrune{}, false, err
	}
	n, err := PruneBulk(ctx, cfg.Database.DataDir, cfg.Security.EncryptionKey, policy, cfg.Backup.Keep)
	if errors.Is(err, ErrBulkBusy) {
		return BulkPrune{}, false, err
	}
	res := BulkPrune{At: now.UTC(), Forgotten: n}
	if err != nil {
		res.Error = recoveryclient.AuditSafe(err.Error())
	}
	raw, merr := json.Marshal(res)
	if merr == nil {
		merr = s.Set(bulkLastPruneKey, string(raw))
	}
	if err == nil {
		err = merr
	}
	return res, true, err
}

func getJSON(s recoveryclient.Settings, key string, v any) (bool, error) {
	raw, err := s.Get(key)
	if errors.Is(err, recoveryclient.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), v)
}

// BulkCheck records the last integrity check, failed or not.
type BulkCheck struct {
	At    time.Time `json:"at"`
	Error string    `json:"error,omitempty"`
}

const bulkLastCheckKey = "bulk_last_check"

func LastBulkCheck(s recoveryclient.Settings) (BulkCheck, bool, error) {
	var last BulkCheck
	ok, err := getJSON(s, bulkLastCheckKey, &last)
	return last, ok, err
}

// CheckBulkIfDue runs CheckBulk at most once a week and records the attempt. It does not run
// (false) without a bulk repository, within a week of the last attempt, or while another bulk
// operation holds the lock (ErrBulkBusy; tried again after the next deposit).
func CheckBulkIfDue(ctx context.Context, cfg *config.Config, s recoveryclient.Settings, now time.Time) (BulkCheck, bool, error) {
	if os.Getenv("KYDRIVE_BULK_BACKUP_REPOSITORY") == "" {
		return BulkCheck{}, false, nil
	}
	last, ok, err := LastBulkCheck(s)
	if err != nil || ok && now.Sub(last.At) < 7*24*time.Hour {
		return BulkCheck{}, false, err
	}
	err = CheckBulk(ctx, cfg.Database.DataDir, cfg.Security.EncryptionKey)
	if errors.Is(err, ErrBulkBusy) {
		return BulkCheck{}, false, err
	}
	res := BulkCheck{At: now.UTC()}
	if err != nil {
		res.Error = recoveryclient.AuditSafe(err.Error())
	}
	raw, merr := json.Marshal(res)
	if merr == nil {
		merr = s.Set(bulkLastCheckKey, string(raw))
	}
	if err == nil {
		err = merr
	}
	return res, true, err
}
