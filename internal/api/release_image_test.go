package api_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
)

// The NAS package defaults to the image the release workflow publishes for this version.
func TestComposeDefaultsToThisRelease(t *testing.T) {
	want := "ghcr.io/busnes-app/kydrive-server:" + api.AppVersion
	for file, line := range map[string]string{
		"../../docker-compose.yml": "image: ${KY_IMAGE:-" + want + "}",
		"../../.env.example":       "KY_IMAGE=" + want,
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), line) {
			t.Errorf("%s must contain %q", file, line)
		}
	}
}
