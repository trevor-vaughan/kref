// Package watermark stores, per identity, the full body a human last saw of a
// todo entry — a robust-local (never-synced) marker used to compute the
// cockpit's "changed"/"to-review" deltas. Storing the full body makes the diff
// exact and squash-proof.
package watermark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/trevor-vaughan/kref/internal/xdg"
)

// Key composes the per-repo, per-entry, per-identity storage key.
func Key(repoRoot, entryID, identityEmail string) string {
	return strings.Join([]string{repoRoot, entryID, identityEmail}, "\x00")
}

// file returns the watermarks file path, or "" when state is unavailable.
func file() string {
	dir := xdg.StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "watermarks.json")
}

func load() (map[string]string, error) {
	p := file()
	if p == "" {
		return map[string]string{}, nil
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if len(b) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Get returns the seen body for key and whether it was present. A
// state-unavailable environment (no home) reports not-present with no error.
func Get(key string) (string, bool, error) {
	m, err := load()
	if err != nil {
		return "", false, err
	}
	v, ok := m[key]
	return v, ok, nil
}

// Set records seenBody for key, writing the file atomically (temp + rename). It
// is a no-op (nil error) when state is unavailable.
func Set(key, seenBody string) error {
	p := file()
	if p == "" {
		return nil
	}
	m, err := load()
	if err != nil {
		return err
	}
	m[key] = seenBody
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
