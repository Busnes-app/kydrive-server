package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (w *kypostWorld) guestPolicy(on bool, days string) {
	w.t.Helper()
	v := map[bool]string{true: "on", false: "off"}[on]
	for k, val := range map[string]string{"drive_guest_links": v, "drive_guest_max_days": days} {
		if err := w.st.Settings().SetSetting(context.Background(), k, val); err != nil {
			w.t.Fatal(err)
		}
	}
}

func (w *kypostWorld) guestSwitch(user string, on bool) {
	w.t.Helper()
	if err := w.st.Drive().SetPeerGuestSetting(context.Background(), user, on); err != nil {
		w.t.Fatal(err)
	}
}

func (w *kypostWorld) status(sub string) map[string]any {
	w.t.Helper()
	rec := w.signed("/api/suite/kypost/status", "kypost.drive.status", "", map[string]any{"sub": sub, "issuer": kypostIssuer}, kypostSecret)
	var out map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		w.t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	return out
}

// Contract K: a guest link through KyPost needs the policy, the user's guest switch and a day
// count within the policy; turning either off refuses new ones.
func TestKyPostGuestLinks(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	var saved struct {
		FileID string `json:"file_id"`
	}
	_ = json.Unmarshal(w.put(w.uploadURL("sub-alice", "a.txt", 1), "", "", "a").Body.Bytes(), &saved)
	link := func(sub, file string, days any, audience string) *httptest.ResponseRecorder {
		body := map[string]any{"sub": sub, "issuer": kypostIssuer, "file_id": file, "audience": audience}
		if days != nil {
			body["days"] = days
		}
		return w.signed("/api/suite/kypost/links", "kypost.drive.link", "", body, kypostSecret)
	}
	guestFields := func(want bool, days float64) {
		t.Helper()
		if got := w.status("sub-alice"); got["guest_links"] != want || got["guest_max_days"] != days {
			t.Errorf("status: %v, want guest_links %v guest_max_days %v", got, want, days)
		}
	}

	// Both off by default.
	guestFields(false, 0)
	code(t, link("sub-alice", saved.FileID, 7, "guest"), 409, "guest_links_off")
	// Policy on, the user's switch still off.
	w.guestPolicy(true, "14")
	guestFields(false, 0)
	code(t, link("sub-alice", saved.FileID, 7, "guest"), 409, "guest_links_off")
	// The switch on, the policy off.
	w.guestPolicy(false, "14")
	w.guestSwitch("alice", true)
	guestFields(false, 0)
	code(t, link("sub-alice", saved.FileID, 7, "guest"), 409, "guest_links_off")

	w.guestPolicy(true, "14")
	guestFields(true, 14)
	rec := link("sub-alice", saved.FileID, 7, "guest")
	var out struct{ URL, Expires string }
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !strings.HasPrefix(out.URL, "https://drive.example/g/#") {
		t.Fatalf("guest link: %d %s", rec.Code, rec.Body.String())
	}
	if exp, err := time.Parse(time.RFC3339, out.Expires); err != nil || exp.Sub(time.Now().Add(7*24*time.Hour)).Abs() > time.Minute {
		t.Errorf("expires %q", out.Expires)
	}
	// The rights of one made in KyDrive: view and download, no upload, no password.
	token := strings.TrimPrefix(out.URL, "https://drive.example/g/#")
	if rec := send(t, w.srv, http.MethodPost, "/api/public/links/view", `{"token":"`+token+`"}`, nil, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"upload_only":false`) {
		t.Errorf("guest view: %d %s", rec.Code, rec.Body.String())
	}
	if rec := guestUploadReq(w.srv, token, "x.txt", "x"); rec.Code == 201 {
		t.Error("a KyPost guest link took an upload")
	}
	// Listed with the file's guest links, so the owner can revoke it; audited as made through KyPost.
	links, err := w.st.Drive().GuestLinks(context.Background(), "alice", "file", saved.FileID)
	if err != nil || len(links) != 1 || links[0].UploadOnly || links[0].HasPassword {
		t.Fatalf("listed: %v %v", links, err)
	}
	records, _, err := w.st.Audit().ListAuditRecords(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var audited bool
	for _, r := range records {
		audited = audited || (r.Action == "guest_link.created" && r.Resource == "file:"+saved.FileID && r.Details == "link="+links[0].ID+" peer=kypost")
	}
	if !audited {
		t.Error("guest link not audited as made through KyPost")
	}
	if strings.Contains(w.logs.String(), token) {
		t.Error("the guest token reached the log")
	}

	// Days: 1 to max_days, never 0; absent means 30, over this policy.
	for _, days := range []any{0, -1, 15, nil, "7", 1.5} {
		code(t, link("sub-alice", saved.FileID, days, "guest"), 400, "bad_body")
	}
	if rec := link("sub-alice", saved.FileID, 1, "guest"); rec.Code != 201 {
		t.Errorf("one day: %d %s", rec.Code, rec.Body.String())
	}
	code(t, link("sub-alice", saved.FileID, 7, "public"), 400, "bad_body")
	code(t, link("sub-bob", saved.FileID, 7, "guest"), 409, "guest_links_off") // bob's switch is off
	w.guestSwitch("bob", true)
	code(t, link("sub-bob", saved.FileID, 7, "guest"), 404, "not_found") // not bob's
	code(t, link("sub-alice", "no-such-file", 7, "guest"), 404, "not_found")
	// organisation (or absent) is still an organisation link.
	if rec := link("sub-alice", saved.FileID, 7, "organisation"); rec.Code != 201 || !strings.Contains(rec.Body.String(), "/l/#") {
		t.Errorf("organisation: %d %s", rec.Code, rec.Body.String())
	}

	// Turning the policy or the switch off refuses new ones; the main switch off refuses all.
	w.guestPolicy(false, "14")
	code(t, link("sub-alice", saved.FileID, 7, "guest"), 409, "guest_links_off")
	w.guestPolicy(true, "14")
	w.guestSwitch("alice", false)
	code(t, link("sub-alice", saved.FileID, 7, "guest"), 409, "guest_links_off")
	w.guestSwitch("alice", true)
	w.setSwitch("alice", false)
	code(t, link("sub-alice", saved.FileID, 7, "guest"), 409, "sharing_off")
	if got := w.status("sub-alice"); got["guest_links"] != false {
		t.Errorf("status with sharing off: %v", got)
	}
}

func TestKyPostGuestSwitchSetting(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	carol := loginWithProvider(t, w.srv, w.st, "carol", "kysignon", "sub-carol")
	get := func() string {
		return strings.TrimSpace(do(t, w.srv, http.MethodGet, "/api/drive/kypost", carol).Body.String())
	}
	put := func(body string) *httptest.ResponseRecorder {
		return send(t, w.srv, http.MethodPut, "/api/drive/kypost", body, carol, "")
	}
	if got := get(); got != `{"enabled":true,"guest_links":false,"guest_policy":false,"paired":true}` {
		t.Errorf("default: %s", got)
	}
	// Cannot be turned on while the policy is off; turning it off always works.
	code(t, put(`{"guest_links":true}`), 409, "guest_links_off")
	if rec := put(`{"guest_links":false}`); rec.Code != 200 {
		t.Errorf("off: %d", rec.Code)
	}
	w.guestPolicy(true, "30")
	if rec := put(`{"guest_links":true}`); rec.Code != 200 || get() != `{"enabled":true,"guest_links":true,"guest_policy":true,"paired":true}` {
		t.Errorf("on: %d %s", rec.Code, get())
	}
	if rec := put(`{"guest_links":true,"enabled":true}`); rec.Code != 400 {
		t.Errorf("both at once: %d", rec.Code)
	}
	if rec := put(`{}`); rec.Code != 400 {
		t.Errorf("neither: %d", rec.Code)
	}
	if rec := send(t, w.srv, http.MethodPut, "/api/drive/kypost", `{"guest_links":false}`, nil, ""); rec.Code != 401 {
		t.Errorf("signed out: %d", rec.Code)
	}
}
