package drive

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"filippo.io/age"
)

// KYB2, written now: "KYB2" || an age v1 file (filippo.io/age: STREAM of ChaCha20-Poly1305
// over 64 KiB chunks, random per-file key, last-chunk flag, header MAC). Its single "kydrive"
// stanza wraps the file key under AES-256-GCM with HKDF-SHA256(deployment key, stanza salt,
// binding), so a blob opens only under the name it was written for.
//
// KYB1, read until the migration ends: "KYB1" || 32-byte salt, then AES-256-GCM chunks of 64
// KiB plaintext under HKDF-SHA256(deployment key, salt, "kydrive:blob"); nonce = 11-byte
// big-endian chunk index || final flag; the header is every chunk's AAD.
//
// Both seal empty plaintext as one empty final chunk.
const (
	blobMagic   = "KYB2"
	blobMagicV1 = "KYB1"
	blobHeader  = len(blobMagic) + 148 + 16 // magic, age header, stream nonce
	headerV1    = len(blobMagicV1) + 32
	blobChunk   = 64 << 10
	blobTag     = 16
	stanzaType  = "kydrive"
)

var errSealed = errors.New("sealed blob failed authentication")

// SealedSize is the on-disk size of a KYB2 blob holding plain bytes.
func SealedSize(plain int64) int64 {
	return int64(blobHeader) + plain + chunks(plain)*blobTag
}

func sealedSizeV1(plain int64) int64 {
	return int64(headerV1) + plain + chunks(plain)*blobTag
}

// StoredSizeOK reports whether disk bytes can hold plain bytes stored as sealed says.
func StoredSizeOK(sealed bool, plain, disk int64) bool {
	if !sealed {
		return disk == plain
	}
	return disk == SealedSize(plain) || disk == sealedSizeV1(plain)
}

func chunks(plain int64) int64 { return max(1, (plain+blobChunk-1)/blobChunk) }

// blobBinding names what a sealed file may be opened as: a blob or a cache entry, by ID.
func blobBinding(id string) string  { return "kydrive:kyb2:blob:" + id }
func cacheBinding(id string) string { return "kydrive:kyb2:cache:" + id }

// fileKey is the age recipient and identity of one binding.
type fileKey struct {
	key  []byte
	bind string
}

func (k fileKey) aead(salt []byte) (cipher.AEAD, error) {
	if len(k.key) != 32 {
		return nil, errors.New("blob encryption requires a 32-byte deployment key")
	}
	wrap, err := hkdf.Key(sha256.New, k.key, salt, k.bind, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(wrap)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Wrap seals the file key under a fresh salt, so the zero nonce never repeats under a key.
func (k fileKey) Wrap(key []byte) ([]*age.Stanza, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	a, err := k.aead(salt)
	if err != nil {
		return nil, err
	}
	body := a.Seal(nil, make([]byte, a.NonceSize()), key, nil)
	return []*age.Stanza{{Type: stanzaType, Args: []string{base64.RawStdEncoding.EncodeToString(salt)}, Body: body}}, nil
}

func (k fileKey) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	if len(stanzas) != 1 || stanzas[0].Type != stanzaType || len(stanzas[0].Args) != 1 {
		return nil, age.ErrIncorrectIdentity
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(stanzas[0].Args[0])
	if err != nil || len(salt) != 16 {
		return nil, errSealed
	}
	a, err := k.aead(salt)
	if err != nil {
		return nil, err
	}
	key, err := a.Open(nil, make([]byte, a.NonceSize()), stanzas[0].Body, nil)
	if err != nil {
		return nil, errSealed
	}
	return key, nil
}

// seal writes r sealed under key and bind to w and returns the plaintext length. Only io.EOF
// ends the data: an io.ErrUnexpectedEOF from r (net/http's truncated body) is a failed read.
func seal(w io.Writer, key []byte, bind string, r io.Reader) (int64, error) {
	if _, err := io.WriteString(w, blobMagic); err != nil {
		return 0, err
	}
	enc, err := age.Encrypt(w, fileKey{key, bind})
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(enc, r)
	if err != nil {
		return n, err
	}
	return n, enc.Close()
}

// sealedBlob reads KYB1.
type sealedBlob struct {
	f      *os.File
	aead   cipher.AEAD
	header []byte
	size   int64
	off    int64
	at     int64 // index of the chunk in plain, -1 for none
	plain  []byte
	ct     []byte
}

// OpenBlob opens a stored blob of size plaintext bytes for reading. A sealed blob must have
// exactly the size its format gives; its last chunk is authenticated before OpenBlob returns
// and every other chunk before any of its bytes are read. A KYB2 blob opens only under the
// file name (blob ID) it was written for.
func OpenBlob(path string, key []byte, sealed bool, size int64) (io.ReadSeekCloser, error) {
	if size < 0 {
		return nil, ErrInvalid
	}
	return openFile(path, key, sealed, size, blobBinding(filepath.Base(path)))
}

// openFile opens path as bound to bind. A sealed file of size -1 takes the size it proves.
func openFile(path string, key []byte, sealed bool, size int64, bind string) (io.ReadSeekCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	b, err := openSealed(f, key, sealed, size, bind)
	if err != nil {
		f.Close()
		return nil, err
	}
	return b, nil
}

type ageBlob struct {
	*io.SectionReader
	io.Closer
}

func openSealed(f *os.File, key []byte, sealed bool, size int64, bind string) (io.ReadSeekCloser, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	disk := info.Size()
	if !sealed {
		if disk != size {
			return nil, fmt.Errorf("blob size %d differs from %d", disk, size)
		}
		return f, nil
	}
	magic := make([]byte, len(blobMagic))
	if _, err = io.ReadFull(f, magic); err != nil {
		return nil, errSealed
	}
	switch string(magic) {
	case blobMagic:
		if size >= 0 && disk != SealedSize(size) || disk < SealedSize(0) {
			return nil, errSealed
		}
		body := disk - int64(len(blobMagic))
		ra, n, err := age.DecryptReaderAt(io.NewSectionReader(f, int64(len(blobMagic)), body), body, fileKey{key, bind})
		if err != nil || disk != SealedSize(n) {
			return nil, errSealed
		}
		return ageBlob{io.NewSectionReader(ra, 0, n), f}, nil
	case blobMagicV1:
		return openV1(f, key, size, disk)
	}
	return nil, errSealed
}

func openV1(f *os.File, key []byte, size, disk int64) (io.ReadSeekCloser, error) {
	if size < 0 {
		var ok bool
		if size, ok = plainSizeV1(disk); !ok {
			return nil, errSealed
		}
	}
	if disk != sealedSizeV1(size) {
		return nil, errSealed
	}
	b := &sealedBlob{f: f, header: make([]byte, headerV1), size: size, at: -1, ct: make([]byte, blobChunk+blobTag)}
	if _, err := f.ReadAt(b.header, 0); err != nil {
		return nil, err
	}
	var err error
	if b.aead, err = aeadV1(key, b.header[len(blobMagicV1):]); err != nil {
		return nil, err
	}
	if err = b.load(chunks(size) - 1); err != nil {
		return nil, err
	}
	return b, nil
}

// plainSizeV1 inverts sealedSizeV1.
func plainSizeV1(disk int64) (int64, bool) {
	body := disk - int64(headerV1)
	if body < blobTag {
		return 0, false
	}
	plain := body - (body+blobChunk+blobTag-1)/(blobChunk+blobTag)*blobTag
	return plain, plain >= 0 && sealedSizeV1(plain) == disk
}

func aeadV1(key, salt []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("blob encryption requires a 32-byte deployment key")
	}
	k, err := hkdf.Key(sha256.New, key, salt, "kydrive:blob", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonceV1(i int64, final bool) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[3:11], uint64(i))
	if final {
		n[11] = 1
	}
	return n
}

func (b *sealedBlob) load(i int64) error {
	b.at, b.plain = -1, nil
	n := min(blobChunk, b.size-i*blobChunk)
	ct := b.ct[:n+blobTag]
	if _, err := b.f.ReadAt(ct, int64(headerV1)+i*(blobChunk+blobTag)); err != nil {
		return err
	}
	plain, err := b.aead.Open(ct[:0], nonceV1(i, i == chunks(b.size)-1), ct, b.header)
	if err != nil {
		return errSealed
	}
	b.at, b.plain = i, plain
	return nil
}

func (b *sealedBlob) Read(p []byte) (int, error) {
	if b.off >= b.size {
		return 0, io.EOF
	}
	if i := b.off / blobChunk; i != b.at {
		if err := b.load(i); err != nil {
			return 0, err
		}
	}
	n := copy(p, b.plain[b.off-b.at*blobChunk:])
	b.off += int64(n)
	return n, nil
}

func (b *sealedBlob) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += b.off
	case io.SeekEnd:
		offset += b.size
	case io.SeekStart:
	default:
		return 0, errors.New("invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("negative position")
	}
	b.off = offset
	return offset, nil
}

func (b *sealedBlob) Close() error { return b.f.Close() }
