package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ExportPrincipal is the sole exception to activePrincipal: an active member whose org is
// scheduled for deletion, enabled for Drive and freshly mirrored. It grants no ordinary access.
func (s *Store) ExportPrincipal(ctx context.Context, user string) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT `+exportPrincipal("?"), user).Scan(&ok)
	return ok, err
}

func exportPrincipal(user string) string {
	return `(` + multiTenantSQL + ` AND EXISTS(SELECT 1 FROM users u JOIN drive_orgs o ON o.id=u.org_id WHERE u.id=` + user + ` AND u.status='active' AND ` + orgAvailable("o", "deletion_scheduled") + `))`
}

// PersonalExport is a snapshot of the caller's own My files, including trash and every
// retained version. The manifest records hierarchy; ID/revision paths avoid name collisions.
// No workspace selector, staff override, share or link can expand this selection.
type PersonalExport struct {
	Folders  []Folder   `json:"folders"`
	Files    []File     `json:"files"`
	Versions []Version  `json:"versions"`
	Entries  []ZipEntry `json:"-"`
}

func (s *Store) PersonalExport(ctx context.Context, user string, maxBytes int64) (PersonalExport, error) {
	var out PersonalExport
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service || narrowed(ctx) {
		return out, ErrDenied
	}
	if !s.Multi() {
		return out, ErrNotFound
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		out = PersonalExport{Folders: []Folder{}, Files: []File{}, Versions: []Version{}}
		var ok bool
		if err := tx.QueryRowContext(ctx, `SELECT `+activePrincipal("?1")+` OR `+exportPrincipal("?1"), user).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrDenied
		}
		var ws string
		err := tx.QueryRowContext(ctx, `SELECT p.workspace FROM drive_personal_workspaces p JOIN drive_workspaces w ON w.id=p.workspace WHERE p.owner=? AND w.org_id=`+userOrg("?"), user, user).Scan(&ws)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // Do not create a workspace just to export an empty account.
		}
		if err != nil {
			return err
		}
		nk, err := s.namesOf(ctx, tx, workspaceOrg("?"), ws)
		if err != nil {
			return err
		}
		keys, err := s.keysOf(ctx, tx, workspaceOrg("?"), ws)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT d.id,d.parent,d.name,d.trashed FROM drive_folders d JOIN drive_workspaces w ON w.id=d.workspace WHERE w.id=? AND w.org_id=`+userOrg("?")+` ORDER BY d.id`, ws, user)
		if err != nil {
			return err
		}
		for rows.Next() {
			f := Folder{Workspace: ws}
			var stored string
			if err = rows.Scan(&f.ID, &f.Parent, &stored, &f.Trashed); err == nil {
				f.Name, err = nk.open("drive_folders", f.ID, stored)
			}
			if err != nil {
				rows.Close()
				return err
			}
			out.Folders = append(out.Folders, f)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.QueryContext(ctx, `SELECT f.id,f.parent,f.name,f.revision,f.trashed,v.revision,v.blob,v.digest,v.size,v.created,v.mtime,v.sealed FROM drive_files f JOIN drive_workspaces w ON w.id=f.workspace JOIN drive_versions v ON v.file_id=f.id WHERE w.id=? AND w.org_id=`+userOrg("?")+` ORDER BY f.id,v.revision`, ws, user)
		if err != nil {
			return err
		}
		defer rows.Close()
		var total int64
		for rows.Next() {
			f, v := File{Workspace: ws}, Version{}
			var stored string
			if err := rows.Scan(&f.ID, &f.Parent, &stored, &f.Revision, &f.Trashed, &v.Revision, &v.Blob, &v.Digest, &v.Size, &v.Created, &v.Mtime, &v.Sealed); err != nil {
				return err
			}
			if v.Size > maxBytes-total {
				return ErrTooLarge
			}
			total += v.Size
			if f.Name, err = nk.open("drive_files", f.ID, stored); err != nil {
				return err
			}
			if len(out.Files) == 0 || out.Files[len(out.Files)-1].ID != f.ID {
				out.Files = append(out.Files, f)
			}
			if v.Revision == f.Revision {
				current := &out.Files[len(out.Files)-1]
				current.Size, current.Digest, current.Modified, current.Mtime = v.Size, v.Digest, v.Created, v.Mtime
			}
			v.FileID = f.ID
			out.Versions = append(out.Versions, v)
			modified, _ := time.Parse(time.RFC3339Nano, v.Created)
			out.Entries = append(out.Entries, ZipEntry{Path: fmt.Sprintf("files/%s/%d/%s", f.ID, v.Revision, f.Name), Blob: v.Blob, Sealed: v.Sealed, Size: v.Size, Modified: modified, Keys: keys})
		}
		return rows.Err()
	})
	return out, err
}
