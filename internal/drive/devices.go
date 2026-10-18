package drive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Device credentials are a desktop client's own bearer tokens. The user approves a link in
// the browser; the client receives a one-time code on its loopback listener and redeems it
// with its PKCE verifier. Codes and tokens are 256 random bits, stored as SHA-256 only.
const (
	DevicePrefix      = "kyd_"
	devicesPerUser    = 20
	deviceCodeLife    = 60 * time.Second
	deviceIdle        = 90 * 24 * time.Hour
	deviceRevokedKeep = 30 * 24 * time.Hour
	deviceTouch       = 5 * time.Minute
)

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Created  string `json:"created"`
	LastUsed int64  `json:"last_used"`
}

// ErrDeviceRevoked answers a revoked token for 30 days, telling the device whether to wipe.
type ErrDeviceRevoked struct{ Wipe bool }

func (ErrDeviceRevoked) Error() string { return "device revoked" }

// Is lets a revoked token count as denied wherever its wipe flag is not needed.
func (ErrDeviceRevoked) Is(target error) bool { return target == ErrDenied }

type deviceKey struct{}

// WithDevice marks ctx as acting through a device token: the user's own access, never
// administration and never a further credential.
func WithDevice(ctx context.Context) context.Context {
	return context.WithValue(ctx, deviceKey{}, true)
}

func isDevice(ctx context.Context) bool { return ctx.Value(deviceKey{}) != nil }

// ApproveLink records the signed-in user's approval and returns the one-time code for the
// client's loopback redirect.
func (s *Store) ApproveLink(ctx context.Context, user, name, platform, challenge, redirect string) (string, error) {
	if !ValidName(name) || len(name) > 100 || !validPlatform(platform) || !validChallenge(challenge) || !loopbackRedirect(redirect) {
		return "", ErrInvalid
	}
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service || isDevice(ctx) {
		return "", ErrDenied
	}
	code := secret256()
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := deviceSlot(ctx, tx, user); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO drive_device_codes(code_hash,user_id,name,platform,challenge,expires) VALUES(?,?,?,?,?,?)`, tokenHash(code), user, name, platform, challenge, time.Now().Add(deviceCodeLife).Unix())
		return err
	})
	return code, err
}

// RedeemLink spends code once, checks the verifier against its S256 challenge and returns a
// new device token and its user. Any failure is ErrDenied and burns the code.
func (s *Store) RedeemLink(ctx context.Context, code, verifier string) (token, user string, err error) {
	if code == "" || !validVerifier(verifier) {
		return "", "", ErrDenied
	}
	denied := false
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var name, platform, challenge string
		var expires int64
		err := tx.QueryRowContext(ctx, `SELECT user_id,name,platform,challenge,expires FROM drive_device_codes WHERE code_hash=?`, tokenHash(code)).Scan(&user, &name, &platform, &challenge, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		if res, err := tx.ExecContext(ctx, `UPDATE drive_device_codes SET used=1 WHERE code_hash=? AND used=0`, tokenHash(code)); err != nil {
			return err
		} else if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errors.Join(err, ErrDenied)
		}
		sum := sha256.Sum256([]byte(verifier))
		if time.Now().Unix() >= expires || subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) != 1 {
			denied = true // commit the burn
			return nil
		}
		if err := deviceSlot(ctx, tx, user); err != nil {
			return err
		}
		token = DevicePrefix + secret256()
		id := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_devices(id,user_id,name,platform,hash,created,last_used) VALUES(?,?,?,?,?,?,?)`, id, user, name, platform, tokenHash(token), now(), time.Now().Unix()); err != nil {
			return err
		}
		return event(ctx, tx, user, "device.linked", id)
	})
	if err != nil || denied {
		return "", "", errors.Join(err, ErrDenied)
	}
	return token, user, nil
}

// deviceSlot requires an active user with fewer than 20 live devices.
func deviceSlot(ctx context.Context, tx *sql.Tx, user string) error {
	var active, count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND status='active'`, user).Scan(&active); err != nil {
		return err
	}
	if active != 1 {
		return ErrDenied
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_devices WHERE user_id=? AND revoked_at=0`, user).Scan(&count); err != nil {
		return err
	}
	if count >= devicesPerUser {
		return fmt.Errorf("%w: at most %d computers", ErrQuota, devicesPerUser)
	}
	return nil
}

// DeviceUser returns the active user a live device token belongs to. A token revoked in the
// last 30 days is ErrDeviceRevoked; anything else unusable is ErrDenied.
func (s *Store) DeviceUser(ctx context.Context, token string) (string, error) {
	if !strings.HasPrefix(token, DevicePrefix) {
		return "", ErrDenied
	}
	var id, user, status string
	var lastUsed, revokedAt int64
	var wipe bool
	err := s.db.QueryRowContext(ctx, `SELECT d.id,d.user_id,d.last_used,d.revoked_at,d.wipe,COALESCE(u.status,'') FROM drive_devices d LEFT JOIN users u ON u.id=d.user_id WHERE d.hash=?`, tokenHash(token)).Scan(&id, &user, &lastUsed, &revokedAt, &wipe, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDenied
	}
	if err != nil {
		return "", err
	}
	t := time.Now()
	if revokedAt != 0 {
		if t.Sub(time.Unix(revokedAt, 0)) < deviceRevokedKeep {
			return "", ErrDeviceRevoked{Wipe: wipe}
		}
		return "", ErrDenied
	}
	if status != "active" || t.Sub(time.Unix(lastUsed, 0)) >= deviceIdle {
		return "", ErrDenied
	}
	if t.Sub(time.Unix(lastUsed, 0)) >= deviceTouch {
		if _, err = s.db.ExecContext(ctx, `UPDATE drive_devices SET last_used=? WHERE id=?`, t.Unix(), id); err != nil {
			return "", err
		}
	}
	return user, nil
}

// Devices lists the user's live (unrevoked) devices.
func (s *Store) Devices(ctx context.Context, user string) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,platform,created,last_used FROM drive_devices WHERE user_id=? AND revoked_at=0 ORDER BY created`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err = rows.Scan(&d.ID, &d.Name, &d.Platform, &d.Created, &d.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeDevice stops one of the user's devices at once; the token keeps answering with wipe
// for 30 days.
func (s *Store) RevokeDevice(ctx context.Context, user, id string, wipe bool) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE drive_devices SET revoked_at=?,wipe=? WHERE id=? AND user_id=? AND revoked_at=0`, time.Now().Unix(), wipe, id, user)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errors.Join(err, ErrDenied)
		}
		return event(ctx, tx, user, "device.revoked", fmt.Sprintf("%s wipe=%v", id, wipe))
	})
}

// UnlinkDevice revokes the device that token belongs to, without wipe: the user unlinked it
// from the computer itself, which keeps its files.
func (s *Store) UnlinkDevice(ctx context.Context, user, token string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, `UPDATE drive_devices SET revoked_at=?,wipe=0 WHERE hash=? AND user_id=? AND revoked_at=0 RETURNING id`, time.Now().Unix(), tokenHash(token), user).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		return event(ctx, tx, user, "device.revoked", id+" wipe=false")
	})
}

// PruneDevices deletes devices revoked 30 days before at or idle for 90, and expired codes.
func (s *Store) PruneDevices(ctx context.Context, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM drive_devices WHERE (revoked_at>0 AND revoked_at<?) OR (revoked_at=0 AND last_used<?)`, at.Add(-deviceRevokedKeep).Unix(), at.Add(-deviceIdle).Unix()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM drive_device_codes WHERE expires<?`, at.Unix())
	return err
}

func secret256() string {
	raw := make([]byte, 32)
	rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func validPlatform(p string) bool { return p == "windows" || p == "macos" || p == "linux" }

// validChallenge is an S256 PKCE challenge: base64url of 32 bytes, unpadded.
func validChallenge(c string) bool {
	b, err := base64.RawURLEncoding.Strict().DecodeString(c)
	return err == nil && len(b) == sha256.Size
}

// validVerifier is RFC 7636's code_verifier: 43-128 unreserved characters.
func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
			return false
		}
	}
	return true
}

// loopbackRedirect accepts only http://127.0.0.1:<port> or http://[::1]:<port> with an
// optional path (RFC 8252 §7.3): no names, userinfo, query or fragment, and only the canonical
// spelling, so no parser can read it as another host.
func loopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 200 || u.Scheme != "http" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.String() != raw {
		return false
	}
	host, port := u.Hostname(), u.Port()
	n, err := strconv.Atoi(port)
	return (host == "127.0.0.1" || host == "::1") && u.Host == net.JoinHostPort(host, port) && err == nil && n > 0 && n < 65536 && strconv.Itoa(n) == port
}
