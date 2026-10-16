package backup

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repository is a bulk Restic repository and the credentials restic needs to reach it. Only
// URL is ever printed; Env reaches the restic child alone.
type Repository struct {
	URL string
	Env []string // KEY=VALUE, keys from credentialKeys
}

func (r Repository) String() string { return r.URL }

// credentialKeys are the variables a credentials file may set: S3 and rest-server credentials,
// nothing that changes what restic runs or where it writes.
var credentialKeys = map[string]bool{
	"AWS_ACCESS_KEY_ID": true, "AWS_SECRET_ACCESS_KEY": true, "AWS_SESSION_TOKEN": true, "AWS_DEFAULT_REGION": true,
	"RESTIC_REST_USERNAME": true, "RESTIC_REST_PASSWORD": true,
}

// bulkRepository is the configured repository: KYDRIVE_BULK_BACKUP_REPOSITORY, with credentials
// from KYDRIVE_BULK_BACKUP_CREDENTIALS_FILE, read on each use so they can rotate.
func bulkRepository(dataDir string) (Repository, error) {
	raw := os.Getenv("KYDRIVE_BULK_BACKUP_REPOSITORY")
	if raw == "" {
		return Repository{}, errors.New("KYDRIVE_BULK_BACKUP_REPOSITORY is required for document backups")
	}
	env := os.Getenv("KY_ENV") // config defaults an unset KY_ENV to development
	loc, err := parseLocation(raw, dataDir, env == "" || env == "development")
	if err != nil {
		return Repository{}, err
	}
	repo := Repository{URL: loc}
	if path := os.Getenv("KYDRIVE_BULK_BACKUP_CREDENTIALS_FILE"); path != "" {
		if repo.Env, err = readCredentials(path); err != nil {
			return Repository{}, err
		}
	}
	return repo, nil
}

// parseLocation accepts an absolute local path outside dataDir, so a lost data volume does not
// take its backups with it, or an s3: or rest: repository over HTTPS with no credentials in the
// URL. Plain HTTP is for development's local test servers only.
func parseLocation(raw, dataDir string, development bool) (string, error) {
	for _, backend := range []string{"s3:", "rest:"} {
		if !strings.HasPrefix(raw, backend) {
			continue
		}
		u, err := url.Parse(strings.TrimPrefix(raw, backend))
		if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", fmt.Errorf("bulk repository must be %shttps://host/path, with credentials in KYDRIVE_BULK_BACKUP_CREDENTIALS_FILE", backend)
		}
		if u.Scheme != "https" && !(development && u.Scheme == "http") {
			return "", fmt.Errorf("bulk repository must use %shttps://", backend)
		}
		return raw, nil
	}
	if !filepath.IsAbs(raw) {
		return "", errors.New("bulk repository must be an absolute path, s3:https://… or rest:https://…")
	}
	repo := filepath.Clean(raw)
	if dataDir == "" { // restoring on a fresh host: there is no data directory to stay outside yet
		return repo, nil
	}
	data, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(data, repo)
	if err != nil || rel == "." || filepath.IsLocal(rel) {
		return "", errors.New("bulk repository must be outside primary data directory")
	}
	return repo, nil
}

// readCredentials reads KEY=VALUE lines (blank lines and # comments allowed) whose keys are all
// in credentialKeys, from a file other users cannot read or write.
func readCredentials(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o007 != 0 {
		return nil, errors.New("bulk credentials file must not be accessible to other users (chmod 600)")
	}
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return nil, err
	}
	return parseCredentials(string(data))
}

// parseCredentials checks credentials text from a file or a capsule.
func parseCredentials(data string) ([]string, error) {
	var env []string
	seen := map[string]bool{}
	for n, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch {
		case !ok || strings.ContainsAny(line, "\r\x00"):
			return nil, fmt.Errorf("bulk credentials file line %d is not KEY=VALUE", n+1)
		case !credentialKeys[key]:
			return nil, fmt.Errorf("bulk credentials file line %d: %s is not an S3 or rest-server credential", n+1, key)
		case seen[key]:
			return nil, fmt.Errorf("bulk credentials file sets %s twice", key)
		}
		seen[key] = true
		env = append(env, key+"="+value)
	}
	return env, nil
}

// Capsule fields: the repository and its credentials travel sealed in config/integrations.json,
// so a fresh host restores from the capsule alone.
const (
	capsuleRepositoryKey  = "KYDRIVE_BULK_BACKUP_REPOSITORY"
	capsuleCredentialsKey = "KYDRIVE_BULK_BACKUP_CREDENTIALS"
)

// CapsuleRepository is the repository an opened capsule names, its credentials checked as a
// file's are. ok is false for a capsule sealed before capsules carried it.
func CapsuleRepository(openedDir string) (repo Repository, ok bool, err error) {
	raw, err := os.ReadFile(filepath.Join(openedDir, "config/integrations.json"))
	if err != nil {
		return repo, false, err
	}
	var integrations map[string]string
	if err = json.Unmarshal(raw, &integrations); err != nil {
		return repo, false, err
	}
	if integrations[capsuleRepositoryKey] == "" {
		return repo, false, nil
	}
	env := os.Getenv("KY_ENV")
	if repo.URL, err = parseLocation(integrations[capsuleRepositoryKey], "", env == "" || env == "development"); err != nil {
		return repo, false, err
	}
	repo.Env, err = parseCredentials(integrations[capsuleCredentialsKey])
	return repo, err == nil, err
}

// resticCommand runs restic on repo with only the repository's credentials and password: the
// server's own RESTIC_ and AWS_ variables never reach it.
func resticCommand(ctx context.Context, repo Repository, password []byte, dir string, stdin io.Reader, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "restic", append([]string{"--repo", repo.URL, "--no-cache"}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "RESTIC_") && !strings.HasPrefix(e, "AWS_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(append(cmd.Env, repo.Env...), "RESTIC_PASSWORD="+hex.EncodeToString(password))
	return cmd
}

// secretKeys are the credentials redact hides; a username or region is not secret, and hiding
// a short common word would garble the message.
var secretKeys = map[string]bool{"AWS_ACCESS_KEY_ID": true, "AWS_SECRET_ACCESS_KEY": true, "AWS_SESSION_TOKEN": true, "RESTIC_REST_PASSWORD": true}

// redact hides repo's secret values in text restic wrote, before it reaches an error or a log.
func redact(text []byte, repo Repository) []byte {
	for _, e := range repo.Env {
		key, value, _ := strings.Cut(e, "=")
		if secretKeys[key] && value != "" {
			text = bytes.ReplaceAll(text, []byte(value), []byte("[redacted]"))
		}
	}
	return text
}
