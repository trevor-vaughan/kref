package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Excerpt disk cache", func() {
	It("saves and loads a diskCache round-trip", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		ls := s.repo.LocalStorage()

		dc := &diskCache{
			Excerpts: map[entity.Id]Excerpt{"a": {ID: "a", Title: "hello"}},
			Refs:     refMap{"a": "h1"},
		}
		Expect(saveDiskCache(ls, entry.TierShared, dc)).To(Succeed())

		got, err := loadDiskCache(ls, entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Excerpts["a"].Title).To(Equal("hello"))
		Expect(string(got.Refs["a"])).To(Equal("h1"))
	})

	It("returns an error for a missing cache (so callers rebuild)", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = loadDiskCache(s.repo.LocalStorage(), entry.TierPersonal)
		Expect(err).To(HaveOccurred())
	})

	It("rejects a version mismatch", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		ls := s.repo.LocalStorage()
		dc := &diskCache{Version: 999, Excerpts: map[entity.Id]Excerpt{}, Refs: refMap{}}
		Expect(writeDiskCacheRaw(ls, entry.TierShared, dc)).To(Succeed())
		_, err = loadDiskCache(ls, entry.TierShared)
		Expect(err).To(MatchError(ContainSubstring("version")))
	})

	// Deliberately brittle: freshness is judged by ref tips, so a cache written
	// before a new Excerpt field existed looks current and would serve that
	// field empty forever. Changing Excerpt must bump the version, and this
	// spec is the reminder.
	It("pins the cache version at 4 so caches holding a persisted verdict are discarded", func() {
		Expect(excerptCacheVersion).To(Equal(uint(4)))
	})
})
