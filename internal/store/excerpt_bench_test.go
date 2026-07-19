package store

import (
	"testing"

	gogit "github.com/go-git/go-git/v5"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// benchRepo returns a fresh temp dir that is already a non-bare git repo, for
// benchmarks (the ginkgo gitRepo() helper needs a running spec).
func benchRepo(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	if _, err := gogit.PlainInit(dir, false); err != nil {
		b.Fatal(err)
	}
	return dir
}

// BenchmarkListExcerptsVsList seeds N entries, then compares the warm excerpt
// cache read against the full DAG List.
// Run: GOTOOLCHAIN=auto go test ./internal/store/ -bench ListExcerpts -run x -benchmem.
func BenchmarkListExcerptsVsList(b *testing.B) {
	s, err := Init(benchRepo(b), "T", "t@e.com")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for range 200 {
		if _, err := s.Add(entry.TierShared, "spec", "T", "body body body"); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := s.ListExcerpts(ListFilter{}); err != nil { // warm the cache
		b.Fatal(err)
	}

	b.Run("cache", func(b *testing.B) {
		for range b.N {
			if _, err := s.ListExcerpts(ListFilter{}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("dag", func(b *testing.B) {
		for range b.N {
			if _, err := s.List(ListFilter{}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
