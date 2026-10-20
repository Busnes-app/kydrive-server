package editor

import (
	"bytes"
	"cmp"
	"context"
	"image/png"
	"io"
	"os"
	"testing"
	"time"
)

// TestConvertAgainstLiveEditor renders a real document's first page through a running Document
// Server. It runs only when KYDRIVE_LIVE_SOURCE names a document that server can fetch, with
// KYDRIVE_EDITOR_URL and KYDRIVE_EDITOR_SECRET set for it; KYDRIVE_LIVE_OUT keeps the PNG.
func TestConvertAgainstLiveEditor(t *testing.T) {
	source := os.Getenv("KYDRIVE_LIVE_SOURCE")
	if source == "" {
		t.Skip("set KYDRIVE_LIVE_SOURCE to run against a Document Server")
	}
	c, err := FromEnv("http://drive.test", "development")
	if err != nil || c == nil {
		t.Fatalf("editor %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := c.Convert(ctx, "live-"+time.Now().Format("150405.000"), os.Getenv("KYDRIVE_LIVE_TYPE"), source, 256)
	if err != nil {
		t.Fatal(err)
	}
	body, err := c.Fetch(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output is not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() > 256 || b.Dy() > 256 || b.Dx() < 16 || b.Dy() < 16 {
		t.Fatalf("thumbnail bounds %v", b)
	}
	t.Logf("%s -> %v, %d bytes", out, img.Bounds(), len(data))
	if keep := os.Getenv("KYDRIVE_LIVE_OUT"); keep != "" {
		os.WriteFile(keep, data, 0o600)
	}
}

// TestConvertTextAgainstLiveEditor converts a real document to text through a running Document
// Server. It runs only when KYDRIVE_LIVE_TEXT_SOURCE names a document that server can fetch
// (KYDRIVE_LIVE_TEXT_TYPE, default pdf), with KYDRIVE_EDITOR_URL and KYDRIVE_EDITOR_SECRET set for
// it; the text must contain KYDRIVE_LIVE_TEXT_WANT.
func TestConvertTextAgainstLiveEditor(t *testing.T) {
	source := os.Getenv("KYDRIVE_LIVE_TEXT_SOURCE")
	if source == "" {
		t.Skip("set KYDRIVE_LIVE_TEXT_SOURCE to run against a Document Server")
	}
	c, err := FromEnv("http://drive.test", "development")
	if err != nil || c == nil {
		t.Fatalf("editor %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := c.ConvertText(ctx, "live-text-"+time.Now().Format("150405.000"), cmp.Or(os.Getenv("KYDRIVE_LIVE_TEXT_TYPE"), "pdf"), source)
	if err != nil {
		t.Fatal(err)
	}
	body, err := c.Fetch(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 16<<20))
	if err != nil {
		t.Fatal(err)
	}
	if want := os.Getenv("KYDRIVE_LIVE_TEXT_WANT"); !bytes.Contains(data, []byte(want)) {
		t.Fatalf("text lacks %q:\n%s", want, data)
	}
	t.Logf("%s -> %d bytes:\n%s", out, len(data), data)
}
