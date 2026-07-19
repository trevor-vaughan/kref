package store

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/git-bug/git-bug/entity"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

var qFind = []scan.Finding{{RuleID: "github-pat", StartLine: 1, Description: "GitHub PAT"}}

// realSecret is a full-length GitHub PAT fixture that betterleaks flags (the
// synthetic short forms are filtered); used to trip the promote-to-shared scan.
const realSecret = "ghp_012345678901234567890123456789abcdef"

// openQuestions counts unresolved question-comments on a snapshot.
func openQuestions(snap *entry.Snapshot) int {
	n := 0
	for _, c := range snap.Comments {
		if c.Question && !c.Resolved {
			n++
		}
	}
	return n
}

var _ = Describe("quarantine item reachability", func() {
	It("retiers a quarantine draft out to its destination tier", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierPersonal, "document", "T", "the body", "", qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Retier(parked.ItemID, entry.TierPersonal, "me", "human")).To(Succeed())
		snap, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal(string(entry.TierPersonal)))
	})

	It("archives a held-op quarantine item", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "doc", "T", "body")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineUpdate(id, "new body", snap.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Archive(parked.ItemID)).To(Succeed())
	})
})

var _ = Describe("quarantine item classification", func() {
	It("reads the review comment id and recognises a held op", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "doc", "T", "body")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineUpdate(id, "new body", snap.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		item, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(reviewCommentID(item)).NotTo(BeEmpty())
		Expect(isHeldOp(item)).To(BeTrue())
		Expect(destTier(item)).To(BeEmpty())
	})

	It("reads the destination tier of a draft", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierPersonal, "document", "T", "the body", "", qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		item, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(isHeldOp(item)).To(BeFalse())
		Expect(destTier(item)).To(Equal("personal"))
	})
})

var _ = Describe("ApproveQuarantine (draft)", func() {
	It("retiers the draft to its q-dest, resolves the thread, clears q-labels", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierPersonal, "document", "Fixture", "the body", "", qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.ApproveQuarantine(parked.ItemID, "ok, fixture", "me", "human")).To(Succeed())

		snap, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal(string(entry.TierPersonal)))
		Expect(snap.Labels).NotTo(ContainElement(HavePrefix("q-dest:")))
		Expect(snap.Labels).NotTo(ContainElement(HavePrefix("q-review:")))
		Expect(openQuestions(snap)).To(Equal(0))
	})

	It("surfaces a RetierBlockedError when approving a flagged draft to shared", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierShared, "document", "T", realSecret, "", qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		err = s.ApproveQuarantine(parked.ItemID, "", "me", "human")
		var blocked *RetierBlockedError
		Expect(errors.As(err, &blocked)).To(BeTrue())
	})
})

var _ = Describe("ApproveQuarantine (held op)", func() {
	It("replays a set-body onto the live target, archives the item, resolves the thread", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "doc", "T", "orig")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineUpdate(id, "new body applied", snap.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.ApproveQuarantine(parked.ItemID, "approved", "me", "human")).To(Succeed())
		tsnap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(tsnap.Body).To(Equal("new body applied"))
		Expect(openQuestions(tsnap)).To(Equal(0))
		isnap, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(isnap.Labels).To(ContainElement("q-status:approved"))
	})

	It("refuses a stale set-body replay on a todo whose version advanced", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "todo", "T", "orig")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineUpdate(id, "held body", snap.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Update(id, "advanced body", "")).To(Succeed()) // target moves after park
		err = s.ApproveQuarantine(parked.ItemID, "", "me", "human")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("stale"))
	})

	It("replays an add-comment intent onto the target", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "doc", "T", "body")
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineComment(id, "held note", false, "", qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.ApproveQuarantine(parked.ItemID, "", "me", "human")).To(Succeed())
		tsnap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		found := false
		for _, c := range tsnap.Comments {
			if c.Body == "held note" {
				found = true
			}
		}
		Expect(found).To(BeTrue())
	})
})

var _ = Describe("QuarantineDetail", func() {
	It("assembles a held set-body detail with the proposed content and current body", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "doc", "Doc", "current body")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineUpdate(id, "proposed secret body", snap.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())

		d, err := s.QuarantineDetail(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Item.HeldOp).To(BeTrue())
		Expect(d.Item.OpKind).To(Equal("set-body"))
		Expect(d.Item.Target).To(Equal(id))
		Expect(d.ProposedContent).To(Equal("proposed secret body"))
		Expect(d.CurrentBody).To(Equal("current body"))
		Expect(d.Item.Findings).NotTo(BeEmpty())
	})

	It("assembles a draft detail with the draft body as proposed content", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		parked, err := s.QuarantineNewEntry(entry.TierPersonal, "spec", "Draft", "draft body", "", qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		d, err := s.QuarantineDetail(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Item.HeldOp).To(BeFalse())
		Expect(d.Item.DestTier).To(Equal("personal"))
		Expect(d.ProposedContent).To(Equal("draft body"))
		Expect(d.CurrentBody).To(BeEmpty())
	})
})

var _ = Describe("RejectedQuarantine + RecoverQuarantine", func() {
	setup := func() (*Store, entity.Id) {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierShared, "doc", "Doc", "orig")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineUpdate(id, "secret body", snap.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		_, err = s.RejectQuarantine(parked.ItemID, "no", "human")
		Expect(err).NotTo(HaveOccurred())
		return s, parked.ItemID
	}

	It("lists rejected items and omits pending ones", func() {
		s, itemID := setup()
		DeferCleanup(func() { _ = s.Close() })
		rej, err := s.RejectedQuarantine()
		Expect(err).NotTo(HaveOccurred())
		ids := make([]string, 0, len(rej))
		for _, it := range rej {
			ids = append(ids, it.ID.String())
		}
		Expect(ids).To(ContainElement(itemID.String()))
		// a pending item does not appear
		q, err := s.QuarantineQueue()
		Expect(err).NotTo(HaveOccurred())
		Expect(q).To(BeEmpty())
	})

	It("recovers a rejected item back into the pending queue", func() {
		s, itemID := setup()
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.RecoverQuarantine(itemID)).To(Succeed())
		q, err := s.QuarantineQueue()
		Expect(err).NotTo(HaveOccurred())
		ids := make([]string, 0, len(q))
		for _, it := range q {
			ids = append(ids, it.ID.String())
		}
		Expect(ids).To(ContainElement(itemID.String())) // back in the queue
		item, err := s.Get(itemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(item.Labels).NotTo(ContainElement("q-status:rejected"))
	})

	It("purges a rejected item, excising it and its recovery file", func() {
		s, itemID := setup()
		DeferCleanup(func() { _ = s.Close() })
		// the reject wrote a recovery file under the isolated XDG_STATE_HOME
		recDir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "kref", "rejected")
		before, _ := filepath.Glob(filepath.Join(recDir, itemID.String()+"-*.md"))
		Expect(before).NotTo(BeEmpty())

		Expect(s.PurgeRejectedQuarantine(itemID)).To(Succeed())
		_, err := s.Get(itemID)
		Expect(err).To(HaveOccurred()) // gone
		after, _ := filepath.Glob(filepath.Join(recDir, itemID.String()+"-*.md"))
		Expect(after).To(BeEmpty()) // recovery file removed
	})

	It("refuses to purge a still-pending item", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, _ := s.Add(entry.TierShared, "doc", "D", "b")
		snap, _ := s.Get(id)
		parked, err := s.QuarantineUpdate(id, "secret", snap.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.PurgeRejectedQuarantine(parked.ItemID)).NotTo(Succeed()) // pending, not rejected
	})
})

var _ = Describe("QuarantineQueue", func() {
	It("lists pending items (held op + draft) and omits decided ones", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		// a held update (pending)
		id, err := s.Add(entry.TierShared, "doc", "Doc", "orig")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		p1, err := s.QuarantineUpdate(id, "secret body", snap.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())

		// a new-entry draft (pending)
		p2, err := s.QuarantineNewEntry(entry.TierPersonal, "spec", "Draft", "body", "", qFind, "agent")
		Expect(err).NotTo(HaveOccurred())

		// a held update that is then approved (decided -> archived, excluded)
		id2, err := s.Add(entry.TierShared, "doc", "D2", "x")
		Expect(err).NotTo(HaveOccurred())
		s2, err := s.Get(id2)
		Expect(err).NotTo(HaveOccurred())
		p3, err := s.QuarantineUpdate(id2, "another secret", s2.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.ApproveQuarantine(p3.ItemID, "", "me", "human")).To(Succeed())

		q, err := s.QuarantineQueue()
		Expect(err).NotTo(HaveOccurred())
		byID := map[string]QuarantineItem{}
		for _, it := range q {
			byID[it.ID.String()] = it
		}
		Expect(byID).To(HaveKey(p1.ItemID.String()))
		Expect(byID).To(HaveKey(p2.ItemID.String()))
		Expect(byID).NotTo(HaveKey(p3.ItemID.String())) // approved is not pending

		held := byID[p1.ItemID.String()]
		Expect(held.HeldOp).To(BeTrue())
		Expect(held.OpKind).To(Equal("set-body"))
		Expect(held.Target).To(Equal(id))
		Expect(held.TargetTitle).To(Equal("Doc"))
		Expect(held.Findings).NotTo(BeEmpty())

		draft := byID[p2.ItemID.String()]
		Expect(draft.HeldOp).To(BeFalse())
		Expect(draft.DestTier).To(Equal("personal"))
		Expect(draft.Kind).To(Equal("spec"))
		Expect(draft.Title).To(Equal("Draft"))
	})
})

var _ = Describe("RejectQuarantine", func() {
	It("preserves the content, leaves the target untouched, tombstones the item", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "doc", "T", "orig")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		parked, err := s.QuarantineUpdate(id, "secret body ghp_x", snap.Version, qFind, "agent")
		Expect(err).NotTo(HaveOccurred())

		path, err := s.RejectQuarantine(parked.ItemID, "not a fixture", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(ContainSubstring("rejected"))
		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("secret body ghp_x"))

		tsnap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(tsnap.Body).To(Equal("orig")) // live target untouched
		isnap, err := s.Get(parked.ItemID)
		Expect(err).NotTo(HaveOccurred())
		Expect(isnap.Labels).To(ContainElement("q-status:rejected"))
	})
})
