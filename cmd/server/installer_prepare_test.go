package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/applysetup"
)

func TestInstallerPreparation(t *testing.T) {
	data := t.TempDir()
	id := "271333e2-c221-4601-b249-563b3d828530"
	file := filepath.Join(data, "installer", id, "setup.json")
	b := applysetup.Common{Schema: applysetup.Schema, Service: "kydrive", SetupID: id, SSO: &applysetup.SSO{IssuerURL: "https://identity.example.test", ClientID: "drive", RedirectURI: "https://drive.example.test/api/sso/kysignon/callback", ClientSecretFile: filepath.Join(data, "installer", id, "client.secret"), SCIMTokenFile: filepath.Join(data, "installer", id, "scim.secret")}, Admins: []applysetup.Identity{{Issuer: "https://identity.example.test", Subject: "owner-subject", Username: "owner-admin"}}, Everyday: []applysetup.Identity{{Issuer: "https://identity.example.test", Subject: "day-subject", Username: "owner"}}}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	run := func(raw []byte, check bool) ([]byte, error) {
		var out bytes.Buffer
		err := prepareInstaller(data, file, bytes.NewReader(raw), &out, check)
		return out.Bytes(), err
	}
	if out, err := run(raw, true); err == nil || len(out) != 0 {
		t.Fatal("missing metadata inspected as ready")
	}
	for i, check := range []bool{false, true, false} {
		out, err := run(raw, check)
		if err != nil {
			t.Fatal(err)
		}
		var receipt struct{ Schema, Service, SetupID, Status string }
		if json.Unmarshal(out, &receipt) != nil || receipt.Schema != "ky.setup-preparation/1" || receipt.Service != "kydrive" || receipt.SetupID != id {
			t.Fatal("invalid preparation receipt")
		}
		want := "present"
		if i == 0 {
			want = "created"
		}
		if receipt.Status != want {
			t.Fatal("invalid retained status")
		}
	}
	for _, name := range []string{"client.secret", "scim.secret", "setup.sock"} {
		if _, err := os.Stat(filepath.Join(data, "installer", id, name)); !os.IsNotExist(err) {
			t.Fatal("metadata preparation invented a credential or socket")
		}
	}
	changed := b
	changed.Admins = []applysetup.Identity{{Issuer: "https://identity.example.test", Subject: "changed-subject", Username: "owner-admin"}}
	other, _ := json.Marshal(changed)
	for _, check := range []bool{false, true} {
		if out, err := run(other, check); err == nil || len(out) != 0 {
			t.Fatal("changed owner intent accepted")
		}
	}
	retained, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(retained, raw) {
		t.Fatal("changed metadata replaced retained bundle")
	}
	for _, bad := range [][]byte{[]byte(`{"schema":"ky.product-setup/1","clientSecret":"do-not-echo"}`), bytes.Repeat([]byte{'x'}, applysetup.MaxBundleBytes+1)} {
		if out, err := run(bad, false); err == nil || len(out) != 0 || strings.Contains(err.Error(), "do-not-echo") {
			t.Fatal("unsafe metadata accepted or echoed")
		}
	}
	// Unsupported recovery intent must be refused before anything is staged.
	fresh := "5f0c1d7e-8a4b-4c2d-9e3f-0a1b2c3d4e5f"
	recovery := b
	recovery.SetupID = fresh
	recovery.SSO = nil
	recovery.Admins, recovery.Everyday = nil, nil
	recovery.Backup = &applysetup.Backup{Recovery: &applysetup.Recovery{URL: "https://recovery.example.test", PairingCodeFile: filepath.Join(data, "installer", fresh, "pairing.code")}}
	recoveryRaw, _ := json.Marshal(recovery)
	var out bytes.Buffer
	if err := prepareInstaller(data, filepath.Join(data, "installer", fresh, "setup.json"), bytes.NewReader(recoveryRaw), &out, false); err == nil || out.Len() != 0 {
		t.Fatal("unsupported recovery intent staged")
	}
	if _, err := os.Stat(filepath.Join(data, "installer", fresh)); !os.IsNotExist(err) {
		t.Fatal("refused recovery intent mutated the data directory")
	}
	b.SSO.DirectorySecretFile = filepath.Join(data, "installer", id, "directory.secret")
	wrong, _ := json.Marshal(b)
	if out, err := run(wrong, false); err == nil || len(out) != 0 {
		t.Fatal("webhook substituted for SCIM")
	}
}

func TestInstallerCapabilitiesAdvertiseOfflineRestoreReset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KY_DATA_DIR", dir)
	output, err := os.CreateTemp(t.TempDir(), "capabilities")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous; _ = output.Close() }()
	err = runInstaller([]string{"--capabilities"})
	os.Stdout = previous
	if err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	var capabilities applysetup.Capabilities
	if err := json.Unmarshal(raw, &capabilities); err != nil || capabilities.Service != "kydrive" || !slices.Contains(capabilities.Features, "restore-reset") {
		t.Fatalf("missing offline restore reset capability: %+v, %v", capabilities, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatal("capability discovery initialized a deployment", entries, err)
	}
}
