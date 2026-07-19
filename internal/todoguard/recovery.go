package todoguard

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/trevor-vaughan/kref/internal/xdg"
)

// WriteRejected preserves a rejected todo body so a fail-closed write never
// destroys the author's work (spec §8, no-work-loss). It writes body to
// $XDG_STATE_HOME/kref/rejected/<id>-<timestamp>.md (falling back to the system
// temp dir when no state dir is available) and returns the file path. This is a
// recovery FILE, deliberately not the ingest tier-quarantine: a lint-rejected
// todo body is not secret content and must not become a private entry.
func WriteRejected(id, body string) (string, error) {
	base := xdg.StateDir()
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "rejected")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s.md", id, time.Now().Format("20060102-150405.000"))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// RemoveRejected deletes every recovery file WriteRejected saved for id (they are
// stamped, so there may be more than one). Used when a rejected item is purged so
// the preserved content — which may be a real secret — does not linger on disk.
func RemoveRejected(id string) error {
	base := xdg.StateDir()
	if base == "" {
		base = os.TempDir()
	}
	matches, err := filepath.Glob(filepath.Join(base, "rejected", id+"-*.md"))
	if err != nil {
		return err
	}
	for _, p := range matches {
		if rerr := os.Remove(p); rerr != nil && !os.IsNotExist(rerr) {
			return rerr
		}
	}
	return nil
}
