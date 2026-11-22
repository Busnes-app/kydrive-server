package drive_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// sqlTableRef finds the tables a statement reads or writes.
var sqlTableRef = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE|TABLE|ON)\s+([a-z_]+)\b`)

// orgScoped are the drive fragments that bind an org; a SQL text calling one is scoped.
var orgScoped = map[string]bool{"rankSQL": true, "itemRankSQL": true, "activePrincipal": true, "sameOrg": true, "userOrg": true, "workspaceOrg": true}

type orgSQLUnit struct {
	text string
	pos  token.Position
}

// orgSQLUnits returns every SQL text (a string literal, or a chain of them joined with +) in
// the non-test files of dirs that names an org-owned table and neither org_id nor a scoped
// fragment, keyed by package and declaration.
func orgSQLUnits(t *testing.T, dirs ...string) map[string][]orgSQLUnit {
	t.Helper()
	owned := map[string]bool{}
	for _, name := range drive.OrgOwnedTables {
		owned[name] = true
	}
	fset := token.NewFileSet()
	out := map[string][]orgSQLUnit{}
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			f, err := parser.ParseFile(fset, file, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range f.Decls {
				name := f.Name.Name + "." + declName(d)
				ast.Inspect(d, func(n ast.Node) bool {
					e, ok := n.(ast.Expr)
					if !ok {
						return true
					}
					text, ok := sqlText(e)
					if !ok {
						return true
					}
					if strings.Contains(text, "org_id") {
						return false
					}
					for _, m := range sqlTableRef.FindAllStringSubmatch(text, -1) {
						if owned[strings.ToLower(m[1])] {
							out[name] = append(out[name], orgSQLUnit{text, fset.Position(e.Pos())})
							break
						}
					}
					return false
				})
			}
		}
	}
	return out
}

func declName(d ast.Decl) string {
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil && len(d.Recv.List) > 0 {
			recv := d.Recv.List[0].Type
			if s, ok := recv.(*ast.StarExpr); ok {
				recv = s.X
			}
			if id, ok := recv.(*ast.Ident); ok {
				return id.Name + "." + d.Name.Name
			}
		}
		return d.Name.Name
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			if v, ok := spec.(*ast.ValueSpec); ok && len(v.Names) > 0 {
				return v.Names[0].Name
			}
		}
	}
	return "?"
}

// sqlText joins the literal parts of a string expression. A call to an org-scoped fragment
// stands in as org_id, any other non-literal part as "?"; ok is false when no part is a
// literal.
func sqlText(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		l, lok := sqlText(e.X)
		r, rok := sqlText(e.Y)
		if !lok && !rok {
			return "", false
		}
		if !lok {
			l = part(e.X)
		}
		if !rok {
			r = part(e.Y)
		}
		return l + r, true
	case *ast.ParenExpr:
		return sqlText(e.X)
	}
	return "", false
}

func part(e ast.Expr) string {
	if c, ok := e.(*ast.CallExpr); ok {
		if id, ok := c.Fun.(*ast.Ident); ok && orgScoped[id.Name] {
			return " org_id "
		}
	}
	return " ? "
}

// Every SQL text in drive, store and api that names an org-owned table also names org_id or
// calls an org-scoped fragment, or its declaration is listed in orgSQLAllowlist with the
// reason it may not. Crude by design: a listed declaration may hold a scoped and an unscoped
// text, so an entry is a reviewed claim about the whole declaration.
func TestOrgSQLNamesOrgID(t *testing.T) {
	units := orgSQLUnits(t, ".", "../store", "../api")
	for _, name := range slices.Sorted(maps.Keys(units)) {
		if _, ok := orgSQLAllowlist[name]; ok {
			continue
		}
		for _, u := range units[name] {
			t.Errorf("%s: %s names an org-owned table and not org_id: %.140q", u.pos, name, strings.Join(strings.Fields(u.text), " "))
		}
	}
	for name := range orgSQLAllowlist {
		if units[name] == nil {
			t.Errorf("orgSQLAllowlist[%q]: no unscoped SQL left there; remove the entry", name)
		}
	}
}

// orgSQLAllowlist: the declarations whose SQL may name an org-owned table without org_id, and why.
// A "step" entry goes when that build step scopes it; the test fails on an entry with nothing
// left to allow.
var orgSQLAllowlist = map[string]string{}

func init() {
	for reason, names := range map[string]string{
		"principal: the caller's own rows, keyed by their user ID (or a row of theirs found by it)":                                                                                  "drive.Store.AppPasswords drive.Store.CloseViewCopy drive.Store.Devices drive.Store.IssueTicket drive.Store.IssueViewTicket drive.Store.PeerFree drive.Store.PeerSetting drive.Store.RevokeAppPassword drive.Store.RevokeDevice drive.Store.SetAgentScope drive.Store.SetPeerGuestSetting drive.Store.UnlinkDevice drive.Store.ViewCopy drive.Store.createCredential drive.Store.peerFolder drive.deviceSlot drive.peerGuest drive.peerReads drive.peerSwitch",
		"credential: finds a token, code or ticket by its hash (or the row that hash found); the owner then passes activePrincipal or rank":                                          "drive.Store.AgentTokenUser drive.Store.AppPasswordUser drive.Store.DeviceUser drive.Store.ServiceToken drive.Store.StoreViewCopy drive.Store.Ticket drive.Store.TicketUpload drive.Store.dropTicket drive.Store.redeem drive.Store.ticketSession",
		"authorized: keyed by a workspace or item the caller passed rank on first (rank binds the org)":                                                                              "drive.Store.Changes drive.Store.EditorSession drive.Store.GuestLinks drive.Store.Links drive.Store.PublishEditor drive.Store.RevokeServiceToken drive.Store.SetEditorsShare drive.Store.Shares drive.Store.Snapshot drive.Store.describe drive.Store.moveFile drive.Store.setFolderTrash drive.fits drive.sharedWorkspace drive.viewBytes",
		"editor: a Euro-Office callback keyed by a document key or session it signed; the session's user passes rank":                                                                "drive.Store.CloseEditor drive.Store.EditorDownload",
		"platform: a worker over every org, by row ID or age; rows keep their own org":                                                                                               "drive.Store.ExpireTickets drive.Store.ExpireViewCopies drive.Store.IndexChanges drive.Store.MarkViewDropped drive.Store.PendingViewDrops drive.Store.PruneChanges drive.Store.PruneDevices drive.Store.PurgeDeletedWorkspaces drive.Store.RevokeUserCredentials drive.Store.dropView drive.Store.expireAgentTokens drive.Store.revokeEditors drive.expiredFile drive.expiredFolders drive.purgeFile drive.removeWorkspace",
		"fragment: the access definition itself, which binds the org through sameOrg and activePrincipal":                                                                            "drive.itemRankSQL drive.rankSQL",
		"schema: DDL, or a migration step that writes the columns of its own, older schema":                                                                                          "drive.Store.renameClashes drive.open drive.schema",
		"step 4: identity binding gives accounts, groups and suite groups their org from the token, webhook or suite answer":                                                         "drive.Store.reconcileSuiteGroup drive.suiteMembers store.caseVariantFree store.caseVariantTaken store.groupStore.CreateGroup store.groupStore.DeleteGroup store.groupStore.GetGroupByExternalID store.groupStore.GetGroupByName store.groupStore.ReplaceGroup store.groupStore.UpdateGroup store.sessionStore.EndSSOSubject store.userStore.ApplyDirectoryUser store.userStore.CreateUser store.userStore.DeleteDirectoryUser store.userStore.GetUserByEmail store.userStore.GetUserBySSO store.userStore.GetUserByUsername store.userStore.RevokeDirectoryCredentials store.userStore.SetDirectoryRole store.userStore.directoryRowMissing",
		"main store: keyed by an account or group ID the caller already holds; the main store also serves PostgreSQL, which has no orgs":                                             "store.SQLStore.withPassword store.groupStore.GetGroupByID store.groupStore.GetUserGroups store.sessionStore.CreateSession store.userStore.CompletePasswordChange store.userStore.DeleteUser store.userStore.GetUserByID store.userStore.OpenInNewTab store.userStore.RenameLocalAdmin store.userStore.ResetAdminPassword store.userStore.SetOpenInNewTab store.userStore.UpdateUser",
		"installer and restore: single-tenant only; their rows are in the default org":                                                                                               "store.SQLStore.ApplyInstaller",
		"single-tenant: refused on a multi-tenant store (singleTenant, D10), or the suite-group reconcile that step 4 binds to its org":                                              "drive.Store.DeleteWorkspace drive.Store.Quota drive.Store.RenameWorkspace drive.Store.SetRetention drive.archiveWorkspace",
		"admin: keyed by a workspace that adminIn or adminOrg first placed in the org staff act in":                                                                                  "drive.Store.Grants drive.Store.handover drive.restoreWorkspace",
		"main store: unused by any route, a platform count at startup, or inbound SCIM, which a multi-tenant install refuses; the directory route filters groups by org (OrgGroups)": "store.auditStore.ListAuditRecords store.groupStore.ListGroups store.userStore.CountUsers store.userStore.ListDirectoryUsers store.userStore.ListUsers",
		"main store: PostgreSQL has no orgs; on SQLite the same statement names org_id":                                                                                              "store.SQLStore.auditSQL",
	} {
		for _, name := range strings.Fields(names) {
			orgSQLAllowlist[name] = reason
		}
	}
}
