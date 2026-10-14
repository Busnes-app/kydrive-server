package drive

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Sealed blob: "KYB1" || 32-byte salt, then AES-256-GCM chunks of 64 KiB plaintext under
// HKDF-SHA256(deployment key, salt, "kydrive:blob"). Nonce = 11-byte big-endian chunk index ||
// final flag; the header is every chunk's AAD. Empty plaintext is one empty final chunk.
const (
	blobMagic  = "KYB1"
	blobHeader = len(blobMagic) + 32
	blobChunk  = 64 << 10
	blobTag    = 16
)

var errSealed = errors.New("sealed blob failed authentication")

// SealedSize is the on-disk size of a sealed blob holding plain bytes.
func SealedSize(plain int64) int64 {
	return int64(blobHeader) + plain + chunks(plain)*blobTag
}

func chunks(plain int64) int64 { return max(1, (plain+blobChunk-1)/blobChunk) }

func blobAEAD(key, salt []byte) (cipher.AEAD, error) {
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

func nonce(i int64, final bool) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[3:11], uint64(i))
	if final {
		n[11] = 1
	}
	return n
}

// fill reads until b is full or the input ends; only real read errors are returned.
func fill(r io.Reader, b []byte) (int, error) {
	n, err := io.ReadFull(r, b)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = nil
	}
	return n, err
}

// seal writes r sealed under key to w and returns the plaintext length.
func seal(w io.Writer, key []byte, r io.Reader) (int64, error) {
	header := make([]byte, blobHeader)
	copy(header, blobMagic)
	rand.Read(header[len(blobMagic):])
	aead, err := blobAEAD(key, header[len(blobMagic):])
	if err != nil {
		return 0, err
	}
	if _, err = w.Write(header); err != nil {
		return 0, err
	}
	cur, next := make([]byte, blobChunk), make([]byte, blobChunk)
	out := make([]byte, 0, blobChunk+blobTag)
	m, err := fill(r, cur)
	var total int64
	for i := int64(0); ; i++ {
		if err != nil {
			return total, err
		}
		// A full chunk is final only when nothing follows it.
		final, k := m < blobChunk, 0
		if !final {
			if k, err = fill(r, next); err != nil {
				return total, err
			}
			final = k == 0
		}
		if _, err = w.Write(aead.Seal(out[:0], nonce(i, final), cur[:m], header)); err != nil {
			return total, err
		}
		total += int64(m)
		if final {
			return total, nil
		}
		cur, next, m = next, cur, k
	}
}

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
// exactly SealedSize(size) bytes; its last chunk is authenticated before OpenBlob returns and
// every other chunk before any of its bytes are read.
func OpenBlob(path string, key []byte, sealed bool, size int64) (io.ReadSeekCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	b, err := openSealed(f, key, sealed, size)
	if err != nil {
		f.Close()
		return nil, err
	}
	return b, nil
}

func openSealed(f *os.File, key []byte, sealed bool, size int64) (io.ReadSeekCloser, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !sealed {
		if info.Size() != size {
			return nil, fmt.Errorf("blob size %d differs from %d", info.Size(), size)
		}
		return f, nil
	}
	if size < 0 || info.Size() != SealedSize(size) {
		return nil, errSealed
	}
	b := &sealedBlob{f: f, header: make([]byte, blobHeader), size: size, at: -1, ct: make([]byte, blobChunk+blobTag)}
	if _, err = io.ReadFull(f, b.header); err != nil {
		return nil, err
	}
	if string(b.header[:len(blobMagic)]) != blobMagic {
		return nil, errSealed
	}
	if b.aead, err = blobAEAD(key, b.header[len(blobMagic):]); err != nil {
		return nil, err
	}
	if err = b.load(chunks(size) - 1); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *sealedBlob) load(i int64) error {
	b.at, b.plain = -1, nil
	n := min(blobChunk, b.size-i*blobChunk)
	ct := b.ct[:n+blobTag]
	if _, err := b.f.ReadAt(ct, int64(blobHeader)+i*(blobChunk+blobTag)); err != nil {
		return err
	}
	plain, err := b.aead.Open(ct[:0], nonce(i, i == chunks(b.size)-1), ct, b.header)
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
