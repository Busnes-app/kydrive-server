package editor

import (
	"bytes"
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
