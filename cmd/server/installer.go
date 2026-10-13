package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func runInstaller(args []string) error {
	opts, err := applysetup.ParsePreparation(args)
	if err != nil {
		return err
	}
	if opts.Capabilities {
		return applysetup.WriteCapabilities(os.Stdout, "kydrive", "kyidentity-oidc", "scim", "dedicated-admin", "local-backup-config", "bundle-preparation", "check", "restart")
	}
	dir := os.Getenv("KY_DATA_DIR")
	if dir == "" {
		dir = "./data"
	}
	if opts.Stage {
		return prepareInstaller(dir, opts.File, os.Stdin, os.Stdout, opts.Check)
	}
	socket, err := config.InstallerSocket(dir)
	if err != nil {
		return err
	}
	if opts.Restart {
		return applysetup.RequestRestart(context.Background(), socket)
	}
	var b applysetup.Common
	if err = applysetup.Decode(opts.File, &b); err != nil {
		return err
	}
	if err = b.Validate("kydrive", "/api/sso/kysignon/callback"); err != nil {
		return err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return errors.New("invalid setup bundle")
	}
	r, err := applysetup.CallLocal(context.Background(), socket, raw, opts.Check)
	if err != nil {
		return err
	}
	if r.Service != b.Service || r.SetupID != b.SetupID {
		return errors.New("product setup report identity mismatch")
	}
	return r.Write(os.Stdout)
}

func loadInstallerConfiguration(ctx context.Context, cfg *config.Config, st store.Store) error {
	raw, err := st.Settings().GetSetting(ctx, store.InstallerConfigKey)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var b applysetup.Common
	if err = applysetup.DecodeRaw([]byte(raw), &b); err != nil {
		return err
	}
	return config.ApplyInstallerConfiguration(cfg, b)
}

func prepareInstaller(dir, file string, input io.Reader, output io.Writer, check bool) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return errors.New("invalid product data directory")
	}
	raw, err := io.ReadAll(io.LimitReader(input, applysetup.MaxBundleBytes+1))
	if err != nil || len(raw) > applysetup.MaxBundleBytes {
		return errors.New("preparation metadata exceeds limit")
	}
	var b applysetup.Common
	if err := applysetup.DecodeRaw(raw, &b); err != nil {
		return errors.New("invalid secret-free preparation metadata")
	}
	if err := b.ValidatePreparation("kydrive", "/api/sso/kysignon/callback", file); err != nil {
		return err
	}
	if b.SSO != nil && (b.SSO.SCIMTokenFile == "" || b.SSO.DirectorySecretFile != "") {
		return errors.New("drive preparation requires native SCIM credential references")
	}
	if b.Backup != nil && b.Backup.Recovery != nil {
		return errors.New("installer recovery pairing is not yet supported")
	}
	canonical, err := json.Marshal(b)
	if err != nil {
		return errors.New("cannot encode preparation metadata")
	}
	status, err := applysetup.Stage(dir, file, b.SetupID, canonical, check)
	if err != nil {
		return err
	}
	return applysetup.WritePreparation(output, "kydrive", b.SetupID, status)
}
