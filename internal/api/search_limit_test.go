package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// Content searches are limited per user, web and MCP together; name searches and other users
// are not affected.
func TestContentSearchIsRateLimited(t *testing.T) {
	w := newSearchWorld(t)
	w.put(t, "usr_alice", drive.File{Workspace: w.aliceMine, Name: "notes.txt"}, []byte("conference notes"))
	w.index(t)
	for i := range api.SearchesPerMinuteForTest {
		if rec := do(t, w.srv, "GET", "/api/drive/search?q=conf*", w.alice); rec.Code != http.StatusOK {
			t.Fatalf("search %d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := do(t, w.srv, "GET", "/api/drive/search?q=conf*", w.alice)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "60" || !strings.Contains(rec.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("over the limit: %d %q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	if rec := do(t, w.srv, "GET", "/api/drive/search?q=notes&contents=0", w.alice); rec.Code != http.StatusOK {
		t.Fatalf("names only: %d", rec.Code)
	}
	if rec := do(t, w.srv, "GET", "/api/drive/search?q=conf*", w.bob); rec.Code != http.StatusOK {
		t.Fatalf("another user: %d", rec.Code)
	}
	// The same budget covers alice's agents.
	_, tok := mcpToken(t, w, drive.ScopeMCPRead)
	cs := agent(t, w.srv, tok)
	if msg := callTool(t, cs, "search_files", map[string]any{"query": "conf*"}, nil); msg != api.MCPSearchRateForTest {
		t.Fatalf("mcp: %q", msg)
	}
	if msg := callTool(t, cs, "search_files", map[string]any{"query": "notes", "in": "names"}, nil); msg != "" {
		t.Fatalf("mcp names only: %q", msg)
	}
}
