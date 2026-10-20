// Package dav serves the drive over WebDAV: a webdav.FileSystem built per request for one
// user over the drive store. Every operation goes through the store, which authorizes it.
package dav

import (
	"cmp"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// FS is one request's view of the drive. Paths are /<workspace>/<folder>/.../<name>.
type FS struct {
	store   *drive.Store
	user    string
	blobs   string
	key     []byte
	maxFile int64
	// IfMatch is set when the request's If-Match was checked: a write then publishes against
	// exactly the revision opened, instead of retrying as last writer wins.
	IfMatch bool
	// Err is the last drive error, so the caller can answer with its real status: x/net
	// reports every failed PUT as 405.
	Err        error
	bodyErr    error
	workspaces map[string]string // display name -> workspace ID, filled on first use
	mtime      string            // client mtime for a PUT, RFC 3339; "" when not sent
	mtimeSaved bool              // a version was published with mtime
}

// Body wraps a request body so a failed read (a dropped connection) is remembered: x/net closes
// the file even when copying the body failed, and a truncated upload must not be published.
func (f *FS) Body(r io.ReadCloser) io.ReadCloser { return &body{r, f} }

type body struct {
	io.ReadCloser
	fs *FS
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		b.fs.bodyErr = err
	}
	return n, err
}

// New builds one request's view. mtime is the request's X-OC-Mtime from Mtime.
func New(store *drive.Store, user, blobs string, key []byte, maxFile int64, mtime string) *FS {
	return &FS{store: store, user: user, blobs: blobs, key: key, maxFile: maxFile, mtime: mtime}
}

// Mtime parses an X-OC-Mtime header (Unix seconds, as ownCloud clients and rclone send it) into
// the drive's form. "" means absent.
func Mtime(header string) (string, error) {
	if header == "" {
		return "", nil
	}
	n, err := strconv.ParseInt(header, 10, 64)
	if err != nil {
		return "", fmt.Errorf("%w: X-OC-Mtime", drive.ErrInvalid)
	}
	return drive.Mtime(time.Unix(n, 0).UTC().Format(time.RFC3339))
}

// node is a resolved path: the root, a workspace root (folder ""), a folder or a file.
type node struct {
	name      string
	workspace string
	folder    string // folder ID; "" with a workspace means its root
	file      *drive.File
}

func (n node) dir() bool { return n.file == nil }

// fail records err and returns the os error x/net tests for.
func (f *FS) fail(op, name string, err error) error {
	f.Err = err
	var target error
	switch {
	case errors.Is(err, drive.ErrNotFound):
		target = fs.ErrNotExist
	case errors.Is(err, drive.ErrDenied):
		target = fs.ErrPermission
	case errors.Is(err, drive.ErrConflict):
		target = fs.ErrExist
	default:
		return err
	}
	return &fs.PathError{Op: op, Path: name, Err: target}
}

// spaces names each readable workspace; duplicate names get " (2)", " (3)" in workspace ID order.
func (f *FS) spaces(ctx context.Context) (map[string]string, error) {
	if f.workspaces != nil {
		return f.workspaces, nil
	}
	list, err := f.store.Workspaces(ctx, f.user, false)
	if err != nil {
		return nil, err
	}
	f.workspaces = WorkspaceNames(list)
	return f.workspaces, nil
}

// WorkspaceNames maps each workspace's display name to its ID; duplicate names get " (2)",
// " (3)" in workspace ID order. The MCP tools use it so agents see the WebDAV tree.
func WorkspaceNames(list []drive.Workspace) map[string]string {
	list = slices.Clone(list)
	slices.SortFunc(list, func(a, b drive.Workspace) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
	names := map[string]string{}
	for _, w := range list {
		name := w.Name
		for i := 2; names[name] != ""; i++ {
			name = fmt.Sprintf("%s (%d)", w.Name, i)
		}
		names[name] = w.ID
	}
	return names
}

func split(name string) []string {
	clean := strings.Trim(path.Clean("/"+name), "/")
	if clean == "" {
		return nil
	}
	return strings.Split(clean, "/")
}

func (f *FS) resolve(ctx context.Context, name string) (node, error) {
	parts := split(name)
	if len(parts) == 0 {
		return node{name: "/"}, nil
	}
	spaces, err := f.spaces(ctx)
	if err != nil {
		return node{}, err
	}
	n := node{name: parts[0], workspace: spaces[parts[0]]}
	if n.workspace == "" {
		return node{}, drive.ErrNotFound
	}
	for i, part := range parts[1:] {
		if !n.dir() {
			return node{}, drive.ErrNotFound
		}
		it, err := f.store.Lookup(ctx, f.user, n.workspace, n.folder, part)
		if err != nil {
			return node{}, err
		}
		n.name = part
		if it.Folder != nil {
			n.folder = it.Folder.ID
		} else {
			n.file = it.File
			if i != len(parts)-2 {
				return node{}, drive.ErrNotFound
			}
		}
	}
	return n, nil
}

// parentOf resolves the directory that would hold name, which must be inside a workspace.
func (f *FS) parentOf(ctx context.Context, name string) (node, string, error) {
	parts := split(name)
	if len(parts) < 2 {
		return node{}, "", drive.ErrDenied // workspaces are not created, renamed or removed here
	}
	dir, err := f.resolve(ctx, strings.Join(parts[:len(parts)-1], "/"))
	if err == nil && !dir.dir() {
		err = drive.ErrNotFound
	}
	return dir, parts[len(parts)-1], err
}

func (f *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	n, err := f.resolve(ctx, name)
	if err != nil {
		return nil, f.fail("stat", name, err)
	}
	return info(n), nil
}

func (f *FS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	dir, base, err := f.parentOf(ctx, name)
	if err == nil {
		_, err = f.store.AddFolder(ctx, f.user, dir.workspace, dir.folder, base)
	}
	if err != nil {
		return f.fail("mkdir", name, err)
	}
	return nil
}

// RemoveAll moves a file or folder to the trash; WebDAV never purges.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	if len(split(name)) < 2 {
		return f.fail("remove", name, drive.ErrDenied)
	}
	n, err := f.resolve(ctx, name)
	switch {
	case err != nil:
	case n.file != nil:
		err = f.store.SetTrash(ctx, f.user, n.file.ID, n.file.Revision, true)
	default:
		err = f.store.SetFolderTrash(ctx, f.user, n.folder, true)
	}
	if err != nil {
		return f.fail("remove", name, err)
	}
	return nil
}

// Rename moves a file anywhere the store allows, and a folder within its workspace.
func (f *FS) Rename(ctx context.Context, oldName, newName string) error {
	err := f.rename(ctx, oldName, newName)
	if err != nil {
		return f.fail("rename", oldName, err)
	}
	return nil
}

func (f *FS) rename(ctx context.Context, oldName, newName string) error {
	if len(split(oldName)) < 2 {
		return drive.ErrDenied
	}
	src, err := f.resolve(ctx, oldName)
	if err != nil {
		return err
	}
	dir, base, err := f.parentOf(ctx, newName)
	if err != nil {
		return err
	}
	if src.file != nil {
		_, err = f.store.MoveFile(ctx, f.user, src.file.ID, src.file.Revision, drive.Location{Workspace: dir.workspace, Parent: dir.folder, Name: base})
		return err
	}
	if dir.workspace != src.workspace {
		return drive.ErrDenied
	}
	_, err = f.store.MoveFolder(ctx, f.user, src.folder, dir.folder, base)
	return err
}

func (f *FS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0 {
		return f.create(ctx, name)
	}
	n, err := f.resolve(ctx, name)
	if err != nil {
		return nil, f.fail("open", name, err)
	}
	if n.dir() {
		return &dirFile{fs: f, ctx: ctx, n: n}, nil
	}
	p, err := drive.BlobPath(f.blobs, n.file.Blob)
	if err != nil {
		return nil, err
	}
	r, err := drive.OpenBlob(p, f.key, n.file.Sealed, n.file.Size)
	if err != nil {
		return nil, err
	}
	return &readFile{ReadSeekCloser: r, n: n}, nil
}

// create opens name for writing: bytes stream into a sealed blob, published on Close.
func (f *FS) create(ctx context.Context, name string) (webdav.File, error) {
	dir, base, err := f.parentOf(ctx, name)
	if err != nil {
		return nil, f.fail("create", name, err)
	}
	target := drive.File{Workspace: dir.workspace, Parent: dir.folder, Name: base}
	it, err := f.store.Lookup(ctx, f.user, dir.workspace, dir.folder, base)
	switch {
	case err == nil && it.Folder != nil:
		return nil, f.fail("create", name, drive.ErrConflict)
	case err == nil:
		target = *it.File
	case !errors.Is(err, drive.ErrNotFound):
		return nil, f.fail("create", name, err)
	}
	if err = f.store.AuthorizeItem(ctx, f.user, dir.workspace, dir.folder, target.ID, 2); err != nil {
		return nil, f.fail("create", name, err)
	}
	pr, pw := io.Pipe()
	w := &writeFile{fs: f, ctx: ctx, target: target, pw: pw, done: make(chan blobResult, 1), info: &fileInfo{name: base}}
	go func() {
		v, err := drive.WriteBlob(f.blobs, f.key, pr, f.maxFile)
		pr.CloseWithError(cmp.Or(err, io.EOF))
		w.done <- blobResult{v, err}
	}()
	return w, nil
}

type blobResult struct {
	v   drive.Version
	err error
}

type writeFile struct {
	fs     *FS
	ctx    context.Context
	target drive.File
	pw     *io.PipeWriter
	done   chan blobResult
	info   *fileInfo
	closed bool
}

func (w *writeFile) Write(p []byte) (int, error) { return w.pw.Write(p) }

// Stat is called before Close; the ETag is read after, from the same fileInfo.
func (w *writeFile) Stat() (os.FileInfo, error) { return w.info, nil }

func (w *writeFile) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.pw.CloseWithError(w.fs.bodyErr) // nil closes normally
	res := <-w.done
	if res.err != nil {
		return w.fs.fail("write", w.target.Name, res.err)
	}
	f, err := w.publish(res.v)
	if err != nil {
		drive.RemoveBlobs(w.fs.blobs, []string{res.v.Blob})
		return w.fs.fail("write", w.target.Name, err)
	}
	*w.info = fileInfo{name: f.Name, size: f.Size, mod: modified(f), etag: etag(f)}
	w.fs.mtimeSaved = w.fs.mtime != ""
	return nil
}

// publish writes against the revision opened. Without If-Match, a newer revision that landed
// meanwhile loses to this write (last writer wins) and stays in the version history.
func (w *writeFile) publish(v drive.Version) (drive.File, error) {
	t := w.target
	v.Mtime = w.fs.mtime
	for range 3 {
		f, err := w.fs.store.Publish(w.ctx, w.fs.user, t, v, t.Revision)
		if !errors.Is(err, drive.ErrConflict) || w.fs.IfMatch {
			return f, err
		}
		it, lerr := w.fs.store.Lookup(w.ctx, w.fs.user, t.Workspace, t.Parent, t.Name)
		switch {
		case errors.Is(lerr, drive.ErrNotFound):
			t.ID, t.Revision = "", 0
		case lerr != nil:
			return f, lerr
		case it.File == nil:
			return f, err
		default:
			t = *it.File
		}
	}
	return drive.File{}, drive.ErrConflict
}

func (w *writeFile) Read([]byte) (int, error)                              { return 0, fs.ErrPermission }
func (w *writeFile) Seek(int64, int) (int64, error)                        { return 0, fs.ErrPermission }
func (w *writeFile) Readdir(int) ([]os.FileInfo, error)                    { return nil, fs.ErrPermission }
func (w *writeFile) DeadProps() (map[xml.Name]webdav.Property, error)      { return nil, nil }
func (w *writeFile) Patch(p []webdav.Proppatch) ([]webdav.Propstat, error) { return accept(p), nil }

type readFile struct {
	io.ReadSeekCloser
	n node
}

func (r *readFile) Write([]byte) (int, error)                             { return 0, fs.ErrPermission }
func (r *readFile) Readdir(int) ([]os.FileInfo, error)                    { return nil, fs.ErrInvalid }
func (r *readFile) Stat() (os.FileInfo, error)                            { return info(r.n), nil }
func (r *readFile) DeadProps() (map[xml.Name]webdav.Property, error)      { return nil, nil }
func (r *readFile) Patch(p []webdav.Proppatch) ([]webdav.Propstat, error) { return accept(p), nil }

type dirFile struct {
	fs   *FS
	ctx  context.Context
	n    node
	read bool
}

func (d *dirFile) Close() error                                          { return nil }
func (d *dirFile) Read([]byte) (int, error)                              { return 0, fs.ErrInvalid }
func (d *dirFile) Seek(int64, int) (int64, error)                        { return 0, fs.ErrInvalid }
func (d *dirFile) Write([]byte) (int, error)                             { return 0, fs.ErrPermission }
func (d *dirFile) Stat() (os.FileInfo, error)                            { return info(d.n), nil }
func (d *dirFile) DeadProps() (map[xml.Name]webdav.Property, error)      { return nil, nil }
func (d *dirFile) Patch(p []webdav.Proppatch) ([]webdav.Propstat, error) { return accept(p), nil }

// Readdir returns every child at once; x/net reads directories with count 0.
func (d *dirFile) Readdir(int) ([]os.FileInfo, error) {
	if d.read {
		return nil, nil
	}
	d.read = true
	var out []os.FileInfo
	if d.n.workspace == "" {
		spaces, err := d.fs.spaces(d.ctx)
		if err != nil {
			return nil, d.fs.fail("readdir", "/", err)
		}
		for name := range spaces {
			out = append(out, &fileInfo{name: name, dir: true})
		}
		slices.SortFunc(out, func(a, b os.FileInfo) int { return strings.Compare(a.Name(), b.Name()) })
		return out, nil
	}
	folders, files, err := d.fs.store.Children(d.ctx, d.fs.user, d.n.workspace, d.n.folder)
	if err != nil {
		return nil, d.fs.fail("readdir", d.n.name, err)
	}
	for _, f := range folders {
		out = append(out, &fileInfo{name: f.Name, dir: true})
	}
	for _, f := range files {
		out = append(out, info(node{name: f.Name, file: &f}))
	}
	return out, nil
}

// accept answers a PROPPATCH as applied and stores nothing: Windows sets Win32 times on every
// copy and fails the copy if they are refused.
func accept(patches []webdav.Proppatch) []webdav.Propstat {
	var props []webdav.Property
	for _, p := range patches {
		for _, prop := range p.Props {
			props = append(props, webdav.Property{XMLName: prop.XMLName})
		}
	}
	return []webdav.Propstat{{Status: 200, Props: props}}
}

type fileInfo struct {
	name string
	size int64
	mod  time.Time
	dir  bool
	etag string
}

func (i *fileInfo) Name() string       { return i.name }
func (i *fileInfo) Size() int64        { return i.size }
func (i *fileInfo) ModTime() time.Time { return i.mod }
func (i *fileInfo) IsDir() bool        { return i.dir }
func (i *fileInfo) Sys() any           { return nil }
func (i *fileInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}

// ETag is the file's ID and revision, which If-Match compares against.
func (i *fileInfo) ETag(context.Context) (string, error) {
	if i.etag == "" {
		return "", webdav.ErrNotImplemented
	}
	return i.etag, nil
}

// ContentType comes from the extension, so x/net never reads the blob to sniff it.
func (i *fileInfo) ContentType(context.Context) (string, error) {
	return cmp.Or(mime.TypeByExtension(path.Ext(i.name)), "application/octet-stream"), nil
}

func info(n node) *fileInfo {
	if n.file == nil {
		return &fileInfo{name: path.Base("/" + n.name), dir: true}
	}
	return &fileInfo{name: n.file.Name, size: n.file.Size, mod: modified(*n.file), etag: etag(*n.file)}
}

// The ID keeps a recreated file at the same path from reusing its predecessor's ETag.
func etag(f drive.File) string { return `"` + f.ID + "-" + strconv.FormatInt(f.Revision, 10) + `"` }

func modified(f drive.File) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, f.Mtime)
	return t
}
