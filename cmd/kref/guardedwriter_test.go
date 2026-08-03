package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/store"
)

// secretBody carries a github-pat pattern. betterleaks filters synthetic AWS
// keys, so a `ghp_` token is the fixture the rest of the suite uses too.
const secretBody = "token: ghp_012345678901234567890123456789abcdef"

func guardedFixture() (*store.Store, entity.Id, *guardedWriter) {
	GinkgoHelper()
	s, err := store.Init(gitRepo(), "T", "t@e.com")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = s.Close() })
	id, err := s.Add(entry.TierPersonal, "note", "N", "body")
	Expect(err).NotTo(HaveOccurred())
	return s, id, newGuardedWriter(s, "T", "human")
}

var _ = Describe("guardedWriter AddComment", func() {
	It("writes a clean comment straight through", func() {
		s, id, g := guardedFixture()

		res, err := g.AddComment(id, "T", "human", "just a note", false, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Parked).To(BeNil())
		Expect(res.Unscanned).To(BeFalse())
		Expect(res.CommentID).NotTo(BeEmpty())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments).To(HaveLen(1))
		Expect(snap.Comments[0].Body).To(Equal("just a note"))
	})

	It("parks a flagged comment instead of writing it", func() {
		s, id, g := guardedFixture()

		res, err := g.AddComment(id, "T", "human", secretBody, false, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Parked).NotTo(BeNil())
		Expect(res.Parked.Findings).NotTo(BeEmpty())
		Expect(res.CommentID).To(BeEmpty())

		q, err := s.QuarantineQueue()
		Expect(err).NotTo(HaveOccurred())
		Expect(q).To(HaveLen(1))
		Expect(q[0].OpKind).To(Equal("add-comment"))

		// The review question is posted on the entry; the flagged body is not.
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		for _, c := range snap.Comments {
			Expect(c.Body).NotTo(ContainSubstring("ghp_"))
		}
	})

	It("reports unscanned and still writes when betterleaks is missing", func() {
		GinkgoT().Setenv("KREF_BETTERLEAKS", "/nonexistent/betterleaks")
		s, id, g := guardedFixture()

		res, err := g.AddComment(id, "T", "human", secretBody, false, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Unscanned).To(BeTrue())
		Expect(res.Parked).To(BeNil())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments).To(HaveLen(1))
	})

	It("preserves the flagged body verbatim in the parked item", func() {
		s, id, g := guardedFixture()
		parent, err := s.AddComment(id, "T", "human", "root", true, "")
		Expect(err).NotTo(HaveOccurred())

		res, err := g.AddComment(id, "T", "human", secretBody, true, parent)

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Parked).NotTo(BeNil())
		// Nothing is lost: approval replays this body through the normal path.
		detail, err := s.QuarantineDetail(res.Parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(detail.ProposedContent).To(Equal(secretBody))
	})
})

var _ = Describe("guardedWriter EditComment", func() {
	It("edits a comment when the new body is clean", func() {
		s, id, g := guardedFixture()
		cid, err := s.AddComment(id, "T", "human", "original", false, "")
		Expect(err).NotTo(HaveOccurred())

		res, err := g.EditComment(id, cid, "revised")

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Parked).To(BeNil())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments[0].Body).To(Equal("revised"))
	})

	It("parks a flagged edit and leaves the comment untouched", func() {
		s, id, g := guardedFixture()
		cid, err := s.AddComment(id, "T", "human", "original", false, "")
		Expect(err).NotTo(HaveOccurred())

		res, err := g.EditComment(id, cid, secretBody)

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Parked).NotTo(BeNil())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments[0].Body).To(Equal("original"))
		q, err := s.QuarantineQueue()
		Expect(err).NotTo(HaveOccurred())
		Expect(q).To(HaveLen(1))
		Expect(q[0].OpKind).To(Equal("edit-comment"))
	})
})

var _ = Describe("guardedWriter ResolveWithNote", func() {
	openQuestion := func(s *store.Store, id entity.Id) string {
		GinkgoHelper()
		cid, err := s.AddComment(id, "T", "human", "why?", true, "")
		Expect(err).NotTo(HaveOccurred())
		return cid
	}

	It("resolves with no note and posts nothing", func() {
		s, id, g := guardedFixture()
		cid := openQuestion(s, id)

		res, err := g.ResolveWithNote(id, cid, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Parked).To(BeNil())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments).To(HaveLen(1))
		Expect(snap.Comments[0].Resolved).To(BeTrue())
	})

	It("posts a clean note and resolves", func() {
		s, id, g := guardedFixture()
		cid := openQuestion(s, id)

		res, err := g.ResolveWithNote(id, cid, "answered offline")

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Parked).To(BeNil())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments).To(HaveLen(2))
		Expect(snap.Comments[0].Resolved).To(BeTrue())
	})

	It("parks a flagged note as ONE resolve intent and does not resolve", func() {
		s, id, g := guardedFixture()
		cid := openQuestion(s, id)

		res, err := g.ResolveWithNote(id, cid, secretBody)

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Parked).NotTo(BeNil())
		q, err := s.QuarantineQueue()
		Expect(err).NotTo(HaveOccurred())
		Expect(q).To(HaveLen(1)) // one intent, not a parked note plus a resolve
		Expect(q[0].OpKind).To(Equal("resolve"))
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments[0].Resolved).To(BeFalse())
	})
})

var _ = Describe("guardedWriter passthroughs", func() {
	It("deletes and unresolves without scanning (no body to scan)", func() {
		s, id, g := guardedFixture()
		cid, err := s.AddComment(id, "T", "human", "why?", true, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.ResolveComment(id, cid)).To(Succeed())

		Expect(g.UnresolveComment(id, cid)).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments[0].Resolved).To(BeFalse())

		Expect(g.DeleteComment(id, cid)).To(Succeed())
		snap, err = s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments[0].Deleted).To(BeTrue())
	})
})
