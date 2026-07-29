package mcpserver_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

// The existing quarantine specs cover a held op, whose findings ride in its
// intent JSON. A parked NEW entry is the other half of the queue and the shape
// kref_remember produces, so it has to name its rule too — otherwise an agent
// whose write was held is told only that it was held, never by which rule.
var _ = Describe("kref_quarantine on a new-entry draft", func() {
	const secret = "ghp_012345678901234567890123456789abcdef" // DevSkim: ignore DS117838

	seed := func() (dir, qid string) {
		GinkgoHelper()
		dir = gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		find := []scan.Finding{{RuleID: "github-pat", Description: "GitHub PAT", Secret: secret, StartLine: 2}}
		parked, err := s.QuarantineNewEntry(entry.TierShared, "note", "Draft", "line\n"+secret, "", find, "", "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		return dir, parked.ItemID.String()
	}

	It("names the flagging rule when listing a held draft", func() {
		dir, qid := seed()
		res := call(dir, "kref_quarantine", map[string]any{"action": "list"})
		Expect(res.IsError).To(BeFalse())
		out := text(res)
		Expect(out).To(ContainSubstring(qid[:12]))
		Expect(out).To(ContainSubstring("github-pat"))
		Expect(out).NotTo(ContainSubstring(secret))
	})

	It("reports the finding's rule and line on show, still withholding the value", func() {
		dir, qid := seed()
		res := call(dir, "kref_quarantine", map[string]any{"action": "show", "id": qid})
		Expect(res.IsError).To(BeFalse())
		out := text(res)
		Expect(out).To(ContainSubstring("github-pat"))
		Expect(out).To(ContainSubstring("line 2"))
		Expect(out).To(ContainSubstring("withheld"))
		Expect(out).NotTo(ContainSubstring(secret))
	})
})
