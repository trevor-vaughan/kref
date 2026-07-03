package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/xdg"
)

// Export writes a git bundle of the selected tiers' entry refs plus all identity
// refs (so authors resolve on import) to w. It errors if no entries exist in the
// selected tiers. Output is a standard git bundle, suitable for `kref import`
// and for piping through an external encryptor (SOPS/age).
func (s *Store) Export(w io.Writer, tiers []entry.Tier) error {
	entryRefs, err := s.refsMatching(tierRefPrefixes(tiers)...)
	if err != nil {
		return err
	}
	if len(entryRefs) == 0 {
		return fmt.Errorf("nothing to export for the selected tier(s)")
	}
	idRefs, err := s.refsMatching("refs/identities/")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(xdg.CacheTempDir(), "kref-export-*.bundle")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpName) }()

	args := append([]string{"-C", s.dir, "bundle", "create", tmpName}, append(entryRefs, idRefs...)...)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git bundle create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	f, err := os.Open(tmpName)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

// ImportResult reports the outcome of an Import.
type ImportResult struct {
	Refs int `json:"refs"` // refs created by the fetch
}

// Import fetches the selected tiers' refs (plus identities) from a git bundle
// into the store. New entries are created; an entry that already exists with a
// diverged history is rejected by git, surfaced via the returned error (nothing
// is clobbered). The primary use is restoring private knowledge into a fresh
// clone, which is conflict-free.
func (s *Store) Import(bundlePath string, tiers []entry.Tier) (ImportResult, error) {
	refspecs := []string{"refs/identities/*:refs/identities/*"}
	for _, p := range tierRefPrefixes(tiers) {
		ns := strings.TrimSuffix(p, "/")
		refspecs = append(refspecs, fmt.Sprintf("%s/*:%s/*", ns, ns))
	}
	args := append([]string{"-C", s.dir, "fetch", bundlePath}, refspecs...)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return ImportResult{}, fmt.Errorf("git fetch from bundle: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return ImportResult{Refs: strings.Count(string(out), "[new ref]")}, nil
}

// VaultPath returns the local-only path of this repo's private vault bundle,
// under $XDG_DATA_HOME (or ~/.local/share), keyed by the repo's absolute path.
// It is always a local filesystem path — never a URL — so the private tier's
// "never leaves the machine over the network" invariant is preserved.
func (s *Store) VaultPath() (string, error) {
	base := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	abs, err := filepath.Abs(s.dir)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs))
	key := hex.EncodeToString(sum[:])[:16]
	return filepath.Join(base, "kref", key, "private.bundle"), nil
}

// Backup exports the private tier to this repo's local vault, returning the
// vault path. Same-machine recovery for `rm -rf`'d or purged private entries.
func (s *Store) VaultBackup() (string, error) {
	path, err := s.VaultPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := s.Export(f, []entry.Tier{entry.TierPrivate}); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// Restore imports the private tier from this repo's local vault, returning the
// result and the vault path.
func (s *Store) VaultRestore() (ImportResult, string, error) {
	path, err := s.VaultPath()
	if err != nil {
		return ImportResult{}, "", err
	}
	if _, err := os.Stat(path); err != nil {
		return ImportResult{}, path, fmt.Errorf("no vault backup at %s (run `kref backup` first)", path)
	}
	r, err := s.Import(path, []entry.Tier{entry.TierPrivate})
	return r, path, err
}

// refsMatching returns the full ref names matching the given for-each-ref
// patterns (e.g. "refs/kref-private/", "refs/identities/").
func (s *Store) refsMatching(patterns ...string) ([]string, error) {
	args := append([]string{"-C", s.dir, "for-each-ref", "--format=%(refname)"}, patterns...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	var refs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			refs = append(refs, line)
		}
	}
	return refs, nil
}

// tierRefPrefixes maps tiers to their `refs/kref-<tier>/` for-each-ref prefixes.
func tierRefPrefixes(tiers []entry.Tier) []string {
	out := make([]string, len(tiers))
	for i, t := range tiers {
		out[i] = "refs/" + t.Namespace() + "/"
	}
	return out
}
