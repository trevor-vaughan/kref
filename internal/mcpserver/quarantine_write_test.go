package mcpserver_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

// A held item is the artefact a human is about to judge, so an agent must not
// be able to change it between the look and the decision: approve promotes the
// item AS IT THEN STANDS. Discussion is the exception — the park message tells
// the agent to explain itself on the review thread, and for a draft that thread
// lives on the item itself.
var _ = Describe("MCP writes against a quarantine-tier item", func() {
	const secret = "ghp_012345678901234567890123456789abcdef" // DevSkim: ignore DS117838

	seed := func() (dir string, item entity.Id) {
		GinkgoHelper()
		dir = gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		find := []scan.Finding{{RuleID: "github-pat", Secret: secret, StartLine: 1}}
		parked, err := s.QuarantineNewEntry(entry.TierPersonal, "note", "Draft", secret, "", find, "", "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		return dir, parked.ItemID
	}

	bodyOf := func(dir string, id entity.Id) *entry.Snapshot {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = s.Close() }()
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		return snap
	}

	It("refuses to swap the held body out from under the reviewer", func() {
		dir, item := seed()
		res := call(dir, "kref_update", map[string]any{"id": item.String(), "body": "innocuous", "model": "test-model"})
		Expect(res.IsError).To(BeTrue())
		Expect(bodyOf(dir, item).Body).To(Equal(secret), "the reviewed content must be unchanged")
	})

	It("refuses to patch the held body", func() {
		dir, item := seed()
		res := call(dir, "kref_patch", map[string]any{
			"id": item.String(), "diff": "@@ -1 +1 @@\n-" + secret + "\n+innocuous\n", "model": "test-model",
		})
		Expect(res.IsError).To(BeTrue())
		Expect(bodyOf(dir, item).Body).To(Equal(secret))
	})

	It("refuses to redirect where an approved draft would land", func() {
		dir, item := seed()
		res := call(dir, "kref_update", map[string]any{
			"id":            item.String(),
			"model":         "test-model",
			"add_labels":    []any{"q-dest:shared"},
			"remove_labels": []any{"q-dest:personal"},
		})
		Expect(res.IsError).To(BeTrue())
		Expect(bodyOf(dir, item).Labels).To(ContainElement("q-dest:personal"))
		Expect(bodyOf(dir, item).Labels).NotTo(ContainElement("q-dest:shared"))
	})

	It("refuses to strip the findings a reviewer decides on", func() {
		dir, item := seed()
		res := call(dir, "kref_update", map[string]any{
			"id": item.String(), "remove_labels": []any{"q-finding:github-pat:1"}, "model": "test-model",
		})
		Expect(res.IsError).To(BeTrue())
		Expect(bodyOf(dir, item).Labels).To(ContainElement("q-finding:github-pat:1"))
	})

	It("refuses to hide a pending review from the queue", func() {
		dir, item := seed()
		for _, action := range []string{"archive", "delete", "set_status"} {
			args := map[string]any{"id": item.String(), "action": action, "model": "test-model"}
			if action == "set_status" {
				args["status"] = "obsolete"
			}
			res := call(dir, "kref_lifecycle", args)
			Expect(res.IsError).To(BeTrue(), "lifecycle "+action+" must be refused")
		}
		snap := bodyOf(dir, item)
		Expect(snap.Archived).To(BeFalse())
		Expect(snap.Deleted).To(BeFalse())
	})

	It("refuses to supersede a held item", func() {
		dir, item := seed()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		other, err := s.Add(entry.TierPersonal, "note", "Other", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		Expect(call(dir, "kref_supersede", map[string]any{
			"old": item.String(), "new": other.String(), "model": "test-model",
		}).IsError).To(BeTrue())
		Expect(call(dir, "kref_supersede", map[string]any{
			"old": other.String(), "new": item.String(), "model": "test-model",
		}).IsError).To(BeTrue())
	})

	It("still lets an agent explain itself on the review thread", func() {
		dir, item := seed()
		res := call(dir, "kref_comment", map[string]any{
			"id": item.String(), "action": "add", "model": "test-model",
			"body": "This is a synthetic fixture, not a live credential — safe to reject.",
		})
		Expect(res.IsError).To(BeFalse(), text(res))
		Expect(bodyOf(dir, item).Comments).To(HaveLen(2)) // the review question + this reply
	})
})
