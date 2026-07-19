package mcpserver

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDirPolicyLockedMode(t *testing.T) {
	ctx := context.Background()
	pinned := canonicalDir(t.TempDir())
	dp := newDirPolicy(pinned, nil, false)

	got, _, err := dp.resolve(ctx, nil, "")
	if err != nil || got != pinned {
		t.Fatalf("empty callDir: got (%q, %v), want (%q, nil)", got, err, pinned)
	}
	if _, _, err := dp.resolve(ctx, nil, canonicalDir(t.TempDir())); err == nil {
		t.Fatal("a different dir should be refused in locked mode")
	}
	if got, _, err := dp.resolve(ctx, nil, pinned); err != nil || got != pinned {
		t.Fatalf("matching callDir: got (%q, %v)", got, err)
	}
}

func TestResolveRestrictedFlag(t *testing.T) {
	ctx := context.Background()
	root := canonicalDir(t.TempDir())

	// --allow static global mode: every call is restricted.
	ap := newDirPolicy("", []string{root}, false)
	if _, restricted, err := ap.resolve(ctx, nil, root); err != nil || !restricted {
		t.Fatalf("allow mode should be restricted: (restricted=%v, err=%v)", restricted, err)
	}

	// locked mode: never restricted.
	lp := newDirPolicy(root, nil, false)
	if _, restricted, err := lp.resolve(ctx, nil, ""); err != nil || restricted {
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

func TestFileURIToPath(t *testing.T) {
	cases := []struct {
		uri  string
		want string
		ok   bool
	}{
		{"file:///home/u/repo", "/home/u/repo", true},
		{"file://localhost/home/u/repo", "/home/u/repo", true},
		{"file:///home/u/a%20b", "/home/u/a b", true}, // percent-decoded
		{"file://remotehost/x", "", false},            // non-local host
		{"https://example.com/x", "", false},          // wrong scheme
		{"/home/u/repo", "", false},                   // no scheme
		{"::not a uri::", "", false},                  // unparseable
	}
	for _, c := range cases {
		got, ok := fileURIToPath(c.uri)
		if ok != c.ok || got != c.want {
			t.Errorf("fileURIToPath(%q) = (%q, %v), want (%q, %v)", c.uri, got, ok, c.want, c.ok)
		}
	}
}
