package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Excerpt build lock", func() {
	It("grants the lock, refuses a second holder, and frees on release", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		ls := s.repo.LocalStorage()

		ok, err := acquireBuildLock(ls, entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		again, err := acquireBuildLock(ls, entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(BeFalse()) // held by this (live) pid

		Expect(releaseBuildLock(ls, entry.TierShared)).To(Succeed())
		reacq, err := acquireBuildLock(ls, entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(reacq).To(BeTrue())
	})

	It("reclaims a lock left by a dead pid", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		ls := s.repo.LocalStorage()
		Expect(writeLockPID(ls, entry.TierShared, 2147483647)).To(Succeed())
		ok, err := acquireBuildLock(ls, entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue()) // stale lock reclaimed
	})
})
