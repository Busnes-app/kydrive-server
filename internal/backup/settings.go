package backup

import (
	"context"
	"errors"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

const recoveryTokenLabel = "kydrive-server:setting:kyrecovery_token"

type settingsAdapter struct {
	ctx context.Context
	s   store.SettingsStore
}

// Settings binds the request context to the settings store for one call into the lib.
func Settings(ctx context.Context, s store.SettingsStore) recoveryclient.Settings {
	return settingsAdapter{ctx: ctx, s: s}
}

func (a settingsAdapter) Get(key string) (string, error) {
	v, err := a.s.GetSetting(a.ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return "", recoveryclient.ErrNotFound
	}
	return v, err
}
func (a settingsAdapter) Set(key, val string) error { return a.s.SetSetting(a.ctx, key, val) }
func (a settingsAdapter) Delete(key string) error   { return a.s.DeleteSetting(a.ctx, key) }

// NewSealer seals the KyRecovery token under the deployment key, domain-separated so a row
// copied from another setting will not open.
func NewSealer(cfg *config.Config) (recoveryclient.Sealer, error) {
	return recoveryclient.NewAESGCMSealer(cfg.Security.EncryptionKey, recoveryTokenLabel)
}

// RunConfig is what Run needs from the scaffold's configuration.
func RunConfig(cfg *config.Config, appVersion string) (recoveryclient.RunConfig, error) {
	sealer, err := NewSealer(cfg)
	if err != nil {
		return recoveryclient.RunConfig{}, err
	}
	return recoveryclient.RunConfig{
		DataDir: cfg.Database.DataDir, AppName: cfg.Server.AppName, AppVersion: appVersion,
		BackupDir: cfg.Backup.Dir, Keep: cfg.Backup.Keep, Sealer: sealer,
	}, nil
}

// OneRun runs a deposit under the backup-run lease, so across the replicas of a multi-tenant
// install one deposit runs at a time; a contender gets recoveryclient.ErrInProgress without
// running. Elsewhere it runs directly (recoveryclient still serializes within a process).
func OneRun(ctx context.Context, st store.Store, run func(context.Context) error) error {
	won, err := store.OnOneReplica(ctx, st, "backup-run", run)
	if err == nil && !won {
		return recoveryclient.ErrInProgress
	}
	return err
}
