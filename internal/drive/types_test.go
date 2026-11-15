package drive_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestFileType(t *testing.T) {
	for name, want := range map[string]string{
		"Report.docx": "documents", "notes.TXT": "documents", "a.md": "documents",
		"Budget.XLSX": "spreadsheets", "data.csv": "spreadsheets",
		"Deck.pptx": "presentations", "talk.odp": "presentations",
		"Scan.PdF": "pdfs", ".pdf": "pdfs",
		"photo.jpeg": "images", "logo.svg": "images",
		"clip.MOV": "videos", "song.flac": "audio",
		"backup.tar.gz": "archives", "x.zip": "archives",
		"pdf": "", "README": "", "report.pdf.exe": "", "trailing.": "", "board.excalidraw": "",
	} {
		if got := drive.FileType(name); got != want {
			t.Errorf("FileType(%q) = %q, want %q", name, got, want)
		}
	}
	for _, typ := range []string{"", "folders", "documents", "spreadsheets", "presentations", "pdfs", "images", "videos", "audio", "archives"} {
		if !drive.ValidType(typ) {
			t.Errorf("%q refused", typ)
		}
	}
	for _, typ := range []string{"pdf", "PDFs", "Folders", "files", " pdfs", "pdfs,images"} {
		if drive.ValidType(typ) {
			t.Errorf("%q accepted", typ)
		}
	}
}

func TestSearchType(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "private", DisplayName: "Private", ExternalID: "external-private", Members: []string{"admin"}}, true); err != nil {
		t.Fatal(err)
	}
	secret, err := d.CreateWorkspace(ctx, "admin", "Secret", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Grant(ctx, "admin", secret.ID, "private", "editor"); err != nil {
		t.Fatal(err)
	}
	reports, err := d.AddFolder(ctx, "worker", w.ID, "", "Reports")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.AddFolder(ctx, "admin", secret.ID, "", "Secret reports"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []drive.File{
		{Workspace: w.ID, Parent: reports.ID, Name: "Q1 report.PDF"}, {Workspace: w.ID, Name: "Q2 report.docx"},
		{Workspace: w.ID, Name: "photo.jpg"}, {Workspace: w.ID, Name: "report.pdf.zip"},
	} {
		if _, err = d.Publish(ctx, "worker", f, blob(t, root, "x"), 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = d.Publish(ctx, "admin", drive.File{Workspace: secret.ID, Name: "Secret report.pdf"}, blob(t, root, "x"), 0); err != nil {
		t.Fatal(err)
	}
	trashed, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "old report.pdf"}, blob(t, root, "x"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetTrash(ctx, "worker", trashed.ID, trashed.Revision, true); err != nil {
		t.Fatal(err)
	}
	search := func(user, q, typ string) string {
		t.Helper()
		hits, _, err := d.SearchContent(ctx, user, q, typ, 200, nil, false)
		if err != nil {
			t.Fatalf("%s %q %q: %v", user, q, typ, err)
		}
		return fmt.Sprint(hitNames(hits))
	}
	for _, c := range []struct {
		q, typ string
		want   []string
	}{
		{"report", "", []string{"Reports/Q1 report.PDF", "/Q2 report.docx", "/report.pdf.zip", "/Reports"}},
		{"report", "pdfs", []string{"Reports/Q1 report.PDF"}},
		{"", "pdfs", []string{"Reports/Q1 report.PDF"}},
		{"", "folders", []string{"/Reports"}},
		{"", "documents", []string{"/Q2 report.docx"}},
		{"", "images", []string{"/photo.jpg"}},
		{"", "archives", []string{"/report.pdf.zip"}},
		{"q2", "pdfs", nil},
		{"", "videos", nil},
	} {
		want := []string{}
		for _, n := range c.want {
			want = append(want, w.ID+":"+n)
		}
		if got := search("worker", c.q, c.typ); got != fmt.Sprint(want) {
			t.Errorf("%q %q: %s, want %s", c.q, c.typ, got, want)
		}
	}
	// The filter only narrows: the other workspace's PDF and folder never show to worker, and
	// the administrator sees only their own.
	if got := search("stranger", "", "pdfs"); got != "[]" {
		t.Fatalf("stranger sees %s", got)
	}
	if got, want := search("admin", "", "pdfs"), fmt.Sprint([]string{secret.ID + ":/Secret report.pdf"}); got != want {
		t.Fatalf("admin sees %s, want %s", got, want)
	}
	if got, want := search("admin", "", "folders"), fmt.Sprint([]string{secret.ID + ":/Secret reports"}); got != want {
		t.Fatalf("admin sees %s, want %s", got, want)
	}
	hits, _, err := d.SearchFolderContent(ctx, "worker", w.ID, reports.ID, "", "pdfs", 200, nil)
	if err != nil || fmt.Sprint(hitNames(hits)) != fmt.Sprint([]string{w.ID + ":/Q1 report.PDF"}) {
		t.Fatalf("folder search: %v %v", hitNames(hits), err)
	}
	if _, _, err = d.SearchFolderContent(ctx, "worker", secret.ID, "", "", "pdfs", 200, nil); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("folder search in an unreadable workspace: %v", err)
	}
	// Limit applies to a type-only search.
	if hits, more, err := d.SearchContent(ctx, "worker", "", "documents", 0, nil, false); err != nil || len(hits) != 0 || !more {
		t.Fatalf("limit: %v %v %v", hits, more, err)
	}
	for _, c := range []struct{ q, typ string }{{"", ""}, {"r", "pdfs"}, {"report", "pdf"}, {"", "Folders"}} {
		if _, _, err := d.SearchContent(ctx, "worker", c.q, c.typ, 200, nil, false); !errors.Is(err, drive.ErrInvalid) {
			t.Errorf("%q %q: %v", c.q, c.typ, err)
		}
	}
}
