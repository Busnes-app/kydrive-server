package vault

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/Busnes-app/ky-primitives/hybridsig"
)

// ReadSigningKey loads the two raw seeds from an owner-only regular file.
func ReadSigningKey(path string) (*hybridsig.PrivateKey, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	seedBytes := hybridsig.SeedBytes + ed25519.SeedSize
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != int64(seedBytes) {
		return nil, errors.New("signing key must be an owner-only regular file of two encoded seeds")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) || opened.Mode().Perm()&0077 != 0 {
		return nil, ErrInvalid
	}
	seeds, err := io.ReadAll(io.LimitReader(f, int64(seedBytes)+1))
	if err != nil {
		return nil, err
	}
	defer clear(seeds)
	if len(seeds) != seedBytes {
		return nil, ErrInvalid
	}
	return hybridsig.FromSeeds(seeds[:hybridsig.SeedBytes], seeds[hybridsig.SeedBytes:])
}

func ParsePublicKey(encoded string) (*hybridsig.PublicKey, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	return hybridsig.ParsePublicKey(raw)
}
