package api_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
)

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The NAS package defaults to the image the release workflow publishes for this version.
func TestComposeDefaultsToThisRelease(t *testing.T) {
	want := "ghcr.io/busnes-app/kydrive-server:" + api.AppVersion
	for file, line := range map[string]string{
		"docker-compose.yml": "image: ${KY_IMAGE:-" + want + "}",
		".env.example":       "KY_IMAGE=" + want,
	} {
		if !strings.Contains(readRepoFile(t, file), line) {
			t.Errorf("%s must contain %q", file, line)
		}
	}
}

// A published X.Y.Z tag is never overwritten: the release checks for it before pushing, and
// master CI writes only commit tags.
func TestReleaseTagsAreNeverOverwritten(t *testing.T) {
	release := readRepoFile(t, ".github/workflows/release-image.yml")
	guard := strings.Index(release, "name: Refuse to republish an existing version tag")
	push := strings.Index(release, "uses: docker/build-push-action@")
	if guard < 0 || push < 0 || guard > push {
		t.Fatal("release-image.yml must refuse an existing version tag before build-push-action")
	}
	if !strings.Contains(release[guard:push], `docker buildx imagetools inspect "$IMAGE_NAME:$VERSION"`) {
		t.Error("the guard must inspect the version tag it is about to push")
	}

	writes := 0
	for _, line := range strings.Split(readRepoFile(t, ".github/workflows/ci.yml"), "\n") {
		l := strings.TrimSpace(line)
		for _, verb := range []string{"docker push ", "docker tag ", "imagetools create", "tags:"} {
			if strings.Contains(l, verb) {
				writes++
				if !strings.HasSuffix(l, `:$GITHUB_SHA"`) {
					t.Errorf("ci.yml may write only commit tags: %s", l)
				}
			}
		}
	}
	if writes == 0 {
		t.Error("ci.yml no longer pushes; update this test")
	}
}
