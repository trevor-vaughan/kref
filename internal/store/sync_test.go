package store

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Sync", func() {
	It("pushes a shared entry from one store and pulls it into another", func() {
		dirA := gitRepo()
		dirB := gitRepo()
		a, err := Init(dirA, "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(dirB, "B", "b@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })

		Expect(a.SetRemote(entry.TierShared, "peer", dirB)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "peer", dirA)).To(Succeed())

		id, err := a.Add(entry.TierShared, "spec", "Shared", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())

		Expect(b.Pull(entry.TierShared)).To(Succeed())
		got, err := b.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Shared"))
	})

	It("refuses to push the private tier", func() {
		dir := gitRepo()
		s, err := Init(dir, "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.Push(entry.TierPrivate)).To(MatchError(ContainSubstring("private")))
	})

	It("never pushes the reserved quarantine tier (private-typed)", func() {
		dir := gitRepo()
		s, err := Init(dir, "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		// quarantine is private-typed and not a user-facing tier, so it can never
		// be pushed: held (secret-bearing) content never leaves the machine.
		Expect(s.TierType(entry.TierQuarantine)).To(Equal(entry.TierPrivate))
		Expect(s.Push(entry.TierQuarantine)).To(HaveOccurred())
	})

	It("GitRemotes surfaces the repository's raw git remotes by name", func() {
		dir := gitRepo()
		s, err := Init(dir, "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		// A fresh repo has no remotes.
		before, err := s.GitRemotes()
		Expect(err).NotTo(HaveOccurred())
		Expect(before).NotTo(HaveKey("origin"))

		// SetRemote with a URL creates the underlying git remote, which
		// GitRemotes then reports.
		hub := GinkgoT().TempDir()
		_, err = repository.InitBareGoGitRepo(hub, "kref")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.SetRemote(entry.TierShared, "origin", hub)).To(Succeed())

		after, err := s.GitRemotes()
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(HaveKey("origin"))
	})
})

var _ = Describe("Hub sync", func() {
	It("propagates author identity through a shared bare remote", func() {
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())

		dirA := gitRepo()
		dirB := gitRepo()
		a, err := Init(dirA, "Ada", "ada@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(dirB, "Bob", "bob@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })

		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		id, err := a.Add(entry.TierShared, "spec", "Shared", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())

		Expect(b.Pull(entry.TierShared)).To(Succeed())
		got, err := b.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Shared"))
		Expect(got.CreatedBy).To(Equal("Ada")) // proves Ada's identity reached Bob via the hub
	})
})

var _ = Describe("Distributed purge", func() {
	It("deletes the entry on the remote so a fresh clone no longer sees it", func() {
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())

		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		id, err := a.Add(entry.TierShared, "spec", "Doomed", "x")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())

		// Purge with --push deletes the ref on origin.
		Expect(a.Purge(id, false, true)).To(Succeed())

		// A fresh clone pulling from origin must NOT see the purged entry.
		c, err := Init(gitRepo(), "C", "c@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = c.Close() })
		Expect(c.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(c.Pull(entry.TierShared)).To(Succeed())
		_, err = c.Get(id)
		Expect(err).To(HaveOccurred()) // gone from the remote
	})
})

var _ = Describe("downloading a repo from elsewhere", func() {
	It("does not inherit the origin's identity, preserves authors, and uses your own identity for new work", func() {
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())

		// Ada publishes an entry to the shared origin.
		a, err := Init(gitRepo(), "Ada", "ada@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		eid, err := a.Add(entry.TierShared, "spec", "Ada's doc", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(a.Push(entry.TierShared)).To(Succeed())

		// "Download": a fresh repo. The user-identity pointer lives in local
		// config and does NOT travel, so before init there is no inherited
		// identity — kref will require you to init your own.
		dirB := gitRepo()
		_, _, ok, err := Initialized(dirB)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		// Bob initializes as himself and pulls the shared knowledge.
		b, err := Init(dirB, "Bob", "bob@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		// Ada's entry is visible and STILL authored by Ada (authorship travels
		// with the entry; the downloader's identity does not overwrite it).
		got, err := b.Get(eid)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.CreatedBy).To(Equal("Ada"))

		// Bob's own new entry is authored by Bob, not Ada.
		fid, err := b.Add(entry.TierShared, "spec", "Bob's doc", "body")
		Expect(err).NotTo(HaveOccurred())
		fb, err := b.Get(fid)
		Expect(err).NotTo(HaveOccurred())
		Expect(fb.CreatedBy).To(Equal("Bob"))
	})
})

var _ = Describe("no-remote warning predicate", func() {
	day := 24 * time.Hour
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

	openInit := func() *Store {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		return s
	}

	It("is quiet while the store has no syncable entries", func() {
		s := openInit()
		due, err := s.WarnNoRemoteDue(now, day)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(BeFalse())
	})

	It("fires once syncable entries exist without any remote, then respects the interval", func() {
		s := openInit()
		_, err := s.Add(entry.TierPersonal, "memory", "M", "b")
		Expect(err).NotTo(HaveOccurred())

		due, err := s.WarnNoRemoteDue(now, day)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(BeTrue())

		Expect(s.MarkNoRemoteWarned(now)).To(Succeed())
		due, err = s.WarnNoRemoteDue(now.Add(time.Hour), day)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(BeFalse(), "within the interval")

		due, err = s.WarnNoRemoteDue(now.Add(25*time.Hour), day)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(BeTrue(), "interval elapsed")
	})

	It("stays quiet for private-only stores and once a remote exists", func() {
		s := openInit()
		_, err := s.Add(entry.TierPrivate, "memory", "P", "b")
		Expect(err).NotTo(HaveOccurred())
		due, err := s.WarnNoRemoteDue(now, day)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(BeFalse(), "private tier cannot sync anyway")

		_, err = s.Add(entry.TierShared, "spec", "S", "b")
		Expect(err).NotTo(HaveOccurred())
		due, _ = s.WarnNoRemoteDue(now, day)
		Expect(due).To(BeTrue(), "shared entry with no remote")

		Expect(s.SetRemote(entry.TierShared, "origin", "")).To(Succeed())
		due, err = s.WarnNoRemoteDue(now, day)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(BeFalse(), "a remote is configured")
	})
})

// The property that made the signing decorator the right approach at all: an
// operation-pack DAG merges concurrent edits without conflict, and signing must
// not cost that. Two sides edit the same entry offline, both signing, and both
// edits have to survive the exchange.
var _ = Describe("signed sync round trip", func() {
	It("merges concurrent edits from two signing clones without conflict", func() {
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())

		dirA, dirB := gitRepo(), gitRepo()
		// Each side signs with its own key; the allowed-signers principal must
		// match the kref author, since that is who the commit is attributed to.
		enableSSHSigning(dirA, "ada@e.com")
		enableSSHSigning(dirB, "bob@e.com")

		a, err := Init(dirA, "Ada", "ada@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(dirB, "Bob", "bob@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })

		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		id, err := a.Add(entry.TierShared, "spec", "Shared", "original")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		// Concurrent, offline: a label on one side, a body edit on the other.
		Expect(a.AddLabel(id, "from-ada")).To(Succeed())
		Expect(b.Update(id, "bob's body", "")).To(Succeed())

		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed()) // merges, never conflicts
		Expect(b.Push(entry.TierShared)).To(Succeed())
		Expect(a.Pull(entry.TierShared)).To(Succeed())

		// Both edits survive on both sides.
		for _, s := range []*Store{a, b} {
			got, err := s.Get(id)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Body).To(Equal("bob's body"))
			Expect(got.Labels).To(ContainElement("from-ada"))
		}

		// After the round trip each side's chain holds the OTHER's commits, and
		// neither has added the other to their allowed signers. So both see
		// `untrusted`: a signature is present throughout, and each can vouch for
		// only half of it.
		//
		// Reading the tip alone got this dangerously wrong. Bob's tip is the
		// merge commit, which Bob signed with his own trusted key, so the entry
		// reported `good` — merging laundered Ada's unverifiable signature into
		// Bob's trust level. The verdict has to cover every commit precisely so
		// that pulling someone's work cannot upgrade it.
		bobState, err := b.SigState(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(bobState).To(Equal(entry.SigUntrusted),
			"merging Ada's work must not make it read as verified by Bob")
		Expect(bobState.Signed()).To(BeTrue())

		adaState, err := a.SigState(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(adaState).To(Equal(entry.SigUntrusted))
		Expect(adaState.Signed()).To(BeTrue())
	})
})
