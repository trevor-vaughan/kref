package store

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/git-bug/git-bug/repository"
	"github.com/git-bug/git-bug/util/lamport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
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

// Witnessing moved OUT of the OpenGoGitRepo call and into witnessTierClocks:
// git-bug's clockLoaders pass walks entity commits with a ReadCommit that cannot
// decode a git-native signature, so with signing on it failed every open. The
// invariant the move has to carry across is stated in witnessTierClocks' own doc
// comment — "the next write cannot mint a lamport time that precedes existing
// history" — and it is the built-in tiers that changed hands, because the guard
// no longer skips them. The failure is silent: a clone's refs arrive by fetch
// while its clock files, which are local-only and never pushed, do not.
var _ = Describe("tier clock witnessing", func() {
	// dropClocks closes the store, deletes its clock directory, and returns the
	// create-clock time that directory held. The path is asked of the repo rather
	// than spelled out: the clocks live under kref's own git-bug namespace, not
	// under ".git/git-bug", and a hard-coded path that stops resolving would leave
	// these specs deleting nothing and passing against clocks that never went
	// away — which is exactly how the first draft of them passed.
	dropClocks := func(s *Store, ns string) lamport.Time {
		GinkgoHelper()
		clocks, err := s.repo.AllClocks()
		Expect(err).NotTo(HaveOccurred())
		Expect(clocks).To(HaveKey(ns + "-create"))
		before := clocks[ns+"-create"].Time()

		clockDir := filepath.Join(s.repo.LocalStorage().Root(), "clocks")
		Expect(s.Close()).To(Succeed())
		Expect(os.ReadDir(clockDir)).NotTo(BeEmpty(), "clock directory moved: %s", clockDir)
		Expect(os.RemoveAll(clockDir)).To(Succeed())
		return before
	}

	// The built-ins are what changed hands: the guard here used to skip them
	// (`d.Builtin() ||`) because git-bug's own clockLoaders pass covered them
	// inside OpenGoGitRepo. That pass is gone — it walks entity commits with a
	// ReadCommit that cannot decode a git-native signature, so with signing on it
	// failed every open — and skipping them now would witness nothing at all.
	//
	// Exercised against witnessTierClocks directly, and against a handle that has
	// never seen the clocks. Going through Open instead proves nothing: reading
	// any entity witnesses its namespace as a side effect (git-bug's dag.read),
	// and Open reads entries, so the clocks come back whether this function works
	// or not.
	It("witnesses the built-in tiers, which git-bug's open no longer does", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		for range 3 {
			_, err = s.Add(entry.TierShared, "spec", "T", "b")
			Expect(err).NotTo(HaveOccurred())
		}
		ns := entry.TierShared.Namespace()
		before := dropClocks(s, ns)
		Expect(before).To(BeNumerically(">=", lamport.Time(3)))

		raw, err := repository.OpenGoGitRepo(dir, localStorageNamespace, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = raw.Close() })
		gone, err := raw.AllClocks()
		Expect(err).NotTo(HaveOccurred())
		Expect(gone).To(BeEmpty())

		Expect(witnessTierClocks(raw, entry.BuiltinTierDefs())).To(Succeed())

		recovered, err := raw.AllClocks()
		Expect(err).NotTo(HaveOccurred())
		Expect(recovered).To(HaveKey(ns + "-create"))
		Expect(recovered).To(HaveKey(ns + "-edit"))
		Expect(recovered[ns+"-create"].Time()).To(BeNumerically(">=", before))
	})

	// The user-visible invariant the move exists to preserve, stated without
	// naming a mechanism: a store whose clock files are gone — the state a clone
	// is in, since clocks are local-only and never travel with a fetch — must not
	// mint a lamport time that collides with history already on disk.
	It("writes above existing history after the clock files are lost", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		for range 3 {
			_, err = s.Add(entry.TierShared, "spec", "T", "b")
			Expect(err).NotTo(HaveOccurred())
		}
		ns := entry.TierShared.Namespace()
		before := dropClocks(s, ns)

		reopened, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })
		_, err = reopened.Add(entry.TierShared, "spec", "After", "b")
		Expect(err).NotTo(HaveOccurred())

		clocks, err := reopened.repo.AllClocks()
		Expect(err).NotTo(HaveOccurred())
		Expect(clocks[ns+"-create"].Time()).To(BeNumerically(">", before))
	})
})

var _ = Describe("tier resolution", func() {
	It("resolves the built-ins in a fresh store", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.TierNames()).To(Equal([]entry.Tier{"private", "personal", "shared"}))
	})

	It("resolves a quarantine entry but keeps quarantine out of the user tier list", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		// quarantine is a hidden system tier: not enumerated with user tiers.
		Expect(s.TierNames()).NotTo(ContainElement(entry.TierQuarantine))
		// but an entry written there is resolvable by id and typed private.
		id, err := s.Add(entry.TierQuarantine, "quarantine", "held", "{}")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal(string(entry.TierQuarantine)))
		Expect(snap.TierType).To(Equal(string(entry.TierPrivate)))
		// by-id writes reach it too (park sets a q-dest label + a review comment).
		Expect(s.AddLabel(id, "q-dest:shared")).To(Succeed())
		snap, err = s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Labels).To(ContainElement("q-dest:shared"))
	})

	It("omits quarantine from a default List but includes it when targeted", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Add(entry.TierQuarantine, "quarantine", "held", "{}")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Add(entry.TierPersonal, "doc", "Visible", "body")
		Expect(err).NotTo(HaveOccurred())

		all, err := s.List(ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		for _, it := range all {
			Expect(it.Tier).NotTo(Equal(string(entry.TierQuarantine)))
		}

		held, err := s.List(ListFilter{Tier: entry.TierQuarantine})
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(HaveLen(1))
		Expect(held[0].Title).To(Equal("held"))
	})

	It("fails to open with a clear message when a custom quarantine tier collides", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.repo.LocalConfig().StoreString("kref.tier.quarantine", "personal")).To(Succeed())
		Expect(s.Close()).To(Succeed())

		_, err = Open(dir)
		Expect(err).To(MatchError(ContainSubstring("git config --unset kref.tier.quarantine")))
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
		Expect(s.repo.UpdateRef("refs/kref-pushed/kref-personal/deadbeef", "", mustAnyCommit(s))).To(Succeed())
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
