package drive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// Thumbnails are PNGs of raster images inside ThumbnailSize square. A decompression bomb is
// refused from its header: the decoder's memory is bounded before any pixel buffer exists, and
// one image decodes at a time.
const (
	ThumbnailSize    = 256
	ThumbnailMaxFile = 64 << 20
	thumbMaxSide     = 16384
	thumbBudget      = 256 << 20 // decoder bytes for one image
)

var ErrNoThumbnail = errors.New("no thumbnail for this file")

var decoding = make(chan struct{}, 1)

// Decoder footprints, in bytes a pixel, are the allocations each decode path was measured to
// make (TestDecoderCostsHold pins them) rounded up: PNG 4, 8 at 16 bits, doubled when
// interlaced; GIF 4, first frame only; WebP 12, lossy with palette-coded alpha being the
// largest at 9.3; JPEG by jpegCost.
const webpCost = 12

// jpegCost is image/jpeg's footprint for a baseline and a progressive frame. Every frame
// decodes into component planes; RGB and CMYK are converted into a new image while the planes
// are held, and a progressive frame keeps 4 bytes of coefficients a component until its last
// scan. Whether three components are RGB is decided by markers DecodeConfig may not have read,
// so three components always cost as RGB.
func jpegCost(m color.Model) (baseline, progressive int64) {
	switch m {
	case color.GrayModel:
		return 4, 16
	case color.CMYKModel:
		return 9, 25
	}
	return 8, 20
}

type raster struct {
	kind   string
	magic  func([]byte) bool
	config func(io.Reader) (image.Config, error)
	decode func(io.Reader) (image.Image, error)
}

var rasters = []raster{
	{"png", func(b []byte) bool { return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) }, png.DecodeConfig, png.Decode},
	{"jpeg", func(b []byte) bool { return bytes.HasPrefix(b, []byte("\xff\xd8\xff")) }, jpeg.DecodeConfig, jpeg.Decode},
	{"gif", func(b []byte) bool {
		return bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a"))
	}, gif.DecodeConfig, gif.Decode},
	{"webp", func(b []byte) bool { return len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" }, webp.DecodeConfig, webp.Decode},
}

// Thumbnail returns a PNG thumbnail of the raster image in r, which holds size bytes.
func Thumbnail(ctx context.Context, r io.ReadSeeker, size int64) ([]byte, error) {
	if size > ThumbnailMaxFile {
		return nil, ErrNoThumbnail
	}
	f, cfg, perPixel, orientation, err := inspect(r)
	if err != nil {
		return nil, err
	}
	w, h := cfg.Width, cfg.Height
	if w < 1 || h < 1 || w > thumbMaxSide || h > thumbMaxSide || int64(w)*int64(h)*perPixel > thumbBudget {
		return nil, ErrNoThumbnail
	}
	select {
	case decoding <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-decoding }()
	if _, err = r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, err := f.decode(r)
	if err != nil {
		return nil, ErrNoThumbnail
	}
	b := img.Bounds()
	tw, th := fit(b.Dx(), b.Dy())
	if orientation >= 5 {
		tw, th = fit(b.Dy(), b.Dx()) // turned a quarter: fit the displayed shape, then swap back
		tw, th = th, tw
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, tw, th))
	draw.BiLinear.Scale(thumb, thumb.Bounds(), img, b, draw.Src, nil)
	var out bytes.Buffer
	if err = png.Encode(&out, orient(thumb, orientation)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// inspect reads an image's format and header, never its pixels, and returns what decoding it
// would cost in bytes a pixel, and its EXIF orientation.
func inspect(r io.ReadSeeker) (f *raster, cfg image.Config, perPixel int64, orientation int, err error) {
	head := make([]byte, 29) // through a PNG's IHDR interlace byte
	n, _ := io.ReadFull(r, head)
	for i := range rasters {
		if rasters[i].magic(head[:n]) {
			f = &rasters[i]
		}
	}
	if f == nil {
		return nil, cfg, 0, 0, ErrNoThumbnail
	}
	if _, err = r.Seek(0, io.SeekStart); err != nil {
		return
	}
	if cfg, err = f.config(r); err != nil {
		return nil, cfg, 0, 0, ErrNoThumbnail
	}
	perPixel, orientation = 4, 1
	switch f.kind {
	case "png":
		switch cfg.ColorModel {
		case color.RGBA64Model, color.NRGBA64Model, color.Gray16Model: // 16-bit gray with tRNS decodes as NRGBA64
			perPixel = 8
		}
		if n == len(head) && head[28] != 0 {
			perPixel *= 2 // Adam7 decodes each pass into its own buffer too
		}
	case "webp":
		perPixel = webpCost
	case "jpeg":
		if _, err = r.Seek(0, io.SeekStart); err != nil {
			return
		}
		var sof byte
		sof, orientation = jpegFrame(r)
		// A frame that is not plainly baseline, or not found, is budgeted as progressive.
		baseline, progressive := jpegCost(cfg.ColorModel)
		if perPixel = progressive; sof == 0xc0 || sof == 0xc1 {
			perPixel = baseline
		}
	}
	return
}

// fit scales w x h down to fit ThumbnailSize square, keeping its shape; it never enlarges.
func fit(w, h int) (int, int) {
	if w <= ThumbnailSize && h <= ThumbnailSize {
		return w, h
	}
	if w >= h {
		return ThumbnailSize, max(1, h*ThumbnailSize/w)
	}
	return max(1, w*ThumbnailSize/h), ThumbnailSize
}

// jpegFrame walks a JPEG's segments up to its frame header and returns the frame marker (0 when
// none is found) and the EXIF orientation (1 when absent or unreadable). It is as lenient as
// image/jpeg, skipping stray and fill bytes and length-less markers, so a file the decoder
// accepts cannot hide its frame type; the caller treats anything but baseline as progressive.
func jpegFrame(r io.Reader) (sof byte, orientation int) {
	orientation = 1
	br := bufio.NewReader(r)
	if _, err := br.Discard(2); err != nil {
		return
	}
	for {
		c, err := br.ReadByte()
		if err != nil {
			return
		}
		if c != 0xff {
			continue
		}
		marker := byte(0xff)
		for marker == 0xff && err == nil {
			marker, err = br.ReadByte()
		}
		if err != nil {
			return
		}
		if marker == 0x00 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd8) {
			continue // stuffed byte, TEM, RSTn, SOI: no length
		}
		var b [2]byte
		if _, err = io.ReadFull(br, b[:]); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint16(b[:])) - 2
		switch {
		case length < 0 || marker == 0xd9 || marker == 0xda:
			return // malformed, or the image ends or its scan starts before any frame
		case marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc:
			return marker, orientation
		case marker == 0xe1:
			seg := make([]byte, length)
			if _, err = io.ReadFull(br, seg); err != nil {
				return
			}
			if o := exifOrientation(seg); o != 0 {
				orientation = o
			}
		default:
			if _, err = br.Discard(length); err != nil {
				return
			}
		}
	}
}

// exifOrientation reads tag 0x0112 from an APP1 segment's first IFD, or returns 0.
func exifOrientation(seg []byte) int {
	if len(seg) < 14 || string(seg[:6]) != "Exif\x00\x00" {
		return 0
	}
	tiff := seg[6:]
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	ifd := int(order.Uint32(tiff[4:]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0
	}
	for i, n := 0, int(order.Uint16(tiff[ifd:])); i < n; i++ {
		e := ifd + 2 + 12*i
		if e+12 > len(tiff) {
			return 0
		}
		if order.Uint16(tiff[e:]) == 0x0112 {
			if o := int(order.Uint16(tiff[e+8:])); o >= 1 && o <= 8 {
				return o
			}
			return 0
		}
	}
	return 0
}

// orient turns and flips img as EXIF orientation o says the camera meant it to be seen.
func orient(img *image.NRGBA, o int) *image.NRGBA {
	if o <= 1 || o > 8 {
		return img
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := range h {
		for x := range w {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			out.SetNRGBA(nx, ny, img.NRGBAAt(x, y))
		}
	}
	return out
}

// WriteThumb seals a thumbnail under key as root/<blob>: named by blob, never by digest, so the
// cache cannot confirm what a file holds.
func WriteThumb(root string, key []byte, blob string, thumb []byte) error {
	path, err := BlobPath(root, blob)
	if err != nil {
		return err
	}
	v, err := WriteBlob(root, key, bytes.NewReader(thumb), int64(len(thumb)))
	if err != nil {
		return err
	}
	if err = os.Rename(filepath.Join(root, v.Blob), path); err != nil {
		os.Remove(filepath.Join(root, v.Blob))
	}
	return err
}

// ReadThumb opens a cached thumbnail; a missing one is fs.ErrNotExist.
func ReadThumb(root string, key []byte, blob string) ([]byte, error) {
	f, err := OpenThumb(root, key, blob)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// OpenThumb streams a cache entry (thumbnail or document text), authenticating each chunk as
// it is read; a missing one is fs.ErrNotExist.
func OpenThumb(root string, key []byte, blob string) (io.ReadSeekCloser, error) {
	path, err := BlobPath(root, blob)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	plain, ok := plainSize(info.Size())
	if !ok {
		return nil, errSealed
	}
	return OpenBlob(path, key, true, plain)
}

// plainSize inverts SealedSize.
func plainSize(sealed int64) (int64, bool) {
	body := sealed - int64(blobHeader)
	if body < blobTag {
		return 0, false
	}
	plain := body - (body+blobChunk+blobTag-1)/(blobChunk+blobTag)*blobTag
	return plain, plain >= 0 && SealedSize(plain) == sealed
}
