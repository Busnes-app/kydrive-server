package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/Busnes-app/ky-primitives/applysetup"
)

// InstallerSocket is private IPC; it is never mounted on the public HTTP router.
func InstallerSocket(dataDir string) (string, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", errors.New("invalid installer data directory")
	}
	return filepath.Join(dir, "installer", "setup.sock"), nil
}

// ApplyInstallerConfiguration narrows an already validated setup into runtime
// config. Nonempty operator SSO settings are immutable to the installer.
func ApplyInstallerConfiguration(cfg *Config, b applysetup.Common) error {
	if err := b.Validate("kydrive", "/api/sso/kysignon/callback"); err != nil {
		return err
	}
	if s := b.SSO; s != nil {
		// SSO defaults on, so disabled means the operator chose KY_SSO_ENABLED=false.
		if !cfg.SSO.Enabled {
			return errors.New("installer refuses to enable SSO the operator disabled")
		}
		if s.RedirectURI != cfg.Server.AppURL+"/api/sso/kysignon/callback" {
			return errors.New("installer callback does not match the configured product URL")
		}
		if s.DirectorySecretFile != "" || s.SCIMTokenFile == "" {
			return errors.New("KyDrive setup requires SCIM provisioning, not legacy directory webhooks")
		}
		secret, err := applysetup.Secret(s.ClientSecretFile)
		if err != nil {
			return err
		}
		token, err := applysetup.Secret(s.SCIMTokenFile)
		if err != nil {
			return err
		}
		if !cfg.SCIM.Enabled || cfg.SCIM.BearerToken != token {
			return errors.New("configure the protected SCIM token before applying setup")
		}
		if cfg.SSO.AutoProvision || cfg.SSO.KySignOnHMACSecret != "" || cfg.SSO.GenericOIDCIssuer != "" || cfg.SSO.SAMLMetadataURL != "" {
			return errors.New("installer refuses conflicting authentication or automatic provisioning")
		}
		for _, pair := range [][2]string{{cfg.SSO.KySignOnIssuer, s.IssuerURL}, {cfg.SSO.KySignOnClientID, s.ClientID}, {cfg.SSO.KySignOnSecret, secret}} {
			if pair[0] != "" && pair[0] != pair[1] {
				return errors.New("installer refuses to replace configured SSO values")
			}
		}
		cfg.SSO.Enabled = true
		cfg.SSO.KySignOnIssuer = s.IssuerURL
		cfg.SSO.KySignOnClientID = s.ClientID
		cfg.SSO.KySignOnSecret = secret
	}
	if (len(b.Admins) > 0 || len(b.Everyday) > 0) && (cfg.SSO.KySignOnIssuer == "" || !cfg.SCIM.Enabled) {
		return errors.New("directory identities require configured KyIdentity and SCIM")
	}
	for _, list := range [][]applysetup.Identity{b.Admins, b.Everyday} {
		for _, id := range list {
			if id.Issuer != cfg.SSO.KySignOnIssuer {
				return errors.New("identity issuer differs from active KyIdentity")
			}
		}
	}
	if backup := b.Backup; backup != nil {
		if backup.Recovery != nil {
			return errors.New("installer recovery pairing is not yet supported")
		}
		if backup.Directory != "" {
			if cfg.Backup.Dir != "" && cfg.Backup.Dir != backup.Directory {
				return errors.New("installer refuses to replace configured backup directory")
			}
			cfg.Backup.Dir = backup.Directory
		}
		if backup.Keep != 0 {
			if os.Getenv("KY_BACKUP_KEEP") != "" && cfg.Backup.Keep != backup.Keep {
				return errors.New("installer refuses to replace configured retention")
			}
			cfg.Backup.Keep = backup.Keep
		}
		if backup.Interval != "" {
			d, err := applysetup.Interval(backup.Interval)
			if err != nil {
				return err
			}
			if os.Getenv("KY_BACKUP_DEPOSIT_INTERVAL") != "" && cfg.Backup.DepositInterval != d {
				return errors.New("installer refuses to replace configured backup interval")
			}
			cfg.Backup.DepositInterval = d
		}
	}
	return nil
}
