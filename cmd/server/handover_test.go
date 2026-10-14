package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

func TestHandoverCommand(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := st.Drive()
	if d == nil {
		t.Skip("drive is SQLite-only")
	}
	for _, u := range []*store.User{
		{ID: "leaver", Username: "leaver", DisplayName: "Lee Ver", Email: "l@local.test", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "sub-leaver"},
		{ID: "heir", Username: "heir", Email: "h@local.test", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "sub-heir"},
	} {
		if err = st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	src, err := d.EnsurePersonalWorkspace(ctx, "leaver")
	if err != nil {
		t.Fatal(err)
	}
	v, err := drive.WriteBlob(t.TempDir(), strings.NewReader("private"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Publish(ctx, "leaver", drive.File{Workspace: src, Name: "secret.txt"}, v, 0); err != nil {
		t.Fatal(err)
	}
	u, _ := st.Users().GetUserByID(ctx, "leaver")
	u.Status = "suspended"
	if err = st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err = handover(ctx, st, nil, &out); err != nil || !strings.Contains(out.String(), src+"\tsuspended\tLee Ver\t7 bytes\t1 items") {
		t.Fatalf("list: %q %v", out.String(), err)
	}
	for _, args := range [][]string{
		{"-from", src},
		{"-from", src, "-workspace", "w", "-to-user", "sub-heir"},
		{"-from", src, "-to-user", "sub-nobody"},
		{"-from", src, "-to-user", "sub-leaver"},
	} {
		if err = handover(ctx, st, args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	out.Reset()
	if err = handover(ctx, st, []string{"-from", src, "-to-user", "sub-heir"}, &out); err != nil || strings.Contains(out.String(), "secret") {
		t.Fatalf("handover: %q %v", out.String(), err)
	}
	mine, _ := d.EnsurePersonalWorkspace(ctx, "heir")
	if files, _ := d.Files(ctx, "heir", mine, false); len(files) != 1 || files[0].Name != "secret.txt" {
		t.Fatalf("heir files: %+v", files)
	}
	records, _, err := st.Audit().ListAuditRecords(ctx, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range records {
		found = found || (r.Action == "drive.handover" && r.IPAddress == "operator-cli" && r.UserID == drive.OperatorActor && !strings.Contains(r.Details, "secret"))
	}
	if !found {
		t.Fatalf("no operator audit record: %+v", records)
	}
}
