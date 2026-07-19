package store

import (
	"strings"

	"github.com/git-bug/git-bug/entity"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

var _ = Describe("store.locate", func() {
	find := []scan.Finding{{RuleID: "github-pat", Description: "GitHub PAT", StartLine: 1}}

	It("finds an entry in a user tier and returns its holding tier", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "doc", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())

		t, e, err := s.locate(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(t).To(Equal(entry.TierShared))
		Expect(e).NotTo(BeNil())
	})

	It("finds a hidden quarantine item by id", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "body", "", find, "agent")
		Expect(err).NotTo(HaveOccurred())

		t, e, err := s.locate(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(t).To(Equal(entry.TierQuarantine))
		Expect(e).NotTo(BeNil())
	})

	It("returns a tier-agnostic not-found for an unknown id", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		_, _, err = s.locate(entity.Id(strings.Repeat("a", 64)))
		Expect(err).To(MatchError(ContainSubstring("not found")))
	})

	It("mutate applies an op and commits, reaching a quarantine item", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "body", "", find, "agent")
		Expect(err).NotTo(HaveOccurred())

		err = s.mutate(parked.ItemID, func(e *entry.Entry) error {
			e.Append(entry.NewSetStatus(s.author, "active"))
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		snap, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Status).To(Equal("active"))
	})

	It("a by-id mutation (SetStatus) reaches a quarantine item", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "body", "", find, "agent")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.SetStatus(parked.ItemID, "active")).To(Succeed())
		snap, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Status).To(Equal("active"))
	})

	It("a read path (Log) reaches a quarantine item", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "body", "", find, "agent")
		Expect(err).NotTo(HaveOccurred())

		log, err := s.Log(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(log).NotTo(BeEmpty())
	})

	It("a link mutation (AddLink) reaches a quarantine item", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		other, err := s.Add(entry.TierShared, "doc", "Other", "x")
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "body", "", find, "agent")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.AddLink(parked.ItemID, other.String(), "relates")).To(Succeed())
	})
})
