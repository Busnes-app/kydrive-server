package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestOpenInNewTabPreference(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	u := &store.User{ID: "usr_pref", Username: "pref", Role: "user", Status: "active", SSOProvider: "local"}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if v, err := st.Users().OpenInNewTab(ctx, u.ID); err != nil || !v {
		t.Fatalf("default: %v %v", v, err)
	}
	if err := st.Users().SetOpenInNewTab(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	// An unrelated profile update must not reset the preference.
	u.DisplayName = "Pref"
	if err := st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if v, err := st.Users().OpenInNewTab(ctx, u.ID); err != nil || v {
		t.Fatalf("after set false: %v %v", v, err)
	}
	if err := st.Users().SetOpenInNewTab(ctx, "usr_missing", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
}
