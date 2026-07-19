package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Excerpt cache orchestrator", func() {
	It("rebuilds a tier from the DAG and persists it", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "Title", "body")
		Expect(err).NotTo(HaveOccurred())

		c := newExcerptCache(s)
		dc, err := c.rebuild(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(dc.Excerpts).To(HaveKey(id))
		Expect(dc.Excerpts[id].Title).To(Equal("Title"))
		Expect(dc.Refs).To(HaveKey(id))

		loaded, err := loadDiskCache(s.repo.LocalStorage(), entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded.Excerpts).To(HaveKey(id))
	})

	It("ensureFresh serves a clean cache without a rebuild, and refreshes only the delta", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		a, err := s.Add(entry.TierShared, "spec", "A", "b")
		Expect(err).NotTo(HaveOccurred())

		c := newExcerptCache(s)
		_, err = c.rebuild(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())

		dc1, err := c.ensureFresh(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(dc1.Excerpts).To(HaveLen(1))

		b, err := s.Add(entry.TierShared, "spec", "B", "b")
		Expect(err).NotTo(HaveOccurred())
		dc2, err := c.ensureFresh(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(dc2.Excerpts).To(HaveKey(a))
		Expect(dc2.Excerpts).To(HaveKey(b))
		Expect(dc2.Excerpts).To(HaveLen(2))
	})

	It("ensureFresh rebuilds when the cache file is missing", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Add(entry.TierShared, "spec", "A", "b")
		Expect(err).NotTo(HaveOccurred())
		c := newExcerptCache(s)
		dc, err := c.ensureFresh(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(dc.Excerpts).To(HaveLen(1))
	})
})
