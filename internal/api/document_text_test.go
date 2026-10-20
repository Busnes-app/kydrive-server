package api_test

import (
	"context"
	"encoding/base64"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "extract", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// read_file returns the text of Word, Excel, PowerPoint and OpenDocument files made by
// LibreOffice, labelled untrusted and extracted, paged over the text, with the text cached sealed
// per version.
func TestMCPReadsOfficeDocuments(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := w.token(t, drive.ScopeMCPRead, "", "")
	cs := agent(t, w.srv, tok)
	want := map[string]struct{ kind, snippet string }{
		"report.docx": {"Word document", "Region\tSales\nNorth\t120\n"},
		"report.odt":  {"OpenDocument text", "Café, naïve, 中文 and a tab:\there."},
		"budget.xlsx": {"Excel workbook", "## Costs\nItem\tAmount\t\t\tNote\n"},
		"budget.ods":  {"OpenDocument spreadsheet", "## Notes\nÜnïcode ✓\n"},
		"deck.pptx":   {"PowerPoint presentation", "## Slide 2\nThank you\n"},
		"deck.odp":    {"OpenDocument presentation", "## Slide 1\nRoadmap 2027\n"},
	}
	files := map[string]drive.File{}
	for name := range want {
		files[name] = w.file(t, w.team, "", name, fixtureBytes(t, name))
	}
	for name, c := range want {
		var r struct {
			readResult
			Extracted string `json:"extracted"`
		}
		if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/" + name}, &r); msg != "" {
			t.Fatalf("%s: %s", name, msg)
		}
		if r.Extracted != c.kind || !strings.Contains(r.Text, c.snippet) || r.Notice != api.MCPReadNoticeForTest || r.Size != int64(len(r.Text)) {
			t.Errorf("%s: %+v", name, r)
		}
		if strings.Contains(r.Text, "Footnote text") || strings.Contains(r.Text, "A comment") || strings.Contains(r.Text, "Speaker notes") {
			t.Errorf("%s: notes or comments extracted: %q", name, r.Text)
		}
	}

	// Pages over the extracted text reassemble it.
	var whole, all strings.Builder
	var r readResult
	if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/report.docx"}, &r); msg != "" {
		t.Fatal(msg)
	}
	all.WriteString(r.Text)
	for offset := int64(0); ; {
		r = readResult{}
		if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/report.docx", "offset": offset, "length": 7}, &r); msg != "" {
			t.Fatal(msg)
		}
		whole.WriteString(r.Text)
		if r.NextOffset == 0 {
			break
		}
		offset = r.NextOffset
	}
	if whole.String() != all.String() {
		t.Fatalf("paged %q\nwhole %q", whole.String(), all.String())
	}
	// The label comes first, before an instruction planted in the document.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "/Team/report.odt"}})
	if err != nil {
		t.Fatal(err)
	}
	if text := textOf(res); strings.Index(text, "Untrusted file content") > strings.Index(text, "IGNORE PREVIOUS") {
		t.Errorf("label after the content: %s", text)
	}

	// The cache is sealed: the extracted text is not on disk in plaintext.
	report := files["report.docx"]
	cached, err := os.ReadFile(filepath.Join(api.TextRootForTest(w.srv), report.Blob))
	if err != nil || strings.Contains(string(cached), "Quarterly") || strings.Contains(string(cached), "North") {
		t.Fatalf("cache %d bytes, err %v, plaintext %v", len(cached), err, strings.Contains(string(cached), "Quarterly"))
	}
	// A damaged cache entry is extracted again, never served.
	if err := os.WriteFile(filepath.Join(api.TextRootForTest(w.srv), report.Blob), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = readResult{}
	if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/report.docx"}, &r); msg != "" || r.Text != all.String() {
		t.Fatalf("after a damaged cache: %q %s", r.Text, msg)
	}
	// Each version has its own text.
	_, writeTok := w.token(t, drive.ScopeMCPWrite, "", "")
	writer := agent(t, w.srv, writeTok)
	if msg := callTool(t, writer, "write_file", map[string]any{"path": "/Team/deck.odp", "revision": files["deck.odp"].Revision,
		"content_base64": b64(fixtureBytes(t, "report.odt"))}, nil); msg != "" {
		t.Fatal(msg)
	}
	r = readResult{}
	if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/deck.odp"}, &r); msg != "" || !strings.Contains(r.Text, "Quarterly report") || strings.Contains(r.Text, "Roadmap") {
		t.Errorf("new version read the old text: %q %s", r.Text, msg)
	}
	// Old binary formats are not extracted.
	w.file(t, w.team, "", "legacy.doc", []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1\x00\x00"))
	if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/legacy.doc"}, nil); msg != api.MCPNotTextForTest {
		t.Errorf("legacy .doc: %q", msg)
	}
}

// With Euro-Office, a PDF's text comes from the Document Server's txt conversion through a
// one-time ticket; the output is cleaned, cached, and a failed conversion says so.
func TestMCPReadsPDFThroughEuroOffice(t *testing.T) {
	ds := &fakeDocumentServer{secret: []byte(strings.Repeat("s", 32))}
	editorServer := httptest.NewServer(ds.handler(t))
	defer editorServer.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYDRIVE_EDITOR_URL", editorServer.URL)
	t.Setenv("KYDRIVE_EDITOR_DRIVE_URL", "http://"+ln.Addr().String())
	t.Setenv("KYDRIVE_EDITOR_SECRET", string(ds.secret))
	w := newMCPWorld(t)
	live := httptest.NewUnstartedServer(w.srv)
	live.Listener.Close()
	live.Listener = ln
	live.Start()
	defer live.Close()
	_, tok := w.token(t, drive.ScopeMCPRead, "", "")
	cs := agent(t, w.srv, tok)

	pdf := []byte("%PDF-1.7\n\xe2\xe3\xcf\xd3\nfake pdf body")
	w.file(t, w.team, "", "scan.pdf", pdf)
	ds.output = []byte("\xef\xbb\xbfInvoice 42\r\nTotal: 10 \xe2\x82\xac\x00\r\n")
	var r struct {
		readResult
		Extracted string `json:"extracted"`
	}
	if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/scan.pdf"}, &r); msg != "" {
		t.Fatal(msg)
	}
	if r.Text != "Invoice 42\nTotal: 10 €\n" || r.Extracted != "PDF" || ds.asked != "txt" || string(ds.got) != string(pdf) {
		t.Fatalf("pdf text %q (%s), asked %q, the server fetched %q", r.Text, r.Extracted, ds.asked, ds.got)
	}
	// Cached: a second read converts nothing.
	ds.got, ds.output = nil, []byte("other")
	if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/scan.pdf"}, &r); msg != "" || r.Text != "Invoice 42\nTotal: 10 €\n" || ds.got != nil {
		t.Fatalf("second read %q %s, fetched again %v", r.Text, msg, ds.got != nil)
	}
	// A conversion the Document Server refuses is reported, and nothing is cached.
	ds.fail = true
	w.file(t, w.team, "", "broken.pdf", []byte("%PDF-1.4 broken"))
	if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/broken.pdf"}, nil); !strings.Contains(msg, "could not get the text of this PDF") {
		t.Errorf("failed conversion: %q", msg)
	}
	ds.fail, ds.output = false, []byte("now it works")
	if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/broken.pdf"}, &r); msg != "" || r.Text != "now it works" {
		t.Errorf("retry after a failed conversion: %q %s", r.Text, msg)
	}
	// Office documents never go to the Document Server.
	ds.asked = ""
	w.file(t, w.team, "", "deck.pptx", fixtureBytes(t, "deck.pptx"))
	if msg := callTool(t, cs, "read_file", map[string]any{"path": "/Team/deck.pptx"}, nil); msg != "" || ds.asked != "" {
		t.Errorf("pptx: %s, document server asked for %q", msg, ds.asked)
	}
}

func b64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

func textOf(res *mcp.CallToolResult) string {
	var out []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out = append(out, tc.Text)
		}
	}
	return strings.Join(out, " ")
}
