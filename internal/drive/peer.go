package drive

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/google/uuid"
)

// KyPost saves files into a user's My files through upload tickets (docs/KYPOST.md). What a
// peer context may do is access.go's; this file holds the switch, the folder and the tickets.
const (
	TicketLife     = 15 * time.Minute
	ticketsPerUser = 8
	// PeerFolderName is the folder KyPost's files go into, made on first use at the root of My files.
	PeerFolderName = "Mail attachments"
	personalQuota  = int64(10 << 30)
)

// ErrTicketLimit is a user already holding ticketsPerUser live tickets.
var ErrTicketLimit = errors.New("too many live upload tickets")

// Ticket is a live upload ticket. Session is the upload session its first byte opened.
type Ticket struct {
	ID      string
	Owner   string
	Name    string
	Size    int64
	Expires int64
	Session string
}

// PeerSetting is the user's stored choice for "Let KyPost save and share my files"; set is
// false when they never chose (the caller applies the pairing's default).
func (s *Store) PeerSetting(ctx context.Context, user string) (enabled, set bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT enabled FROM drive_peer_settings WHERE user_id=?`, user).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return enabled, err == nil, err
}

// SetPeerSetting stores the user's own choice. Only the user's sign-in reaches it.
func (s *Store) SetPeerSetting(ctx context.Context, user string, enabled bool) error {
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service || isDevice(ctx) || isPeer(ctx) {
		return ErrDenied
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_peer_settings(user_id,enabled) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled`, user, enabled); err != nil {
			return err
		}
		return event(ctx, tx, user, "kypost.switch_changed", fmt.Sprintf("enabled=%t", enabled))
	})
}

// PeerFree is the free quota of the user's My files, or a new one's quota when there is none.
func (s *Store) PeerFree(ctx context.Context, user string) (int64, error) {
	var free int64
	err := s.db.QueryRowContext(ctx, `SELECT w.quota-COALESCE((SELECT SUM(v.size) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=w.id),0) FROM drive_personal_workspaces p JOIN drive_workspaces w ON w.id=p.workspace WHERE p.owner=?`, user).Scan(&free)
	if errors.Is(err, sql.ErrNoRows) {
		return personalQuota, nil
	}
	return max(free, 0), err
}

// IssueTicket gives KyPost (a peer context) a ticket for one file of exactly size bytes named
// name, bound to user. The token is 256 random bits, returned once; only its hash is stored.
func (s *Store) IssueTicket(ctx context.Context, user, name string, size, maxSize int64) (string, Ticket, error) {
	t := Ticket{ID: uuid.NewString(), Owner: user, Name: name, Size: size, Expires: time.Now().Add(TicketLife).Unix()}
	if !isPeer(ctx) {
		return "", t, ErrDenied
	}
	// Every numbering must fit, as for guest uploads, or a long name fails only when taken.
	if !ValidName(name) || !ValidName(numbered(name, 100)) || size < 0 {
		return "", t, ErrInvalid
	}
	if size > maxSize {
		return "", t, ErrTooLarge
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
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_peer_tickets WHERE owner=? AND expires>?`, user, time.Now().Unix()).Scan(&live); err != nil {
			return err
		}
		if live >= ticketsPerUser {
			return ErrTicketLimit
		}
		stored, _, err := s.names.seal("drive_peer_tickets", t.ID, name)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO drive_peer_tickets(id,token_hash,owner,name,size,expires) VALUES(?,?,?,?,?,?)`, t.ID, tokenHash(token), user, stored, size, t.Expires); err != nil {
			return err
		}
		return event(ctx, tx, user, "kypost.ticket_issued", t.ID)
	})
	return token, t, err
}

// Ticket finds a live ticket by its token's hash; anything else is ErrNotFound.
func (s *Store) Ticket(ctx context.Context, token string) (Ticket, error) {
	var t Ticket
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT id,owner,name,size,expires,session FROM drive_peer_tickets WHERE token_hash=? AND expires>?`, tokenHash(token), time.Now().Unix()).
		Scan(&t.ID, &t.Owner, &stored, &t.Size, &t.Expires, &t.Session)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	t.Name, err = s.names.open("drive_peer_tickets", t.ID, stored)
	return t, err
}

// TicketUpload (a peer context) stores length bytes of r at start of t's file through the ordinary upload
// session path, as t's owner in a peer context. The first bytes open the session in the
// owner's peer folder (made or remade here), under the first free numbered name; the session
// expires with the ticket. The last bytes publish the file and spend the ticket. f is nil
// until then.
func (s *Store) TicketUpload(ctx context.Context, staging, blobs string, key []byte, t Ticket, start, length int64, r io.Reader) (received int64, f *File, err error) {
	if !isPeer(ctx) {
		return 0, nil, ErrDenied
	}
	if t.Session == "" {
		if start != 0 {
			return 0, nil, ErrConflict
		}
		if t.Session, err = s.ticketSession(ctx, staging, t); err != nil {
			return 0, nil, err
		}
	}
	var u UploadSession
	if length > 0 {
		u, err = s.AppendUpload(ctx, staging, key, t.Owner, t.Session, start, length, r)
	} else {
		u, err = s.Upload(ctx, t.Owner, t.Session)
	}
	if err != nil || u.Received < u.Size {
		return u.Received, nil, err
	}
	file, err := s.CompleteUpload(ctx, staging, blobs, key, t.Owner, t.Session)
	for n := 2; errors.Is(err, errNameTaken) && n <= 100; n++ {
		// Taken since the session opened: number it again.
		if err = s.renameUpload(ctx, t.Session, numbered(t.Name, n)); err == nil {
			file, err = s.CompleteUpload(ctx, staging, blobs, key, t.Owner, t.Session)
		}
	}
	if errors.Is(err, ErrPeerOff) {
		// Switched off while the bytes arrived: nothing is published, nothing kept.
		if derr := s.dropUpload(context.WithoutCancel(ctx), staging, t.Session, nil); derr != nil {
			log.Printf("kypost ticket %s: staged bytes not removed: %v", t.ID, derr)
		}
		s.db.ExecContext(context.WithoutCancel(ctx), `DELETE FROM drive_peer_tickets WHERE id=?`, t.ID)
	}
	if err != nil {
		return u.Received, nil, err
	}
	err = s.transaction(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM drive_peer_tickets WHERE id=?`, t.ID); err != nil {
			return err
		}
		return event(ctx, tx, t.Owner, "kypost.file_saved", file.ID+" peer=kypost ticket="+t.ID)
	})
	if err != nil {
		// Published: the ticket expires on its own and its session is gone.
		log.Printf("kypost ticket %s: published, not spent: %v", t.ID, err)
	}
	return u.Received, &file, nil
}

// ticketSession opens t's upload session and binds it to t; a concurrent first request loses.
func (s *Store) ticketSession(ctx context.Context, staging string, t Ticket) (string, error) {
	var u UploadSession
	err := s.transaction(ctx, func(tx *sql.Tx) (err error) {
		u.Workspace, u.Parent, err = s.peerFolder(ctx, tx, t.Owner)
		return err
	})
	if err != nil {
		return "", err
	}
	u.Name, u.Size = t.Name, t.Size
	opened, err := s.startUpload(ctx, t.Owner, u, t.Size, time.Unix(t.Expires, 0))
	for n := 2; errors.Is(err, errNameTaken) && n <= 100; n++ {
		u.Name = numbered(t.Name, n)
		opened, err = s.startUpload(ctx, t.Owner, u, t.Size, time.Unix(t.Expires, 0))
	}
	if err != nil {
		return "", err
	}
	n, err := rowsAffected(s.db.ExecContext(ctx, `UPDATE drive_peer_tickets SET session=? WHERE id=? AND session=''`, opened.ID, t.ID))
	if err != nil || n != 1 {
		if derr := s.dropUpload(context.WithoutCancel(ctx), staging, opened.ID, nil); derr != nil {
			log.Printf("kypost ticket %s: losing session not removed: %v", t.ID, derr)
		}
		if err == nil {
			err = fmt.Errorf("%w: upload already started", ErrConflict)
		}
		return "", err
	}
	return opened.ID, nil
}

// renameUpload gives a ticket's session a new name before its completion is retried.
func (s *Store) renameUpload(ctx context.Context, id, name string) error {
	stored, mac, err := s.names.seal("drive_upload_sessions", id, name)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE drive_upload_sessions SET name=?,name_hmac=? WHERE id=?`, stored, mac, id)
	return err
}

// peerFolder returns the user's live peer folder in their My files, adopting a live root
// folder of that name or making one when the recorded folder is gone, trashed or moved away.
func (s *Store) peerFolder(ctx context.Context, tx *sql.Tx, user string) (workspace, folder string, err error) {
	if workspace, err = ensurePersonal(ctx, tx, user); err != nil {
		return "", "", err
	}
	err = tx.QueryRowContext(ctx, `SELECT pf.folder FROM drive_peer_folders pf JOIN drive_folders d ON d.id=pf.folder AND d.workspace=? AND d.trashed=false WHERE pf.owner=?`, workspace, user).Scan(&folder)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return workspace, folder, err
	}
	for n := 1; folder == "" && n <= 100; n++ {
		name := PeerFolderName
		if n > 1 {
			name = numbered(name, n)
		}
		err = tx.QueryRowContext(ctx, `SELECT id FROM drive_folders WHERE workspace=? AND parent='' AND name_hmac=? AND trashed=false`, workspace, s.names.hmac(name)).Scan(&folder)
		if errors.Is(err, sql.ErrNoRows) {
			f := Folder{ID: uuid.NewString(), Workspace: workspace, Name: name}
			// A file holding the name is a conflict: try the next number.
			if err = s.addFolder(ctx, tx, user, f); err == nil {
				folder = f.ID
			} else if !errors.Is(err, ErrConflict) {
				return "", "", err
			}
		} else if err != nil {
			return "", "", err
		}
	}
	if folder == "" {
		return "", "", fmt.Errorf("%w: no free folder name", ErrConflict)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO drive_peer_folders(owner,folder) VALUES(?,?) ON CONFLICT(owner) DO UPDATE SET folder=excluded.folder`, user, folder)
	return workspace, folder, err
}

// ExpireTickets deletes expired tickets. Their sessions expire with them (ExpireUploads).
func (s *Store) ExpireTickets(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM drive_peer_tickets WHERE expires<=?`, time.Now().Unix())
	return err
}
