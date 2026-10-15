package dav

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	ls  webdav.LockSystem
	key []byte
}

func NewLocks() *Locks {
	key := make([]byte, 32)
	rand.Read(key)
	return &Locks{ls: webdav.NewMemLS(), key: key}
}

// view is one request's LockSystem.
type view struct {
	*Locks
	fs     *FS
	ctx    context.Context
	method string
}

func (l *Locks) view(ctx context.Context, f *FS, method string) *view {
	return &view{Locks: l, fs: f, ctx: ctx, method: method}
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

// sign binds an internal token to the user: another user cannot present or guess it.
func (v *view) sign(token string) string {
	return "urn:kydrive:lock:" + token + ":" + v.mac(token)
}

func (v *view) mac(token string) string {
	h := hmac.New(sha256.New, v.key)
	h.Write([]byte(v.fs.user + "\x00" + token))
	return hex.EncodeToString(h.Sum(nil))
}

func (v *view) unsign(signed string) (string, bool) {
	rest, ok := strings.CutPrefix(signed, "urn:kydrive:lock:")
	token, mac, found := strings.Cut(rest, ":")
	return token, ok && found && hmac.Equal([]byte(mac), []byte(v.mac(token)))
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

// Create takes a LOCK only on an item inside a workspace the user can edit, for at most maxLock.
// x/net also creates a lock for every write it confirms and releases it with the request;
// those pass through, since the write itself is authorized by the store.
func (v *view) Create(now time.Time, d webdav.LockDetails) (string, error) {
	if v.method == "LOCK" {
		parts := split(d.Root)
		spaces, err := v.fs.spaces(v.ctx)
		if len(parts) < 2 || err != nil || spaces[parts[0]] == "" || v.fs.store.Authorize(v.ctx, v.fs.user, spaces[parts[0]], 2) != nil {
			v.fs.Err = drive.ErrDenied // x/net answers any other Create error with 500
			return "", webdav.ErrForbidden
		}
		d.Duration = capped(d.Duration)
	}
	d.Root = v.internal(d.Root)
	token, err := v.ls.Create(now, d)
	if err != nil {
		return "", err
	}
	return v.sign(token), nil
}

func (v *view) Confirm(now time.Time, name0, name1 string, conditions ...webdav.Condition) (func(), error) {
	for i, c := range conditions {
		if c.Token != "" {
			if token, ok := v.unsign(c.Token); ok {
				conditions[i].Token = token
			} else {
				conditions[i].Token = "invalid" // matches no lock
			}
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

func (v *view) Refresh(now time.Time, signed string, d time.Duration) (webdav.LockDetails, error) {
	token, ok := v.unsign(signed)
	if !ok {
		return webdav.LockDetails{}, webdav.ErrNoSuchLock
	}
	ld, err := v.ls.Refresh(now, token, capped(d))
	ld.Root = v.display(ld.Root)
	return ld, err
}

func (v *view) Unlock(now time.Time, signed string) error {
	token, ok := v.unsign(signed)
	if !ok {
		return webdav.ErrNoSuchLock
	}
	return v.ls.Unlock(now, token)
}
