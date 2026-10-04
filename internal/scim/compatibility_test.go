package scim_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestKyIdentityExternalLookupReplacementAndReplay(t *testing.T) {
	srv, mux, token := setupSCIMServer(t)
	handler := srv.AuthMiddleware(mux)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/scim+json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	create := call("POST", "/scim/v2/Users", `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"externalId":"kyidentity-user-1","userName":"alice","active":true,"roles":[{"value":"admin"}]}`)
	if create.Code != 201 {
		t.Fatal(create.Code, create.Body.String())
	}
	var user struct {
		ID string `json:"id"`
	}
	json.Unmarshal(create.Body.Bytes(), &user)
	if strings.Contains(create.Body.String(), `"value":"admin"`) {
		t.Fatal("directory elevated application admin")
	}
	lookup := call("GET", "/scim/v2/Users?"+url.Values{"filter": {`externalId eq "kyidentity-user-1"`}, "startIndex": {"1"}, "count": {"2"}}.Encode(), "")
	if lookup.Code != 200 || !strings.Contains(lookup.Body.String(), `"totalResults":1`) {
		t.Fatal(lookup.Code, lookup.Body.String())
	}
	missing := call("GET", "/scim/v2/Users?"+url.Values{"filter": {`externalId eq "kyidentity-user"`}}.Encode(), "")
	if !strings.Contains(missing.Body.String(), `"totalResults":0`) {
		t.Fatal("substring externalId matched", missing.Body.String())
	}
	group := call("POST", "/scim/v2/Groups", `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"externalId":"kyidentity-group-1","displayName":"Team","members":[{"value":"`+user.ID+`"}]}`)
	if group.Code != 201 {
		t.Fatal(group.Code, group.Body.String())
	}
	var g struct {
		ID string `json:"id"`
	}
	json.Unmarshal(group.Body.Bytes(), &g)
	body := `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"externalId":"kyidentity-group-1","displayName":"Renamed","members":[]}`
	for range 2 {
		replace := call("PUT", "/scim/v2/Groups/"+g.ID, body)
		if replace.Code != 200 {
			t.Fatal(replace.Code, replace.Body.String())
		}
	}
	lookup = call("GET", "/scim/v2/Groups?"+url.Values{"filter": {`externalId eq "kyidentity-group-1"`}, "startIndex": {"1"}, "count": {"2"}}.Encode(), "")
	if lookup.Code != 200 || !strings.Contains(lookup.Body.String(), "Renamed") || strings.Contains(lookup.Body.String(), user.ID) {
		t.Fatal(lookup.Code, lookup.Body.String())
	}
	removed := call(http.MethodDelete, "/scim/v2/Groups/"+g.ID, "")
	if removed.Code != 204 {
		t.Fatal(removed.Code)
	}
}
