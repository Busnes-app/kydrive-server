package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCellsAndObjectConfiguration(t *testing.T) {
	for _, raw := range []string{"", ",", "e2e-1,", "e2e-1,e2e-1", "../other", "US-east", "x", "us_east", strings.Repeat("e2e-1,", 33)} {
		if _, err := ParseCells(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	cells, err := ParseCells(" e2e-2 , e2e-1 ")
	if err != nil || strings.Join(cells, ",") != "e2e-1,e2e-2" {
		t.Fatal(cells, err)
	}
	t.Setenv("KY_CELLS", "e2e-1")
	if _, _, err := hostedStorage(false); err == nil {
		t.Fatal("single mode accepted cells")
	}
	if !multiTenantBuild {
		return
	}
	t.Setenv("KY_DB_DRIVER", "postgres")
	t.Setenv("KYDRIVE_S3_ENDPOINT", "https://objects.e2e.test")
	t.Setenv("KYDRIVE_S3_BUCKET", "drive-test")
	t.Setenv("KYDRIVE_S3_ACCESS_KEY", "test-key")
	t.Setenv("KYDRIVE_S3_SECRET_KEY", "")
	file := filepath.Join(t.TempDir(), "s3-secret")
	if err := os.WriteFile(file, []byte("NeverEchoThisSecret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYDRIVE_S3_SECRET_KEY_FILE", file)
	_, o, err := hostedStorage(true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(o)
	if strings.Contains(string(raw), "NeverEchoThisSecret") {
		t.Fatal("serialized secret")
	}
	for _, endpoint := range []string{"https://user:NeverEchoThisSecret@objects.e2e.test", "https://objects.e2e.test/path", "https://objects.e2e.test#fragment", "http://objects.e2e.test"} {
		t.Setenv("KYDRIVE_S3_ENDPOINT", endpoint)
		if _, _, err := hostedStorage(true); err == nil || strings.Contains(err.Error(), "NeverEchoThisSecret") {
			t.Fatal("endpoint refusal", err)
		}
	}
}
