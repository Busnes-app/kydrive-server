package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/ky-primitives/keyfile"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

// Config encapsulates all runtime configuration for kydrive-server.
type Config struct {
	Server   ServerConfig   `json:"server"`
	Database DatabaseConfig `json:"database"`
	Security SecurityConfig `json:"security"`
	SSO      SSOConfig      `json:"sso"`
	SCIM     SCIMConfig     `json:"scim"`
	Backup   BackupConfig   `json:"backup"`
	Captcha  CaptchaConfig  `json:"captcha"`
	Drive    DriveConfig    `json:"drive"`
}

// DriveConfig bounds resumable uploads and folder zip downloads, in bytes. ContentSearch
// (KYDRIVE_CONTENT_SEARCH, default on) runs the search index worker and matches file contents.
type DriveConfig struct {
	MaxFileSize   int64 `json:"max_file_size"`
	MaxZipSize    int64 `json:"max_zip_size"`
	ContentSearch bool  `json:"content_search"`
}

// ServerConfig defines HTTP and network settings.
type ServerConfig struct {
	Host         string        `json:"host"`
	Port         int           `json:"port"`
	AppURL       string        `json:"app_url"`
	AppName      string        `json:"app_name"`
	ReadTimeout  time.Duration `json:"read_timeout"`
	WriteTimeout time.Duration `json:"write_timeout"`
	Environment  string        `json:"environment"`
	// ExcalidrawFontsDir holds the CJK whiteboard fonts kept out of the binary; empty serves none.
	ExcalidrawFontsDir string `json:"excalidraw_fonts_dir"`
}

// DatabaseConfig holds connection settings for pluggable storage (SQLite, PostgreSQL, MySQL).
type DatabaseConfig struct {
	Driver          string        `json:"driver"` // "sqlite", "postgres", "mysql"
	DSN             string        `json:"dsn"`    // Connection string or file path
	DataDir         string        `json:"data_dir"`
	MaxOpenConns    int           `json:"max_open_conns"`
	MaxIdleConns    int           `json:"max_idle_conns"`
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"`
}

// SecurityConfig holds encryption keys, cookie secrets, and session settings.
type SecurityConfig struct {
	SessionSecret string `json:"session_secret"`
	EncryptionKey []byte `json:"-"` // 32 bytes for AES-256-GCM; never serialised
	CookieSecure  bool   `json:"cookie_secure"`
	CookieDomain  string `json:"cookie_domain"`
	SessionTTL    time.Duration
	// TrustedProxies is the parsed KY_TRUSTED_PROXIES allowlist. Only a request whose peer
	// address falls inside it may speak for a client other than itself.
	TrustedProxies []netip.Prefix `json:"-"`
	// KeyInDataDir is set while <DataDir>/encryption.key exists, whether or not the key was
	// read from it, or while <DataDir>/drill holds residue of an interrupted recovery drill
	// (its opened capsule carries the key): a copy of the data directory then decrypts itself.
	KeyInDataDir bool `json:"-"`
}

// SSOConfig holds identity provider and federation parameters.
type SSOConfig struct {
	Enabled            bool   `json:"enabled"`
	KySignOnIssuer     string `json:"kysignon_issuer"`
	KySignOnClientID   string `json:"kysignon_client_id"`
	KySignOnSecret     string `json:"kysignon_secret"`
	KySignOnHMACSecret string `json:"kysignon_hmac_secret"`
	GenericOIDCIssuer  string `json:"generic_oidc_issuer"`
	SAMLMetadataURL    string `json:"saml_metadata_url"`
	AutoProvision      bool   `json:"auto_provision"`
}

// SCIMConfig holds settings for RFC 7643/7644 inbound user provisioning.
type SCIMConfig struct {
	Enabled     bool   `json:"enabled"`
	BearerToken string `json:"bearer_token"`
}

// BackupConfig holds parameters for KyBackup capsules & recovery drills.
type BackupConfig struct {
	Dir string `json:"dir"`
	// Keep is how many capsule generations recoveryclient retains; it refuses values below 1.
	Keep int `json:"keep"`
	// DepositInterval is how often a paired instance seals and deposits a capsule to
	// KyRecovery. Zero disables the schedule; deposits then happen only on request.
	DepositInterval time.Duration `json:"deposit_interval"`
	// AllowPrivateRecovery admits private and CGNAT KyRecovery destinations (HTTPS still
	// required). Off by default: KyRecovery destinations must be public.
	AllowPrivateRecovery bool `json:"allow_private_recovery"`
	// CollectTimeout bounds one payload collection: database snapshot, blob verification and
	// the Restic bulk snapshot. Zero is unbounded (literal test configs only).
	CollectTimeout time.Duration `json:"collect_timeout"`
	// DrillDir is the restore drill's scratch root; DrillTimeout bounds one drill (zero is unbounded).
	DrillDir     string        `json:"drill_dir"`
	DrillTimeout time.Duration `json:"drill_timeout"`
}

// CaptchaConfig holds anti-abuse settings (PoW default, Turnstile, Friendly).
type CaptchaConfig struct {
	Provider      string `json:"provider"` // "pow", "turnstile", "friendly", "none"
	SiteKey       string `json:"site_key"`
	SecretKey     string `json:"secret_key"`
	DifficultyPoW int    `json:"difficulty_pow"`
}

// MinDepositInterval is the shortest schedule accepted: each run snapshots the whole database
// and uploads it, and KyRecovery admits 60 deposits per token per 15 minutes.
const MinDepositInterval = 15 * time.Minute

// DefaultAppName is the service name an unconfigured instance runs under. Capsules are sealed
// under it, so the restore CLI has to agree with it without loading a whole Config.
const DefaultAppName = "KyDrive"

// LoadFromEnv initializes a Config struct populated from environment variables with sensible defaults.
func LoadFromEnv() (*Config, error) { return load(false) }

// LoadExisting is LoadFromEnv for operator commands on an existing deployment: a missing
// keyfile or SQLite database is refused, never created.
func LoadExisting() (*Config, error) {
	cfg, err := load(true)
	if err != nil || cfg.Database.Driver != "sqlite" {
		return cfg, err
	}
	path, _, _ := strings.Cut(strings.TrimPrefix(cfg.Database.DSN, "file:"), "?")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no existing database at %s (check KY_DATA_DIR/KY_DB_DSN): %w", path, err)
	}
	return cfg, nil
}

func load(existing bool) (*Config, error) {
	for _, key := range placeholderChecked {
		if strings.Contains(strings.ToLower(os.Getenv(key)), "replace-with") {
			return nil, fmt.Errorf("%s still holds the example placeholder; generate a value (openssl rand -hex 32)", key)
		}
	}
	var invalid []string
	envInt := func(key string, def int) int {
		v, err := getEnvInt(key, def)
		if err != nil {
			invalid = append(invalid, key)
		}
		return v
	}
	envBool := func(key string, def bool) bool {
		v, err := getEnvBool(key, def)
		if err != nil {
			invalid = append(invalid, key)
		}
		return v
	}

	port := envInt("KY_PORT", envInt("PORT", 8080))
	host := getEnv("KY_HOST", "0.0.0.0")
	appURL := getEnv("KY_APP_URL", fmt.Sprintf("http://localhost:%d", port))
	appName := getEnv("KY_APP_NAME", DefaultAppName)
	env := getEnv("KY_ENV", "development")
	if env != "production" && env != "development" {
		return nil, fmt.Errorf("KY_ENV must be production or development, got %q", env)
	}

	driver := strings.ToLower(getEnv("KY_DB_DRIVER", "sqlite"))
	dataDir := getEnv("KY_DATA_DIR", "./data")
	defaultDSN := fmt.Sprintf("%s/ky_server.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dataDir)
	if driver == "postgres" || driver == "postgresql" {
		driver = "postgres"
		defaultDSN = "postgres://postgres:postgres@localhost:5432/ky_server?sslmode=disable"
	}
	dsn := getEnv("KY_DB_DSN", defaultDSN)

	sessionSecret := getEnv("KY_SESSION_SECRET", "")
	if env == "production" && sessionSecret == "" {
		return nil, fmt.Errorf("KY_SESSION_SECRET is required in production")
	}
	if sessionSecret == "" {
		sessionSecret = generateRandomHex(32)
	}
	if len(sessionSecret) < crypto.MinHMACKeyBytes {
		return nil, fmt.Errorf("KY_SESSION_SECRET must contain at least %d bytes", crypto.MinHMACKeyBytes)
	}
	kysignonHMAC := getEnv("KY_KYSIGNON_HMAC_SECRET", "")
	if kysignonHMAC != "" && len(kysignonHMAC) < crypto.MinHMACKeyBytes {
		return nil, fmt.Errorf("KY_KYSIGNON_HMAC_SECRET must contain at least %d bytes", crypto.MinHMACKeyBytes)
	}

	encryptionKey, ok, err := keyfile.FromEnv("KY_ENCRYPTION_KEY", 32)
	if err != nil {
		return nil, fmt.Errorf("KY_ENCRYPTION_KEY: %w", err)
	}
	keyPath := filepath.Join(dataDir, "encryption.key")
	if !ok && existing {
		if encryptionKey, err = keyfile.Load(keyPath, 32); err != nil {
			return nil, fmt.Errorf("no existing deployment key at %s (check KY_DATA_DIR or set KY_ENCRYPTION_KEY): %w", keyPath, err)
		}
	} else if !ok {
		encryptionKey, err = keyfile.LoadOrCreate(keyPath, 32)
		if err != nil {
			return nil, fmt.Errorf("encryption key: %w", err)
		}
	}
	_, statErr := os.Lstat(keyPath)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("encryption key: %w", statErr)
	}
	drillDir := getEnv("KY_DRILL_DIR", filepath.Join(dataDir, "drill"))
	if !filepath.IsAbs(drillDir) && getEnv("KY_DRILL_DIR", "") != "" {
		return nil, fmt.Errorf("KY_DRILL_DIR must be an absolute path")
	}
	drillResidue, err := os.ReadDir(drillDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("recovery drill directory: %w", err)
	}
	collectTimeout, err := getEnvDuration("KY_BACKUP_COLLECT_TIMEOUT", 2*time.Hour)
	if err != nil || collectTimeout < time.Minute {
		return nil, fmt.Errorf("KY_BACKUP_COLLECT_TIMEOUT must be a duration of at least 1m")
	}
	drillTimeout, err := getEnvDuration("KY_DRILL_TIMEOUT", 2*time.Hour)
	if err != nil || drillTimeout < time.Minute {
		return nil, fmt.Errorf("KY_DRILL_TIMEOUT must be a duration of at least 1m")
	}

	depositInterval, err := getEnvDuration("KY_BACKUP_DEPOSIT_INTERVAL", 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("KY_BACKUP_DEPOSIT_INTERVAL: %w", err)
	}
	if depositInterval != 0 && depositInterval < MinDepositInterval {
		return nil, fmt.Errorf("KY_BACKUP_DEPOSIT_INTERVAL: %s is below the %s minimum (0 disables)", depositInterval, MinDepositInterval)
	}

	backupKeep := envInt("KY_BACKUP_KEEP", 7)
	if backupKeep < 1 {
		return nil, fmt.Errorf("KY_BACKUP_KEEP: must be at least 1, got %d", backupKeep)
	}

	trustedProxies, err := ParseTrustedProxies(getEnv("KY_TRUSTED_PROXIES", ""))
	if err != nil {
		return nil, fmt.Errorf("KY_TRUSTED_PROXIES: %w", err)
	}

	readTimeout, err := getEnvDuration("KY_READ_TIMEOUT", 5*time.Minute)
	if err != nil || readTimeout < 15*time.Second || readTimeout > 15*time.Minute {
		return nil, fmt.Errorf("KY_READ_TIMEOUT must be between 15s and 15m")
	}
	writeTimeout, err := getEnvDuration("KY_WRITE_TIMEOUT", 5*time.Minute)
	if err != nil || writeTimeout < 15*time.Second || writeTimeout > 15*time.Minute {
		return nil, fmt.Errorf("KY_WRITE_TIMEOUT must be between 15s and 15m")
	}
	maxFile, err := getEnvSize("KYDRIVE_MAX_FILE_SIZE", 10<<30)
	if err != nil {
		return nil, err
	}
	maxZip, err := getEnvSize("KYDRIVE_MAX_ZIP_SIZE", 10<<30)
	if err != nil {
		return nil, err
	}
	fontsDir := getEnv("KY_EXCALIDRAW_FONTS_DIR", "")
	if fontsDir != "" {
		if info, err := os.Stat(fontsDir); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("KY_EXCALIDRAW_FONTS_DIR: %s is not a directory", fontsDir)
		}
	}
	cfg := &Config{
		Server: ServerConfig{
			Host:               host,
			Port:               port,
			AppURL:             strings.TrimRight(appURL, "/"),
			AppName:            appName,
			ReadTimeout:        readTimeout,
			WriteTimeout:       writeTimeout,
			Environment:        env,
			ExcalidrawFontsDir: fontsDir,
		},
		Database: DatabaseConfig{
			Driver:          driver,
			DSN:             dsn,
			DataDir:         dataDir,
			MaxOpenConns:    envInt("KY_DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    envInt("KY_DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: 15 * time.Minute,
		},
		Security: SecurityConfig{
			SessionSecret:  sessionSecret,
			EncryptionKey:  encryptionKey,
			KeyInDataDir:   statErr == nil || len(drillResidue) > 0,
			CookieSecure:   envBool("KY_COOKIE_SECURE", env == "production"),
			CookieDomain:   getEnv("KY_COOKIE_DOMAIN", ""),
			SessionTTL:     7 * 24 * time.Hour,
			TrustedProxies: trustedProxies,
		},
		SSO: SSOConfig{
			Enabled:            envBool("KY_SSO_ENABLED", true),
			KySignOnIssuer:     getEnv("KY_KYSIGNON_ISSUER", ""),
			KySignOnClientID:   getEnv("KY_KYSIGNON_CLIENT_ID", ""),
			KySignOnSecret:     getEnv("KY_KYSIGNON_SECRET", ""),
			KySignOnHMACSecret: kysignonHMAC,
			GenericOIDCIssuer:  getEnv("KY_OIDC_ISSUER", ""),
			SAMLMetadataURL:    getEnv("KY_SAML_METADATA_URL", ""),
			AutoProvision:      envBool("KY_SSO_AUTO_PROVISION", false),
		},
		SCIM: SCIMConfig{
			Enabled:     envBool("KY_SCIM_ENABLED", true),
			BearerToken: getEnv("KY_SCIM_TOKEN", generateRandomHex(24)),
		},
		Backup: BackupConfig{
			Dir:                  getEnv("KY_BACKUP_DIR", ""),
			Keep:                 backupKeep,
			DepositInterval:      depositInterval,
			AllowPrivateRecovery: envBool("KY_BACKUP_ALLOW_PRIVATE_RECOVERY", false),
			CollectTimeout:       collectTimeout,
			DrillDir:             filepath.Clean(drillDir),
			DrillTimeout:         drillTimeout,
		},
		Captcha: CaptchaConfig{
			Provider:      getEnv("KY_CAPTCHA_PROVIDER", "pow"),
			SiteKey:       getEnv("KY_CAPTCHA_SITE_KEY", ""),
			SecretKey:     getEnv("KY_CAPTCHA_SECRET_KEY", ""),
			DifficultyPoW: envInt("KY_CAPTCHA_POW_DIFFICULTY", 100000),
		},
		Drive: DriveConfig{MaxFileSize: maxFile, MaxZipSize: maxZip, ContentSearch: envBool("KYDRIVE_CONTENT_SEARCH", true)},
	}

	if len(invalid) > 0 {
		return nil, fmt.Errorf("unparseable value for %s", strings.Join(invalid, ", "))
	}

	if file := os.Getenv("KY_SCIM_TOKEN_FILE"); file != "" {
		token, err := applysetup.Secret(file)
		if err != nil {
			return nil, err
		}
		if explicit := os.Getenv("KY_SCIM_TOKEN"); explicit != "" && explicit != token {
			return nil, fmt.Errorf("conflicting SCIM token sources")
		}
		cfg.SCIM.BearerToken = token
	}
	if env == "production" && cfg.SCIM.Enabled && os.Getenv("KY_SCIM_TOKEN") == "" && os.Getenv("KY_SCIM_TOKEN_FILE") == "" {
		return nil, fmt.Errorf("KY_SCIM_TOKEN is required for production provisioning")
	}
	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

// placeholderChecked are secrets the example env files ship as "replace-with-..." text.
var placeholderChecked = []string{"KY_SESSION_SECRET", "KY_KYSIGNON_SECRET", "KY_KYSIGNON_HMAC_SECRET", "KY_SCIM_TOKEN", "KYDRIVE_EDITOR_SECRET", "KY_ADMIN_PASSWORD"}

// getEnvInt treats a blank value as unset; anything else must parse.
func getEnvInt(key string, defaultVal int) (int, error) {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal, err
	}
	return n, nil
}

func getEnvBool(key string, defaultVal bool) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return defaultVal, nil
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return defaultVal, fmt.Errorf("%s is not a boolean", key)
}

// getEnvDuration parses a Go duration such as "24h" or "90m". Negative is refused; "0" disables.
func getEnvDuration(key string, defaultVal time.Duration) (time.Duration, error) {
	val, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(val) == "" {
		return defaultVal, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(val))
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("%s is negative", val)
	}
	return d, nil
}

// getEnvSize parses a whole number of MiB, GiB or TiB between 1 MiB and 1 TiB.
func getEnvSize(key string, defaultVal int64) (int64, error) {
	val := getEnv(key, "")
	if val == "" {
		return defaultVal, nil
	}
	for unit, shift := range map[string]int{"MiB": 20, "GiB": 30, "TiB": 40} {
		if n, err := strconv.ParseInt(strings.TrimSuffix(val, unit), 10, 64); err == nil && strings.HasSuffix(val, unit) && n >= 1 && n <= 1<<(40-shift) {
			return n << shift, nil
		}
	}
	return 0, fmt.Errorf("%s must be a whole number of MiB, GiB or TiB between 1MiB and 1TiB, got %q", key, val)
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b) // never returns an error since Go 1.24; it crashes the program instead
	return hex.EncodeToString(b)
}

// ParseTrustedProxies parses a comma-separated list of IPs and CIDR blocks into prefixes.
// A bare IP becomes a single-address prefix. An unparsable entry is a startup error: a
// silently dropped proxy would make the server ignore its X-Forwarded-For and lump every
// client behind it into one rate-limit bucket.
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if strings.Contains(field, "/") {
			prefix, err := netip.ParsePrefix(field)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", field, err)
			}
			// Checked after unmapping: a mapped form like ::ffff:0.0.0.0/96 has Bits() == 96
			// here but means 0.0.0.0/0 once rewritten, and would otherwise slip the guard.
			masked := unmapPrefix(prefix).Masked()
			if masked.Bits() == 0 {
				return nil, fmt.Errorf("trusted proxy %q trusts every address; list the proxy's own address or subnet instead", field)
			}
			out = append(out, masked)
			continue
		}
		addr, err := netip.ParseAddr(field)
		if err != nil {
			return nil, fmt.Errorf("invalid IP %q: %w", field, err)
		}
		addr = addr.Unmap()
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// unmapPrefix rewrites an IPv4-mapped IPv6 prefix as the plain IPv4 one it means. Peers are
// unmapped before the allowlist is consulted, and netip.Prefix.Contains never matches across
// families, so a mapped entry would otherwise match nothing at all.
func unmapPrefix(p netip.Prefix) netip.Prefix {
	if !p.Addr().Is4In6() || p.Bits() < 96 {
		return p
	}
	return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
}
