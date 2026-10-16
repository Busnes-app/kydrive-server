package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// convertible are the formats the Document Server renders a first page of.
var convertible = map[string]bool{"docx": true, "odt": true, "rtf": true, "xlsx": true, "ods": true, "csv": true, "pptx": true, "odp": true, "pdf": true}

const (
	ticketLife      = 2 * time.Minute
	converterOutput = 8 << 20 // bytes of converter PNG read
)

// converting bounds conversions running at once, so a folder of documents cannot queue the
// Document Server's workers behind thumbnails.
var converting = make(chan struct{}, 2)

// A conversion ticket lets the Document Server fetch one file for one conversion. It is a random
// name in memory, never a session or credential: the fetch must also carry the server's token
// signed for exactly that URL, and the ticket ends when the conversion returns or after ticketLife.
type convertTicket struct {
	file    drive.File
	expires time.Time
}

func (s *Server) newTicket(f drive.File) string {
	raw := make([]byte, 16)
	rand.Read(raw)
	id := hex.EncodeToString(raw)
	now := time.Now()
	s.ticketsMu.Lock()
	defer s.ticketsMu.Unlock()
	for k, t := range s.tickets {
		if now.After(t.expires) {
			delete(s.tickets, k)
		}
	}
	s.tickets[id] = convertTicket{f, now.Add(ticketLife)}
	return id
}

func (s *Server) ticket(id string) (drive.File, bool) {
	s.ticketsMu.Lock()
	defer s.ticketsMu.Unlock()
	t, ok := s.tickets[id]
	return t.file, ok && time.Now().Before(t.expires)
}

func (s *Server) dropTicket(id string) {
	s.ticketsMu.Lock()
	delete(s.tickets, id)
	s.ticketsMu.Unlock()
}

// editorConvertSource serves a ticket's file to the Document Server.
func (s *Server) editorConvertSource(w http.ResponseWriter, r *http.Request) {
	if s.editor == nil {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("ticket")
	if s.editor.Download(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), s.editor.DriveOrigin+"/api/editor/convert/"+id) != nil {
		s.writeError(w, 401, "Invalid editor signature")
		return
	}
	f, ok := s.ticket(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, f)
}

// officeThumbnail has the Document Server render f's first page, then decodes its PNG with the
// same bounds as an upload: the converter's output is not trusted either.
func (s *Server) officeThumbnail(ctx context.Context, f drive.File, ext string) ([]byte, error) {
	if f.Size > drive.ThumbnailMaxFile {
		return nil, drive.ErrNoThumbnail
	}
	select {
	case converting <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-converting }()
	id := s.newTicket(f)
	defer s.dropTicket(id)
	out, err := s.editor.Convert(ctx, "thumb-"+f.Blob, ext, s.editor.DriveOrigin+"/api/editor/convert/"+id, drive.ThumbnailSize)
	if err != nil {
		log.Printf("thumbnail conversion %s: %v", f.ID, err)
		return nil, drive.ErrNoThumbnail
	}
	body, err := s.editor.Fetch(ctx, out)
	if err != nil {
		log.Printf("thumbnail conversion output %s: %v", f.ID, err)
		return nil, drive.ErrNoThumbnail
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, converterOutput+1))
	if err != nil || len(data) > converterOutput {
		return nil, drive.ErrNoThumbnail
	}
	return drive.Thumbnail(ctx, bytes.NewReader(data), int64(len(data)))
}
