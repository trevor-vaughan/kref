package main

import (
	"bytes"
	"errors"
	"strings"

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
		m := newReviewModel(f, q, 0, true, 60, testReviewer, "human")
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
		m := newReviewModel(f, q, 0, true, 60, testReviewer, "human")
		m.sv.Resize(80, 24)
		m.Update(key('r'))
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(f.rejected).To(ContainElement("q111"))
		Expect(m.queue).To(HaveLen(0)) // queue now clear
	})

	// Queue navigation moved from n/p to ]/[ so that n/N mean "next/previous
	// search match" on this surface as they already did on the other three.
	It("]/[ move through the queue and clamp at the ends", func() {
		f, q := setup("q111", "q222", "q333")
		m := newReviewModel(f, q, 0, true, 60, testReviewer, "human")
		m.sv.Resize(80, 24)
		m.Update(key('[')) // clamp low
		Expect(m.idx).To(Equal(0))
		m.Update(key(']'))
		Expect(m.idx).To(Equal(1))
		m.Update(key(']'))
		m.Update(key(']')) // clamp high at 2
		Expect(m.idx).To(Equal(2))
	})

	It("toggles colour from the view-options menu, not t", func() {
		f, q := setup("q111")
		m := newReviewModel(f, q, 0, true, 60, testReviewer, "human")
		m.sv.Resize(80, 24)
		before := m.sv.Plain()

		m.Update(key('t'))
		Expect(m.sv.Plain()).To(Equal(before)) // t no longer means anything

		m.Update(key(','))
		Expect(m.View()).To(ContainSubstring("view options"))
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.sv.Plain()).NotTo(Equal(before))
		_, uc, err := loadUserConfigForEdit()
		Expect(err).NotTo(HaveOccurred())
		Expect(uc.Color).NotTo(BeNil())
		Expect(*uc.Color).To(Equal(m.color))
	})

	It("exits with open-target on 'o'", func() {
		f, q := setup("q111")
		m := newReviewModel(f, q, 0, true, 60, testReviewer, "human")
		m.sv.Resize(80, 24)
		_, cmd := m.Update(key('o'))
		Expect(cmd).NotTo(BeNil())
		Expect(m.result.action).To(Equal("open"))
		Expect(m.result.target).To(Equal(entity.Id("aaaa")))
	})

	It("dismisses the help popup on esc instead of quitting", func() {
		f, q := setup("q111")
		m := newReviewModel(f, q, 0, true, 60, testReviewer, "human")
		m.sv.Resize(80, 24)
		m.Update(key('?'))
		Expect(m.sv.HelpOpen()).To(BeTrue())
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		Expect(m.sv.HelpOpen()).To(BeFalse())
		Expect(cmd).To(BeNil())
	})
})

var _ = Describe("review viewer decision attribution", func() {
	decide := func(k rune) *fakeActions {
		GinkgoHelper()
		f := newFake()
		q := []store.QuarantineItem{{ID: "q1", HeldOp: true, OpKind: "add-comment", Target: "e1"}}
		f.queue = q
		m := newReviewModel(f, q, 0, false, 80, testReviewer, "human")
		m.sv.Resize(80, 24)
		m.Update(key(k))
		m.input.SetValue("a note")
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return f
	}

	It("approves as the resolved reviewer, not a placeholder", func() {
		f := decide('a')
		Expect(f.approvedBy).To(Equal(testReviewer))
		Expect(f.approvedKind).To(Equal("human"))
	})

	It("rejects as the resolved reviewer, not an empty string", func() {
		f := decide('r')
		Expect(f.rejectedBy).To(Equal(testReviewer))
		Expect(f.rejectedKind).To(Equal("human"))
	})
})

// n meant "next search match" in the other three surfaces but "next held write"
// here. Queue navigation moves to ]/[ so n/N mean one thing everywhere.
var _ = Describe("review viewer navigation", func() {
	twoItems := func() *reviewModel {
		f := newFake()
		q := []store.QuarantineItem{
			{ID: "q1", HeldOp: true, OpKind: "set-body", Target: "e1"},
			{ID: "q2", HeldOp: true, OpKind: "set-body", Target: "e2"},
		}
		for _, it := range q {
			f.details[it.ID] = store.QuarantineDetail{
				Item:            it,
				CurrentBody:     strings.Repeat("filler\n", 20),
				ProposedContent: strings.Repeat("filler\n", 20) + "needle here\n",
			}
		}
		f.queue = q
		m := newReviewModel(f, q, 0, false, 80, testReviewer, "human")
		m.sv.Resize(80, 24)
		return m
	}

	It("moves through the queue with ] and [", func() {
		m := twoItems()
		m.Update(key(']'))
		Expect(m.idx).To(Equal(1))
		m.Update(key('['))
		Expect(m.idx).To(Equal(0))
	})

	It("uses n/N for search matches, not queue navigation", func() {
		m := twoItems()
		m.Update(key('/'))
		for _, r := range "needle" {
			m.Update(key(r))
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.idx).To(Equal(0), "a search must not move the queue cursor")
		Expect(m.View()).To(MatchRegexp(`match \d+/\d+`))
	})
})

// loadDetail recorded the error but returned before touching the content, so the
// viewer kept rendering the previous held write under the new item's index —
// the worst possible framing for a screen whose job is deciding on one specific
// write.
var _ = Describe("review viewer detail failure", func() {
	It("clears the previous item's content when the next one fails to load", func() {
		f := newFake()
		q := []store.QuarantineItem{
			{ID: "q1", HeldOp: true, OpKind: "set-body", Target: "e1", TargetTitle: "First"},
			{ID: "q2", HeldOp: true, OpKind: "set-body", Target: "e2", TargetTitle: "Second"},
		}
		f.queue = q
		f.details[entity.Id("q1")] = store.QuarantineDetail{Item: q[0], ProposedContent: "unique-first-body"}
		m := newReviewModel(f, q, 0, false, 80, testReviewer, "human")
		m.sv.Resize(80, 24)
		Expect(m.View()).To(ContainSubstring("unique-first-body"))

		f.detailErr = errors.New("held payload unreadable")
		m.Update(key(']'))
		Expect(m.err).To(ContainSubstring("held payload unreadable"))
		Expect(m.View()).NotTo(ContainSubstring("unique-first-body"), "stale detail from the previous item")
	})
})
