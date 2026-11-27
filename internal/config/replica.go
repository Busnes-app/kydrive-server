package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
)

// ReplicaConfigSetting is private coordination state, cleared after restore.
const ReplicaConfigSetting = "replica_config_fingerprint"
const EncryptionKeyField = "KY_ENCRYPTION_KEY"

// ReplicaField fingerprints a named shared value without exposing it to a database reader.
// The product label also separates this from every other use of the retained deployment key.
func ReplicaField(key []byte, name, value string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("kydrive:replica-config:" + name + "\x00" + value))
	return hex.EncodeToString(m.Sum(nil))
}

// ReplicaFingerprint covers effective shared config; editor adds its validated settings at
// the command boundary. DataDir, bind addresses, pools, proxies, certificate/secret file
// paths and local backup directories belong to each host, not to the shared database.
func ReplicaFingerprint(c *Config) map[string]string {
	b := strconv.FormatBool
	scim := ""
	if c.SCIM.Enabled {
		scim = c.SCIM.BearerToken
	}
	fields := map[string]string{
		EncryptionKeyField:                 "",
		"KY_APP_NAME":                      c.Server.AppName,
		"KY_APP_URL":                       c.Server.AppURL,
		"KY_ENV":                           c.Server.Environment,
		"KY_SESSION_SECRET":                c.Security.SessionSecret,
		"SESSION_TTL":                      c.Security.SessionTTL.String(),
		"KY_COOKIE_SECURE":                 b(c.Security.CookieSecure),
		"KY_COOKIE_DOMAIN":                 c.Security.CookieDomain,
		"KYDRIVE_IDENTITY_KEY_FILE":        string(c.Security.IdentityKey),
		"KYDRIVE_TLS_PIN_PROBE":            b(c.Security.TLSPinProbe),
		"KY_SCIM_ENABLED":                  b(c.SCIM.Enabled),
		"KY_SCIM_TOKEN":                    scim,
		"KY_SSO_ENABLED":                   b(c.SSO.Enabled),
		"KY_KYSIGNON_ISSUER":               c.SSO.KySignOnIssuer,
		"KY_KYSIGNON_CLIENT_ID":            c.SSO.KySignOnClientID,
		"KY_KYSIGNON_SECRET":               c.SSO.KySignOnSecret,
		"KY_KYSIGNON_HMAC_SECRET":          c.SSO.KySignOnHMACSecret,
		"KY_SSO_AUTO_PROVISION":            b(c.SSO.AutoProvision),
		"KY_CAPTCHA_PROVIDER":              c.Captcha.Provider,
		"KY_CAPTCHA_SITE_KEY":              c.Captcha.SiteKey,
		"KY_CAPTCHA_SECRET_KEY":            c.Captcha.SecretKey,
		"KY_CAPTCHA_POW_DIFFICULTY":        strconv.Itoa(c.Captcha.DifficultyPoW),
		"KY_RATE_LIMIT_EXEMPT_NETWORKS":    c.Security.RateLimitExemptNetworks,
		"KY_BACKUP_DEPOSIT_INTERVAL":       c.Backup.DepositInterval.String(),
		"KY_BACKUP_ALLOW_PRIVATE_RECOVERY": b(c.Backup.AllowPrivateRecovery),
		"KYDRIVE_KYPOST_URL":               c.Drive.KyPostURL,
		"KYDRIVE_KYPOST_SECRET_FILE":       string(c.Drive.KyPostSecret),
		"KYDRIVE_TENANCY":                  c.Database.Tenancy,
		"KYDRIVE_STAFF_ORG":                c.Database.StaffOrg,
		"KYDRIVE_MAX_FILE_SIZE":            strconv.FormatInt(c.Drive.MaxFileSize, 10),
		"KYDRIVE_MAX_ZIP_SIZE":             strconv.FormatInt(c.Drive.MaxZipSize, 10),
		"KYDRIVE_CONTENT_SEARCH":           b(c.Drive.ContentSearch),
	}
	cells := slices.Clone(c.Database.Cells)
	slices.Sort(cells)
	fields["KY_CELLS"] = strings.Join(cells, ",")
	fields["KYDRIVE_S3_ENDPOINT"] = c.ObjectStorage.Endpoint
	fields["KYDRIVE_S3_BUCKET"] = c.ObjectStorage.Bucket
	fields["KYDRIVE_S3_REGION"] = c.ObjectStorage.Region
	fields["KYDRIVE_S3_PATH_STYLE"] = b(c.ObjectStorage.PathStyle)
	for name, value := range fields {
		fields[name] = ReplicaField(c.Security.EncryptionKey, name, value)
	}
	return fields
}

// DiffFingerprint reports known differing names only, with the deployment key alone when
// it differs. New fields can be recorded during rolling upgrades; obsolete fields are ignored.
func DiffFingerprint(stored, current map[string]string) []string {
	if !hmac.Equal([]byte(stored[EncryptionKeyField]), []byte(current[EncryptionKeyField])) {
		return []string{EncryptionKeyField}
	}
	var diff []string
	for name, value := range current {
		if old, ok := stored[name]; ok && !hmac.Equal([]byte(old), []byte(value)) {
			diff = append(diff, name)
		}
	}
	slices.Sort(diff)
	return diff
}
