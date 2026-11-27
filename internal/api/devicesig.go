package api

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// Device request signing. A device that enrolled a signing key (ML-DSA-65+Ed25519 hybrid for
// software clients or ECDSA P-256 for hardware-backed mobile keystores and legacy clients) when
// it linked signs every request with it, so its bearer token alone authorises nothing: a
// certificate the device trusts in the wrong hands reads the token, but not the key, which never
// leaves the device's keystore.
//
// For a hybrid key, the device signs the message below under the "kydrive-request-v1" domain
// label (ky-primitives/hybridsig) and sends the encoded signature, std base64, in
// KyDrive-Signature. For a P-256 key, the device signs SHA-256 of the message with ECDSA P-256
// and sends the DER signature, std base64, in KyDrive-Signature. The message is
// "kydrive-request-v1" and these fields, joined by "|":
//
//  1. origin: the server's KY_APP_URL as scheme://host[:port], lowercased, default port dropped
//  2. method
//  3. request URI, exactly as sent on the request line
//  4. KyDrive-Date: Unix milliseconds, within deviceSkew of the server's clock
//  5. KyDrive-Nonce: 16 random bytes, base64url unpadded, once per device per window
//  6. hex SHA-256 of "name:value\n" for each of signedHeaders present, in that order
//  7. KyDrive-Content-SHA256: hex SHA-256 of the body, checked against the bytes received
const (
	deviceSigPrefix = "kydrive-request-v1"
	deviceSkew      = 5 * time.Minute
	// deviceNonces bounds one device's requests per window; a device past it waits, others don't.
	deviceNonces = 20000
	// signedBodyBuffer is the body checked before the handler runs; a longer one (an upload) is
	// streamed and its last byte refused unless the whole body matches.
	signedBodyBuffer = 1 << 20
)

// signedHeaders are the request headers a handler acts on. Lowercase, in signing order.
var signedHeaders = []string{"content-range", "depth", "destination", "if", "if-match", "if-none-match", "lock-token", "overwrite", "timeout", "x-oc-mtime"}

var (
	errDeviceSignature = errors.New("device request signature refused")
	errDeviceBody      = errors.New("request body does not match its signed digest")
	// errDeviceKeyRequired refuses a device linked before signing: it must link again.
	errDeviceKeyRequired = errors.New("device has no signing key")
)

// verifyDeviceRequest checks a request from the device id against its enrolled key, then binds
// the body to the signed digest. A device linked before signing has no key and is refused.
func (s *Server) verifyDeviceRequest(r *http.Request, id string, key *drive.DeviceKey) error {
	if key == nil {
		return errDeviceKeyRequired
	}
	date, nonce, digest := r.Header.Get("KyDrive-Date"), r.Header.Get("KyDrive-Nonce"), r.Header.Get("KyDrive-Content-SHA256")
	ms, err := strconv.ParseInt(date, 10, 64)
	if err != nil || strconv.FormatInt(ms, 10) != date {
		return errDeviceSignature
	}
	at := time.UnixMilli(ms)
	if d := s.now().Sub(at); d > deviceSkew || d < -deviceSkew {
		return errDeviceSignature
	}
	if n, err := base64.RawURLEncoding.DecodeString(nonce); err != nil || len(n) != 16 {
		return errDeviceSignature
	}
	want, err := hex.DecodeString(digest)
	if err != nil || len(want) != sha256.Size || digest != strings.ToLower(digest) {
		return errDeviceSignature
	}
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("KyDrive-Signature"))
	if err != nil {
		return errDeviceSignature
	}
	headers, ok := headerDigest(r.Header)
	if !ok {
		return errDeviceSignature
	}
	msg, ok := deviceMessage(canonicalOrigin(s.config.Server.AppURL), r.Method, r.RequestURI, date, nonce, headers, digest)
	if !ok || !key.Verify(deviceSigPrefix, []byte(msg), sig) {
		return errDeviceSignature
	}
	var guard any = sharedReplay{s, "device:" + id, 2 * deviceSkew}
	if s.shared == nil {
		var ok bool
		if guard, ok = s.deviceNonces.Load(id); !ok {
			guard, _ = s.deviceNonces.LoadOrStore(id, syncauth.NewMemoryReplay(2*deviceSkew, deviceNonces))
		}
	}
	// Held for 2*deviceSkew from now: a date accepted now stays acceptable for at most that long.
	if err := guard.(syncauth.Replay).Check(nonce, time.Now()); err != nil {
		if errors.Is(err, syncauth.ErrReplay) {
			return errDeviceSignature
		}
		return err
	}
	return bindBody(r, want)
}

// deviceMessage joins the signed fields, refusing any that would make the joining ambiguous.
func deviceMessage(fields ...string) (string, bool) {
	for _, f := range fields {
		if f == "" || strings.ContainsAny(f, "|\r\n") {
			return "", false
		}
	}
	return deviceSigPrefix + "|" + strings.Join(fields, "|"), true
}

// canonicalOrigin is scheme://host[:port], lowercased, without a default port.
func canonicalOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port == "" || scheme == "https" && port == "443" || scheme == "http" && port == "80" {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + port
}

// headerDigest is false when a signed header is sent more than once: handlers read only the
// first field, so the fields' boundaries must not be lost in what was signed.
func headerDigest(h http.Header) (string, bool) {
	sum := sha256.New()
	for _, name := range signedHeaders {
		switch v := h.Values(name); len(v) {
		case 0:
		case 1:
			io.WriteString(sum, name+":"+v[0]+"\n")
		default:
			return "", false
		}
	}
	return hex.EncodeToString(sum.Sum(nil)), true
}

// bindBody makes the handler see only the bytes the device signed. A body up to
// signedBodyBuffer is read and checked now; a longer one needs a Content-Length and fails on
// its last read unless it matches, so the handler, which publishes only after reading to the
// end, never publishes it.
func bindBody(r *http.Request, want []byte) error {
	if r.Body == nil || r.Body == http.NoBody {
		empty := sha256.Sum256(nil)
		if subtle.ConstantTimeCompare(empty[:], want) != 1 {
			return errDeviceBody
		}
		return nil
	}
	head, err := io.ReadAll(io.LimitReader(r.Body, signedBodyBuffer+1))
	if err != nil {
		return err
	}
	body := io.MultiReader(bytes.NewReader(head), r.Body)
	if len(head) <= signedBodyBuffer {
		got := sha256.Sum256(head)
		if subtle.ConstantTimeCompare(got[:], want) != 1 {
			return errDeviceBody
		}
		r.Body = readCloser{body, r.Body}
		return nil
	}
	if r.ContentLength < 0 {
		return errDeviceBody
	}
	r.Body = readCloser{&digestReader{r: body, h: sha256.New(), want: want, left: r.ContentLength}, r.Body}
	return nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

type digestReader struct {
	r      io.Reader
	h      hash.Hash
	want   []byte
	left   int64
	failed bool
}

func (d *digestReader) Read(p []byte) (int, error) {
	if d.failed {
		return 0, errDeviceBody
	}
	n, err := d.r.Read(p)
	d.h.Write(p[:n])
	d.left -= int64(n)
	if d.left <= 0 || err != nil {
		if d.left != 0 || subtle.ConstantTimeCompare(d.h.Sum(nil), d.want) != 1 {
			// The final bytes are withheld: the handler never holds the whole of a wrong body.
			d.failed = true
			return 0, errDeviceBody
		}
	}
	return n, err
}
