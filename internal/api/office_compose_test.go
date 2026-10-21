package api_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// officeStartCheck is the Euro-Office entrypoint script from docker-compose.office.yml, with
// Compose's $$ escapes undone, the JWT path replaced by key and the editor start by a marker.
func officeStartCheck(t *testing.T, key string) string {
	t.Helper()
	compose := readRepoFile(t, "docker-compose.office.yml")
	start := strings.Index(compose, "    command:\n      - |\n")
	end := strings.Index(compose, "    secrets:\n")
	if start < 0 || end < start {
		t.Fatal("docker-compose.office.yml has no euro-office command block")
	}
	var script strings.Builder
	for _, line := range strings.Split(compose[start:end], "\n")[2:] {
		script.WriteString(strings.TrimPrefix(line, "        ") + "\n")
	}
	s := strings.ReplaceAll(script.String(), "$$", "$")
	s = strings.Replace(s, "key=/var/www/euro-office/Data/.private/jwt_secret", "key='"+key+"'", 1)
	return strings.Replace(s, "exec /entrypoint.sh", "echo started", 1)
}

// Each refusal names its real cause. A file the container cannot even stat (SELinux denying a
// mislabelled bind mount; here, a directory without search permission) is unreadable, not
// "not regular", which sent operators looking for a symlink.
func TestOfficeStartCheckNamesTheCause(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their permissions")
	}
	dir := t.TempDir()
	write := func(name, value string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	secret := strings.Repeat("k", 40)
	good := write("good", secret, 0o600)
	hidden := filepath.Join(dir, "denied", "jwt")
	if err := os.Mkdir(filepath.Dir(hidden), 0o700); err != nil {
		t.Fatal(err)
	}
	write("denied/jwt", secret, 0o600)
	if err := os.Chmod(filepath.Dir(hidden), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Dir(hidden), 0o700) })
	link := filepath.Join(dir, "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		good:                           "started",
		hidden:                         "cannot read its JWT file",
		filepath.Join(dir, "missing"):  "cannot read its JWT file",
		link:                           "must not be a symlink",
		dir:                            "must be a regular file",
		write("shared", secret, 0o644): "must have mode 0600",
		write("short", "short", 0o600): "at least 32 bytes",
	} {
		out, _ := exec.Command("/bin/sh", "-ec", officeStartCheck(t, key)).CombinedOutput()
		if !strings.Contains(string(out), want) {
			t.Errorf("%s: got %q, want %q", filepath.Base(key), out, want)
		}
	}
}
