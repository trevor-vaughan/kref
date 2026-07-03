package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("WhatsNew", func() {
	bareHub := func() string {
		GinkgoHelper()
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())
		return origin
	}

	It("reports incoming entries and unpushed local entries", func() {
		origin := bareHub()
		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(gitRepo(), "B", "b@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		incomingID, err := a.Add(entry.TierShared, "spec", "FromA", "x")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		localID, err := b.Add(entry.TierShared, "spec", "LocalB", "y")
		Expect(err).NotTo(HaveOccurred())

		incoming, unpushed, err := b.WhatsNew()
		Expect(err).NotTo(HaveOccurred())
		Expect(idsOf(incoming)).To(ContainElement(incomingID.String()))
		Expect(idsOf(unpushed)).To(ContainElement(localID.String()))
	})

	It("SincePull returns only the ops added after the pull (and flags no-baseline)", func() {
		origin := bareHub()
		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(gitRepo(), "B", "b@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		id, err := a.Add(entry.TierShared, "spec", "Doc", "v1")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		ops, baseline, err := b.SincePull(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(baseline).To(BeTrue())
		Expect(ops).To(BeEmpty())

		Expect(b.Update(id, "v2 local", "")).To(Succeed())
		ops, baseline, err = b.SincePull(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(baseline).To(BeTrue())
		Expect(len(ops)).To(BeNumerically(">=", 1))

		localID, err := b.Add(entry.TierShared, "spec", "LocalOnly", "z")
		Expect(err).NotTo(HaveOccurred())
		_, baseline, err = b.SincePull(localID)
		Expect(err).NotTo(HaveOccurred())
		Expect(baseline).To(BeFalse())
	})
})

func idsOf(snaps []*entry.Snapshot) []string {
	out := make([]string, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.ID.String())
	}
	return out
}

var _ = Describe("whats-new pull capture", func() {
	bareHub := func() string {
		GinkgoHelper()
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())
		return origin
	}

	It("Pull records the entries it brought in as incoming", func() {
		origin := bareHub()
		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(gitRepo(), "B", "b@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		id, err := a.Add(entry.TierShared, "spec", "Hello", "from A")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		got, err := b.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Hello"))
		Expect(b.incomingEntries(entry.TierShared)).To(HaveKey(id.String()))
	})

	It("Pull with no remote changes records empty incoming", func() {
		// Seed the hub with one entry from A so it is non-empty, then pull twice
		// from B. The second pull sees no new entries and must record empty incoming.
		origin := bareHub()
		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(gitRepo(), "B", "b@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		_, err = a.Add(entry.TierShared, "spec", "Seed", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())

		// First pull: brings in the seed entry (incoming non-empty).
		Expect(b.Pull(entry.TierShared)).To(Succeed())
		// Second pull: remote has not changed — incoming must be empty.
		Expect(b.Pull(entry.TierShared)).To(Succeed())
		Expect(b.incomingEntries(entry.TierShared)).To(BeEmpty())
	})
})
