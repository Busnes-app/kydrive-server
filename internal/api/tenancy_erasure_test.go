package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// erasedOrg is a registered org with a KyIdentity-style ID, as the erased list carries.
const erasedOrg = "e7a5ed00-0000-4000-8000-000000000001"

func (m *multiAPI) state(t *testing.T, sub string) (s string) {
	t.Helper()
	_ = m.db.QueryRow(`SELECT status||'/'||org_id FROM users WHERE sso_provider='kysignon' AND sso_subject=?`, sub).Scan(&s)
	return s
}

func (m *multiAPI) sessions(t *testing.T, sub string) int {
	return m.count(t, `SELECT COUNT(*) FROM sessions WHERE user_id IN (SELECT id FROM users WHERE sso_subject=?)`, sub)
}

// A multi-tenant KyIdentity's user.deleted disables: the account and its revision stay, so
// an older event cannot bring it back, and a newer one can.
func TestUserDeletedDisablesInMultiTenant(t *testing.T) {
	m, f := multiSSO(t)
	f.org = "org-a"
	if c := sessionCookie(ssoLogin(t, m.srv, f, "dave", "sid-1")); c == nil {
		t.Fatal("sign-in failed")
	}
	if w := hook(t, m.srv, "user.deleted", scimBody("dave", false, 2, "org-a")); w.Code != http.StatusOK || m.state(t, "dave") != "inactive/org-a" || m.sessions(t, "dave") != 0 {
		t.Fatalf("user.deleted: %d %s %q", w.Code, w.Body, m.state(t, "dave"))
	}
	if w := hook(t, m.srv, "user.updated", scimBody("dave", true, 1, "org-a")); w.Code != http.StatusOK || m.state(t, "dave") != "inactive/org-a" {
		t.Fatalf("an older update reactivated: %d %q", w.Code, m.state(t, "dave"))
	}
	if w := hook(t, m.srv, "user.updated", scimBody("dave", true, 3, "org-a")); w.Code != http.StatusOK || m.state(t, "dave") != "active/org-a" {
		t.Fatalf("a newer update did not reactivate: %d %q", w.Code, m.state(t, "dave"))
	}
}

// user.erased is final: the account is disabled, its sessions end, and neither a newer
// update nor a sign-in brings it back. An erased subject never seen is never created.
func TestUserErasedIsFinal(t *testing.T) {
	m, f := multiSSO(t)
	f.org = "org-a"
	if c := sessionCookie(ssoLogin(t, m.srv, f, "dave", "sid-1")); c == nil {
		t.Fatal("sign-in failed")
	}
	erasures := func(sub string) int { return m.count(t, `SELECT COUNT(*) FROM drive_erasures WHERE subject=?`, sub) }
	// Another org cannot erase the account.
	if w := hook(t, m.srv, "user.erased", scimBody("dave", false, 2, "org-b")); w.Code != http.StatusBadRequest || m.state(t, "dave") != "active/org-a" || erasures("dave") != 0 {
		t.Fatalf("erase from org-b: %d %q", w.Code, m.state(t, "dave"))
	}
	for range 2 { // a redelivery changes nothing
		if w := hook(t, m.srv, "user.erased", scimBody("dave", false, 2, "org-a")); w.Code != http.StatusOK || m.state(t, "dave") != "inactive/org-a" || m.sessions(t, "dave") != 0 || erasures("dave") != 1 {
			t.Fatalf("user.erased: %d %s %q", w.Code, w.Body, m.state(t, "dave"))
		}
	}
	if w := hook(t, m.srv, "user.updated", scimBody("dave", true, 9, "org-a")); w.Code != http.StatusOK || m.state(t, "dave") != "inactive/org-a" {
		t.Fatalf("an update reactivated an erased account: %d %q", w.Code, m.state(t, "dave"))
	}
	if w := ssoLogin(t, m.srv, f, "dave", "sid-2"); w.Code != http.StatusForbidden || sessionCookie(w) != nil {
		t.Fatalf("erased account signed in: %d %s", w.Code, w.Body)
	}
	if w := hook(t, m.srv, "user.erased", scimBody("zed", false, 1, "org-a")); w.Code != http.StatusOK || erasures("zed") != 1 {
		t.Fatalf("erase of an unknown subject: %d %s", w.Code, w.Body)
	}
	for _, try := range []func() int{
		func() int { return ssoLogin(t, m.srv, f, "zed", "sid-3").Code },
		func() int { return hook(t, m.srv, "user.created", scimBody("zed", true, 2, "org-a")).Code },
	} {
		if code := try(); m.state(t, "zed") != "" {
			t.Fatalf("an erased subject was provisioned (%d): %q", code, m.state(t, "zed"))
		}
	}
}

// The poll reads KyIdentity's erased list before the orgs, applies each entry, and resumes
// after the last revision. An entry naming an account in another org is not applied, and a
// failed read stops the poll before the orgs.
func TestErasedListIsApplied(t *testing.T) {
	m, f := multiSSO(t)
	if _, err := m.db.Exec(`INSERT INTO drive_orgs(id,status,grant_ceiling,grant_expires) VALUES(?,'active',1099511627776,99999999999)`, erasedOrg); err != nil {
		t.Fatal(err)
	}
	for _, u := range [][2]string{{"fay", erasedOrg}, {"gus", "org-a"}} {
		if w := hook(t, m.srv, "user.created", scimBody(u[0], true, 1, u[1])); w.Code != http.StatusOK {
			t.Fatalf("create %s: %d %s", u[0], w.Code, w.Body)
		}
	}
	entry := func(sub string, rev int) string {
		return fmt.Sprintf(`{"sub":%q,"org_id":%q,"revision":%d}`, sub, erasedOrg, rev)
	}
	f.erased = `{"subjects":[` + entry("fay", 5) + `,` + entry("gus", 6) + `,` + entry("hal", 7) + `]}`
	ctx := context.Background()
	if err := api.PollOrgsForTest(m.srv, ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t, "fay") != "inactive/"+erasedOrg || m.state(t, "gus") != "active/org-a" {
		t.Fatalf("after the poll: fay %q, gus %q", m.state(t, "fay"), m.state(t, "gus"))
	}
	if n := m.count(t, `SELECT COUNT(*) FROM drive_erasures WHERE subject IN ('fay','hal')`); n != 2 || m.count(t, `SELECT COUNT(*) FROM drive_erasures`) != 2 {
		t.Fatalf("erasures recorded: %d", n)
	}
	if v, err := m.st.Settings().GetSetting(ctx, store.ErasedSince); err != nil || v != "7" {
		t.Fatalf("erased_since %q, %v", v, err)
	}
	f.erased = `{"subjects":[]}`
	if err := api.PollOrgsForTest(m.srv, ctx); err != nil || f.asked[len(f.asked)-1] != "7" {
		t.Fatalf("second read asked since %v: %v", f.asked, err)
	}
	f.down = true
	polls := f.polls
	if err := api.PollOrgsForTest(m.srv, ctx); err == nil || f.polls != polls {
		t.Fatalf("a failed erased read still polled the orgs: %v, %d polls", err, f.polls-polls)
	}
}
