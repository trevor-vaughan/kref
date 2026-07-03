package store

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/repository"

	"github.com/riotbox/kref/internal/entry"
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
