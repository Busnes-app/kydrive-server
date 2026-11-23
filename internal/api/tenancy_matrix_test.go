package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/google/uuid"
)

const testStaffOrg = "5ea1f000-0000-4000-8000-000000000001"

// multiAPI is a multi-tenant server with a staff administrator and orgs A and B. Each org has
// two members in a group that manages a shared workspace holding a folder and a file, which
// one member shared with the other and published as an organisation link. Each org also has
// a leaver whose My files holds a file, and a deleted shared workspace.
type multiAPI struct {
	srv     *api.Server
	cfg     *config.Config
	st      store.Store
	db      *sql.DB
	session map[string]*http.Cookie
	ids     map[string]map[string]string // org -> kind -> ID
}

func setupMultiServer(t *testing.T) *multiAPI {
	t.Helper()
	dir, err := os.MkdirTemp("", "drive-api-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("KY_DATA_DIR", dir)
	cfg, _ := config.LoadFromEnv()
	db := testdb.Config(t)
	db.DataDir = cfg.Database.DataDir
	db.Tenancy, db.StaffOrg = drive.TenancyMulti, testStaffOrg
	cfg.Database = db
	cfg.Captcha.Provider = "none"
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database, cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	raw := testdb.Open(t, cfg.Database)
	m := &multiAPI{srv: api.NewServer(cfg, st), cfg: cfg, st: st, db: raw, session: map[string]*http.Cookie{}, ids: map[string]map[string]string{}}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := raw.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	hash, err := password.Hash("SuperSecretPass123!")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO drive_orgs(id,status) VALUES('org-a','active'),('org-b','active')`)
	users := [][4]string{{"staff", "admin", "active", testStaffOrg}}
	for _, o := range []string{"a", "b"} {
		users = append(users, [4]string{o + "1", "user", "active", "org-" + o}, [4]string{o + "2", "user", "active", "org-" + o}, [4]string{o + "-gone", "user", "active", "org-" + o})
	}
	for _, u := range users {
		exec(`INSERT INTO users(id,username,email,display_name,password_hash,role,status,sso_provider,recovery_codes_hash,created_at,updated_at,org_id) VALUES(?,?,?,?,?,?,?,'local','[]',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, u[0], u[0], u[0]+"@local.test", "Person "+u[0], hash, u[1], u[2], u[3])
	}
	d := st.Drive()
	for _, o := range []string{"a", "b"} {
		org, ids := "org-"+o, map[string]string{}
		m.ids[org] = ids
		ids["group"], ids["workspace"], ids["deleted"] = "group-"+o, uuid.NewString(), uuid.NewString()
		exec(`INSERT INTO groups(id,display_name,external_id,created_at,updated_at,org_id) VALUES(?,?,'',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, ids["group"], "Group "+o, org)
		exec(`INSERT INTO group_members VALUES(?,?),(?,?)`, ids["group"], o+"1", ids["group"], o+"2")
		exec(`INSERT INTO drive_workspaces(id,name,quota,org_id) VALUES(?,'Team',1048576,?),(?,'Old',1048576,?)`, ids["workspace"], org, ids["deleted"], org)
		exec(`INSERT INTO drive_grants VALUES(?,?,'manager'),(?,?,'manager')`, ids["workspace"], ids["group"], ids["deleted"], ids["group"])
		member := o + "1"
		folder, err := d.AddFolder(ctx, member, ids["workspace"], "", "Plans")
		if err != nil {
			t.Fatal(err)
		}
		ids["folder"] = folder.ID
		v, err := drive.WriteBlob(objects.Dir(filepath.Join(dir, "blobs")), cfg.Security.EncryptionKey, strings.NewReader("secret plan of "+org), 1024)
		if err != nil {
			t.Fatal(err)
		}
		f, err := d.Publish(ctx, member, drive.File{Workspace: ids["workspace"], Parent: folder.ID, Name: "plan.txt"}, v, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids["file"] = f.ID
		share, err := d.Share(ctx, member, "file", f.ID, "user", o+"2", "reader")
		if err != nil {
			t.Fatal(err)
		}
		ids["share"] = share.ID
		link, _, err := d.CreateLink(ctx, member, "file", f.ID, "reader", 0)
		if err != nil {
			t.Fatal(err)
		}
		ids["link"] = link.ID
		if ids["personal"], err = d.EnsurePersonalWorkspace(ctx, o+"-gone"); err != nil {
			t.Fatal(err)
		}
		if _, err = d.Publish(ctx, o+"-gone", drive.File{Workspace: ids["personal"], Name: "notes.txt"}, v, 0); err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE users SET status='inactive' WHERE id=?`, o+"-gone")
		exec(`UPDATE drive_workspaces SET deleted_at='2026-11-01T00:00:00Z',deleted_by=? WHERE id=?`, member, ids["deleted"])
	}
	for _, u := range []string{"staff", "a1", "b1"} {
		m.session[u] = m.signIn(t, u)
	}
	return m
}

func (m *multiAPI) signIn(t *testing.T, username string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": "SuperSecretPass123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.srv.ServeHTTP(w, req)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatalf("sign in %s: %d %s", username, w.Code, w.Body.String())
	return nil
}

// call sends method path as user's session, with org in X-Ky-Org when set, and a JSON body
// of {} for a write.
func (m *multiAPI) call(user, org, method, path string) *httptest.ResponseRecorder {
	var body *bytes.Reader
	if method == http.MethodGet || method == http.MethodDelete {
		body = bytes.NewReader(nil)
	} else {
		body = bytes.NewReader([]byte(`{}`))
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	if org != "" {
		req.Header.Set("X-Ky-Org", org)
	}
	req.AddCookie(m.session[user])
	api.SignCSRFForTest(m.srv, req)
	w := httptest.NewRecorder()
	m.srv.ServeHTTP(w, req)
	return w
}

// orgBState renders every row of org B's content, so a test can tell it was left alone.
func (m *multiAPI) orgBState(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT id||deleted_at||name||quota FROM drive_workspaces WHERE org_id='org-b' ORDER BY id`,
		`SELECT f.id||f.parent||f.name||f.revision||f.trashed FROM drive_files f JOIN drive_workspaces w ON w.id=f.workspace WHERE w.org_id='org-b' ORDER BY f.id`,
		`SELECT d.id||d.parent||d.name||d.trashed FROM drive_folders d JOIN drive_workspaces w ON w.id=d.workspace WHERE w.org_id='org-b' ORDER BY d.id`,
		`SELECT s.id||s.principal_id||s.role FROM drive_shares s JOIN drive_workspaces w ON w.id=s.workspace WHERE w.org_id='org-b' ORDER BY s.id`,
		`SELECT l.id||l.revoked||l.role FROM drive_links l JOIN drive_workspaces w ON w.id=l.workspace WHERE w.org_id='org-b' ORDER BY l.id`,
		`SELECT g.workspace||g.group_id||g.role FROM drive_grants g JOIN drive_workspaces w ON w.id=g.workspace WHERE w.org_id='org-b' ORDER BY 1`,
		`SELECT group_id||user_id FROM group_members WHERE group_id='group-b' ORDER BY 1`,
		`SELECT link||user_id FROM drive_link_users ORDER BY 1`,
	} {
		rows, err := m.db.Query(q)
		if err != nil {
			t.Fatal(q, err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			b.WriteString(s + "\n")
		}
		rows.Close()
	}
	return b.String()
}

var driveRoute = regexp.MustCompile(`HandleFunc\("([A-Z]+) (/api/drive/[^"]*\{[^"]*)"`)

// idRoutes are the /api/drive routes that take an ID in their path, read from the source so a
// new route is covered without listing it.
func idRoutes(t *testing.T) [][2]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out [][2]string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range driveRoute.FindAllStringSubmatch(string(src), -1) {
			out = append(out, [2]string{m[1], m[2]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][1]+out[i][0] < out[j][1]+out[j][0] })
	if len(out) < 30 {
		t.Fatalf("found only %d ID routes; the route scan is broken", len(out))
	}
	return out
}

var pathParam = regexp.MustCompile(`\{[a-z]+\.{0,3}\}`)

// Every /api/drive route that takes an ID answers a member of org A, and staff acting in org A,
// for each of org B's real IDs exactly as for an ID that never existed (an idempotent no-op
// may succeed for both), and changes nothing of B's.
func TestCrossOrgMatrix(t *testing.T) {
	m := setupMultiServer(t)
	before := m.orgBState(t)
	kinds := []string{"workspace", "file", "folder", "share", "link", "group", "personal", "deleted"}
	callers := []struct{ user, org string }{{"a1", ""}, {"staff", "org-a"}}
	for _, route := range idRoutes(t) {
		method, pattern := route[0], route[1]
		for _, c := range callers {
			for _, kind := range kinds {
				real, missing := m.ids["org-b"][kind], uuid.NewString()
				cross := m.call(c.user, c.org, method, pathParam.ReplaceAllString(pattern, real))
				never := m.call(c.user, c.org, method, pathParam.ReplaceAllString(pattern, missing))
				got := strings.ReplaceAll(cross.Body.String(), real, "ID")
				want := strings.ReplaceAll(never.Body.String(), missing, "ID")
				if cross.Code != never.Code || got != want {
					t.Errorf("%s %s as %s: B's %s answers %d %s, a missing ID %d %s", method, pattern, c.user, kind, cross.Code, got, never.Code, want)
				}
			}
		}
	}
	if after := m.orgBState(t); after != before {
		t.Errorf("org B changed:\n%s\nbecame\n%s", before, after)
	}
	// The same reads with A's own IDs succeed, so the calls above reached their handlers.
	reached := 0
	for _, route := range idRoutes(t) {
		if route[0] != http.MethodGet {
			continue
		}
		for _, kind := range kinds {
			if w := m.call("a1", "", route[0], pathParam.ReplaceAllString(route[1], m.ids["org-a"][kind])); w.Code == http.StatusOK {
				reached++
			}
		}
	}
	if reached < 10 {
		t.Errorf("only %d reads of A's own items succeeded; the matrix is not reaching its handlers", reached)
	}
}

// The org admin routes need X-Ky-Org naming an org staff may select, show only that org, and
// audit each read there; the refused routes are 404; a member is no administrator.
func TestAdminSurfaceIsBoundToOneOrg(t *testing.T) {
	m := setupMultiServer(t)
	for _, org := range []string{"", "no-such-org", testStaffOrg + "x"} {
		if w := m.call("staff", org, http.MethodGet, "/api/drive/leavers"); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "org_required") {
			t.Errorf("leavers with X-Ky-Org %q: %d %s", org, w.Code, w.Body.String())
		}
	}
	w := m.call("staff", "org-a", http.MethodGet, "/api/drive/leavers")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), m.ids["org-a"]["personal"]) || strings.Contains(w.Body.String(), m.ids["org-b"]["personal"]) {
		t.Fatalf("A's leavers: %d %s", w.Code, w.Body.String())
	}
	w = m.call("staff", "org-a", http.MethodGet, "/api/drive/deleted-workspaces")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), m.ids["org-a"]["deleted"]) || strings.Contains(w.Body.String(), m.ids["org-b"]["deleted"]) {
		t.Fatalf("A's deleted workspaces: %d %s", w.Code, w.Body.String())
	}
	w = m.call("staff", "org-a", http.MethodGet, "/api/drive/directory")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "group-a") || strings.Contains(w.Body.String(), "group-b") {
		t.Fatalf("A's directory: %d %s", w.Code, w.Body.String())
	}
	w = m.call("staff", "org-a", http.MethodGet, "/api/drive/audit")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"b1"`) || !strings.Contains(w.Body.String(), "staff.read") {
		t.Fatalf("A's audit: %d %s", w.Code, w.Body.String())
	}
	var reads int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM drive_events WHERE action='staff.read' AND user_id='staff' AND org_id='org-a'`).Scan(&reads); err != nil || reads != 4 {
		t.Errorf("%d staff reads audited in org A: %v", reads, err)
	}
	for _, route := range [][2]string{
		{http.MethodPost, "/api/drive/workspaces"},
		{http.MethodPut, "/api/drive/workspaces/" + m.ids["org-a"]["workspace"] + "/quota"},
		{http.MethodPut, "/api/drive/workspaces/" + m.ids["org-a"]["workspace"] + "/name"},
		{http.MethodDelete, "/api/drive/workspaces/" + m.ids["org-a"]["workspace"]},
		{http.MethodPut, "/api/drive/workspaces/" + m.ids["org-a"]["workspace"] + "/retention"},
		{http.MethodGet, "/scim/v2/Users"},
	} {
		// A malformed body: refused before any handler could answer 400 for it.
		req := httptest.NewRequest(route[0], route[1], strings.NewReader("{"))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(m.session["staff"])
		api.SignCSRFForTest(m.srv, req)
		w := httptest.NewRecorder()
		m.srv.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s on a multi-tenant install: %d", route[0], route[1], w.Code)
		}
	}
	if w := m.call("staff", "", http.MethodGet, "/api/drive/status"); w.Code != http.StatusOK {
		t.Errorf("platform status: %d %s", w.Code, w.Body.String())
	}
	if w := m.call("a1", "org-a", http.MethodGet, "/api/drive/leavers"); w.Code != http.StatusForbidden {
		t.Errorf("a member lists leavers: %d", w.Code)
	}
}

// A customer account given role='admin' with the trigger dropped is still no administrator.
func TestPlantedCustomerAdminIsRefused(t *testing.T) {
	m := setupMultiServer(t)
	for _, name := range []string{"users_admin_is_staff_insert", "users_admin_is_staff_update"} {
		q := `DROP TRIGGER ` + name
		if testdb.Postgres() {
			q += ` ON users`
		}
		if _, err := m.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.db.Exec(`UPDATE users SET role='admin' WHERE id='a1'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/drive/status", "/api/backup/status", "/api/drive/leavers"} {
		if w := m.call("a1", "org-a", http.MethodGet, path); w.Code != http.StatusForbidden {
			t.Errorf("%s as a planted customer administrator: %d %s", path, w.Code, w.Body.String())
		}
	}
}

// adminClass is every administrator route's class, as the tenancy design's Admin surface
// lists it. A route registered with another class, or none, fails.
var adminClass = map[string]string{
	"GET /api/drive/status": "platformAdmin", "GET /api/settings/kypost": "platformAdmin", "/api/settings/theme": "platformAdmin",
	"GET /api/settings/rate-limit-networks": "platformAdmin", "PUT /api/settings/rate-limit-networks": "platformAdmin",
	"GET /api/drive/trash-retention": "platformAdmin", "PUT /api/drive/trash-retention": "platformAdmin",
	"PUT /api/drive/guest-links-policy": "platformAdmin", "PUT /api/drive/device-policy": "platformAdmin",
	"POST /api/backup/drill": "platformAdmin", "POST /api/backup/export-capsule": "platformAdmin", "POST /api/backup/pair-remote": "platformAdmin",
	"POST /api/backup/deposit": "platformAdmin", "DELETE /api/backup/pairing": "platformAdmin", "POST /api/backup/pin-key": "platformAdmin",
	"PUT /api/backup/schedule": "platformAdmin", "PUT /api/backup/bulk-retention": "platformAdmin", "GET /api/backup/status": "platformAdmin",
	"GET /api/drive/directory": "orgAdmin", "GET /api/drive/audit": "orgAdmin", "GET /api/drive/leavers": "orgAdmin",
	"POST /api/drive/leavers/{workspace}/handover": "orgAdmin", "GET /api/drive/deleted-workspaces": "orgAdmin",
	"POST /api/drive/workspaces/{id}/restore": "orgAdmin",
	"POST /api/drive/workspaces":              "singleTenantAdmin", "PUT /api/drive/workspaces/{id}/quota": "singleTenantAdmin",
	"PUT /api/drive/workspaces/{id}/name": "singleTenantAdmin", "DELETE /api/drive/workspaces/{id}": "singleTenantAdmin",
	"PUT /api/drive/workspaces/{id}/retention": "singleTenantAdmin",
}

var adminRoute = regexp.MustCompile(`HandleFunc\("([^"]+)",[^\n]*?s\.(platformAdmin|orgAdmin|singleTenantAdmin|requireAdmin)\(`)

func TestAdminRoutesHaveTheirClass(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range adminRoute.FindAllStringSubmatch(string(src), -1) {
			got[m[1]] = m[2]
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(adminClass) {
		for route, class := range got {
			if adminClass[route] != class {
				t.Errorf("%s is %s, want %q", route, class, adminClass[route])
			}
		}
		for route := range adminClass {
			if got[route] == "" {
				t.Errorf("%s is not registered as an administrator route", route)
			}
		}
	}
}

// Revoking another org's service token answers exactly as revoking an ID that never existed:
// the same status and body for an active, a revoked and an expired org B token, whether the
// caller is an org A member's session or an org A reader service token. Org B's tokens are left
// as they were.
func TestForeignServiceTokensAnswerAsMissing(t *testing.T) {
	m := setupMultiServer(t)
	ctx, d := context.Background(), m.st.Drive()
	tokenB := func(secret string) string {
		t.Helper()
		id, err := d.CreateServiceToken(ctx, "b1", m.ids["org-b"]["workspace"], "robot", "reader", secret)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	active, revoked, expired := tokenB(strings.Repeat("1", 64)), tokenB(strings.Repeat("2", 64)), tokenB(strings.Repeat("3", 64))
	if err := d.RevokeServiceToken(ctx, "b1", revoked); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`UPDATE drive_service_tokens SET expires=1 WHERE id=?`, expired); err != nil {
		t.Fatal(err)
	}
	readerA := strings.Repeat("4", 64)
	if _, err := d.CreateServiceToken(ctx, "a1", m.ids["org-a"]["workspace"], "robot", "reader", readerA); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		var s strings.Builder
		rows, err := m.db.Query(`SELECT id||revoked||expires FROM drive_service_tokens WHERE org_id='org-b' ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var r string
			if err := rows.Scan(&r); err != nil {
				t.Fatal(err)
			}
			s.WriteString(r + "\n")
		}
		return s.String()
	}
	orgB := state()
	callers := map[string]func(path string) *httptest.ResponseRecorder{
		"session": func(path string) *httptest.ResponseRecorder { return m.call("a1", "", http.MethodDelete, path) },
		"reader service token": func(path string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodDelete, path, nil)
			req.Header.Set("Authorization", "Bearer "+readerA)
			w := httptest.NewRecorder()
			m.srv.ServeHTTP(w, req)
			return w
		},
	}
	for who, call := range callers {
		missing := call("/api/drive/tokens/" + uuid.NewString())
		for name, id := range map[string]string{"active": active, "revoked": revoked, "expired": expired} {
			got := call("/api/drive/tokens/" + id)
			if got.Code != missing.Code || got.Body.String() != missing.Body.String() {
				t.Errorf("%s, org B's %s token: %d %s; a missing ID: %d %s", who, name, got.Code, got.Body.String(), missing.Code, missing.Body.String())
			}
		}
	}
	if now := state(); now != orgB {
		t.Fatalf("org B's tokens changed:\n%s\nwant\n%s", now, orgB)
	}
}
