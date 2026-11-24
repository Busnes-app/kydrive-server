package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/editor"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func replicaConfig(ctx context.Context, cfg *config.Config, st store.Store, accept bool) ([]string, error) {
	if cfg.Database.Tenancy != "multi" {
		if accept {
			return nil, errors.New("accept-replica-config applies only to multi-tenant PostgreSQL; single mode is unchanged")
		}
		return nil, nil
	}
	ed, err := editor.FromEnv(cfg.Server.AppURL, cfg.Server.Environment)
	if err != nil {
		return nil, err
	}
	current := config.ReplicaFingerprint(cfg)
	maps.Copy(current, ed.ReplicaFingerprint(cfg.Security.EncryptionKey))
	return st.UpdateReplicaConfig(ctx, current, accept)
}

func acceptReplicaConfig(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("accept-replica-config takes no arguments")
	}
	cfg, st, err := openExisting(ctx) // never mint retained keys; apply effective installer config
	if err != nil {
		return err
	}
	defer st.Close()
	changed, err := replicaConfig(ctx, cfg, st, true)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Replica configuration accepted. Changed fields: "+strings.Join(changed, ", "))
	return err
}
