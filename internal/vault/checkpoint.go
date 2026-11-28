package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/hybridsig"
)

const (
	receiptDomain    = "kydrive-vault-receipt-v1"
	checkpointDomain = "kydrive-vault-checkpoint-v1"
	MaxCheckpointAge = 5 * time.Minute
)

var (
	ErrDenied   = errors.New("vault access denied")
	ErrInvalid  = errors.New("invalid vault data")
	ErrConflict = errors.New("vault state changed")
	ErrMissing  = errors.New("vault data not found")
)

// Link commits to one signed vault record without revealing its author, keys or membership.
type Link struct {
	Sequence int64  `json:"sequence"`
	Previous string `json:"previous"`
	Record   string `json:"record"`
}

type Receipt struct {
	Log       string `json:"log"`
	Link      Link   `json:"link"`
	Signature []byte `json:"signature"`
}

type Head struct {
	Log      string `json:"log"`
	Sequence int64  `json:"sequence"`
	Hash     string `json:"hash"`
}

type Checkpoint struct {
	Head      Head   `json:"head"`
	IssuedMS  int64  `json:"issued_ms"`
	Signature []byte `json:"signature"`
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validDigest(s string) bool {
	if len(s) != sha256.Size*2 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func (l Link) valid() bool {
	return validDigest(l.Record) && (l.Sequence == 1 && l.Previous == "" ||
		l.Sequence > 1 && validDigest(l.Previous))
}

func (l Link) Hash() string {
	data, _ := json.Marshal(l)
	return Digest(data)
}

func (r Receipt) message() []byte {
	data, _ := json.Marshal(struct {
		Log  string `json:"log"`
		Link Link   `json:"link"`
	}{r.Log, r.Link})
	return data
}

// SignReceipt is called for a committed vault link with a dedicated publisher key.
func SignReceipt(key *hybridsig.PrivateKey, log string, link Link) (Receipt, error) {
	r := Receipt{Log: log, Link: link}
	if !r.valid() {
		return Receipt{}, ErrInvalid
	}
	sig, err := key.Sign(receiptDomain, r.message())
	r.Signature = sig
	return r, err
}

func (r Receipt) valid() bool {
	return validDigest(r.Log) && r.Link.valid() && (r.Link.Sequence != 1 || r.Link.Hash() == r.Log)
}

func (r Receipt) verify(key *hybridsig.PublicKey) error {
	if !r.valid() || key.Verify(receiptDomain, r.message(), r.Signature) != nil {
		return ErrInvalid
	}
	return nil
}

func (c Checkpoint) message() []byte {
	data, _ := json.Marshal(struct {
		Head     Head  `json:"head"`
		IssuedMS int64 `json:"issued_ms"`
	}{c.Head, c.IssuedMS})
	return data
}

// VerifyCheckpoint binds the independent witness to the exact expected head and age.
func VerifyCheckpoint(key *hybridsig.PublicKey, c Checkpoint, want Head, now time.Time) error {
	age := now.Sub(time.UnixMilli(c.IssuedMS))
	if key == nil || c.Head != want || !validDigest(want.Log) || !validDigest(want.Hash) ||
		want.Sequence < 1 || c.IssuedMS <= 0 || age < 0 || age >= MaxCheckpointAge ||
		key.Verify(checkpointDomain, c.message(), c.Signature) != nil {
		return ErrInvalid
	}
	return nil
}
