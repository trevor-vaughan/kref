package main

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/tui"
)

var _ = Describe("pagerSearch", func() {
	newSV := func() tui.ScrollView {
		sv := tui.NewScrollView("t")
		sv.SetContent("alpha\nbeta\nalpha two\ngamma\n")
		sv.Resize(40, 2)
		return sv
	}
	typeRunes := func(ps *pagerSearch, s string, matcher func(string) []int, sv *tui.ScrollView) {
		for _, r := range s {
			ps.input(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, matcher, sv)
		}
	}

	It("captures a query, commits on enter, and cycles matches", func() {
		sv := newSV()
		matcher := sv.Matches
		var ps pagerSearch
		ps.start()
		Expect(ps.searching()).To(BeTrue())
		typeRunes(&ps, "alpha", matcher, &sv)
		Expect(ps.query).To(Equal("alpha"))
		ps.input(tea.KeyMsg{Type: tea.KeyEnter}, matcher, &sv)
		Expect(ps.searching()).To(BeFalse())
		Expect(ps.matches).To(Equal([]int{0, 2}))
		Expect(sv.YOffset()).To(Equal(0)) // jumped to the first match
		ps.cycle(1, &sv)
		Expect(sv.YOffset()).To(Equal(2)) // next match
		ps.cycle(1, &sv)                  // wraps back to the first
		Expect(sv.YOffset()).To(Equal(0))
	})

	It("esc cancels without committing", func() {
		sv := newSV()
		var ps pagerSearch
		ps.start()
		ps.query = "alpha"
		ps.input(tea.KeyMsg{Type: tea.KeyEsc}, func(string) []int { return []int{0} }, &sv)
		Expect(ps.searching()).To(BeFalse())
		Expect(ps.query).To(Equal(""))
		Expect(ps.matches).To(BeEmpty())
	})

	It("footer shows the prompt while active and the count after", func() {
		var ps pagerSearch
		ps.start()
		ps.query = "foo"
		Expect(ps.footer()).To(Equal("/foo"))
		ps.active = false
		ps.matches, ps.idx = []int{1, 2, 3}, 0
		Expect(ps.footer()).To(Equal("match 1/3"))
	})

	It("refresh recomputes matches for the current query and clamps the index", func() {
		sv := newSV()
		var ps pagerSearch
		ps.query, ps.idx = "alpha", 5
		ps.refresh(sv.Matches)
		Expect(ps.matches).To(Equal([]int{0, 2}))
		Expect(ps.idx).To(Equal(0)) // clamped
	})
})
