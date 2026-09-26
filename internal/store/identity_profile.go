package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// identityConfigKey names the active identity profile. It is read from the
// merged git config, so a repo can pin one locally while the global config sets
// a default.
const identityConfigKey = "kref.identity"

// identityProfileDir returns $XDG_CONFIG_HOME/kref/identities (or
// $HOME/.config/...). A profile is an ordinary gitconfig file: that is the whole
// point, because it lets one file carry a name, an email and a signing key as a
// single unit that git itself already knows how to read.
func identityProfileDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", errors.New("cannot resolve the identities directory: neither XDG_CONFIG_HOME nor HOME is set")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "kref", "identities"), nil
}

// IdentityProfiles lists the identity profiles on disk, sorted. A missing
// directory is not an error: it simply means none have been created.
func IdentityProfiles() ([]string, error) {
	dir, err := identityProfileDir()
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// identityProfilePath resolves a profile name to its file, rejecting anything
// that is not a plain filename.
//
// The name arrives from git config, which a repository can set — so a repo
// cloned from elsewhere could otherwise point kref at an arbitrary path and
// have it exported as GIT_CONFIG_GLOBAL to every git subprocess kref runs.
// Confining it to one directory entry closes that.
//
// There is no empty-name case because every caller rules it out first:
// activeIdentityProfile returns early, UseIdentity reads "" as "clear the pin",
// and DescribeIdentities iterates names read off disk.
func identityProfilePath(name string) (string, error) {
	if name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsRune(name, os.PathSeparator) {
		return "", fmt.Errorf("invalid identity name %q: it must be a single file name in the identities directory", name)
	}
	dir, err := identityProfileDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("identity %q not found in %s (kref identity list shows the available ones)", name, dir)
		}
		return "", err
	}
	return path, nil
}

// activeIdentityProfile resolves the configured profile to its path, or "" when
// none is set. KREF_IDENTITY overrides git config, matching how KREF_AUTHOR_*
// overrides kref.author.*.
func activeIdentityProfile(cfg *gitConfigSnapshot) (name, path string, err error) {
	name = strings.TrimSpace(os.Getenv("KREF_IDENTITY"))
	if name == "" {
		// Resolved from the snapshot taken WITHOUT a profile's own config layer:
		// which profile is active cannot be decided by the profile.
		name = strings.TrimSpace(cfg.get(identityConfigKey))
	}
	if name == "" {
		return "", "", nil
	}
	path, err = identityProfilePath(name)
	if err != nil {
		return "", "", err
	}
	return name, path, nil
}

// ActiveIdentity returns the name of the identity profile in force, or "" when
// the store uses the plain git identity.
func (s *Store) ActiveIdentity() (string, error) {
	name, _, err := activeIdentityProfile(s.gitcfg)
	return name, err
}

// UseIdentity pins an identity profile for this repository, or clears it when
// name is empty. It validates the name first so a typo fails here rather than on
// the next open.
func (s *Store) UseIdentity(name string) error {
	if name == "" {
		if err := gitConfigUnset(s.dir, identityConfigKey); err != nil {
			return err
		}
		return s.refreshGitConfig()
	}
	if _, err := identityProfilePath(name); err != nil {
		return err
	}
	if err := s.repo.LocalConfig().StoreString(identityConfigKey, name); err != nil {
		return err
	}
	return s.refreshGitConfig()
}

// refreshGitConfig re-reads the config snapshot after kref has written to it, so
// the store does not keep answering from the state it opened in. Nothing today
// reads the active identity back in the same process, which is exactly why this
// belongs next to the write rather than in whichever caller first does.
func (s *Store) refreshGitConfig() error {
	cfg, err := loadGitConfigSnapshot(s.dir, nil)
	if err != nil {
		return err
	}
	s.gitcfg = cfg
	return nil
}

// IdentityProfileSummary describes one profile for `kref identity list`.
type IdentityProfileSummary struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Author string `json:"author"`
	Signs  bool   `json:"signs"` // carries its own signing key
	Active bool   `json:"active"`
}

// DescribeIdentities returns every profile with the identity it supplies. A
// profile that cannot be read is reported with empty fields rather than failing
// the listing: one broken file should not hide the rest.
func (s *Store) DescribeIdentities() ([]IdentityProfileSummary, error) {
	names, err := IdentityProfiles()
	if err != nil {
		return nil, err
	}
	active, err := s.ActiveIdentity()
	if err != nil {
		// A dangling active profile must not stop the user seeing the list that
		// would let them fix it.
		active = ""
	}
	out := make([]IdentityProfileSummary, 0, len(names))
	for _, n := range names {
		sum := IdentityProfileSummary{Name: n, Active: n == active}
		if path, err := identityProfilePath(n); err == nil {
			if a, e, err := identityProfileAuthor(path); err == nil {
				sum.Author, sum.Email = a, e
			}
			if key, err := gitConfigFile(path, "user.signingkey"); err == nil {
				sum.Signs = key != ""
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

// identityProfileAuthor reads the name and email a profile attributes work to.
//
// Both must be present: a profile supplying only one would silently assemble a
// mixed identity, exactly the failure authorOverride already refuses.
func identityProfileAuthor(path string) (name, email string, err error) {
	name, err = gitConfigFile(path, "user.name")
	if err != nil {
		return "", "", err
	}
	email, err = gitConfigFile(path, "user.email")
	if err != nil {
		return "", "", err
	}
	if name == "" || email == "" {
		return "", "", fmt.Errorf("identity profile %s must set both user.name and user.email", path)
	}
	return name, email, nil
}

// gitConfigFile reads one key from a standalone gitconfig file, treating a
// missing key as empty. Asking git rather than parsing the file keeps every
// include, casing and quoting rule git supports working here too.
//
// `git config --get` exits 1 for "key not found" and uses other codes for real
// problems (2 = unparseable file), so only 1 is folded into an empty result.
func gitConfigFile(path, key string) (string, error) {
	out, err := runGit(nil, "", "config", "--file", path, "--get", key)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}
