package drive_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/vault"
	"github.com/google/uuid"
)

type checkpointReadProbe struct {
	io.Reader
	check func()
}

func (p *checkpointReadProbe) Read(data []byte) (int, error) {
	if p.check != nil {
		p.check()
		p.check = nil
	}
	return p.Reader.Read(data)
}

func TestVaultCheckpointQuota(t *testing.T) {
	o := poolOrgs(t)
	doc := drive.VaultRelayGrant{
		Org: "org-a", Vault: uuid.NewString(), File: uuid.NewString(), User: "a1",
		Device: uuid.NewString(), Binding: "fixture-binding", Expires: time.Now().Add(time.Minute).UnixMilli(), Edit: true,
	}
	blob := strings.Repeat("a1", 24)
	other := strings.Repeat("b2", 24)
	// Install the same catalogue rows a witnessed membership, signed editor binding and
	// linked device produce. This test exercises the quota transaction across both SQL engines.
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO drive_devices(id,user_id,name,platform,hash,created,last_used,org_id)
VALUES(?,?,'fixture','vault-browser','fixture-device-hash','',unixepoch(),?)`, []any{doc.Device, doc.User, doc.Org}},
		{`INSERT INTO drive_vaults(org_id,id,log_id,sequence,hash) VALUES(?,?,'fixture-log',1,'fixture-head')`, []any{doc.Org, doc.Vault}},
		{`INSERT INTO drive_vault_bindings(org_id,vault_id,file_id,head_hash,binding_id,binding)
VALUES(?,?,?,'fixture-head',?,'{}')`, []any{doc.Org, doc.Vault, doc.File, doc.Binding}},
		{`INSERT INTO drive_vault_editor_blobs(blob,binding_id) VALUES(?,?),(?,?)`, []any{blob, doc.Binding, other, doc.Binding}},
	} {
		if _, err := o.db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}

	root := objects.Dir(t.TempDir())
	payload := "blob"
	size := int64(len(payload))
	var used int64
	if err := o.db.QueryRow(`SELECT SUM(v.size) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id
JOIN drive_workspaces w ON w.id=f.workspace WHERE w.org_id=?`, doc.Org).Scan(&used); err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("quota denial must precede the first read")
	check := func(id string, body io.Reader, want error) {
		t.Helper()
		err := o.d.WriteVaultCheckpoint(o.ctx, root, doc, id, size, body, time.Now)
		if !errors.Is(err, want) {
			t.Fatalf("checkpoint write %s: got %v, want %v", id[:2], err, want)
		}
	}
	missing := func(id string) {
		t.Helper()
		if src, err := o.d.ReadVaultCheckpoint(o.ctx, root, doc, id, time.Now); !errors.Is(err, vault.ErrMissing) {
			if src != nil {
				src.Close()
			}
			t.Fatalf("failed checkpoint became readable: %v", err)
		}
	}
	grant(t, o.db, doc.Org, used)
	check(blob, iotest.ErrReader(rejected), drive.ErrQuota)
	missing(blob)
	if err := root.List(o.ctx, func(string, time.Time) error { return rejected }); err != nil {
		t.Fatalf("quota denial left data in the blob store: %v", err)
	}

	grant(t, o.db, doc.Org, used+size)
	check(blob, iotest.ErrReader(io.ErrUnexpectedEOF), io.ErrUnexpectedEOF)
	missing(blob)

	check(blob, &checkpointReadProbe{Reader: strings.NewReader(payload), check: func() {
		missing(blob)
		check(other, iotest.ErrReader(rejected), drive.ErrQuota)
	}}, nil)
	check(blob, iotest.ErrReader(rejected), vault.ErrConflict)
	check(other, iotest.ErrReader(rejected), drive.ErrQuota)
	missing(other)
	src, err := o.d.ReadVaultCheckpoint(o.ctx, root, doc, blob, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	raw, err := io.ReadAll(src)
	if err != nil || string(raw) != payload {
		t.Fatalf("checkpoint read: %q, %v", raw, err)
	}
}
