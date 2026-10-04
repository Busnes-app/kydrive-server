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
	"strings"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

const BulkManifestPath = "data/drive-bulk.json"

type BulkFile struct {
	Blob   string `json:"blob"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
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
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("restic %s failed: %w", args[0], err)
	}
	return out.Bytes(), nil
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
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT blob,digest,size FROM drive_versions ORDER BY blob`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []BulkFile
	for rows.Next() {
		var f BulkFile
		if err = rows.Scan(&f.Blob, &f.Digest, &f.Size); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
func CollectBulk(ctx context.Context, dataDir string, dbBytes, key []byte) ([]byte, error) {
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
	stage, err := os.MkdirTemp(dataDir, "bulk-stage-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	root := filepath.Join(stage, "blobs")
	if err = os.Mkdir(root, 0700); err != nil {
		return nil, err
	}
	for _, f := range files {
		source, err := drive.BlobPath(filepath.Join(dataDir, "blobs"), f.Blob)
		if err != nil {
			return nil, err
		}
		if err = verifyBulkFile(source, f); err != nil {
			return nil, err
		}
		if err = os.Link(source, filepath.Join(root, f.Blob)); err != nil {
			return nil, err
		}
	}
	out, err := restic(ctx, repo, key, stage, "backup", "--json", "--tag", "kydrive", "blobs")
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
	return json.Marshal(BulkManifest{Version: 1, Snapshot: snapshot, Root: "/blobs", Files: files})
}
func verifyBulkFile(path string, f BulkFile) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
		return errors.New("bulk file missing or size differs")
	}
	file, err := os.Open(path)
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
	if manifest.Version != 1 || !shaPattern.MatchString(manifest.Snapshot) || !filepath.IsAbs(manifest.Root) || strings.Contains(manifest.Root, ":") || len(manifest.Files) == 0 {
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
		if e != nil || f.Size < 0 || !shaPattern.MatchString(f.Digest) {
			return errors.New("invalid bulk file reference")
		}
		if err = verifyBulkFile(path, f); err != nil {
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
