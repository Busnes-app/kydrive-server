package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"golang.org/x/sys/unix"
)

const BulkManifestPath = "data/drive-bulk.json"

type BulkFile struct {
	Blob   string `json:"blob"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	Sealed bool   `json:"sealed,omitempty"`
}
type BulkManifest struct {
	Version  int        `json:"version"`
	Snapshot string     `json:"snapshot"`
	Root     string     `json:"root"`
	Files    []BulkFile `json:"files"`
}

var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Local repositories are deliberately outside the primary data directory. A production
// operator mounts this path from an independent destination; local tests use a second tree.
func bulkRepository(dataDir string) (string, error) {
	raw := os.Getenv("KYDRIVE_BULK_BACKUP_REPOSITORY")
	if raw == "" {
		return "", errors.New("KYDRIVE_BULK_BACKUP_REPOSITORY is required for document backups")
	}
	repo, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	data, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(data, repo)
	if err != nil || rel == "." || filepath.IsLocal(rel) {
		return "", errors.New("bulk repository must be outside primary data directory")
	}
	return repo, nil
}
func restic(ctx context.Context, repo string, key []byte, dir string, args ...string) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("bulk backup requires a 32-byte deployment key")
	}
	cmd := exec.CommandContext(ctx, "restic", append([]string{"--repo", repo, "--no-cache"}, args...)...)
	cmd.Dir = dir
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "RESTIC_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "RESTIC_PASSWORD="+hex.EncodeToString(key))
	// Commands never run through a shell. Restic owns authenticated encryption and deduplication.
	var out limitedBuffer
	var stderr tailBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("restic %s failed: %w: %s", args[0], err, bytes.TrimSpace(stderr.b))
	}
	return out.Bytes(), nil
}

// tailBuffer keeps the last 2 KiB written, where restic puts the reason it failed.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 2<<10 {
		t.b = t.b[len(t.b)-2<<10:]
	}
	return len(p), nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, errors.New("restic output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func InitBulkRepository(ctx context.Context, dataDir string, key []byte) error {
	repo, err := bulkRepository(dataDir)
	if err != nil {
		return err
	}
	_, err = restic(ctx, repo, key, "", "init")
	return err
}

func snapshotFiles(ctx context.Context, dbBytes []byte, dataDir string) ([]BulkFile, error) {
	dir, err := os.MkdirTemp(dataDir, "bulk-db-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "snapshot.db")
	if err = os.WriteFile(path, dbBytes, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var exists int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='drive_versions'`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, nil
	}
	// Databases from before blob sealing have no sealed column; every blob there is plaintext.
	sealed := "0"
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('drive_versions') WHERE name='sealed'`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 1 {
		sealed = "sealed"
	}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT blob,digest,size,`+sealed+` FROM drive_versions ORDER BY blob`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []BulkFile
	for rows.Next() {
		var f BulkFile
		if err = rows.Scan(&f.Blob, &f.Digest, &f.Size, &f.Sealed); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// Purpose tags a bulk snapshot with why it was taken, so retention treats deposits apart
// from drills and exports. Snapshots from before tagging carry only "kydrive".
type Purpose string

const (
	PurposeDeposit Purpose = "deposit"
	PurposeDrill   Purpose = "drill"
	PurposeExport  Purpose = "export"
)

func CollectBulk(ctx context.Context, dataDir string, dbBytes, key []byte, purpose Purpose) ([]byte, error) {
	files, err := snapshotFiles(ctx, dbBytes, dataDir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	repo, err := bulkRepository(dataDir)
	if err != nil {
		return nil, err
	}
	lock, err := lockFile(dataDir, bulkLock, true)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	stage, err := os.MkdirTemp(dataDir, "bulk-stage-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	root := filepath.Join(stage, "blobs")
	if err = os.Mkdir(root, 0700); err != nil {
		return nil, err
	}
	// Link first so a purge after the database snapshot can only race this loop, not hashing.
	for _, f := range files {
		source, err := drive.BlobPath(filepath.Join(dataDir, "blobs"), f.Blob)
		if err != nil {
			return nil, err
		}
		if err = os.Link(source, filepath.Join(root, f.Blob)); err != nil {
			return nil, err
		}
	}
	for _, f := range files {
		if err = verifyBulkFile(filepath.Join(root, f.Blob), f, key); err != nil {
			return nil, err
		}
	}
	out, err := restic(ctx, repo, key, stage, "backup", "--json", "--tag", "kydrive,"+string(purpose), "blobs")
	if err != nil {
		return nil, err
	}
	var snapshot string
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var line struct {
			Type     string `json:"message_type"`
			Snapshot string `json:"snapshot_id"`
		}
		if err = dec.Decode(&line); err != nil {
			return nil, err
		}
		if line.Type == "summary" {
			snapshot = line.Snapshot
		}
	}
	if !shaPattern.MatchString(snapshot) {
		return nil, errors.New("restic did not return a full snapshot ID")
	}
	return json.Marshal(BulkManifest{Version: 2, Snapshot: snapshot, Root: "/blobs", Files: files})
}

// verifyBulkFile checks a blob's size and plaintext digest, decrypting a sealed blob.
func verifyBulkFile(path string, f BulkFile, key []byte) error {
	size := f.Size
	if f.Sealed {
		size = drive.SealedSize(f.Size)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return errors.New("bulk file missing or size differs")
	}
	file, err := drive.OpenBlob(path, key, f.Sealed, f.Size)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.Digest {
		return errors.New("bulk file digest differs")
	}
	return nil
}

// RestoreBulk accepts only a manifest from an opened, verified capsule. It checks every
// referenced blob before publishing the restored directory; absence never becomes success.
func RestoreBulk(ctx context.Context, openedDir, repository, target string) error {
	raw, err := os.ReadFile(filepath.Join(openedDir, BulkManifestPath))
	if err != nil {
		return err
	}
	var manifest BulkManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&manifest); err != nil {
		return err
	}
	if manifest.Version != 1 && manifest.Version != 2 || !shaPattern.MatchString(manifest.Snapshot) || !filepath.IsAbs(manifest.Root) || strings.Contains(manifest.Root, ":") || len(manifest.Files) == 0 {
		return errors.New("invalid bulk manifest")
	}
	dbBytes, err := os.ReadFile(filepath.Join(openedDir, "data/ky_server.db"))
	if err != nil {
		return err
	}
	refs, err := snapshotFiles(ctx, dbBytes, openedDir)
	if err != nil {
		return err
	}
	if len(refs) != len(manifest.Files) {
		return errors.New("bulk manifest differs from database")
	}
	for i, f := range refs {
		if f != manifest.Files[i] {
			return errors.New("bulk manifest differs from database")
		}
	}
	keyHex, err := os.ReadFile(filepath.Join(openedDir, encryptionKeyPath))
	if err != nil {
		return err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(keyHex)))
	if err != nil || len(key) != 32 {
		return errors.New("invalid restored deployment key")
	}
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("bulk restore target must not exist")
	}
	parent := filepath.Dir(target)
	if err = os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	scratch, err := os.MkdirTemp(parent, ".bulk-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if _, err = restic(ctx, repository, key, "", "restore", manifest.Snapshot+":"+filepath.ToSlash(manifest.Root), "--target", scratch, "--verify"); err != nil {
		return err
	}
	for _, f := range manifest.Files {
		path, e := drive.BlobPath(scratch, f.Blob)
		if e != nil || f.Size < 0 || !shaPattern.MatchString(f.Digest) || f.Sealed && manifest.Version < 2 {
			return errors.New("invalid bulk file reference")
		}
		if err = verifyBulkFile(path, f, key); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		return err
	}
	if len(entries) != len(manifest.Files) {
		return errors.New("unexpected bulk restore members")
	}
	if err = drive.SyncDirectory(scratch); err != nil {
		return err
	}
	if err = os.Rename(scratch, target); err != nil {
		return err
	}
	return drive.SyncDirectory(parent)
}

// PruneBulk forgets bulk snapshots outside policy, then prunes unreferenced data. Off forgets
// nothing. The newest keepLast deposit snapshots, and the newest keepLast of any kind (which
// covers untagged ones a retained local copy may still name), are never forgotten. Prune runs
// even when nothing was forgotten, so a run that died after forget is finished by the next.
// Another bulk operation holding the lock returns ErrBulkBusy.
func PruneBulk(ctx context.Context, dataDir string, key []byte, policy BulkPolicy, keepLast int) (int, error) {
	if policy.Off() {
		return 0, nil
	}
	if err := policy.validate(); err != nil || keepLast < 1 {
		return 0, ErrBadBulkPolicy
	}
	lock, err := lockFile(dataDir, bulkLock, false)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return 0, ErrBulkBusy
	}
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	repo, err := bulkRepository(dataDir)
	if err != nil {
		return 0, err
	}
	last := strconv.Itoa(keepLast)
	// keep-daily and friends count days that have snapshots, so a tag that stops receiving
	// them (legacy, rare exports) never ages out under them; it ages out by date instead.
	horizon := fmt.Sprintf("%dd", max(policy.Daily, 7*policy.Weekly, 31*policy.Monthly))
	passes := [][]string{
		{"--tag", "kydrive," + string(PurposeDeposit), "--keep-last", last, "--keep-daily", strconv.Itoa(policy.Daily),
			"--keep-weekly", strconv.Itoa(policy.Weekly), "--keep-monthly", strconv.Itoa(policy.Monthly)},
		{"--tag", "kydrive", "--keep-tag", string(PurposeDeposit), "--keep-last", last, "--keep-within", horizon},
		// A drill capsule never leaves the server; its snapshot is needed only while it runs.
		{"--tag", "kydrive," + string(PurposeDrill), "--keep-within", "1d"},
	}
	forgotten := 0
	for _, pass := range passes {
		out, err := restic(ctx, repo, key, "", append([]string{"forget", "--json", "--group-by", ""}, pass...)...)
		if err != nil {
			return forgotten, err
		}
		// No matching snapshot prints nothing at all.
		var groups []struct {
			Remove []json.RawMessage `json:"remove"`
		}
		if len(bytes.TrimSpace(out)) > 0 {
			if err = json.Unmarshal(out, &groups); err != nil {
				return forgotten, err
			}
		}
		for _, g := range groups {
			forgotten += len(g.Remove)
		}
	}
	_, err = restic(ctx, repo, key, "", "prune")
	return forgotten, err
}
