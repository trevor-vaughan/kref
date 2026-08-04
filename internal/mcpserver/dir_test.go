package mcpserver

import (
	"path/filepath"
	"testing"
)

func TestDirPolicyLockedMode(t *testing.T) {
	pinned := canonicalDir(t.TempDir())
	dp := newDirPolicy(pinned, nil)

	got, _, err := dp.resolve("")
	if err != nil || got != pinned {
		t.Fatalf("empty callDir: got (%q, %v), want (%q, nil)", got, err, pinned)
	}
	if _, _, err := dp.resolve(canonicalDir(t.TempDir())); err == nil {
		t.Fatal("a different dir should be refused in locked mode")
	}
	if got, _, err := dp.resolve(pinned); err != nil || got != pinned {
		t.Fatalf("matching callDir: got (%q, %v)", got, err)
	}
}

func TestResolveRestrictedFlag(t *testing.T) {
	root := canonicalDir(t.TempDir())

	// --allow global mode: every call is restricted, even with a sole root.
	ap := newDirPolicy("", []string{root})
	if _, restricted, err := ap.resolve(root); err != nil || !restricted {
		t.Fatalf("allow mode should be restricted: (restricted=%v, err=%v)", restricted, err)
	}

	// locked mode: never restricted.
	lp := newDirPolicy(root, nil)
	if _, restricted, err := lp.resolve(""); err != nil || restricted {
		t.Fatalf("locked mode should not be restricted: (restricted=%v, err=%v)", restricted, err)
	}
}

func TestMatchRoots(t *testing.T) {
	root := canonicalDir(t.TempDir())
	other := canonicalDir(t.TempDir())
	roots := []string{root}

	if got, err := matchRoots(roots, ""); err != nil || got != root {
		t.Fatalf("one-root default: got (%q, %v), want %q", got, err, root)
	}
	if got, err := matchRoots(roots, root); err != nil || got != root {
		t.Fatalf("dir==root: got (%q, %v)", got, err)
	}
	sub := filepath.Join(root, "sub")
	if got, err := matchRoots(roots, sub); err != nil || got != sub {
		t.Fatalf("descendant: got (%q, %v), want %q", got, err, sub)
	}
	if _, err := matchRoots(roots, other); err == nil {
		t.Fatal("dir outside roots should be refused")
	}
	if _, err := matchRoots(roots, "relative/path"); err == nil {
		t.Fatal("relative dir should be refused")
	}
	if _, err := matchRoots(roots, root+"x"); err == nil {
		t.Fatalf("sibling %q must not be authorized by root %q", root+"x", root)
	}
}

func TestMatchRootsMultiRootRequiresDir(t *testing.T) {
	roots := []string{canonicalDir(t.TempDir()), canonicalDir(t.TempDir())}
	if _, err := matchRoots(roots, ""); err == nil {
		t.Fatal("empty callDir with multiple roots should be refused")
	}
}
