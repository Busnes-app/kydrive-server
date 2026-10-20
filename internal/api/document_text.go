package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/extract"
)

// Document text for agents: Office Open XML and OpenDocument files are extracted in process
// (internal/extract), PDFs by the Document Server when Euro-Office is configured. Nothing parses a
// PDF in this process. Each version's text is cached sealed like a thumbnail, under
// <KY_DATA_DIR>/text/<blob>, outside backups and safe to delete.

var (
	errNoPDFConverter = errors.New("no PDF converter")
	errExtractFailed  = errors.New("extraction failed")
)

const (
	maxDocumentFile = 128 << 20 // a document bigger than this is not extracted
	kindPDF         = "PDF"
)

// extracting bounds in-process extractions at once, as converting does conversions.
var extracting = make(chan struct{}, 2)

func (s *Server) textRoot() string { return filepath.Join(s.config.Database.DataDir, "text") }

// documentKind names the document type whose text read_file extracts for ext, or "".
func (s *Server) documentKind(ext string) string {
	if strings.EqualFold(ext, "pdf") {
		return kindPDF
	}
	return extract.Kind(ext)
}

// documentText returns f's text and whether it was cut at extract.MaxText.
func (s *Server) documentText(ctx context.Context, f drive.File, ext string) (string, bool, error) {
	if cached, err := drive.ReadThumb(s.textRoot(), s.config.Security.EncryptionKey, f.Blob); err == nil && len(cached) > 0 {
		return string(cached[1:]), cached[0] == 't', nil
	}
	if f.Size > maxDocumentFile {
		return "", false, extract.ErrTooLarge
	}
	var res extract.Result
	var err error
	if strings.EqualFold(ext, "pdf") {
		res, err = s.pdfText(ctx, f)
	} else {
		res, err = s.extractText(ctx, f, ext)
	}
	if err != nil {
		return "", false, err
	}
	flag := byte('c')
	if res.Truncated {
		flag = 't'
	}
	if err := drive.WriteThumb(s.textRoot(), s.config.Security.EncryptionKey, f.Blob, append([]byte{flag}, res.Text...)); err != nil {
		log.Printf("document text cache %s: %v", f.ID, err)
	}
	return res.Text, res.Truncated, nil
}

func (s *Server) extractText(ctx context.Context, f drive.File, ext string) (extract.Result, error) {
	select {
	case extracting <- struct{}{}:
	case <-ctx.Done():
		return extract.Result{}, ctx.Err()
	}
	defer func() { <-extracting }()
	blob, err := s.openBlob(f)
	if err != nil {
		log.Printf("document text %s: %v", f.ID, err)
		return extract.Result{}, errExtractFailed
	}
	defer blob.Close()
	res, err := extract.Text(ctx, &readerAt{r: blob}, f.Size, ext)
	if err != nil && !errors.Is(err, extract.ErrUnsupported) && !errors.Is(err, extract.ErrTooLarge) {
		log.Printf("document text %s: %v", f.ID, err)
		return extract.Result{}, errExtractFailed
	}
	return res, err
}

// pdfText has the Document Server convert f to text, through a conversion ticket as for
// thumbnails. Its output is untrusted too: capped, and made valid UTF-8.
func (s *Server) pdfText(ctx context.Context, f drive.File) (extract.Result, error) {
	if s.editor == nil {
		return extract.Result{}, errNoPDFConverter
	}
	select {
	case converting <- struct{}{}:
	case <-ctx.Done():
		return extract.Result{}, ctx.Err()
	}
	defer func() { <-converting }()
	id := s.newTicket(f)
	defer s.dropTicket(id)
	out, err := s.editor.ConvertText(ctx, "text-"+f.Blob, "pdf", s.editor.DriveOrigin+"/api/editor/convert/"+id)
	if err != nil {
		log.Printf("pdf text conversion %s: %v", f.ID, err)
		return extract.Result{}, errExtractFailed
	}
	body, err := s.editor.Fetch(ctx, out)
	if err != nil {
		log.Printf("pdf text conversion output %s: %v", f.ID, err)
		return extract.Result{}, errExtractFailed
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, extract.MaxText+1))
	if err != nil {
		log.Printf("pdf text conversion output %s: %v", f.ID, err)
		return extract.Result{}, errExtractFailed
	}
	res := extract.Result{Truncated: len(data) > extract.MaxText}
	data = bytes.TrimPrefix(data[:min(len(data), extract.MaxText)], []byte("\xef\xbb\xbf"))
	res.Text = strings.ReplaceAll(strings.ToValidUTF8(strings.ReplaceAll(string(data), "\r\n", "\n"), ""), "\x00", "")
	return res, nil
}

// readerAt reads a sealed blob at offsets for archive/zip, which reads one part at a time.
type readerAt struct {
	mu sync.Mutex
	r  io.ReadSeeker
}

func (a *readerAt) ReadAt(p []byte, off int64) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.r.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(a.r, p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF // the ReaderAt contract at the end
	}
	return n, err
}
