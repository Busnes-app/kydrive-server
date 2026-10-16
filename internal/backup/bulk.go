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
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
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

// errWrongPassword means no key in the repository opens with the password: exit status 12 from
// restic 0.17 on, exit 1 with this message before it.
var errWrongPassword = errors.New("wrong repository password")

// keyIDPattern matches a restic key ID: full from 0.17, the 8-character short form before it.
var keyIDPattern = regexp.MustCompile(`^[a-f0-9]{8,64}$`)

// runRestic is the one exec seam; tests replace it.
var runRestic = execRestic

// restic runs one command; password is hex-encoded into RESTIC_PASSWORD.
func restic(ctx context.Context, repo Repository, password []byte, dir string, args ...string) ([]byte, error) {
	return runRestic(ctx, repo, password, dir, nil, args...)
}

func execRestic(ctx context.Context, repo Repository, password []byte, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := resticCommand(ctx, repo, password, dir, stdin, args...)
	// Commands never run through a shell. Restic owns authenticated encryption and deduplication.
	var out limitedBuffer
	var stderr tailBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 12 || bytes.Contains(stderr.b, []byte("wrong password or no key found"))) {
			err = errWrongPassword
		}
		return nil, fmt.Errorf("restic %s failed: %w: %s", args[0], err, bytes.TrimSpace(redact(stderr.b, repo)))
	}
	return out.Bytes(), nil
}

// resticPassword is the repository password: a "kydrive:restic" subkey, never the deployment
// key itself. Repositories initialized before it used the raw key; see openRepository.
func resticPassword(key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("bulk backup requires a 32-byte deployment key")
	}
	return crypto.DeriveKey(key, "kydrive:restic")
}

// openRepository returns the password for repo, first moving a raw-key repository to the
// derived password: add the derived key while the raw key opens it, then remove the raw key
// using the derived one. Each run redoes whichever step is left, so an interrupted move is
// finished by the next backup or prune.
func openRepository(ctx context.Context, repo Repository, key []byte) ([]byte, error) {
	derived, err := resticPassword(key)
	if err != nil {
		return nil, err
	}
	_, err = currentKey(ctx, repo, derived)
	if err != nil && !errors.Is(err, errWrongPassword) {
		return nil, err
	}
	legacy, lerr := currentKey(ctx, repo, key)
	if err == nil && errors.Is(lerr, errWrongPassword) {
		return derived, nil
	}
	if lerr != nil {
		return nil, lerr
	}
	if err != nil {
		pw := strings.NewReader(hex.EncodeToString(derived))
		if _, err = runRestic(ctx, repo, key, "", pw, "key", "add", "--new-password-file", "/dev/stdin"); err != nil {
			return nil, err
		}
	}
	// A raw key left behind is the pre-upgrade state, not a backup failure; the next run retries.
	if _, err = restic(ctx, repo, derived, "", "key", "remove", legacy); err != nil {
		log.Printf("bulk: raw-key password still opens %s: %v", repo, err)
	}
	return derived, nil
}

// currentKey returns the ID of the repository key that password opens.
func currentKey(ctx context.Context, repo Repository, password []byte) (string, error) {
	out, err := restic(ctx, repo, password, "", "key", "list", "--json")
	if err != nil {
		return "", err
	}
	var keys []struct {
		Current bool   `json:"current"`
		ID      string `json:"id"`
	}
	if err = json.Unmarshal(out, &keys); err != nil {
		return "", err
	}
	for _, k := range keys {
		if k.Current && keyIDPattern.MatchString(k.ID) {
			return k.ID, nil
		}
	}
	return "", errors.New("restic key list named no current key")
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
	password, err := resticPassword(key)
	if err != nil {
		return err
	}
	_, err = restic(ctx, repo, password, "", "init")
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
	if err = verifyBulkFiles(ctx, dataDir, root, files, key, time.Now()); err != nil {
		return nil, err
	}
	password, err := openRepository(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	out, err := restic(ctx, repo, password, stage, "backup", "--json", "--tag", "kydrive,"+string(purpose), "blobs")
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

// verifiedPath caches the blobs CollectBulk last verified: blobs are immutable, so one whose
// row and file are unchanged is only size-checked. The whole set is re-read weekly.
const verifiedPath = "bulk-verified.json"

const fullVerifyEvery = 7 * 24 * time.Hour

type verifiedBlob struct {
	BulkFile
	ModTime int64 `json:"mtime"`
}

type verifiedCache struct {
	Full  time.Time               `json:"full"`
	Files map[string]verifiedBlob `json:"files"`
}

// verifyBulkFiles checks every blob in root, decrypting and hashing only those not in the cache.
func verifyBulkFiles(ctx context.Context, dataDir, root string, files []BulkFile, key []byte, now time.Time) error {
	var old verifiedCache
	raw, err := os.ReadFile(filepath.Join(dataDir, verifiedPath))
	if err == nil {
		err = json.Unmarshal(raw, &old)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("bulk: re-verifying every blob, cache unreadable: %v", err)
	}
	if err != nil || now.Sub(old.Full) > fullVerifyEvery {
		old = verifiedCache{Full: now}
	}
	next := verifiedCache{Full: old.Full, Files: make(map[string]verifiedBlob, len(files))}
	for _, f := range files {
		if err = ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(root, f.Blob)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != diskSize(f) {
			return errors.New("bulk file missing or size differs")
		}
		entry := verifiedBlob{f, info.ModTime().UnixNano()}
		if old.Files[f.Blob] != entry {
			if err = verifyBulkFile(path, f, key); err != nil {
				return err
			}
		}
		next.Files[f.Blob] = entry
	}
	// A lost cache costs only a full verification next time.
	if raw, err = json.Marshal(next); err == nil {
		err = writeFileAtomic(filepath.Join(dataDir, verifiedPath), raw)
	}
	if err != nil {
		log.Printf("bulk: verification cache not saved: %v", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// diskSize is a blob's size on disk: sealed blobs carry their encryption overhead.
func diskSize(f BulkFile) int64 {
	if f.Sealed {
		return drive.SealedSize(f.Size)
	}
	return f.Size
}

// verifyBulkFile checks a blob's size and plaintext digest, decrypting a sealed blob.
func verifyBulkFile(path string, f BulkFile, key []byte) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != diskSize(f) {
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

// freeSpace is the space an unprivileged writer may use on dir's filesystem; tests replace it.
var freeSpace = func(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// RestoreBulk accepts only a manifest from an opened, verified capsule. It checks every
// referenced blob before publishing the restored directory; absence never becomes success.
func RestoreBulk(ctx context.Context, openedDir string, repository Repository, target string) error {
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
	// Refuse before restic fills the volume (the drill's scratch shares the live data volume).
	var need uint64
	for _, f := range manifest.Files {
		need += uint64(max(diskSize(f), 0))
	}
	need += need/10 + 64<<20
	free, err := freeSpace(parent)
	if err != nil {
		return err
	}
	if free < need {
		return fmt.Errorf("bulk restore needs %d MiB free in %s, %d MiB available", need>>20, parent, free>>20)
	}
	scratch, err := os.MkdirTemp(parent, ".bulk-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	// Read-only: a repository from before the derived password still opens with the raw key.
	password, err := resticPassword(key)
	if err != nil {
		return err
	}
	restore := []string{"restore", manifest.Snapshot + ":" + filepath.ToSlash(manifest.Root), "--target", scratch, "--verify"}
	if _, err = restic(ctx, repository, password, "", restore...); errors.Is(err, errWrongPassword) {
		_, err = restic(ctx, repository, key, "", restore...)
	}
	if err != nil {
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
	password, err := openRepository(ctx, repo, key)
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
		out, err := restic(ctx, repo, password, "", append([]string{"forget", "--json", "--group-by", ""}, pass...)...)
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
	_, err = restic(ctx, repo, password, "", "prune")
	return forgotten, err
}

// CheckBulk verifies the repository's structure and reads back a random 5% of its data, so a
// destination that silently loses or corrupts packs shows within a few weekly runs. Another
// bulk operation holding the lock returns ErrBulkBusy.
func CheckBulk(ctx context.Context, dataDir string, key []byte) error {
	lock, err := lockFile(dataDir, bulkLock, false)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrBulkBusy
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	repo, err := bulkRepository(dataDir)
	if err != nil {
		return err
	}
	password, err := openRepository(ctx, repo, key)
	if err != nil {
		return err
	}
	_, err = restic(ctx, repo, password, "", "check", "--read-data-subset=5%")
	return err
}

// BulkLocation is the configured repository as the Recovery screen shows it: credentials never
// appear in it, since they cannot be part of an accepted location.
func BulkLocation(dataDir string) (string, error) {
	repo, err := bulkRepository(dataDir)
	return repo.URL, err
}
