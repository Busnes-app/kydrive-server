package dav

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// maxLock caps a LOCK's timeout; clients refresh well within it, and a crashed client's lock
// cannot outlive it.
const maxLock = time.Hour

// Locks is the server's one lock table. Each request sees it through a view that names locks
// by workspace ID, not by the user's display names, and signs tokens to the user.
type Locks struct {
	ls     webdav.LockSystem
	shared SharedLocks
	key    []byte
}

func NewLocks() *Locks {
	key := make([]byte, 32)
	rand.Read(key)
	return &Locks{ls: webdav.NewMemLS(), key: key}
}

// SharedLocks is a lock table every replica sees (store.DavLocks).
type SharedLocks interface {
	Create(context.Context, webdav.LockDetails) (string, error)
	Confirm(ctx context.Context, name0, name1 string, tokens []string) (func(context.Context) error, error)
	Refresh(context.Context, string, time.Duration) (webdav.LockDetails, error)
	Unlock(context.Context, string) error
}

// NewSharedLocks keeps locks in shared and signs tokens under a key derived from the
// deployment key, so a token one replica issued is honoured by every other.
func NewSharedLocks(shared SharedLocks, deploymentKey []byte) *Locks {
	h := hmac.New(sha256.New, deploymentKey)
	h.Write([]byte("kydrive webdav lock tokens v1"))
	return &Locks{shared: shared, key: h.Sum(nil)}
}

// view is one request's LockSystem.
type view struct {
	*Locks
	ls     webdav.LockSystem
	fs     *FS
	ctx    context.Context
	method string
}

func (l *Locks) view(ctx context.Context, f *FS, method string) *view {
	ls := l.ls
	if l.shared != nil {
		ls = requestLocks{l.shared, ctx}
	}
	return &view{Locks: l, ls: ls, fs: f, ctx: ctx, method: method}
}

// requestLocks is SharedLocks as a webdav.LockSystem for one request. The table keeps
// database time, so x/net's now is unused. A release or unlock outlives a cancelled request:
// otherwise a client that hung up would leave its lock held.
type requestLocks struct {
	shared SharedLocks
	ctx    context.Context
}

func (r requestLocks) after() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.ctx), 10*time.Second)
}

func (r requestLocks) Confirm(_ time.Time, name0, name1 string, conditions ...webdav.Condition) (func(), error) {
	if name0 != "" {
		name0 = path.Clean("/" + name0)
	}
	if name1 != "" {
		name1 = path.Clean("/" + name1)
	}
	tokens := make([]string, len(conditions))
	for i, c := range conditions {
		tokens[i] = c.Token
	}
	release, err := r.shared.Confirm(r.ctx, name0, name1, tokens)
	if err != nil {
		return nil, err
	}
	return func() {
		ctx, cancel := r.after()
		defer cancel()
		if err := release(ctx); err != nil {
			log.Printf("dav: release locks: %v", err)
		}
	}, nil
}

func (r requestLocks) Create(_ time.Time, d webdav.LockDetails) (string, error) {
	d.Root = path.Clean("/" + d.Root)
	return r.shared.Create(r.ctx, d)
}

func (r requestLocks) Refresh(_ time.Time, token string, d time.Duration) (webdav.LockDetails, error) {
	return r.shared.Refresh(r.ctx, token, d)
}

func (r requestLocks) Unlock(_ time.Time, token string) error {
	ctx, cancel := r.after()
	defer cancel()
	return r.shared.Unlock(ctx, token)
}

// internal maps a display path to /<workspace ID>/<rest>, so two users' same-named
// workspaces never share a lock. A path outside the user's workspaces gets its own name.
func (v *view) internal(name string) string {
	parts := split(name)
	if len(parts) == 0 {
		return "/"
	}
	spaces, err := v.fs.spaces(v.ctx)
	id := spaces[parts[0]]
	if err != nil || id == "" {
		id = "unknown-" + v.fs.user
	}
	return "/" + strings.Join(append([]string{id}, parts[1:]...), "/")
}

// display maps an internal path back for the lock root a refresh echoes.
func (v *view) display(name string) string {
	parts := split(name)
	if spaces, err := v.fs.spaces(v.ctx); err == nil && len(parts) > 0 {
		for display, id := range spaces {
			if id == parts[0] {
				parts[0] = display
			}
		}
	}
	return "/" + strings.Join(parts, "/")
}

// sign binds an internal token to the user and the lock's workspace ("" for the root): another
// user cannot present or guess it, and a refresh can recheck access to that workspace.
func (v *view) sign(token, workspace string) string {
	return "urn:kydrive:lock:" + token + ":" + workspace + ":" + v.mac(token, workspace)
}

func (v *view) mac(token, workspace string) string {
	h := hmac.New(sha256.New, v.key)
	h.Write([]byte(v.fs.user + "\x00" + token + "\x00" + workspace))
	return hex.EncodeToString(h.Sum(nil))
}

func (v *view) unsign(signed string) (token, workspace string, ok bool) {
	rest, prefixed := strings.CutPrefix(signed, "urn:kydrive:lock:")
	parts := strings.Split(rest, ":")
	if !prefixed || len(parts) != 3 {
		return "", "", false
	}
	return parts[0], parts[1], hmac.Equal([]byte(parts[2]), []byte(v.mac(parts[0], parts[1])))
}

// lockTimeout caps a Timeout header's first value at maxLock; "Infinite", absent or malformed
// values become maxLock.
func lockTimeout(header string) string {
	secs := int64(maxLock / time.Second)
	first, _, _ := strings.Cut(header, ",")
	if v, ok := strings.CutPrefix(strings.TrimSpace(first), "Second-"); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			secs = min(n, secs)
		}
	}
	return "Second-" + strconv.FormatInt(secs, 10)
}

func capped(d time.Duration) time.Duration {
	if d < 0 || d > maxLock {
		return maxLock
	}
	return d
}

// Create needs edit access to the lock's workspace, both for LOCK and for the lock x/net takes
// around every write before the store sees it: otherwise a reader's slow request would block
// editors. A LOCK must name an item inside a workspace and lasts at most maxLock.
func (v *view) Create(now time.Time, d webdav.LockDetails) (string, error) {
	parts := split(d.Root)
	spaces, err := v.fs.spaces(v.ctx)
	workspace := ""
	if err == nil && len(parts) > 0 {
		workspace = spaces[parts[0]]
	}
	if workspace == "" || (v.method == "LOCK" && len(parts) < 2) || v.fs.store.Authorize(v.ctx, v.fs.user, workspace, 2) != nil {
		v.fs.Err = drive.ErrDenied // x/net answers any other Create error with 500
		return "", webdav.ErrForbidden
	}
	if v.method == "LOCK" {
		d.Duration = capped(d.Duration)
	}
	d.Root = v.internal(d.Root)
	token, err := v.ls.Create(now, d)
	if err != nil {
		return "", err
	}
	return v.sign(token, workspace), nil
}

// Confirm honours a presented token only while its owner can still edit the lock's workspace;
// otherwise the lock is released and the token matches nothing, so the request fails (412)
// before its body is read and editors are not held up.
func (v *view) Confirm(now time.Time, name0, name1 string, conditions ...webdav.Condition) (func(), error) {
	for i, c := range conditions {
		if c.Token == "" {
			continue
		}
		token, workspace, ok := v.unsign(c.Token)
		if ok && v.fs.store.Authorize(v.ctx, v.fs.user, workspace, 2) != nil {
			v.ls.Unlock(now, token)
			ok = false
		}
		conditions[i].Token = "invalid" // matches no lock
		if ok {
			conditions[i].Token = token
		}
	}
	if name0 != "" {
		name0 = v.internal(name0)
	}
	if name1 != "" {
		name1 = v.internal(name1)
	}
	return v.ls.Confirm(now, name0, name1, conditions...)
}

// Refresh extends a lock only while its owner can still edit the workspace it covers; a lock
// whose owner lost that access is released at once (412), so it stops blocking editors.
func (v *view) Refresh(now time.Time, signed string, d time.Duration) (webdav.LockDetails, error) {
	token, workspace, ok := v.unsign(signed)
	if !ok {
		return webdav.LockDetails{}, webdav.ErrNoSuchLock
	}
	if workspace == "" || v.fs.store.Authorize(v.ctx, v.fs.user, workspace, 2) != nil {
		v.ls.Unlock(now, token)
		return webdav.LockDetails{}, webdav.ErrNoSuchLock
	}
	ld, err := v.ls.Refresh(now, token, capped(d))
	ld.Root = v.display(ld.Root)
	return ld, err
}

func (v *view) Unlock(now time.Time, signed string) error {
	token, _, ok := v.unsign(signed)
	if !ok {
		return webdav.ErrNoSuchLock
	}
	return v.ls.Unlock(now, token)
}
