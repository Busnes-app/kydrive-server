package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBulkRepositoryLocations(t *testing.T) {
	data := t.TempDir()
	outside := t.TempDir()
	for raw, want := range map[string]string{
		outside:                             outside,
		"s3:https://s3.example.com/kydrive": "s3:https://s3.example.com/kydrive",
		"s3:https://minio.example:9000/bucket/pre": "s3:https://minio.example:9000/bucket/pre",
		"rest:https://backup.example.com:8000/kyd": "rest:https://backup.example.com:8000/kyd",
		"rest:https://backup.example.com/kydrive/": "rest:https://backup.example.com/kydrive/",
	} {
		if got, err := parseLocation(raw, data, false); err != nil || got != want {
			t.Errorf("%q: %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{
		"",
		filepath.Join(data, "bulk"),              // inside the data directory
		data,                                     // the data directory itself
		"s3:http://s3.example.com/kydrive",       // plain HTTP
		"rest:http://backup.example.com/kydrive", // plain HTTP
		"s3:s3.example.com/kydrive",              // restic's implicit scheme
		"rest:https://user:secret@backup.example.com/kyd", // credentials in the URL
		"rest:https://backup.example.com/kyd?x=1",         // query
		"rest:https://backup.example.com/kyd#frag",        // fragment
		"rest:https:///kyd",                               // no host
		"sftp:backup.example.com:/srv/kydrive",            // other backends
		"rclone:remote:kydrive",
		"b2:bucket:kydrive",
		"azure:container:/kydrive",
		"gs:bucket:/kydrive",
		"swift:container:/kydrive",
		"S3:https://s3.example.com/kydrive", // backend names are lower case
		"relative/path",
		"-o=foo",
	} {
		if got, err := parseLocation(raw, data, false); err == nil {
			t.Errorf("%q accepted as %q", raw, got)
		}
	}
	// Development allows plain HTTP for local test servers, and nothing else extra.
	for _, raw := range []string{"rest:http://127.0.0.1:8000/kyd", "s3:http://127.0.0.1:9000/bucket"} {
		if _, err := parseLocation(raw, data, true); err != nil {
			t.Errorf("development %q: %v", raw, err)
		}
	}
	if _, err := parseLocation("rest:http://user:pw@127.0.0.1:8000/kyd", data, true); err == nil {
		t.Error("development accepted credentials in the URL")
	}
}

func TestBulkCredentialsFile(t *testing.T) {
	write := func(body string, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "credentials")
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		os.Chmod(path, mode)
		return path
	}
	env, err := readCredentials(write("# offsite\nAWS_ACCESS_KEY_ID=AKIA1\n\nAWS_SECRET_ACCESS_KEY=s3cr=t\nRESTIC_REST_USERNAME = drive\n", 0o600))
	if err != nil || !slices.Equal(env, []string{"AWS_ACCESS_KEY_ID=AKIA1", "AWS_SECRET_ACCESS_KEY=s3cr=t", "RESTIC_REST_USERNAME=drive"}) {
		t.Fatalf("credentials %q %v", env, err)
	}
	for name, c := range map[string]struct {
		body string
		mode os.FileMode
	}{
		"unknown key":      {"LD_PRELOAD=/tmp/x.so\n", 0o600},
		"restic password":  {"RESTIC_PASSWORD=x\n", 0o600},
		"repository":       {"RESTIC_REPOSITORY=/elsewhere\n", 0o600},
		"no equals":        {"AWS_ACCESS_KEY_ID\n", 0o600},
		"world readable":   {"AWS_ACCESS_KEY_ID=x\n", 0o604},
		"world writable":   {"AWS_ACCESS_KEY_ID=x\n", 0o602},
		"repeated key":     {"AWS_ACCESS_KEY_ID=x\nAWS_ACCESS_KEY_ID=y\n", 0o600},
		"control in value": {"AWS_ACCESS_KEY_ID=x\ry\n", 0o600},
		"nul in value":     {"AWS_ACCESS_KEY_ID=x\x00y\n", 0o600},
	} {
		if env, err := readCredentials(write(c.body, c.mode)); err == nil {
			t.Errorf("%s accepted: %q", name, env)
		}
	}
	if _, err := readCredentials(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing file accepted")
	}
}

// Restic sees the file's credentials and none of the server's own RESTIC_ or AWS_ variables,
// and no message carries a credential.
func TestResticEnvironment(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "restic")
	os.WriteFile(script, []byte("#!/bin/sh\nenv | sort\necho \"leak: $AWS_SECRET_ACCESS_KEY\" >&2\nexit 3\n"), 0o700)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("AWS_ACCESS_KEY_ID", "server-wide")
	t.Setenv("RESTIC_REPOSITORY", "/elsewhere")
	repo := Repository{URL: "rest:https://backup.example/kyd", Env: []string{"AWS_SECRET_ACCESS_KEY=topsecret"}}
	_, err := execRestic(context.Background(), repo, []byte{1}, "", nil, "snapshots")
	if err == nil {
		t.Fatal("failing restic reported success")
	}
	if strings.Contains(err.Error(), "topsecret") || !strings.Contains(err.Error(), "leak: [redacted]") {
		t.Fatalf("restic stderr not redacted: %v", err)
	}
	cmd := resticCommand(context.Background(), repo, []byte{1}, "", nil, "snapshots")
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "AWS_SECRET_ACCESS_KEY=topsecret") || strings.Contains(env, "server-wide") || strings.Contains(env, "/elsewhere") {
		t.Fatalf("restic environment:\n%s", env)
	}
	if s := fmt.Sprintf("%v %+v %s", repo, repo, repo); strings.Contains(s, "topsecret") {
		t.Fatalf("repository printed its credentials: %s", s)
	}
}

// A capsule's repository and credentials restore on a host that has neither configured, and a
// capsule cannot smuggle in what a credentials file may not set.
func TestCapsuleRepository(t *testing.T) {
	capsule := func(fields map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "config"), 0o700)
		raw, _ := json.Marshal(fields)
		os.WriteFile(filepath.Join(dir, "config/integrations.json"), raw, 0o600)
		return dir
	}
	t.Setenv("KY_ENV", "production")
	repo, ok, err := CapsuleRepository(capsule(map[string]string{
		capsuleRepositoryKey:  "rest:https://backup.example/kyd",
		capsuleCredentialsKey: "RESTIC_REST_USERNAME=drive\nRESTIC_REST_PASSWORD=pw",
	}))
	if err != nil || !ok || repo.URL != "rest:https://backup.example/kyd" || !slices.Equal(repo.Env, []string{"RESTIC_REST_USERNAME=drive", "RESTIC_REST_PASSWORD=pw"}) {
		t.Fatalf("capsule repository %+v %v %v", repo.Env, ok, err)
	}
	if _, ok, err = CapsuleRepository(capsule(map[string]string{"KY_APP_NAME": "KyDrive"})); err != nil || ok {
		t.Fatalf("older capsule: %v %v", ok, err)
	}
	for name, fields := range map[string]map[string]string{
		"injected env": {capsuleRepositoryKey: "rest:https://backup.example/kyd", capsuleCredentialsKey: "LD_PRELOAD=/tmp/x.so"},
		"plain http":   {capsuleRepositoryKey: "rest:http://backup.example/kyd"},
		"other scheme": {capsuleRepositoryKey: "sftp:backup.example:/kyd"},
	} {
		if _, ok, err := CapsuleRepository(capsule(fields)); err == nil || ok {
			t.Errorf("%s accepted", name)
		}
	}
}
