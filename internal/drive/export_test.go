package drive

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

func PurgeFileUnauthorized(ctx context.Context, s *Store, id string) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) (err error) {
		blobs, err = purgeFile(ctx, tx, id)
		return
	})
	return blobs, err
}

// SchemaVersion is the version a fully migrated database records.
var SchemaVersion = len(schema)

// KYB1SchemaVersion queues KYB1 blobs; LoginSchemaVersion binds editor sessions to sign-ins.
const KYB1SchemaVersion, LoginSchemaVersion = 16, 17

func Scrub(ctx context.Context, s *Store) error { return scrub(ctx, s.db) }

// MoveFileFrom is MoveFile with the file as the caller loaded it, possibly before another move:
// the race between MoveFile's first read and its transaction, made deterministic.
func MoveFileFrom(ctx context.Context, s *Store, user string, f File, expected int64, to Location) (File, error) {
	return s.moveFile(ctx, user, f, expected, to)
}

// GuestPath is the breadcrumb walk GuestView runs over the ancestors it loaded.
func GuestPath(chain []Folder, folder, root string) ([]GuestItem, error) {
	return guestPath(chain, folder, root)
}

// DecodeCost is what Thumbnail budgets for decoding the image in r, in bytes a pixel, and
// the decoder it would use.
func DecodeCost(r io.ReadSeeker) (int64, func(io.Reader) (image.Image, error), error) {
	f, _, perPixel, _, err := inspect(r)
	if err != nil {
		return 0, nil, err
	}
	return perPixel, f.decode, nil
}

// JPEGFrame returns a JPEG's frame marker (0 when none) and EXIF orientation.
func JPEGFrame(r io.Reader) (byte, int) { return jpegFrame(r) }

// SeedFiles inserts n live files named file-<i>.txt at the root of workspace in one transaction.
func SeedFiles(ctx context.Context, s *Store, workspace string, n int) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		for i := range n {
			id := fmt.Sprintf("seed-%d", i)
			stored, mac, err := s.names.seal("drive_files", id, fmt.Sprintf("file-%d.txt", i))
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO drive_files(id,workspace,parent,name,name_hmac,revision) VALUES(?,?,'',?,?,1)`, id, workspace, stored, mac); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO drive_versions(file_id,revision,blob,digest,size,created,sealed) VALUES(?,1,?,?,0,?,1)`, id, id, strings.Repeat("0", 64), now()); err != nil {
				return err
			}
		}
		return nil
	})
}

// ChangesAfter lists change rows after seq in order, as "workspace kind item".
func ChangesAfter(ctx context.Context, s *Store, seq int64) ([]string, error) {
	return column(ctx, s.db, `SELECT workspace||' '||kind||' '||item FROM drive_changes WHERE seq>? ORDER BY seq`, seq)
}

// ChangeSeqs lists the seq of each published file's change row in the order the version
// events were inserted, which is commit order under the one writer.
func ChangeSeqs(ctx context.Context, s *Store) ([]string, error) {
	return column(ctx, s.db, `SELECT printf('%020d',c.seq) FROM drive_events e JOIN drive_changes c ON c.item=e.resource WHERE e.action='file.version_created' ORDER BY e.rowid`)
}

func ExecSQL(ctx context.Context, s *Store, q string, args ...any) error {
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// RetainFile and RetainVersion run one retention step in its own transaction, as ApplyRetention does.
func RetainFile(ctx context.Context, s *Store, id string, at time.Time) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) (err error) {
		blobs, err = retainFile(ctx, tx, id, at.UTC().Format(time.RFC3339Nano), 0)
		return
	})
	return blobs, err
}

func RetainVersion(ctx context.Context, s *Store, id string, revision int64) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) (err error) {
		blobs, err = retainVersion(ctx, tx, id, revision)
		return
	})
	return blobs, err
}

// SetTrashAs runs the trash step with a workspace resolved earlier, as a caller holding a stale lookup would.
func SetTrashAs(ctx context.Context, s *Store, user, id, workspace string, expected int64, trash bool) error {
	return s.transaction(ctx, func(tx *sql.Tx) error { return setTrash(ctx, tx, user, id, workspace, expected, trash) })
}

// SealUntilCommit runs one legacy swap up to its commit and stops, as a crash before cleanup would.
func SealUntilCommit(ctx context.Context, s *Store, root string, key []byte, id string) error {
	v, err := s.sealCopy(ctx, root, key, id)
	if err != nil {
		return err
	}
	return s.swapBlob(ctx, root, id, v)
}

// SealCopy and SwapBlob are the two halves of one legacy swap, so tests can interleave a purge.
func SealCopy(ctx context.Context, s *Store, root string, key []byte, id string) (Version, error) {
	return s.sealCopy(ctx, root, key, id)
}

func SwapBlob(ctx context.Context, s *Store, root, old string, v Version) error {
	return s.swapBlob(ctx, root, old, v)
}

func QueryInt(ctx context.Context, s *Store, q string, args ...any) (n int, err error) {
	err = s.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return
}

// RawNames returns the stored name and name_hmac of every row in table.
func RawNames(ctx context.Context, s *Store, table string) ([]string, error) {
	return column(ctx, s.db, `SELECT name||' '||name_hmac FROM `+table)
}

// UsedQuery is the per-workspace usage sum fits checks under every publish.
const UsedQuery = usedBytes

// QueryPlan returns the EXPLAIN QUERY PLAN details for q.
func QueryPlan(ctx context.Context, s *Store, q string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			return nil, err
		}
		out = append(out, detail)
	}
	return out, rows.Err()
}

// WriteBlobV1 stores r as a KYB1 blob, as releases before KYB2 did.
func WriteBlobV1(root string, key []byte, r io.Reader) (Version, error) {
	h := sha256.New()
	var buf bytes.Buffer
	n, err := sealV1(&buf, key, io.TeeReader(r, h))
	if err != nil {
		return Version{}, err
	}
	v := Version{Blob: uuid.NewString(), Digest: hex.EncodeToString(h.Sum(nil)), Size: n, Sealed: true}
	return v, os.WriteFile(filepath.Join(root, v.Blob), buf.Bytes(), 0600)
}

// sealV1 is the KYB1 writer this package shipped before KYB2.
func sealV1(w io.Writer, key []byte, r io.Reader) (int64, error) {
	header := make([]byte, headerV1)
	copy(header, blobMagicV1)
	rand.Read(header[len(blobMagicV1):])
	aead, err := aeadV1(key, header[len(blobMagicV1):])
	if err != nil {
		return 0, err
	}
	if _, err = w.Write(header); err != nil {
		return 0, err
	}
	cur, next := make([]byte, blobChunk), make([]byte, blobChunk)
	m, err := io.ReadFull(r, cur)
	var total int64
	for i := int64(0); ; i++ {
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return total, err
		}
		final, k := m < blobChunk, 0
		if !final {
			k, err = io.ReadFull(r, next)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				return total, err
			}
			final = k == 0
		}
		if _, err = w.Write(aead.Seal(nil, nonceV1(i, final), cur[:m], header)); err != nil {
			return total, err
		}
		total += int64(m)
		if final {
			return total, nil
		}
		cur, next, m = next, cur, k
	}
}

// StageUpgrade and CommitUpgrade are the two halves of one KYB2 rewrite.
func StageUpgrade(ctx context.Context, s *Store, root string, key []byte, id string) (string, error) {
	tmp, _, err := s.stageUpgrade(ctx, root, key, id)
	return tmp, err
}

func CommitUpgrade(ctx context.Context, s *Store, root, id, tmp string) error {
	return s.commitUpgrade(ctx, root, id, tmp)
}

// Migrate reruns every schema step not recorded in drive_schema.
func Migrate(ctx context.Context, s *Store) error { return s.migrate(ctx) }

// CheckStaged is the decrypt check a staged rewrite must pass before it replaces its blob.
func CheckStaged(tmp string, key []byte, id string, size int64, digest string) error {
	return checkStaged(tmp, key, id, size, digest)
}
