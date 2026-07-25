package mcpserver_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

// kref_quarantine withholds a held write's content from agents on purpose:
// approve/reject is a human decision, and the excerpt cache excludes the
// quarantine tier so a held secret never reaches an agent-visible surface.
// kref_get resolves ids across every tier, quarantine included, which handed
// back exactly what was being withheld — and the park response and
// kref_quarantine list both give an agent the ids to ask for.
var _ = Describe("kref_get on a quarantine-tier id", func() {
	const secret = "ghp_012345678901234567890123456789abcdef"

	It("refuses a held new-entry draft instead of serving its proposed body", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		find := []scan.Finding{{RuleID: "github-pat", Secret: secret, StartLine: 1}}
		parked, err := s.QuarantineNewEntry(entry.TierShared, "note", "Draft", secret, "", find, "", "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		res := call(dir, "kref_get", map[string]any{"id": parked.ItemID.String()})
		Expect(res.IsError).To(BeTrue())
		out := text(res)
		Expect(out).NotTo(ContainSubstring(secret))
		Expect(out).To(ContainSubstring("kref_quarantine"))
	})

	It("refuses a held op instead of serving its intent payload", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		target, err := s.Add(entry.TierShared, "doc", "Doc", "clean")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(target)
		Expect(err).NotTo(HaveOccurred())
		find := []scan.Finding{{RuleID: "github-pat", Secret: secret, StartLine: 1}}
		parked, err := s.QuarantineUpdate(target, "body with "+secret, snap.Version, find, "", "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		res := call(dir, "kref_get", map[string]any{"id": parked.ItemID.String()})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).NotTo(ContainSubstring(secret))
	})

	It("still serves an ordinary entry", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierShared, "doc", "Doc", "ordinary body")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		res := call(dir, "kref_get", map[string]any{"id": id.String()})
		Expect(res.IsError).To(BeFalse(), text(res))
		Expect(text(res)).To(ContainSubstring("ordinary body"))
	})
})
