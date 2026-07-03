package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Hygiene", func() {
	It("supersede links new→old and marks old superseded", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		oldID, err := s.Add(entry.TierPersonal, "note", "Old", "a")
		Expect(err).NotTo(HaveOccurred())
		newID, err := s.Add(entry.TierPersonal, "note", "New", "b")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.Supersede(oldID, newID)).To(Succeed())

		oldSnap, err := s.Get(oldID)
		Expect(err).NotTo(HaveOccurred())
		Expect(oldSnap.Status).To(Equal("superseded"))
		newSnap, err := s.Get(newID)
		Expect(err).NotTo(HaveOccurred())
		Expect(newSnap.Links).To(ContainElement(entry.Link{To: oldID.String(), Type: "supersedes"}))
	})

	It("supersede rejects superseding an entry with itself", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierPersonal, "note", "Solo", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Supersede(id, id)).To(MatchError(ContainSubstring("itself")))
	})

	It("Links reports outgoing and incoming typed edges", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		a, err := s.Add(entry.TierPersonal, "note", "A", "a")
		Expect(err).NotTo(HaveOccurred())
		b, err := s.Add(entry.TierPersonal, "note", "B", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.AddLink(a, b.String(), "relates")).To(Succeed())

		va, err := s.Links(a)
		Expect(err).NotTo(HaveOccurred())
		Expect(va.Outgoing).To(HaveLen(1))
		Expect(va.Outgoing[0].ID).To(Equal(b))
		Expect(va.Outgoing[0].Title).To(Equal("B"))

		vb, err := s.Links(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(vb.Incoming).To(HaveLen(1))
		Expect(vb.Incoming[0].ID).To(Equal(a))
		Expect(vb.Incoming[0].Type).To(Equal("relates"))
	})

	It("Links degrades an outgoing edge to a tombstoned target to an empty title", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		a, err := s.Add(entry.TierPersonal, "note", "A", "a")
		Expect(err).NotTo(HaveOccurred())
		gone, err := s.Add(entry.TierPersonal, "note", "Gone", "g")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.AddLink(a, gone.String(), "relates")).To(Succeed())
		Expect(s.Tombstone(gone)).To(Succeed())

		va, err := s.Links(a)
		Expect(err).NotTo(HaveOccurred())
		Expect(va.Outgoing).To(HaveLen(1))
		Expect(va.Outgoing[0].ID).To(Equal(gone))
		Expect(va.Outgoing[0].Title).To(Equal(""))
	})

	It("Links returns non-nil empty slices when there are no links (JSON [] not null)", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		a, err := s.Add(entry.TierPersonal, "note", "Lonely", "x")
		Expect(err).NotTo(HaveOccurred())
		v, err := s.Links(a)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Outgoing).NotTo(BeNil())
		Expect(v.Incoming).NotTo(BeNil())
		Expect(v.Outgoing).To(BeEmpty())
		Expect(v.Incoming).To(BeEmpty())
	})

	It("Tree descends into entries that name a node as parent", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		root, err := s.Add(entry.TierPersonal, "note", "Root", "r")
		Expect(err).NotTo(HaveOccurred())
		child, err := s.Add(entry.TierPersonal, "note", "Child", "c")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.AddLink(child, root.String(), "parent-child")).To(Succeed())

		tree, err := s.Tree(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(tree.ID).To(Equal(root))
		Expect(tree.Children).To(HaveLen(1))
		Expect(tree.Children[0].ID).To(Equal(child))
	})

	It("Tidy clusters duplicates and superseded entries", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Add(entry.TierPersonal, "note", "Auth Design", "one")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Add(entry.TierPersonal, "note", "auth   design", "two")
		Expect(err).NotTo(HaveOccurred())
		old, err := s.Add(entry.TierPersonal, "note", "Old", "x")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.SetStatus(old, "superseded")).To(Succeed())

		report, err := s.Tidy()
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Duplicates).To(HaveLen(1))
		Expect(report.Duplicates[0].Entries).To(HaveLen(2))
		Expect(report.Superseded).To(HaveLen(1))
		Expect(report.Superseded[0].Title).To(Equal("Old"))
	})

	It("Tidy reports a diverged (concurrently-merged) entry", func() {
		// Mirror the divergence setup from the Merged tests: a bare hub lets both
		// sides push/pull, and concurrent edits synced via Pull form a merge commit.
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())

		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(gitRepo(), "B", "b@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })

		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		id, err := a.Add(entry.TierShared, "spec", "Diverging", "base")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		Expect(a.Update(id, "edit-from-A", "")).To(Succeed())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Update(id, "edit-from-B", "")).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		report, err := b.Tidy()
		Expect(err).NotTo(HaveOccurred())
		ids := make([]string, 0, len(report.Diverged))
		for _, e := range report.Diverged {
			ids = append(ids, e.ID.String())
		}
		Expect(ids).To(ContainElement(id.String()))
	})
})
