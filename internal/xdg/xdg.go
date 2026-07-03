// Package xdg resolves kref's XDG base-directory paths. Scratch files carry
// entry bodies (including private-tier content) and secret-scan reports, so
// they belong in the user-owned cache tree rather than the shared system temp
// dir, per the todo's safety requirement.
package xdg

import (
	"os"
	"path/filepath"
)

// CacheTempDir returns kref's scratch directory — $XDG_CACHE_HOME/kref/tmp,
// defaulting to ~/.cache/kref/tmp — created 0700 on first use. It returns ""
// (meaning: use the system temp dir, exactly what os.CreateTemp does with an
// empty dir argument) when no home directory can be resolved or the cache
// tree cannot be created, so HOME-less environments keep working; CreateTemp
// files are 0600 either way.
func CacheTempDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "kref", "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}
