// Package testenv fences a test binary off the developer's home directory.
//
// kref reads and writes real things under $HOME: its own config
// ($XDG_CONFIG_HOME/kref/config.yaml), its identity profiles, its scratch tree
// ($XDG_CACHE_HOME/kref/tmp, which carries entry bodies and secret-scan
// reports), the private-tier vault ($XDG_DATA_HOME), and git's global config.
// A suite that does not fence those runs against whatever the developer happens
// to have, which fails in two directions at once: the developer's config
// silently decides what the suite asserts, and the suite writes into a tree that
// is not its own.
//
// Both had happened. internal/bridge and internal/mcpserver — 152 specs between
// them — loaded the developer's real config.yaml, so a single key their
// config.Config did not recognise failed the lot on that machine and passed in
// CI. internal/scan created the developer's ~/.cache/kref/tmp.
//
// The fence is per-package because Go gives no module-wide test hook, and it
// hangs off TestMain rather than each TestXxx so that a plain `func
// TestSomething` added later is covered without anyone remembering to opt in.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// RealHome is the home directory the test binary started in, captured before
// Main redirects HOME.
//
// Canary specs need it. os.UserHomeDir resolves through $HOME, so once the
// fence is up it reports the throwaway directory — a canary that compares
// against it is comparing the fence to itself, and cannot tell a fence from a
// leak.
var RealHome string

// Main runs a package's tests with HOME and every XDG base directory pointed at
// a throwaway tree, then exits with the suite's status. Use it as the whole body
// of a package's TestMain:
//
//	func TestMain(m *testing.M) { testenv.Main(m) }
//
// Specs that need their own home still set one; this runs first and is
// overridden by any t.Setenv on top of it.
func Main(m *testing.M) {
	os.Exit(run(m))
}

// run is separate from Main so its deferred cleanup happens before os.Exit,
// which runs no deferred functions at all.
func run(m *testing.M) int {
	restore, err := isolate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testenv: %v\n", err)
		return 1
	}
	defer restore()
	return m.Run()
}

// isolate redirects HOME and the XDG base directories, returning a function
// that removes the throwaway tree.
//
// The XDG variables are set explicitly rather than left to fall back to the new
// HOME, because a developer's environment commonly exports XDG_CONFIG_HOME
// already — and an inherited one points straight back at the real config that
// moving HOME was meant to fence off.
//
// The tree is left EMPTY. Nothing a suite asserts should come from the ambient
// environment, so a spec that wants a config, a profile or a keyring writes one
// into this directory itself.
func isolate() (restore func(), err error) {
	// Before the override: os.UserHomeDir resolves through $HOME.
	RealHome, err = os.UserHomeDir()
	if err != nil {
		// A machine with no home is one where none of this can leak, but it is
		// also one where the canaries cannot say so. Refusing is the honest
		// answer, and every environment that runs these tests has a HOME.
		return nil, fmt.Errorf("resolve the real home directory: %w", err)
	}
	home, err := os.MkdirTemp("", "kref-testenv-")
	if err != nil {
		return nil, fmt.Errorf("create a throwaway home: %w", err)
	}
	// Anything that lands here may be private-tier content or a keyring, and gpg
	// refuses a keyring others can read.
	if err := os.Chmod(home, 0o700); err != nil {
		return nil, fmt.Errorf("tighten the throwaway home: %w", err)
	}
	for k, v := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
	} {
		if err := os.Setenv(k, v); err != nil {
			return nil, fmt.Errorf("set %s: %w", k, err)
		}
	}
	return func() { _ = os.RemoveAll(home) }, nil
}
