package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// replicaKeyPaths checks D13's configured key locations before loading or creating any
// key. It never moves or removes retained keys. Filesystem ownership is the operator's
// boundary; this checks symlinked ancestors as well as ordinary directory ancestry.
func replicaKeyPaths(dataDir string) error {
	identity := getEnv("KYDRIVE_IDENTITY_KEY_FILE", "")
	if identity == "" || !filepath.IsAbs(identity) {
		return fmt.Errorf("KYDRIVE_IDENTITY_KEY_FILE must name an existing absolute file outside KY_DATA_DIR in multi mode")
	}
	data, err := resolveKeyPath(dataDir)
	if err != nil {
		return fmt.Errorf("KY_DATA_DIR cannot be resolved for multi-mode key separation")
	}
	path, err := resolveKeyPath(identity)
	if err != nil {
		return fmt.Errorf("KYDRIVE_IDENTITY_KEY_FILE cannot be resolved in multi mode")
	}
	rel, err := filepath.Rel(data, path)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("KYDRIVE_IDENTITY_KEY_FILE must be outside KY_DATA_DIR in multi mode")
	}
	for _, name := range []string{"encryption.key", "identity.key"} {
		if _, err := os.Lstat(filepath.Join(dataDir, name)); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("KY_DATA_DIR must contain no %s in multi mode; retain keys outside the data directory before removing local copies", name)
		}
	}
	return nil
}

// resolveKeyPath resolves existing ancestors even when a fresh replica's data directory
// does not exist yet. A dangling symlink or an unreadable ancestor fails closed.
func resolveKeyPath(path string) (string, error) {
	// Cleaning "symlink/.." before resolving it can change which directory the OS
	// actually opens. Require unambiguous configured paths at this boundary.
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return "", fmt.Errorf("key separation paths must not contain parent traversal")
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return resolved, err
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, fs.ErrNotExist) {
		return "", err
	}
	parent, err := resolveKeyPath(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
