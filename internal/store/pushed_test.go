package store

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("scan-on-push", func() {
	It("scanForPush flags a secret in any body version (even one edited away)", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "Leaky", "ghp_012345678901234567890123456789abcdef")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Update(id, "now clean", "")).To(Succeed()) // secret only in history

		off, err := s.scanForPush([]entity.Id{id})
		Expect(err).NotTo(HaveOccurred())
		Expect(off).To(HaveLen(1))
		Expect(off[0].ID).To(Equal(id))
	})

	It("scanForPush passes clean entries", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "Fine", "no secrets here, just prose")
		Expect(err).NotTo(HaveOccurred())

		off, err := s.scanForPush([]entity.Id{id})
		Expect(err).NotTo(HaveOccurred())
		Expect(off).To(BeEmpty())
	})

	It("PushBlockedError guides remediation without leaking the secret", func() {
		e := &PushBlockedError{
			Tier:   entry.TierShared,
			Remote: "origin",
			Offenders: []pushOffender{
				{ID: entity.Id("abc123"), Title: "Leaky", Rule: "github-pat", Desc: "GitHub Personal Access Token"},
			},
		}
		msg := e.Error()
		Expect(msg).To(ContainSubstring("push blocked"))
		Expect(msg).To(ContainSubstring("kref purge"))
		Expect(msg).To(ContainSubstring("abc123"))
		Expect(msg).NotTo(ContainSubstring("ghp_012345678901234567890123456789abcdef"))
	})
})

var _ = Describe("pushed-state", func() {
	It("pushDelta lists new entries and skips recorded ones", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "clean")
		Expect(err).NotTo(HaveOccurred())

		d, err := s.pushDelta(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(ContainElement(id)) // new → in delta

		Expect(s.recordPushed(entry.TierShared, d)).To(Succeed())
		d, err = s.pushDelta(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(d).NotTo(ContainElement(id)) // recorded → skipped

		Expect(s.Update(id, "clean v2", "")).To(Succeed())
		d, err = s.pushDelta(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(ContainElement(id)) // changed → in delta
	})

	It("recordPushed creates a kref-pushed mirror ref", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "x")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.recordPushed(entry.TierShared, []entity.Id{id})).To(Succeed())
		exist, err := s.repo.RefExist("refs/kref-pushed/kref-shared/" + id.String())
		Expect(err).NotTo(HaveOccurred())
		Expect(exist).To(BeTrue())
	})
})

var _ = Describe("Push trust boundary", func() {
	bareHub := func() string {
		GinkgoHelper()
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())
		return origin
	}

	It("blocks a push carrying a secret and records nothing as pushed", func() {
		s, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.SetRemote(entry.TierShared, "origin", bareHub())).To(Succeed())
		id, err := s.Add(entry.TierShared, "spec", "Leaky", "ghp_012345678901234567890123456789abcdef")
		Expect(err).NotTo(HaveOccurred())

		err = s.Push(entry.TierShared)
		Expect(err).To(HaveOccurred())
		var pb *PushBlockedError
		Expect(errors.As(err, &pb)).To(BeTrue())
		Expect(pb.Offenders).To(HaveLen(1))
		Expect(pb.Offenders[0].ID).To(Equal(id))

		exist, err := s.repo.RefExist("refs/kref-pushed/kref-shared/" + id.String())
		Expect(err).NotTo(HaveOccurred())
		Expect(exist).To(BeFalse()) // nothing recorded as pushed
	})

	It("Push without force refuses when the scanner is missing, and says how to override", func() {
		GinkgoT().Setenv("KREF_BETTERLEAKS", "/nonexistent/betterleaks-missing")
		s, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.SetRemote(entry.TierShared, "origin", bareHub())).To(Succeed())
		_, err = s.Add(entry.TierShared, "spec", "S", "prose")
		Expect(err).NotTo(HaveOccurred())

		err = s.Push(entry.TierShared)
		Expect(err).To(MatchError(ContainSubstring("betterleaks not found")))
		Expect(err).To(MatchError(ContainSubstring("--force")))
	})

	It("PushForce pushes UNSCANNED when the scanner is missing and records pushed-state", func() {
		GinkgoT().Setenv("KREF_BETTERLEAKS", "/nonexistent/betterleaks-missing")
		s, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.SetRemote(entry.TierShared, "origin", bareHub())).To(Succeed())
		id, err := s.Add(entry.TierShared, "spec", "S", "prose")
		Expect(err).NotTo(HaveOccurred())

		unscanned, err := s.PushForce(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(unscanned).To(BeTrue())
		exist, err := s.repo.RefExist("refs/kref-pushed/kref-shared/" + id.String())
		Expect(err).NotTo(HaveOccurred())
		Expect(exist).To(BeTrue())
	})

	It("PushForce still blocks a positive finding from a working scanner", func() {
		s, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.SetRemote(entry.TierShared, "origin", bareHub())).To(Succeed())
		_, err = s.Add(entry.TierShared, "spec", "Leaky", "ghp_012345678901234567890123456789abcdef")
		Expect(err).NotTo(HaveOccurred())

		unscanned, err := s.PushForce(entry.TierShared)
		Expect(unscanned).To(BeFalse())
		var pb *PushBlockedError
		Expect(errors.As(err, &pb)).To(BeTrue())
	})

	It("allows a clean push and records pushed-state", func() {
		s, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.SetRemote(entry.TierShared, "origin", bareHub())).To(Succeed())
		id, err := s.Add(entry.TierShared, "spec", "Clean", "no secrets here, just prose")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.Push(entry.TierShared)).To(Succeed())
		exist, err := s.repo.RefExist("refs/kref-pushed/kref-shared/" + id.String())
		Expect(err).NotTo(HaveOccurred())
		Expect(exist).To(BeTrue())
	})
})
