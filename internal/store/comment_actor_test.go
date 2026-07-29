package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// A comment's git author is the repo identity, so an agent's comment used to
// display under the human's name with only actor_kind to distinguish it. The
// op carries the agent's own name alongside the kind so a reader can tell which
// agent wrote it.
var _ = Describe("comment actor attribution", func() {
	It("records an agent's name on the comment and survives a reload", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "doc", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		cid, err := s.AddComment(id, "claude-opus-5", "agent", "from an agent", false, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		reopened, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })
		snap, err := reopened.Get(id)
		Expect(err).NotTo(HaveOccurred())
		c := snap.FindComment(cid)
		Expect(c).NotTo(BeNil())
		Expect(c.Actor).To(Equal("claude-opus-5"))
		Expect(c.AuthorKind).To(Equal("agent"))
		Expect(c.Author).To(Equal("T"), "the git identity is still the committing author")
	})

	It("leaves the actor empty for a human comment", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierPersonal, "doc", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		cid, err := s.AddComment(id, "", "human", "from a person", false, "")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.FindComment(cid).Actor).To(BeEmpty())
	})
})
