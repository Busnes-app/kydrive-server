package drive

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/google/uuid"
)

// A guest link lets someone without an account view and download one file or folder, or, in
// upload-only mode, drop files into one folder without seeing it. It always expires. It works
// only while its creator could still create it, so a link dies with its creator's access.
// Every unusable link answers ErrNotFound; a missing or wrong password is ErrPassword.
var ErrPassword = errors.New("link password required or wrong")

type GuestLink struct {
	ID          string `json:"id"`
	Expires     int64  `json:"expires"`
	Created     string `json:"created"`
	CreatedBy   string `json:"created_by"`
	UploadOnly  bool   `json:"upload_only"`
	HasPassword bool   `json:"has_password"`
}

// GuestItem is a file or folder as a guest sees it: never its workspace or anything above the link.
type GuestItem struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type GuestListing struct {
	Name       string      `json:"name"` // the shared item's name
	Kind       string      `json:"kind"`
	UploadOnly bool        `json:"upload_only"`
	Path       []GuestItem `json:"path"` // folders from the link's folder down to the one listed
	Items      []GuestItem `json:"items"`
}

// CreateGuestLink returns a guest link and its token, which is not stored. maxLife is the
// administrator's limit; the expiry is mandatory.
func (s *Store) CreateGuestLink(ctx context.Context, user, kind, id string, expires int64, secret string, uploadOnly bool, maxLife time.Duration) (GuestLink, string, error) {
	g := GuestLink{ID: uuid.NewString(), Expires: expires, Created: now(), CreatedBy: user, UploadOnly: uploadOnly, HasPassword: secret != ""}
	if expires <= time.Now().Unix() || expires > time.Now().Add(maxLife).Unix() || (uploadOnly && kind != "folder") || (secret != "" && (len(secret) < 8 || len(secret) > 1024)) {
		return g, "", ErrInvalid
	}
	hash := ""
	if secret != "" {
		var err error
		if hash, err = password.Hash(secret); err != nil {
			return g, "", err
		}
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		workspace, folder, file, trashed, err := resource(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		if err = guestRights(ctx, tx, user, workspace, folder, file, uploadOnly); err != nil {
			return err
		}
		if trashed {
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO drive_links(id,workspace,resource_kind,resource_id,role,scope,token_hash,expires,password_hash,upload_only,created_by,created) VALUES(?,?,?,?,'reader','guest',?,?,?,?,?,?)`, g.ID, workspace, kind, id, tokenHash(token), expires, hash, uploadOnly, user, g.Created); err != nil {
			return err
		}
		return event(ctx, tx, user, "guest_link.created", g.ID)
	})
	return g, token, err
}

// guestRights is what creating, and later using, a guest link needs: the right to share the
// item, and edit rights on the folder for an upload-only link.
func guestRights(ctx context.Context, q queryer, user, workspace, folder, file string, uploadOnly bool) error {
	if err := mayShare(ctx, q, user, workspace, folder, file, "reader"); err != nil {
		return err
	}
	if uploadOnly {
		return authorizeItem(ctx, q, user, workspace, folder, "", 2)
	}
	return nil
}

// GuestLinks lists an item's live guest links for those who may share it.
func (s *Store) GuestLinks(ctx context.Context, user, kind, id string) ([]GuestLink, error) {
	workspace, folder, file, _, err := resource(ctx, s.db, kind, id)
	if err != nil {
		return nil, err
	}
	if err = mayShare(ctx, s.db, user, workspace, folder, file, "reader"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT l.id,l.expires,l.created,COALESCE((SELECT COALESCE(NULLIF(display_name,''),username) FROM users WHERE id=l.created_by),'a removed account'),l.upload_only,l.password_hash!='' FROM drive_links l WHERE l.resource_kind=? AND l.resource_id=? AND l.scope='guest' AND l.revoked=0 AND l.expires>? ORDER BY l.created`, kind, id, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GuestLink{}
	for rows.Next() {
		var g GuestLink
		if err = rows.Scan(&g.ID, &g.Expires, &g.Created, &g.CreatedBy, &g.UploadOnly, &g.HasPassword); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

type guestAccess struct {
	link, workspace, kind, root, creator string
	uploadOnly                           bool
}

// guest checks a token and password. Only a live link whose item is live and whose creator may
// still create it passes; the password is checked last so a wrong one cannot probe the rest.
func (s *Store) guest(ctx context.Context, token, secret string) (guestAccess, error) {
	var g guestAccess
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace,resource_kind,resource_id,created_by,upload_only,password_hash FROM drive_links WHERE token_hash=? AND scope='guest' AND revoked=0 AND expires>?`, tokenHash(token), time.Now().Unix()).
		Scan(&g.link, &g.workspace, &g.kind, &g.root, &g.creator, &g.uploadOnly, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	_, folder, file, trashed, err := resource(ctx, s.db, g.kind, g.root)
	if err != nil || trashed {
		return g, ErrNotFound
	}
	if err = guestRights(ctx, s.db, g.creator, g.workspace, folder, file, g.uploadOnly); errors.Is(err, ErrDenied) {
		return g, ErrNotFound
	} else if err != nil {
		return g, err
	}
	if hash != "" {
		ok, err := password.Verify(secret, hash)
		if err != nil || !ok {
			return g, ErrPassword
		}
	}
	return g, nil
}

// LinkID names a guest link for rate limiting and audit without revealing its token.
func LinkID(token string) string { return tokenHash(token)[:16] }

// within reports whether folder is root or lies below it, live and in workspace.
func within(ctx context.Context, q queryer, workspace, folder, root string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `WITH RECURSIVE up(id) AS (SELECT id FROM drive_folders WHERE id=? AND workspace=? AND trashed=false UNION SELECT f.parent FROM drive_folders f JOIN up ON f.id=up.id WHERE f.parent!='') SELECT COUNT(*) FROM up WHERE id=?`, folder, workspace, root).Scan(&n)
	return n > 0, err
}

// GuestView lists what a guest link shows: the file, or a folder at or below the link's folder.
func (s *Store) GuestView(ctx context.Context, token, secret, folder string) (GuestListing, error) {
	g, err := s.guest(ctx, token, secret)
	if err != nil {
		return GuestListing{}, err
	}
	out := GuestListing{Kind: g.kind, UploadOnly: g.uploadOnly, Path: []GuestItem{}, Items: []GuestItem{}}
	if g.kind == "file" {
		files, err := s.queryFiles(ctx, `f.id=? AND f.trashed=false`, g.root)
		if err != nil || len(files) == 0 {
			return out, cmpErr(err)
		}
		out.Name = files[0].Name
		out.Items = append(out.Items, GuestItem{Kind: "file", ID: files[0].ID, Name: files[0].Name, Size: files[0].Size})
		return out, nil
	}
	if folder == "" || g.uploadOnly {
		folder = g.root
	}
	if ok, err := within(ctx, s.db, g.workspace, folder, g.root); err != nil || !ok {
		return out, cmpErr(err)
	}
	chain, err := s.queryFolders(ctx, `id IN (WITH RECURSIVE up(id) AS (SELECT ? UNION SELECT f.parent FROM drive_folders f JOIN up ON f.id=up.id WHERE f.id!=?) SELECT id FROM up)`, folder, g.root)
	if err != nil {
		return out, err
	}
	byID := map[string]Folder{}
	for _, f := range chain {
		byID[f.ID] = f
	}
	for at := folder; ; at = byID[at].Parent {
		out.Path = append([]GuestItem{{Kind: "folder", ID: at, Name: byID[at].Name}}, out.Path...)
		if at == g.root {
			break
		}
	}
	out.Name = out.Path[0].Name
	if g.uploadOnly {
		out.Path = out.Path[:1]
		return out, nil // drop files in; never see what is there
	}
	folders, err := s.queryFolders(ctx, `workspace=? AND parent=? AND trashed=false`, g.workspace, folder)
	if err != nil {
		return out, err
	}
	files, err := s.queryFiles(ctx, `f.workspace=? AND f.parent=? AND f.trashed=false`, g.workspace, folder)
	if err != nil {
		return out, err
	}
	for _, f := range folders {
		out.Items = append(out.Items, GuestItem{Kind: "folder", ID: f.ID, Name: f.Name})
	}
	for _, f := range files {
		out.Items = append(out.Items, GuestItem{Kind: "file", ID: f.ID, Name: f.Name, Size: f.Size})
	}
	return out, nil
}

// cmpErr turns "nothing there" into ErrNotFound and passes real errors through.
func cmpErr(err error) error {
	if err != nil {
		return err
	}
	return ErrNotFound
}

// GuestFile returns a file a guest link can download, and audits the download.
func (s *Store) GuestFile(ctx context.Context, token, secret, id string) (File, error) {
	g, err := s.guest(ctx, token, secret)
	if err != nil {
		return File{}, err
	}
	if g.uploadOnly {
		return File{}, ErrNotFound
	}
	files, err := s.queryFiles(ctx, `f.id=? AND f.workspace=? AND f.trashed=false`, id, g.workspace)
	if err != nil || len(files) == 0 {
		return File{}, cmpErr(err)
	}
	f := files[0]
	if g.kind == "file" && f.ID != g.root {
		return File{}, ErrNotFound
	}
	if g.kind == "folder" {
		if ok, err := within(ctx, s.db, g.workspace, f.Parent, g.root); err != nil || !ok {
			return File{}, cmpErr(err)
		}
	}
	return f, s.transaction(ctx, func(tx *sql.Tx) error {
		return event(ctx, tx, "guest-link:"+g.link, "guest_link.downloaded", f.ID)
	})
}

// GuestUpload publishes a blob into an upload-only link's folder on its creator's authority.
func (s *Store) GuestUpload(ctx context.Context, token, secret, name string, v Version) (File, error) {
	if !ValidName(name) {
		return File{}, ErrInvalid
	}
	g, err := s.guest(ctx, token, secret)
	if err != nil {
		return File{}, err
	}
	if !g.uploadOnly {
		return File{}, ErrNotFound
	}
	f := File{ID: uuid.NewString(), Workspace: g.workspace, Parent: g.root, Name: name}
	v.FileID, v.Revision, v.Created = f.ID, 1, now()
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := s.publishTX(ctx, tx, g.creator, f, v, 0); err != nil {
			return err
		}
		return event(ctx, tx, "guest-link:"+g.link, "guest_link.uploaded", f.ID)
	})
	f.Revision, f.Size, f.Digest, f.Blob, f.Sealed, f.Modified = 1, v.Size, v.Digest, v.Blob, v.Sealed, v.Created
	return f, err
}
