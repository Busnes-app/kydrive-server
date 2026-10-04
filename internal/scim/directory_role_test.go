package scim_test

import (
	"bytes"
	"context"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/scim"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSCIMPreservesExplicitApplicationAdminAndOffboards(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u := &store.User{ID: "directory", Username: "yoshi", SSOProvider: "scim", SSOSubject: "identity-subject", Role: "user", Status: "active"}
	if err = st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err = st.Users().SetDirectoryRole(ctx, u.SSOSubject, "admin"); err != nil {
		t.Fatal(err)
	}
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: "test-token"}, "https://drive.example")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	h := srv.AuthMiddleware(mux)
	for _, body := range []string{
		`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"externalId":"identity-subject","userName":"yoshi","active":true,"roles":[{"value":"user"}]}`,
		`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"externalId":"identity-subject","userName":"yoshi","active":false}`,
	} {
		r := httptest.NewRequest("PUT", "/scim/v2/Users/directory", bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer test-token")
		r.Header.Set("Content-Type", "application/scim+json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		got, err := st.Users().GetUserByID(ctx, u.ID)
		if err != nil || got.Role != "admin" {
			t.Fatal("directory overwrote app role", got, err)
		}
	}
	got, err := st.Users().GetUserByID(ctx, u.ID)
	if err != nil || got.Status != "inactive" {
		t.Fatal("administrator escaped offboarding", got, err)
	}
}
