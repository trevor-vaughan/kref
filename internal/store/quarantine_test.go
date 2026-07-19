package store

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

var _ = Describe("quarantine park primitives", func() {
	find := []scan.Finding{{RuleID: "github-pat", Description: "GitHub PAT", StartLine: 1}}

	It("parks a new entry as a draft with a q-dest label and a review thread on the draft", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "the body", "", find, "agent")
		Expect(err).NotTo(HaveOccurred())

		snap, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal(string(entry.TierQuarantine)))
		Expect(snap.Kind).To(Equal("spec"))
		Expect(snap.Body).To(Equal("the body"))
		Expect(snap.Labels).To(ContainElement("q-dest:shared"))
		Expect(snap.Comments).To(HaveLen(1))
		Expect(snap.Comments[0].Question).To(BeTrue())
		Expect(snap.Comments[0].Body).To(ContainSubstring("github-pat"))
	})

	It("parks a held update as an intent-item, target untouched, review thread on the target", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		target, err := s.Add(entry.TierShared, "doc", "Doc", "clean")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		snap, err := s.Get(target)
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineUpdate(target, "new body with secret", snap.Version, find, "agent")
		Expect(err).NotTo(HaveOccurred())

		item, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(item.Tier).To(Equal(string(entry.TierQuarantine)))
		Expect(item.Kind).To(Equal("quarantine"))
		Expect(item.Body).To(ContainSubstring(`"op_kind":"set-body"`))
		Expect(item.Body).To(ContainSubstring(target.String()))

		tsnap, err := s.Get(target)
		Expect(err).NotTo(HaveOccurred())
		Expect(tsnap.Body).To(Equal("clean")) // target untouched
		Expect(tsnap.Comments).To(HaveLen(1))
		Expect(tsnap.Comments[0].Question).To(BeTrue())
		Expect(tsnap.Comments[0].Body).To(ContainSubstring(parked.ItemID.String()))
	})

	It("parks a held comment as an add-comment intent-item without adding the held comment to the target", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		target, err := s.Add(entry.TierShared, "doc", "Doc", "clean")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineComment(target, "comment with secret", true, "", find, "agent")
		Expect(err).NotTo(HaveOccurred())

		item, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(item.Body).To(ContainSubstring(`"op_kind":"add-comment"`))
		Expect(item.Body).To(ContainSubstring(`"question":true`))

		// Only the review question-comment lands on the target, not the held comment.
		tsnap, err := s.Get(target)
		Expect(err).NotTo(HaveOccurred())
		Expect(tsnap.Comments).To(HaveLen(1))
		Expect(tsnap.Comments[0].Body).To(ContainSubstring("quarantined"))
	})
})

var _ = Describe("quarantine intent v2", func() {
	find := []scan.Finding{{RuleID: "github-pat", Description: "GitHub PAT", StartLine: 1}}

	It("captures base version and a q-review label for a held update", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		target, err := s.Add(entry.TierShared, "doc", "Doc", "clean")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(target)
		Expect(err).NotTo(HaveOccurred())

		parked, err := s.QuarantineUpdate(target, "new body with secret", snap.Version, find, "agent")
		Expect(err).NotTo(HaveOccurred())

		item, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(item.Kind).To(Equal("quarantine"))
		Expect(item.Labels).To(ContainElement(HavePrefix("q-review:")))

		var in intent
		Expect(json.Unmarshal([]byte(item.Body), &in)).To(Succeed())
		Expect(in.Schema).To(Equal(2))
		Expect(in.OpKind).To(Equal("set-body"))
		Expect(in.BaseVersion).To(Equal(snap.Version))
	})

	It("labels a new-entry draft with q-dest and q-review", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "the body", "", find, "agent")
		Expect(err).NotTo(HaveOccurred())
		item, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(item.Labels).To(ContainElement("q-dest:shared"))
		Expect(item.Labels).To(ContainElement(HavePrefix("q-review:")))
	})
})
