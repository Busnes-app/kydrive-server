package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func typeSearch(t *testing.T, w *searchWorld, who *http.Cookie, query string) searchResponse {
	t.Helper()
	rec := do(t, w.srv, "GET", "/api/drive/search?"+query, who)
	if rec.Code != http.StatusOK {
		t.Fatalf("search %s: %d %s", query, rec.Code, rec.Body.String())
	}
	var r searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	r.raw = rec.Body.String()
	return r
}

func (r searchResponse) names() []string {
	out := []string{}
	for _, h := range r.Results {
		out = append(out, h.Name)
	}
	slices.Sort(out)
	return out
}

// The type filter over HTTP: every type, with and without a query, applied to name and content
// hits alike, never widening what a user can read.
func TestSearchTypeFilter(t *testing.T) {
	w := newSearchWorld(t)
	ctx := context.Background()
	if _, err := w.st.Drive().AddFolder(ctx, "usr_alice", w.team, "", "Plans"); err != nil {
		t.Fatal(err)
	}
	byType := map[string]string{
		"documents": "plan.docx", "spreadsheets": "plan.CSV", "presentations": "plan.pptx", "pdfs": "plan.pdf",
		"images": "plan.png", "videos": "plan.mp4", "audio": "plan.mp3", "archives": "plan.zip", "folders": "Plans",
	}
	for typ, name := range byType {
		if typ != "folders" {
			w.put(t, "usr_alice", drive.File{Workspace: w.team, Name: name}, []byte("x"))
		}
	}
	w.put(t, "usr_alice", drive.File{Workspace: w.team, Name: "notes.md"}, []byte("okapimarker in a document"))
	w.put(t, "usr_alice", drive.File{Workspace: w.team, Name: "table.tsv"}, []byte("okapimarker\tin a spreadsheet"))
	w.put(t, "usr_bob", drive.File{Workspace: w.bobMine, Name: "bob plan.pdf"}, []byte("x"))
	w.put(t, "usr_bob", drive.File{Workspace: w.bobMine, Name: "bob.md"}, []byte("okapimarker bob"))
	w.index(t)

	for typ, name := range byType {
		for _, q := range []string{"", "plan", "PLAN"} {
			r := typeSearch(t, w, w.alice, "type="+typ+"&q="+q)
			if !slices.Contains(r.names(), name) || q != "" && len(r.Results) != 1 {
				t.Errorf("%s %q: %v", typ, q, r.names())
			}
			for _, h := range r.Results {
				if got := drive.FileType(h.Name); h.Kind == "folder" != (typ == "folders") || h.Kind == "file" && got != typ {
					t.Errorf("%s %q: %s is %s %q", typ, q, h.Name, h.Kind, got)
				}
			}
		}
	}
	if r := typeSearch(t, w, w.alice, "q=plan"); len(r.Results) != len(byType) {
		t.Fatalf("no type: %v", r.names())
	}
	// Content hits are filtered too; an empty query searches no contents.
	if r := typeSearch(t, w, w.alice, "q=okapimarker&type=documents"); fmt.Sprint(r.names()) != "[notes.md]" || r.Results[0].Snippet == nil || !r.Contents.Searched {
		t.Fatalf("content documents: %s", r.raw)
	}
	if r := typeSearch(t, w, w.alice, "q=okapimarker&type=spreadsheets"); fmt.Sprint(r.names()) != "[table.tsv]" {
		t.Fatalf("content spreadsheets: %s", r.raw)
	}
	if r := typeSearch(t, w, w.alice, "type=documents"); fmt.Sprint(r.names()) != "[notes.md plan.docx]" || r.Contents.Searched || strings.Contains(r.raw, "snippet") {
		t.Fatalf("type only: %s", r.raw)
	}

	// Never reveals what the user cannot read: bob's PDF stays his, the admin sees nothing.
	if r := typeSearch(t, w, w.alice, "type=pdfs"); strings.Contains(r.raw, "bob") {
		t.Fatalf("alice sees bob's: %s", r.raw)
	}
	if r := typeSearch(t, w, w.bob, "type=pdfs"); fmt.Sprint(r.names()) != "[bob plan.pdf]" {
		t.Fatalf("bob: %s", r.raw)
	}
	if r := typeSearch(t, w, w.bob, "q=okapimarker&type=documents"); fmt.Sprint(r.names()) != "[bob.md]" {
		t.Fatalf("bob contents: %s", r.raw)
	}
	for _, typ := range []string{"pdfs", "folders", "documents"} {
		if r := typeSearch(t, w, w.boss, "type="+typ); len(r.Results) != 0 {
			t.Fatalf("admin bypass %s: %s", typ, r.raw)
		}
	}
	token := strings.Repeat("s", 64)
	if _, err := w.st.Drive().CreateServiceToken(ctx, "usr_bob", w.bobMine, "Robot", "reader", token); err != nil {
		t.Fatal(err)
	}
	if rec := send(t, w.srv, "GET", "/api/drive/search?type=pdfs", "", nil, token); rec.Code != 200 || !strings.Contains(rec.Body.String(), "bob plan.pdf") || strings.Contains(rec.Body.String(), w.team) {
		t.Fatalf("service token: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, w.srv, "GET", "/api/drive/search?type=pdfs", nil); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "plan") {
		t.Fatalf("anonymous: %d %s", rec.Code, rec.Body.String())
	}

	// Unknown types are refused with bad_type, before any search; a bare empty query is invalid.
	for _, query := range []string{"type=pdf", "type=PDFs", "type=files&q=plan", "type=%20pdfs", "type=pdfs,images", "type=folders%00"} {
		rec := do(t, w.srv, "GET", "/api/drive/search?"+query, w.alice)
		var body map[string]string
		if rec.Code != 400 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["code"] != "bad_type" || body["error"] == "" {
			t.Errorf("%s: %d %s", query, rec.Code, rec.Body.String())
		}
	}
	for _, query := range []string{"q=", "q=p&type=pdfs", "type=&q=", "q=" + strings.Repeat("p", 101) + "&type=pdfs"} {
		if rec := do(t, w.srv, "GET", "/api/drive/search?"+query, w.alice); rec.Code != 400 || strings.Contains(rec.Body.String(), "bad_type") {
			t.Errorf("%s: %d %s", query, rec.Code, rec.Body.String())
		}
	}
}

func TestMCPSearchFilesType(t *testing.T) {
	w := newSearchWorld(t)
	ctx := context.Background()
	inner, err := w.st.Drive().AddFolder(ctx, "usr_alice", w.team, "", "Inner")
	if err != nil {
		t.Fatal(err)
	}
	w.put(t, "usr_alice", drive.File{Workspace: w.team, Name: "outside.pdf"}, []byte("x"))
	w.put(t, "usr_alice", drive.File{Workspace: w.team, Parent: inner.ID, Name: "inside.pdf"}, []byte("x"))
	w.put(t, "usr_alice", drive.File{Workspace: w.team, Parent: inner.ID, Name: "inside.md"}, []byte("okapimarker"))
	w.put(t, "usr_bob", drive.File{Workspace: w.bobMine, Name: "bob.pdf"}, []byte("x"))
	w.index(t)
	type result struct {
		Results []struct{ Path, Kind string } `json:"results"`
	}
	paths := func(r result) string {
		var out []string
		for _, h := range r.Results {
			out = append(out, h.Path)
		}
		slices.Sort(out)
		return fmt.Sprint(out)
	}
	_, tok := mcpToken(t, w, drive.ScopeMCPRead)
	cs := agent(t, w.srv, tok)
	var r result
	if msg := callTool(t, cs, "search_files", map[string]any{"type": "pdfs"}, &r); msg != "" || paths(r) != "[/Team/Inner/inside.pdf /Team/outside.pdf]" {
		t.Fatalf("pdfs: %+v %s", r, msg)
	}
	r = result{}
	if msg := callTool(t, cs, "search_files", map[string]any{"query": "okapimarker", "type": "pdfs"}, &r); msg != "" || len(r.Results) != 0 {
		t.Fatalf("content pdfs: %+v %s", r, msg)
	}
	r = result{}
	if msg := callTool(t, cs, "search_files", map[string]any{"query": "okapimarker", "type": "documents"}, &r); msg != "" || paths(r) != "[/Team/Inner/inside.md]" {
		t.Fatalf("content documents: %+v %s", r, msg)
	}
	r = result{}
	if msg := callTool(t, cs, "search_files", map[string]any{"type": "folders"}, &r); msg != "" || paths(r) != "[/Team/Inner]" {
		t.Fatalf("folders: %+v %s", r, msg)
	}
	if msg := callTool(t, cs, "search_files", map[string]any{"query": "inside", "type": "pdf"}, nil); msg != api.MCPSearchTypeForTest {
		t.Fatalf("bad type: %q", msg)
	}
	if msg := callTool(t, cs, "search_files", map[string]any{}, nil); !strings.Contains(msg, "2 to 100") {
		t.Fatalf("no query, no type: %q", msg)
	}
	_, limited, err := w.st.Drive().CreateAgentToken(ctx, "usr_alice", "limited", drive.ScopeMCPRead, w.team, inner.ID)
	if err != nil {
		t.Fatal(err)
	}
	r = result{}
	if msg := callTool(t, agent(t, w.srv, limited), "search_files", map[string]any{"type": "pdfs"}, &r); msg != "" || paths(r) != "[/inside.pdf]" {
		t.Fatalf("folder-limited agent: %+v %s", r, msg)
	}
}
