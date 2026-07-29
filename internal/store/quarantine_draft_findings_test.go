package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

// A held op carries its findings inside its intent JSON, but a draft's body IS
// the proposed entry, so a draft has to record them separately. Without that,
// the review surfaces report which rule flagged a held op but say nothing about
// a flagged new entry.
var _ = Describe("quarantine draft findings", func() {
	find := []scan.Finding{
		{RuleID: "github-pat", Description: "GitHub PAT", StartLine: 3},
		{RuleID: "aws-key", Description: "AWS key", StartLine: 7},
	}

	seed := func() (*Store, QuarantineItem) {
		GinkgoHelper()
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "the body", "", find, "", "agent")
		Expect(err).NotTo(HaveOccurred())
		q, err := s.QuarantineQueue()
		Expect(err).NotTo(HaveOccurred())
		Expect(q).To(HaveLen(1))
		return s, q[0]
	}

	It("reports every finding's rule and line on a queued draft", func() {
		_, it := seed()
		Expect(it.HeldOp).To(BeFalse())
		Expect(it.Findings).To(HaveLen(2))
		rules := []string{it.Findings[0].RuleID, it.Findings[1].RuleID}
		Expect(rules).To(ConsistOf("github-pat", "aws-key"))
		lines := []int{it.Findings[0].StartLine, it.Findings[1].StartLine}
		Expect(lines).To(ConsistOf(3, 7))
	})

	It("reports the findings on the draft's detail view too", func() {
		s, it := seed()
		d, err := s.QuarantineDetail(it.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Item.Findings).To(HaveLen(2))
	})

	It("never records a finding's secret value in the draft's labels", func() {
		s, it := seed()
		snap, err := s.Get(it.ID)
		Expect(err).NotTo(HaveOccurred())
		for _, l := range snap.Labels {
			Expect(l).NotTo(ContainSubstring("ghp_"))
		}
	})

	It("keeps a draft with no findings free of finding labels", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "body", "", nil, "", "agent")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		for _, l := range snap.Labels {
			Expect(l).NotTo(HavePrefix(qFindingPrefix))
		}
	})
})
