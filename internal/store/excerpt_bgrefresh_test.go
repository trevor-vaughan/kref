package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Refresh all tiers", func() {
	It("RefreshAll makes every tier's cache fresh on disk", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "A", "b")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.RefreshAll()).To(Succeed())
		dc, err := loadDiskCache(s.repo.LocalStorage(), entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(dc.Excerpts).To(HaveKey(id))
	})
})
