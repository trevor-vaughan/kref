package store

import (
	"errors"

	"github.com/git-bug/git-bug/repository"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/riotbox/kref/internal/entry"
)

// mustAnyCommit stores an empty tree + commit so tests can point refs at a
// real object.
func mustAnyCommit(s *Store) repository.Hash {
	tree, err := s.repo.StoreTree([]repository.TreeEntry{})
	Expect(err).NotTo(HaveOccurred())
	c, err := s.repo.StoreCommit(tree)
	Expect(err).NotTo(HaveOccurred())
	return c
}

var _ = Describe("tier resolution", func() {
	It("resolves the built-ins in a fresh store", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.TierNames()).To(Equal([]entry.Tier{"private", "personal", "shared"}))
	})

	It("resolves config-declared custom tiers in display order", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.repo.LocalConfig().StoreString("kref.tier.research", "personal")).To(Succeed())
		Expect(s.repo.LocalConfig().StoreString("kref.tier.team-x", "shared")).To(Succeed())
		Expect(s.Close()).To(Succeed())

		s, err = Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.TierNames()).To(Equal([]entry.Tier{"private", "personal", "research", "shared", "team-x"}))

		d, err := s.TierDef("research")
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Type).To(Equal(entry.TierPersonal))
		Expect(d.Declared).To(BeTrue())
	})

	It("errors loudly on an invalid declared type", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.repo.LocalConfig().StoreString("kref.tier.bad", "secret")).To(Succeed())
		Expect(s.Close()).To(Succeed())

		_, err = Open(dir)
		Expect(err).To(MatchError(ContainSubstring("kref.tier.bad")))
	})

	It("discovers undeclared namespaces from refs, typed shared", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		// Simulate a teammate's tier arriving via fetch: declare, write, undeclare.
		Expect(s.repo.LocalConfig().StoreString("kref.tier.theirs", "personal")).To(Succeed())
		Expect(s.reloadTiers()).To(Succeed())
		_, err = s.Add(entry.Tier("theirs"), "note", "Foreign", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.repo.LocalConfig().RemoveAll("kref.tier")).To(Succeed()) // drop the whole subsection
		Expect(s.Close()).To(Succeed())

		s, err = Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		d, err := s.TierDef("theirs")
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Declared).To(BeFalse())
		Expect(d.Type).To(Equal(entry.TierShared)) // display default for foreign namespaces

		_, err = s.DeclaredTier("theirs")
		Expect(err).To(MatchError(ContainSubstring("not declared")))
	})

	It("never reports bookkeeping namespaces (kref-pushed) as tiers", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.repo.UpdateRef("refs/kref-pushed/kref-personal/deadbeef", mustAnyCommit(s))).To(Succeed())
		Expect(s.reloadTiers()).To(Succeed())
		_, err = s.TierDef("pushed")
		Expect(err).To(HaveOccurred())
	})

	It("TierType defaults unknown names to shared", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.TierType(entry.TierPrivate)).To(Equal(entry.TierPrivate))
		Expect(s.TierType(entry.Tier("nonexistent"))).To(Equal(entry.TierShared))
	})
})

var _ = Describe("TierAdd / TierRemove", func() {
	It("declares a custom tier and lists it", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		Expect(s.TierAdd("research", entry.TierPersonal, "", "")).To(Succeed())
		d, err := s.DeclaredTier("research")
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Type).To(Equal(entry.TierPersonal))

		// Persisted: a fresh Open sees it too.
		Expect(s.Close()).To(Succeed())
		s, err = Open(dir)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.DeclaredTier("research")
		Expect(err).NotTo(HaveOccurred())
	})

	It("wires the remote in one step", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		remoteDir := gitRepo()
		Expect(s.TierAdd("team-x", entry.TierShared, "teamx", remoteDir)).To(Succeed())
		name, err := s.RemoteFor(entry.Tier("team-x"))
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("teamx"))
	})

	It("refuses reserved names, bad types, and duplicates", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.TierAdd("private", entry.TierPersonal, "", "")).To(MatchError(ContainSubstring("reserved")))
		Expect(s.TierAdd("pushed", entry.TierPersonal, "", "")).To(MatchError(ContainSubstring("reserved")))
		Expect(s.TierAdd("ok-name", entry.TierPrivate, "", "")).To(MatchError(ContainSubstring("invalid tier type")))
		Expect(s.TierAdd("research", entry.TierPersonal, "", "")).To(Succeed())
		Expect(s.TierAdd("research", entry.TierShared, "", "")).To(MatchError(ContainSubstring("already declared")))
	})

	It("removes an empty custom tier, refuses a populated one without force", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.TierAdd("research", entry.TierPersonal, "", "")).To(Succeed())
		_, err = s.Add(entry.Tier("research"), "note", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.TierRemove("research", false)).To(MatchError(ContainSubstring("still holds")))
		Expect(s.TierRemove("research", true)).To(Succeed()) // orphans, deletes nothing

		// The namespace survives as a discovered (undeclared) tier.
		d, err := s.TierDef("research")
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Declared).To(BeFalse())

		Expect(s.TierRemove("shared", false)).To(MatchError(ContainSubstring("built-in")))
	})
})

var _ = Describe("sync with a custom tier", func() {
	It("pushes and pulls a shared-typed custom tier through its own remote", func() {
		remoteDir := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(remoteDir, "kref")
		Expect(err).NotTo(HaveOccurred())

		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.TierAdd("team-x", entry.TierShared, "teamx", remoteDir)).To(Succeed())
		id, err := s.Add(entry.Tier("team-x"), "note", "Shared doc", "clean body")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.Push(entry.Tier("team-x"))).To(Succeed())

		// A second clone declaring the same tier pulls the entry.
		dir2 := gitRepo()
		s2, err := Init(dir2, "U", "u@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		Expect(s2.TierAdd("team-x", entry.TierShared, "teamx", remoteDir)).To(Succeed())
		Expect(s2.Pull(entry.Tier("team-x"))).To(Succeed())
		snap, err := s2.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Title).To(Equal("Shared doc"))
		Expect(snap.Tier).To(Equal("team-x"))
	})

	It("refuses to push an undeclared tier", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		err = s.Push(entry.Tier("ghost"))
		Expect(err).To(MatchError(ContainSubstring("unknown tier")))
	})
})

var _ = Describe("custom tiers in the entry lifecycle", func() {
	var dir string
	var s *Store

	BeforeEach(func() {
		dir = gitRepo()
		var err error
		s, err = Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.TierAdd("research", entry.TierPersonal, "", "")).To(Succeed())
		Expect(s.TierAdd("team-x", entry.TierShared, "", "")).To(Succeed())
	})

	It("lists and gets entries living in a custom tier, with TierType set", func() {
		id, err := s.Add(entry.Tier("research"), "note", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal("research"))
		Expect(snap.TierType).To(Equal("personal"))

		items, err := s.List(ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		found := false
		for _, it := range items {
			if it.ID == id {
				found = true
				Expect(it.TierType).To(Equal("personal"))
			}
		}
		Expect(found).To(BeTrue())
	})

	It("retiers into a declared custom tier and refuses an undeclared target", func() {
		id, err := s.Add(entry.TierPersonal, "note", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Retier(id, entry.Tier("research"), "tester", "human")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal("research"))

		Expect(s.Retier(id, entry.Tier("nope"), "tester", "human")).
			To(MatchError(ContainSubstring("unknown tier")))
	})

	It("gates retier-to-shared-TYPED tiers on the secret scan", func() {
		id, err := s.Add(entry.TierPersonal, "note", "Leaky",
			"ghp_012345678901234567890123456789abcdef")
		Expect(err).NotTo(HaveOccurred())
		err = s.Retier(id, entry.Tier("team-x"), "tester", "human")
		Expect(err).To(HaveOccurred())
		var rb *RetierBlockedError
		Expect(errors.As(err, &rb)).To(BeTrue())

		// personal-typed custom target: no shared gate.
		Expect(s.Retier(id, entry.Tier("research"), "tester", "human")).To(Succeed())
	})
})
