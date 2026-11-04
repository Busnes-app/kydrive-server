package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kydrive-server/internal/config"
	_ "modernc.org/sqlite"
)

// encryptionKeyPath is where a restore drops the key that decrypts users.totp_secret_enc,
// relative to the restore target: the same <DataDir>/encryption.key config.LoadFromEnv reads.
const encryptionKeyPath = "data/encryption.key"

// recoveryPubPath is where a restore drops the suite recovery public key, matching the
// <DataDir>/recovery.pub that recoveryclient.RecoveryKeyPath reads.
const recoveryPubPath = "data/recovery.pub"

// dbName is the restored database file; dbPath is its capsule member when it fits in one.
const (
	dbName = "ky_server.db"
	dbPath = "data/" + dbName
)

// dbPartBytes is the most of a snapshot one capsule member carries. A larger snapshot is
// sealed as ordered parts (ky_server.db.000-of-NNN, ...) that JoinDatabase reassembles.
// The library limit is never raised; tests lower this instead of writing 64 MiB fixtures.
var dbPartBytes = int(capsule.MaxFileBytes)

// warnBytes is 80% of the tar stream one capsule may hold (capsule.MaxExpandedBytes).
const warnBytes = capsule.MaxExpandedBytes / 5 * 4

// ErrDatabaseParts refuses a split database that is incomplete, out of order or beside a
// whole one.
var ErrDatabaseParts = errors.New("backup: database parts are incomplete or inconsistent")

// ErrNoDatabaseSnapshot is returned when the payload cannot carry a consistent copy of the
// database, so a capsule without one is never sealed as if it were a backup.
var ErrNoDatabaseSnapshot = errors.New("backup: no consistent database snapshot for this driver")

// Collect assembles the payload every sealing caller uses: the local application files
// (SQLite database, configuration) plus the members that may only ever travel inside a
// sealed capsule (the encryption key, the pinned recovery public key). Nothing that returns
// from here may leave the process except through a Sealer. purpose tags the bulk snapshot.
func Collect(ctx context.Context, cfg *config.Config, appVersion string, purpose Purpose) (recoveryclient.Payload, error) {
	// recoveryclient.Run calls its collector without a context; the bound lives here.
	if cfg.Backup.CollectTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Backup.CollectTimeout)
		defer cancel()
	}
	if strings.ToLower(cfg.Database.Driver) != "sqlite" {
		return recoveryclient.Payload{}, fmt.Errorf("%w: %s", ErrNoDatabaseSnapshot, cfg.Database.Driver)
	}
	dbBytes, err := snapshotSQLite(ctx, cfg.Database.DSN, cfg.Database.DataDir)
	if err != nil {
		return recoveryclient.Payload{}, err
	}
	files := dbMembers(dbBytes)
	// The integrity check runs on the database the drill reassembles, never on a part.
	sqlitePaths := []string{dbPath}

	cfgJSON, err := json.MarshalIndent(map[string]any{
		"server":   cfg.Server,
		"database": map[string]any{"driver": cfg.Database.Driver},
	}, "", "  ")
	if err != nil {
		return recoveryclient.Payload{}, err
	}
	files = append(files, recoveryclient.File{Path: "config/settings.json", Data: cfgJSON, Mode: 0600})

	integrations := map[string]string{"KY_SESSION_SECRET": cfg.Security.SessionSecret, "KY_SSO_ENABLED": strconv.FormatBool(cfg.SSO.Enabled), "KY_SSO_AUTO_PROVISION": strconv.FormatBool(cfg.SSO.AutoProvision), "KY_KYSIGNON_ISSUER": cfg.SSO.KySignOnIssuer, "KY_KYSIGNON_CLIENT_ID": cfg.SSO.KySignOnClientID, "KY_KYSIGNON_SECRET": cfg.SSO.KySignOnSecret, "KY_KYSIGNON_HMAC_SECRET": cfg.SSO.KySignOnHMACSecret, "KY_SCIM_ENABLED": strconv.FormatBool(cfg.SCIM.Enabled), "KY_SCIM_TOKEN": cfg.SCIM.BearerToken, "KY_APP_URL": cfg.Server.AppURL, "KY_APP_NAME": cfg.Server.AppName, "KY_BACKUP_ALLOW_PRIVATE_RECOVERY": strconv.FormatBool(cfg.Backup.AllowPrivateRecovery)}
	for _, name := range []string{"KYDRIVE_EDITOR_URL", "KYDRIVE_EDITOR_INTERNAL_URL", "KYDRIVE_EDITOR_DRIVE_URL", "KYDRIVE_EDITOR_SECRET", "KYDRIVE_IDENTITY_URL"} {
		integrations[name] = os.Getenv(name)
	}
	if os.Getenv("KYDRIVE_BULK_BACKUP_REPOSITORY") != "" {
		repo, err := bulkRepository(cfg.Database.DataDir)
		if err != nil {
			return recoveryclient.Payload{}, err
		}
		integrations[capsuleRepositoryKey] = repo.URL
		integrations[capsuleCredentialsKey] = strings.Join(repo.Env, "\n")
	}
	sealedConfig, err := json.Marshal(integrations)
	if err != nil {
		return recoveryclient.Payload{}, err
	}
	files = append(files, recoveryclient.File{Path: "config/integrations.json", Data: sealedConfig, Mode: 0600})
	if len(cfg.Security.EncryptionKey) != 32 {
		return recoveryclient.Payload{}, fmt.Errorf("backup: encryption key is %d bytes, want 32; refusing to seal a capsule that cannot decrypt what it restores", len(cfg.Security.EncryptionKey))
	}
	files = append(files, recoveryclient.File{
		Path: encryptionKeyPath,
		Data: []byte(hex.EncodeToString(cfg.Security.EncryptionKey) + "\n"),
		Mode: 0600,
	})

	if pub, err := os.ReadFile(recoveryclient.RecoveryKeyPath(cfg.Database.DataDir)); err == nil {
		files = append(files, recoveryclient.File{Path: recoveryPubPath, Data: pub, Mode: 0600})
	} else if !errors.Is(err, os.ErrNotExist) {
		return recoveryclient.Payload{}, err
	}

	bulk, err := CollectBulk(ctx, cfg.Database.DataDir, dbBytes, cfg.Security.EncryptionKey, purpose)
	if err != nil {
		return recoveryclient.Payload{}, err
	}
	if len(bulk) > 0 {
		files = append(files, recoveryclient.File{Path: BulkManifestPath, Data: bulk, Mode: 0600})
	}
	var total int64
	for _, f := range files {
		total += int64(len(f.Data))
	}
	if w := sizeWarning(total); w != "" {
		log.Printf("[BACKUP] warning: %s", w)
	}
	payload := recoveryclient.Payload{
		ServiceName: cfg.Server.AppName,
		AppVersion:  appVersion,
		Files:       files,
		Dependencies: map[string]any{
			"ports": []int{cfg.Server.Port},
			"env":   []string{"KY_PORT", "KY_DB_DRIVER"},
		},
		VerificationRecipe: map[string]any{
			"check_sqlite_integrity": true,
			"sqlite_paths":           sqlitePaths,
			"required_files":         requiredFiles(files),
			"expected_env":           []string{"KY_PORT", "KY_DB_DRIVER"},
			"expected_ports":         []int{cfg.Server.Port},
		},
	}
	return payload, nil
}

// snapshotSQLite returns a consistent single-file copy of the live database. The store runs
// in WAL mode, so reading the main file misses every commit still in the -wal and can tear
// under a concurrent checkpoint; the lib's SQLiteSnapshot runs VACUUM INTO through a live
// connection. The scaffold opens its own handle from the DSN because store.Store exposes no
// *sql.DB.
func snapshotSQLite(ctx context.Context, dsn, dataDir string) ([]byte, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	dir, err := os.MkdirTemp(dataDir, "snapshot-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "ky_server.db")
	if err := recoveryclient.SQLiteSnapshot(ctx, db, path); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoDatabaseSnapshot, err)
	}
	return os.ReadFile(path)
}

// dbMembers is the snapshot as one member when it fits, else as ordered parts each within
// capsule.MaxFileBytes.
func dbMembers(db []byte) []recoveryclient.File {
	if len(db) <= dbPartBytes {
		return []recoveryclient.File{{Path: dbPath, Data: db, Mode: 0600}}
	}
	n := (len(db) + dbPartBytes - 1) / dbPartBytes
	files := make([]recoveryclient.File, 0, n)
	for i := range n {
		part := db[i*dbPartBytes : min((i+1)*dbPartBytes, len(db))]
		files = append(files, recoveryclient.File{Path: "data/" + dbPartName(i, n), Data: part, Mode: 0600})
	}
	return files
}

// dbPartPattern names a part; other ky_server.db.* files (an operator's .bak) are left alone.
var dbPartPattern = regexp.MustCompile(`^ky_server\.db\.[0-9]{3}-of-[0-9]{3}$`)

func dbPartName(i, n int) string { return fmt.Sprintf("%s.%03d-of-%03d", dbName, i, n) }

// JoinDatabase reassembles a database sealed as parts into dataDir/ky_server.db and removes
// the parts, before anything opens it. Without parts it does nothing, so a single-member
// capsule or an already joined directory passes unchanged. It refuses a missing, extra or
// renumbered part, and parts beside a whole database.
func JoinDatabase(dataDir string) error {
	entries, err := os.ReadDir(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var parts []string
	for _, e := range entries {
		if dbPartPattern.MatchString(e.Name()) {
			parts = append(parts, e.Name())
		}
	}
	if len(parts) == 0 {
		return nil
	}
	// ReadDir sorts by name, so a complete set is exactly part 0 to n-1 of n, in order.
	_, count, _ := strings.Cut(parts[0], "-of-")
	n, err := strconv.Atoi(count)
	if err != nil || len(parts) > n {
		return fmt.Errorf("%w: %s", ErrDatabaseParts, parts[0])
	}
	for i, name := range parts {
		if name != dbPartName(i, n) {
			return fmt.Errorf("%w: found %s where %s belongs", ErrDatabaseParts, name, dbPartName(i, n))
		}
	}
	whole := filepath.Join(dataDir, dbName)
	if _, err := os.Lstat(whole); !errors.Is(err, os.ErrNotExist) {
		// A join interrupted after publishing leaves the parts behind, a leading run of them
		// when it stopped while removing them (last first): finish it when the database begins
		// with exactly those bytes (and is exactly all of them), and refuse anything else.
		same, err := equalsParts(whole, dataDir, parts, len(parts) == n)
		if err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("%w: %s and its parts are both present and differ", ErrDatabaseParts, dbName)
		}
		return removeParts(dataDir, parts)
	}
	if len(parts) != n {
		return fmt.Errorf("%w: %d of %d parts present", ErrDatabaseParts, len(parts), n)
	}
	tmp, err := os.CreateTemp(dataDir, ".join-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	for _, name := range parts {
		if err := appendFile(tmp, filepath.Join(dataDir, name)); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := errors.Join(tmp.Chmod(0600), tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), whole); err != nil {
		return err
	}
	return removeParts(dataDir, parts)
}

// removeParts removes the last part first, so an interruption always leaves a leading run.
func removeParts(dataDir string, parts []string) error {
	for i := len(parts) - 1; i >= 0; i-- {
		if err := os.Remove(filepath.Join(dataDir, parts[i])); err != nil {
			return err
		}
	}
	return nil
}

// equalsParts reports whether the file at path begins with the parts, in order, and, when
// complete, holds nothing more.
func equalsParts(path, dataDir string, parts []string, complete bool) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	got, want := make([]byte, 1<<16), make([]byte, 1<<16)
	for _, name := range parts {
		p, err := os.Open(filepath.Join(dataDir, name))
		if err != nil {
			return false, err
		}
		for {
			n, rerr := io.ReadFull(p, want)
			if n > 0 {
				if _, err := io.ReadFull(f, got[:n]); err != nil || !bytes.Equal(got[:n], want[:n]) {
					p.Close()
					return false, nil
				}
			}
			if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
				break
			}
			if rerr != nil {
				p.Close()
				return false, rerr
			}
		}
		p.Close()
	}
	if !complete {
		return true, nil
	}
	n, _ := f.Read(got[:1])
	return n == 0, nil
}

func appendFile(dst io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(dst, f)
	return err
}

// sizeWarning is non-empty once a capsule's members pass 80% of what one may hold.
func sizeWarning(n int64) string {
	if n <= warnBytes {
		return ""
	}
	return fmt.Sprintf("Backups are %d MiB, over 80%% of the %d MiB a capsule holds; they fail once the database outgrows it", n>>20, capsule.MaxExpandedBytes>>20)
}

// SizeWarning is sizeWarning for the live database file, for the status route.
func SizeWarning(cfg *config.Config) string {
	path, _, _ := strings.Cut(strings.TrimPrefix(cfg.Database.DSN, "file:"), "?")
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return sizeWarning(fi.Size())
}

// Members names what a capsule carries, for the screen; it is what Collect would seal now.
func Members(cfg *config.Config) []string {
	m := []string{"data/ky_server.db", "config/settings.json", "config/integrations.json", encryptionKeyPath}
	if os.Getenv("KYDRIVE_BULK_BACKUP_REPOSITORY") != "" {
		m = append(m, BulkManifestPath)
	}
	if _, err := os.Stat(recoveryclient.RecoveryKeyPath(cfg.Database.DataDir)); err == nil {
		m = append(m, recoveryPubPath)
	}
	return m
}

func requiredFiles(files []recoveryclient.File) []string {
	req := make([]string, 0, len(files))
	for _, f := range files {
		req = append(req, f.Path)
	}
	return req
}
