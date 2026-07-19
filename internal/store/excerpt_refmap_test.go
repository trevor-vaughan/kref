package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Excerpt ref map", func() {
	It("builds an {id -> tip} map for a tier's refs", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())

		m, err := buildRefMap(s.repo, entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(HaveKey(id))
		Expect(string(m[id])).NotTo(BeEmpty())
	})

	It("diffs into changed and removed id sets", func() {
		old := refMap{"a": "h1", "b": "h2"}
		cur := refMap{"a": "h1", "b": "h2b", "c": "h3"}
		changed, removed := refDiff(old, cur)
		Expect(changed).To(ConsistOf(entity.Id("b"), entity.Id("c")))
		Expect(removed).To(BeEmpty())

		changed2, removed2 := refDiff(refMap{"a": "h1", "d": "h4"}, cur)
		Expect(changed2).To(ConsistOf(entity.Id("b"), entity.Id("c")))
		Expect(removed2).To(ConsistOf(entity.Id("d")))
		_ = repository.Hash("")
	})
})
