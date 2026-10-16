package drive_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/google/uuid"
)

func encoded(t *testing.T, w, h int, enc func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, h/2, color.RGBA{200, 30, 30, 255})
	}
	var b bytes.Buffer
	if err := enc(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func thumbOf(t *testing.T, body []byte) (image.Image, error) {
	t.Helper()
	out, err := drive.Thumbnail(context.Background(), bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("thumbnail is not a PNG: %v", err)
	}
	return img, nil
}

func TestThumbnailScales(t *testing.T) {
	webp, err := os.ReadFile("testdata/lossy.webp")
	if err != nil {
		t.Fatal(err)
	}
	small, err := os.ReadFile("testdata/gradient.webp")
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		body []byte
		w, h int
	}{
		"wide png":      {encoded(t, 600, 300, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) }), 256, 128},
		"tall jpeg":     {encoded(t, 300, 600, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) }), 128, 256},
		"small gif":     {encoded(t, 100, 50, func(b *bytes.Buffer, i image.Image) error { return gif.Encode(b, i, nil) }), 100, 50},
		"lossy webp":    {webp, 256, 128},
		"lossless webp": {small, 40, 20},
	} {
		img, err := thumbOf(t, c.body)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b := img.Bounds(); b.Dx() != c.w || b.Dy() != c.h {
			t.Fatalf("%s: %v, want %dx%d", name, b, c.w, c.h)
		}
	}
	// A phone's portrait photo: stored landscape-wide with EXIF orientation 6 (turn 90° clockwise).
	tall := encoded(t, 600, 300, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) })
	app1 := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x06\x00\x00\x00\x00\x00\x00")
	rotated := append([]byte{0xff, 0xd8, 0xff, 0xe1, 0, byte(len(app1) + 2)}, app1...)
	if img, err := thumbOf(t, append(rotated, tall[2:]...)); err != nil || img.Bounds().Dx() != 128 || img.Bounds().Dy() != 256 {
		t.Fatalf("EXIF orientation not applied: %v %v", img, err)
	}
	for name, body := range map[string][]byte{
		"html":            []byte("<!DOCTYPE html><img src=x onerror=alert(1)>"),
		"svg":             []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`),
		"pdf":             []byte("%PDF-1.4\n%%EOF"),
		"bmp":             append([]byte("BM"), make([]byte, 60)...),
		"png that is not": []byte("\x89PNG\r\n\x1a\nnot really"),
	} {
		if _, err := thumbOf(t, body); !errors.Is(err, drive.ErrNoThumbnail) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func pngChunk(out []byte, kind string, data []byte) []byte {
	chunk := append([]byte(kind), data...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
	out = append(out, chunk...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
}

// pngBomb is a PNG claiming w x h at the given bit depth and color type, whose pixel data is a
// megabyte of zeros compressed to about a kilobyte: a decoder that trusts it allocates it all.
func pngBomb(w, h uint32, depth, colorType byte, extra ...[]byte) []byte {
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(make([]byte, 1<<20))
	zw.Close()
	out := pngChunk([]byte("\x89PNG\r\n\x1a\n"), "IHDR", append(ihdr, depth, colorType, 0, 0, 0))
	for _, c := range extra { // whole chunks: type then data
		out = pngChunk(out, string(c[:4]), c[4:])
	}
	return pngChunk(pngChunk(out, "IDAT", z.Bytes()), "IEND", nil)
}

// adam7 marks a PNG from pngBomb interlaced: the IHDR interlace byte, then its CRC.
func adam7(p []byte) []byte {
	p[28] = 1
	binary.BigEndian.PutUint32(p[29:], crc32.ChecksumIEEE(p[12:29]))
	return p
}

// jpegHeader is a JPEG start and a frame header claiming w x h with three components.
func jpegHeader(w, h uint16, progressive bool) []byte {
	sof := byte(0xc0)
	if progressive {
		sof = 0xc2
	}
	out := []byte{0xff, 0xd8, 0xff, sof, 0, 17, 8}
	out = binary.BigEndian.AppendUint16(out, h)
	out = binary.BigEndian.AppendUint16(out, w)
	return append(out, 3, 1, 0x11, 0, 2, 0x11, 1, 3, 0x11, 1)
}

// A decompression bomb is refused from its header, before any pixel buffer exists.
func TestThumbnailRefusesBombs(t *testing.T) {
	for name, body := range map[string][]byte{
		"100000 square png":      pngBomb(100000, 100000, 8, 6),
		"side over 16384":        pngBomb(17000, 10, 8, 6),
		"16-bit png over budget": pngBomb(8000, 6000, 16, 6),
		// Grayscale reports 2 bytes a pixel, but transparency makes it decode as 8-byte NRGBA64.
		"16-bit gray with transparency": pngBomb(6000, 6000, 16, 0, []byte("tRNS\x00\x00")),
		// Interlacing decodes every pass into its own buffer as well as the image.
		"interlaced png":   adam7(pngBomb(7000, 7000, 8, 6)),
		"progressive jpeg": jpegHeader(5000, 5000, true),
		"zero width":       pngBomb(0, 10, 8, 6),
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := drive.Thumbnail(context.Background(), bytes.NewReader(body), int64(len(body)))
		runtime.ReadMemStats(&after)
		if !errors.Is(err, drive.ErrNoThumbnail) {
			t.Fatalf("%s: %v", name, err)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 4<<20 {
			t.Fatalf("%s allocated %d bytes before refusing", name, grew)
		}
	}
	// A real 5000 × 5000 progressive JPEG, also hidden from a frame walker that gives up early:
	// past 64 empty comments, and behind a fill byte the standard decoder skips. Each must be
	// budgeted as progressive and refused before its coefficients are allocated.
	prog, err := os.ReadFile("testdata/progressive.jpg")
	if err != nil {
		t.Fatal(err)
	}
	padded := []byte{0xff, 0xd8}
	for range 65 {
		padded = append(padded, 0xff, 0xfe, 0, 2)
	}
	// CMYK decodes to YCbCr plus a black plane, then converts to a new image: 8200 × 4096
	// baseline and 4000 × 3000 progressive are each just over budget only at their real size.
	cmyk, err := os.ReadFile("testdata/cmyk.jpg")
	if err != nil {
		t.Fatal(err)
	}
	cmykProg, err := os.ReadFile("testdata/cmyk-progressive.jpg")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"progressive jpeg":             prog,
		"progressive past 65 comments": append(padded, prog[2:]...),
		"progressive behind fill byte": append([]byte{0xff, 0xd8, 0xff}, prog[2:]...),
		"baseline cmyk":                cmyk,
		"progressive cmyk":             cmykProg,
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := drive.Thumbnail(context.Background(), bytes.NewReader(body), int64(len(body)))
		runtime.ReadMemStats(&after)
		if !errors.Is(err, drive.ErrNoThumbnail) {
			t.Fatalf("%s: %v", name, err)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 4<<20 {
			t.Fatalf("%s allocated %d bytes before refusing", name, grew)
		}
	}
	// A progressive JPEG is budgeted at 16 bytes a pixel, a baseline one at 4.
	// The frame walk is as lenient as the decoder: fill bytes, stray bytes and length-less
	// markers do not hide the frame, and a frame never found reads as 0.
	base := jpegHeader(10, 10, false)
	for name, c := range map[string]struct {
		body []byte
		sof  byte
	}{
		"progressive":       {jpegHeader(10, 10, true), 0xc2},
		"baseline":          {base, 0xc0},
		"fill bytes":        {append([]byte{0xff, 0xd8, 0xff, 0xff}, base[2:]...), 0xc0},
		"stray byte, RST0":  {append([]byte{0xff, 0xd8, 0x00, 0xff, 0xd0}, base[2:]...), 0xc0},
		"scan before frame": {[]byte{0xff, 0xd8, 0xff, 0xda, 0, 2}, 0},
		"truncated":         {[]byte{0xff, 0xd8, 0xff}, 0},
	} {
		if sof, _ := drive.JPEGFrame(bytes.NewReader(c.body)); sof != c.sof {
			t.Fatalf("%s: frame %#x, want %#x", name, sof, c.sof)
		}
	}
	// The same frame as a baseline JPEG is within budget: only its missing data stops it.
	if _, err := drive.Thumbnail(context.Background(), bytes.NewReader(jpegHeader(5000, 5000, false)), 30); !errors.Is(err, drive.ErrNoThumbnail) {
		t.Fatalf("truncated baseline: %v", err)
	}
	// Too large a file is refused without reading it.
	if _, err := drive.Thumbnail(context.Background(), bytes.NewReader(nil), drive.ThumbnailMaxFile+1); !errors.Is(err, drive.ErrNoThumbnail) {
		t.Fatalf("oversized file: %v", err)
	}
}

// Thumbnails are sealed like blobs and named by blob ID, never by digest.
func TestThumbnailCacheIsSealed(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	key := bytes.Repeat([]byte{7}, 32)
	root := filepath.Join(t.TempDir(), "thumbs")
	thumb, err := drive.Thumbnail(context.Background(), bytes.NewReader(encoded(t, 64, 64, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) })), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	live := blob(t, blobs, "x")
	if _, err = d.Publish(ctx, "worker", drive.File{ID: uuid.NewString(), Workspace: w.ID, Name: "live.png"}, live, 0); err != nil {
		t.Fatal(err)
	}
	gone := uuid.NewString()
	for _, id := range []string{live.Blob, gone} {
		if err = drive.WriteThumb(root, key, id, thumb); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, live.Blob))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("\x89PNG")) || bytes.Contains(raw, []byte("IDAT")) {
		t.Fatal("thumbnail stored in plaintext")
	}
	if got, err := drive.ReadThumb(root, key, live.Blob); err != nil || !bytes.Equal(got, thumb) {
		t.Fatalf("read back %d bytes, %v", len(got), err)
	}
	if _, err = drive.ReadThumb(root, bytes.Repeat([]byte{8}, 32), live.Blob); err == nil {
		t.Fatal("thumbnail opened with the wrong key")
	}
	if _, err = drive.ReadThumb(root, key, uuid.NewString()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing thumbnail: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, id := range []string{live.Blob, gone} {
		os.Chtimes(filepath.Join(root, id), old, old)
	}
	if n, err := d.SweepOrphans(ctx, root, time.Hour); err != nil || n != 1 {
		t.Fatalf("sweep removed %d, %v", n, err)
	}
	if _, err = os.Stat(filepath.Join(root, gone)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("orphan thumbnail kept")
	}
}
