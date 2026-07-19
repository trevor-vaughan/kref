package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Store comment edit/delete", func() {
	It("edits and deletes a comment round-trip", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierShared, "note", "n", "b")
		Expect(err).NotTo(HaveOccurred())
		cid, err := s.AddComment(id, "human", "first", false, "")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.EditComment(id, cid, "second")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments[0].Body).To(Equal("second"))
		Expect(snap.Comments[0].Edited).To(BeTrue())

		Expect(s.DeleteComment(id, cid)).To(Succeed())
		snap, err = s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments[0].Deleted).To(BeTrue())
	})

	It("errors editing/deleting a comment on an unknown entry", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.EditComment("deadbeef", "x", "y")).NotTo(Succeed())
		Expect(s.DeleteComment("deadbeef", "x")).NotTo(Succeed())
	})
})
