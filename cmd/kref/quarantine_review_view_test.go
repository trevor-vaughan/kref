package main

import (
	"bytes"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

var _ = Describe("renderQuarantineReview", func() {
	It("renders a set-body review as a diff of current vs proposed", func() {
		d := store.QuarantineDetail{
			Item: store.QuarantineItem{
				HeldOp: true, OpKind: "set-body", Target: "aaaa", TargetTitle: "Doc",
				Findings: []scan.Finding{{RuleID: "github-pat", StartLine: 1}},
			},
			CurrentBody:     "old line",
			ProposedContent: "new secret line",
		}
		var b bytes.Buffer
		renderQuarantineReview(&b, d, false, 60)
		out := b.String()
		Expect(out).To(ContainSubstring("held set-body"))
		Expect(out).To(ContainSubstring("github-pat (line 1)"))
		Expect(out).To(ContainSubstring("proposed change"))
		Expect(out).To(ContainSubstring("- old line"))
		Expect(out).To(ContainSubstring("+ new secret line"))
	})

	It("renders a draft review with the proposed body", func() {
		d := store.QuarantineDetail{
			Item:            store.QuarantineItem{HeldOp: false, Kind: "spec", DestTier: "personal", Title: "Draft"},
			ProposedContent: "the draft body content",
		}
		var b bytes.Buffer
		renderQuarantineReview(&b, d, false, 60)
		out := b.String()
		Expect(out).To(ContainSubstring("new spec → personal"))
		Expect(out).To(ContainSubstring("the draft body content"))
	})
})

var _ = Describe("reviewModel", func() {
	item := func(id string) store.QuarantineItem {
		return store.QuarantineItem{ID: entity.Id(id), HeldOp: true, OpKind: "set-body", Target: "aaaa", TargetTitle: "Doc",
			Findings: []scan.Finding{{RuleID: "github-pat", StartLine: 1}}}
	}
	detailFor := func(id string) store.QuarantineDetail {
		return store.QuarantineDetail{Item: item(id), CurrentBody: "old", ProposedContent: "new"}
	}
	setup := func(ids ...string) (*fakeActions, []store.QuarantineItem) {
		f := newFake()
		var q []store.QuarantineItem
		for _, id := range ids {
			q = append(q, item(id))
			f.details[entity.Id(id)] = detailFor(id)
		}
		f.queue = q
		return f, q
	}

	It("approves via 'a' + note and advances (queue shrinks)", func() {
		f, q := setup("q111", "q222")
		m := newReviewModel(f, q, 0, true, 60)
		m.sv.Resize(80, 24)
		m.Update(key('a'))
		Expect(m.mode).To(Equal(listModeNote))
		m.input.SetValue("ok")
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(f.approved).To(ContainElement("q111"))
		Expect(m.queue).To(HaveLen(1))                        // decided item removed
		Expect(m.detail.Item.ID).To(Equal(entity.Id("q222"))) // advanced to next
	})

	It("rejects via 'r' + reason", func() {
		f, q := setup("q111")
		m := newReviewModel(f, q, 0, true, 60)
		m.sv.Resize(80, 24)
		m.Update(key('r'))
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(f.rejected).To(ContainElement("q111"))
		Expect(m.queue).To(HaveLen(0)) // queue now clear
	})

	It("n/p move through the queue and clamp at the ends", func() {
		f, q := setup("q111", "q222", "q333")
		m := newReviewModel(f, q, 0, true, 60)
		m.sv.Resize(80, 24)
		m.Update(key('p')) // clamp low
		Expect(m.idx).To(Equal(0))
		m.Update(key('n'))
		Expect(m.idx).To(Equal(1))
		m.Update(key('n'))
		m.Update(key('n')) // clamp high at 2
		Expect(m.idx).To(Equal(2))
	})

	It("exits with open-target on 'o'", func() {
		f, q := setup("q111")
		m := newReviewModel(f, q, 0, true, 60)
		m.sv.Resize(80, 24)
		_, cmd := m.Update(key('o'))
		Expect(cmd).NotTo(BeNil())
		Expect(m.result.action).To(Equal("open"))
		Expect(m.result.target).To(Equal(entity.Id("aaaa")))
	})

	It("dismisses the help popup on esc instead of quitting", func() {
		f, q := setup("q111")
		m := newReviewModel(f, q, 0, true, 60)
		m.sv.Resize(80, 24)
		m.Update(key('?'))
		Expect(m.sv.HelpOpen()).To(BeTrue())
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		Expect(m.sv.HelpOpen()).To(BeFalse())
		Expect(cmd).To(BeNil())
	})
})
