package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("searchMatches", func() {
	lines := []string{"alpha beta", "gamma", "Beta carotene", "delta"}

	It("returns case-insensitive matching line indices in order", func() {
		Expect(searchMatches(lines, "beta")).To(Equal([]int{0, 2}))
	})

	It("returns nil for an empty query or no match", func() {
		Expect(searchMatches(lines, "")).To(BeNil())
		Expect(searchMatches(lines, "zeta")).To(BeNil())
	})
})

var _ = Describe("pager gutter", func() {
	m := newPagerModel(pagerContent{
		title:   "t",
		header:  []string{"ID  x", ""},
		body:    []string{"first body line", "second body line"},
		number:  true,
		gutterW: 4, // digits(2)=1 → 1+3
	})

	It("numbers body lines and blanks the gutter for header lines", func() {
		c := m.content()
		Expect(c).To(ContainSubstring("1 │ first body line"))
		Expect(c).To(ContainSubstring("2 │ second body line"))
		Expect(c).To(ContainSubstring("│ ID  x"))
	})

	It("keeps search indices on the raw (gutter-free) lines", func() {
		Expect(searchMatches(m.lines, "second")).To(Equal([]int{3}))
	})
})

var _ = Describe("pager navigation", func() {
	makeModel := func() pagerModel {
		body := make([]string, 50)
		for i := range body {
			body[i] = fmt.Sprintf("line %d", i+1)
		}
		m := newPagerModel(pagerContent{
			title: "t", header: []string{"H", ""}, body: body, number: true, gutterW: 5,
		})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		return nm.(pagerModel)
	}
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	It("jumps to a body line with <n>g", func() {
		m := makeModel()
		a, _ := m.Update(key("5"))
		b, _ := a.(pagerModel).Update(key("g"))
		Expect(b.(pagerModel).vp.YOffset).To(Equal(6)) // bodyStart 2 + (5-1)
	})

	It("goes to the top with gg and the bottom with G", func() {
		m := makeModel()
		bottom, _ := m.Update(key("G"))
		Expect(bottom.(pagerModel).vp.YOffset).To(BeNumerically(">", 0))

		g1, _ := bottom.(pagerModel).Update(key("g"))
		g2, _ := g1.(pagerModel).Update(key("g"))
		Expect(g2.(pagerModel).vp.YOffset).To(Equal(0))
	})

	It("clamps <n>g past the last body line to the bottom", func() {
		m := makeModel()
		a, _ := m.Update(key("9"))
		a, _ = a.(pagerModel).Update(key("9"))
		a, _ = a.(pagerModel).Update(key("9"))
		c, _ := a.(pagerModel).Update(key("g"))
		// total 52 lines, viewport height 8 → max offset 44
		Expect(c.(pagerModel).vp.YOffset).To(Equal(44))
	})
})

var _ = Describe("lean pager (numbering off)", func() {
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	makeModel := func() pagerModel {
		body := make([]string, 50)
		for i := range body {
			body[i] = fmt.Sprintf("line %d", i+1)
		}
		m := newPagerModel(pagerContent{title: "t", body: body})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		return nm.(pagerModel)
	}

	It("ignores <n>g digits so there is no line-jump shortcut", func() {
		m := makeModel()
		bottom, _ := m.Update(key("G"))
		was := bottom.(pagerModel).vp.YOffset
		Expect(was).To(BeNumerically(">", 0))

		a, _ := bottom.(pagerModel).Update(key("5"))
		Expect(a.(pagerModel).footer()).NotTo(ContainSubstring("5g"))
		b, _ := a.(pagerModel).Update(key("g"))
		Expect(b.(pagerModel).vp.YOffset).To(Equal(was)) // no jump to line 5
	})

	It("keeps the standard gg/G motions", func() {
		m := makeModel()
		bottom, _ := m.Update(key("G"))
		Expect(bottom.(pagerModel).vp.YOffset).To(BeNumerically(">", 0))
		g1, _ := bottom.(pagerModel).Update(key("g"))
		g2, _ := g1.(pagerModel).Update(key("g"))
		Expect(g2.(pagerModel).vp.YOffset).To(Equal(0))
	})
})

var _ = Describe("pager refresh", func() {
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	makeModel := func(reload func() (pagerContent, error)) pagerModel {
		m := newPagerModel(pagerContent{
			title:   "t",
			header:  []string{"H", ""},
			body:    []string{"old line"},
			number:  true,
			gutterW: 4,
			reload:  reload,
		})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		return nm.(pagerModel)
	}

	It("replaces the content on r and notes the refresh in the footer", func() {
		m := makeModel(func() (pagerContent, error) {
			return pagerContent{
				title:   "t2",
				header:  []string{"H2", ""},
				body:    []string{"new line", "another"},
				number:  true,
				gutterW: 4,
			}, nil
		})
		nm, _ := m.Update(key("r"))
		rm := nm.(pagerModel)
		Expect(rm.content()).To(ContainSubstring("new line"))
		Expect(rm.content()).NotTo(ContainSubstring("old line"))
		Expect(rm.title).To(Equal("t2"))
		Expect(rm.footer()).To(ContainSubstring("refreshed"))
	})

	It("keeps the old content and reports the error when reload fails", func() {
		m := makeModel(func() (pagerContent, error) {
			return pagerContent{}, fmt.Errorf("entry vanished")
		})
		nm, _ := m.Update(key("r"))
		rm := nm.(pagerModel)
		Expect(rm.content()).To(ContainSubstring("old line"))
		Expect(rm.footer()).To(ContainSubstring("entry vanished"))
	})

	It("scrolls without refreshing when no reload is wired", func() {
		m := makeModel(nil)
		nm, _ := m.Update(key("r"))
		Expect(nm.(pagerModel).content()).To(ContainSubstring("old line"))
	})

	It("clears the refresh notice on the next keypress", func() {
		m := makeModel(func() (pagerContent, error) {
			return pagerContent{title: "t", body: []string{"new"}, number: true, gutterW: 4}, nil
		})
		a, _ := m.Update(key("r"))
		b, _ := a.(pagerModel).Update(key("j"))
		Expect(b.(pagerModel).footer()).NotTo(ContainSubstring("refreshed"))
	})
})
