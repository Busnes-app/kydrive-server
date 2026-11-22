package drive

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Viewing copies (docs/KYPOST.md "Viewing copies"): one mail attachment KyPost sends for one
// person to read in Euro-Office's view mode. A copy is not a file: no folder, share, link,
// search, change row, WebDAV, MCP or sync ever sees it. It counts against My files' quota
// from the moment its ticket is issued, and it is gone at ViewLife.
const (
	ViewLife          = 24 * time.Hour
	viewCopiesPerUser = 4
	// ViewMaxBytes is the save-attachment limit: KyPost's 25 MiB inbound cap.
	ViewMaxBytes = int64(25 << 20)
	// ViewKeyPrefix names a viewing copy's Euro-Office document key; ParseDocumentKey refuses it.
	ViewKeyPrefix = "view-"
)

// viewTypes maps each viewable extension to Euro-Office's document type.
var viewTypes = map[string]string{"docx": "word", "odt": "word", "xlsx": "cell", "ods": "cell", "csv": "cell", "pptx": "slide", "odp": "slide", "pdf": "pdf"}

// ViewKind is name's lowercase extension and Euro-Office document type, or "" for a type a
// viewing copy may not have.
func ViewKind(name string) (ext, kind string) {
	ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if kind = viewTypes[ext]; kind == "" {
		return "", ""
	}
	return ext, kind
}

// viewSniffs reports whether head, the first bytes, looks like a file of ext: Office and
// OpenDocument are zip, pdf is pdf, csv is text.
func viewSniffs(ext string, head []byte) bool {
	got := http.DetectContentType(head)
	switch ext {
	case "pdf":
		return got == "application/pdf"
	case "csv":
		return strings.HasPrefix(got, "text/plain")
	}
	return got == "application/zip"
}

// ViewCopy is one viewing copy. Blob is empty until its bytes land.
type ViewCopy struct {
	ID, Owner, Name, Blob, Pairing string
	Size, Expires                  int64
}

// IssueViewTicket (a peer context) reserves a viewing copy of exactly size bytes named name
// for user and returns its single-use upload ticket, of which only the hash is stored.
func (s *Store) IssueViewTicket(ctx context.Context, user, name string, size, maxSize int64) (string, ViewCopy, error) {
	p, _ := ctx.Value(peerKey{}).(Peer)
	at := time.Now()
	c := ViewCopy{ID: uuid.NewString(), Owner: user, Name: name, Size: size, Pairing: p.Pairing, Expires: at.Add(ViewLife).Unix()}
	if !isPeer(ctx) || p.Pairing == "" {
		return "", c, ErrDenied
	}
	if ext, _ := ViewKind(name); ext == "" || !ValidName(name) || size < 1 {
		return "", c, ErrInvalid
	}
	if size > min(maxSize, ViewMaxBytes) {
		return "", c, ErrTooLarge
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		ws, err := ensurePersonal(ctx, tx, user)
		if err != nil {
			return err
		}
		if err = peerSwitch(ctx, tx, user); err != nil {
			return err
		}
		if err = fits(ctx, tx, ws, size); err != nil {
			return err
		}
		var live int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_view_copies WHERE user_id=? AND revoked=0 AND expires>? AND (blob!='' OR ticket_expires>?)`, user, at.Unix(), at.Unix()).Scan(&live); err != nil {
			return err
		}
		if live >= viewCopiesPerUser {
			return ErrTicketLimit
		}
		stored, _, err := s.names.seal("drive_view_copies", c.ID, name)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO drive_view_copies(id,user_id,workspace,name,size,pairing,token_hash,ticket_expires,expires,org_id) VALUES(?,?,?,?,?,?,?,?,?,`+workspaceOrg("?")+`)`, c.ID, user, ws, stored, size, c.Pairing, tokenHash(token), at.Add(TicketLife).Unix(), c.Expires, ws); err != nil {
			return err
		}
		return event(ctx, tx, user, "kypost.view_ticket_issued", "view:"+c.ID)
	})
	return token, c, err
}

// ViewTicket finds the copy an unspent, unexpired upload ticket names; anything else is ErrNotFound.
func (s *Store) ViewTicket(ctx context.Context, token string) (ViewCopy, error) {
	now := time.Now().Unix()
	return s.viewCopy(ctx, `token_hash=? AND blob='' AND ticket_expires>? AND expires>?`, tokenHash(token), now, now)
}

// viewCopy finds a live copy, matched by where, whose owner is an active principal.
func (s *Store) viewCopy(ctx context.Context, where string, args ...any) (ViewCopy, error) {
	var c ViewCopy
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT id,user_id,name,blob,pairing,size,expires FROM drive_view_copies WHERE revoked=0 AND `+activePrincipal("user_id")+` AND `+where, args...).Scan(&c.ID, &c.Owner, &stored, &c.Blob, &c.Pairing, &c.Size, &c.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	c.Name, err = s.names.open("drive_view_copies", c.ID, stored)
	return c, err
}

// StoreViewCopy (a peer context, under the pairing that issued the ticket) seals exactly
// c.Size bytes of r into root and spends the ticket. Bytes that do not look like the name's
// type are ErrUnsupported; a wrong length is ErrInvalid.
func (s *Store) StoreViewCopy(ctx context.Context, root string, key []byte, c ViewCopy, token string, r io.Reader) error {
	if p, _ := ctx.Value(peerKey{}).(Peer); !isPeer(ctx) || p.Pairing == "" || p.Pairing != c.Pairing {
		return ErrPeerGone
	}
	ext, _ := ViewKind(c.Name)
	head := make([]byte, min(c.Size, 512))
	if _, err := io.ReadFull(r, head); err != nil {
		return ErrInvalid
	}
	if !viewSniffs(ext, head) {
		return ErrUnsupported
	}
	v, err := WriteBlob(root, key, io.MultiReader(bytes.NewReader(head), r), c.Size)
	if errors.Is(err, ErrTooLarge) || (err == nil && v.Size != c.Size) {
		err = ErrInvalid
	}
	if err == nil {
		err = s.transaction(ctx, func(tx *sql.Tx) error {
			if err := peerSwitch(ctx, tx, c.Owner); err != nil {
				return err
			}
			now := time.Now().Unix()
			res, err := tx.ExecContext(ctx, `UPDATE drive_view_copies SET blob=?,token_hash=NULL WHERE id=? AND token_hash=? AND blob='' AND ticket_expires>? AND expires>?`, v.Blob, c.ID, tokenHash(token), now, now)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return ErrNotFound
			}
			return event(ctx, tx, c.Owner, "kypost.view_stored", "view:"+c.ID)
		})
	}
	if err != nil && v.Blob != "" {
		os.Remove(filepath.Join(root, v.Blob))
	}
	return err
}

// ErrUnsupported is content that is not what its name says.
var ErrUnsupported = errors.New("content does not match its type")

// signedIn refuses service, device and peer contexts: only the owner's own sign-in opens or
// closes a viewing copy.
func signedIn(ctx context.Context) bool {
	_, service := ctx.Value(serviceAccessKey{}).(ServiceAccess)
	return !service && !narrowed(ctx)
}

// ViewCopy opens user's own stored, unexpired copy id through the sign-in on ctx (WithLogin),
// binding the copy to it as EditorSession does (only the first sign-in may open it): whatever revokes that sign-in's editors
// (revokeEditors) revokes the copy too. Anything else is ErrNotFound.
func (s *Store) ViewCopy(ctx context.Context, user, id string) (ViewCopy, error) {
	if !signedIn(ctx) {
		return ViewCopy{}, ErrDenied
	}
	login, _ := ctx.Value(loginKey{}).(Login)
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := liveLogin(ctx, tx); err != nil {
			return err
		}
		// Bound once, at the first open; another sign-in cannot open it, so one binding is enough.
		at := max(login.At.UnixNano(), 1)
		res, err := tx.ExecContext(ctx, `UPDATE drive_view_copies SET sso_sid=?,login_at=? WHERE id=? AND user_id=? AND blob!='' AND expires>? AND revoked=0 AND (login_at=0 OR (login_at=? AND sso_sid=?))`, login.SSOSID, at, id, user, time.Now().Unix(), at, login.SSOSID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return ViewCopy{}, err
	}
	return s.viewCopy(ctx, `id=? AND user_id=?`, id, user)
}

// ViewDownload is copy id for Euro-Office, whose signature the caller verified: only a copy
// opened through a sign-in that has not been revoked.
func (s *Store) ViewDownload(ctx context.Context, id string) (ViewCopy, error) {
	return s.viewCopy(ctx, `id=? AND blob!='' AND expires>? AND login_at>0`, id, time.Now().Unix())
}

// PendingViewDrops are revoked copies Euro-Office may still be showing; the API drops each
// from Euro-Office and marks it with MarkViewDropped.
func (s *Store) PendingViewDrops(ctx context.Context) ([]ViewCopy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id FROM drive_view_copies WHERE revoked=1 AND dropped=0 AND login_at>0 AND next_attempt<=? LIMIT 100`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ViewCopy
	for rows.Next() {
		var c ViewCopy
		if err = rows.Scan(&c.ID, &c.Owner); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkViewDropped records a drop, or with ok false backs the next attempt off a minute.
// ponytail: a flat one-minute retry, not editor sessions' doubling backoff; copies die at ViewLife anyway.
func (s *Store) MarkViewDropped(ctx context.Context, id string, ok bool) error {
	q := `UPDATE drive_view_copies SET next_attempt=? WHERE id=?`
	if ok {
		q = `UPDATE drive_view_copies SET dropped=1,next_attempt=? WHERE id=?`
	}
	_, err := s.db.ExecContext(ctx, q, time.Now().Add(time.Minute).Unix(), id)
	return err
}

// CloseViewCopy ends user's own copy id and returns its blob for removal ("" when none). An
// opened copy may still be showing in another tab, so its row stays, revoked, until its
// Euro-Office drop is delivered (PendingViewDrops); one never opened is deleted.
func (s *Store) CloseViewCopy(ctx context.Context, user, id string) (blob string, err error) {
	if !signedIn(ctx) {
		return "", ErrDenied
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var opened int64
		err := tx.QueryRowContext(ctx, `SELECT blob,login_at FROM drive_view_copies WHERE id=? AND user_id=?`, id, user).Scan(&blob, &opened)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		q := `DELETE FROM drive_view_copies WHERE id=?`
		if opened > 0 {
			q = `UPDATE drive_view_copies SET revoked=1,blob='' WHERE id=?`
		}
		if _, err = tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
		return event(ctx, tx, user, "view.closed", "view:"+id)
	})
	return blob, err
}

// ViewClosed deletes copy id when Euro-Office reports its document closed: nobody is left to drop.
func (s *Store) ViewClosed(ctx context.Context, id string) (string, error) {
	return s.dropView(ctx, `id=?`, "view.closed", id)
}

func (s *Store) dropView(ctx context.Context, where, action string, args ...any) (blob string, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var id, owner string
		err := tx.QueryRowContext(ctx, `DELETE FROM drive_view_copies WHERE `+where+` RETURNING id,user_id,blob`, args...).Scan(&id, &owner, &blob)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return event(ctx, tx, owner, action, "view:"+id)
	})
	return blob, err
}

// ExpireViewCopies ends copies past ViewLife and returns the blobs to remove. An opened copy
// Euro-Office may still be showing is revoked, not deleted, so PendingViewDrops still drops
// it; its row goes once the drop is delivered, or editorGrace after expiry if it never is.
// Unopened copies, unused tickets and delivered drops are deleted.
func (s *Store) ExpireViewCopies(ctx context.Context) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		blobs = nil
		now := time.Now().Unix()
		ended, err := s.viewRows(ctx, tx, `SELECT id,user_id,blob FROM drive_view_copies WHERE expires<=? AND login_at>0 AND revoked=0`, now)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE drive_view_copies SET revoked=1,blob='' WHERE expires<=? AND login_at>0 AND revoked=0`, now); err != nil {
			return err
		}
		gone, err := s.viewRows(ctx, tx, `DELETE FROM drive_view_copies WHERE (login_at=0 AND (expires<=? OR (blob='' AND ticket_expires<=?))) OR (revoked=1 AND (dropped=1 OR login_at=0)) OR expires<=? RETURNING id,user_id,blob`, now, now, now-int64(editorGrace.Seconds()))
		if err != nil {
			return err
		}
		for _, r := range append(ended, gone...) {
			if r[2] != "" {
				blobs = append(blobs, r[2])
			}
			if err = event(ctx, tx, r[1], "view.expired", "view:"+r[0]); err != nil {
				return err
			}
		}
		return nil
	})
	return blobs, err
}

// viewRows reads id, user and blob triples.
func (s *Store) viewRows(ctx context.Context, tx *sql.Tx, q string, args ...any) ([][3]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out [][3]string
	for rows.Next() {
		var r [3]string
		if err = rows.Scan(&r[0], &r[1], &r[2]); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}
