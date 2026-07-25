package main

import (
	"bytes"
	"fmt"
	"strings"

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
		body:    []string{"first body line", "second body line"},
		number:  true,
		gutterW: 4, // digits(2)=1 → 1+3
	})

	It("numbers the display lines", func() {
		c := m.content()
		Expect(c).To(ContainSubstring("1 │ first body line"))
		Expect(c).To(ContainSubstring("2 │ second body line"))
	})

	It("keeps search indices on the raw (gutter-free) lines", func() {
		Expect(searchMatches(m.lines, "second")).To(Equal([]int{1}))
	})
})

var _ = Describe("pager footer and search", func() {
	send := func(m pagerModel, msgs ...tea.Msg) pagerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(pagerModel)
	}
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	It("shows the lines-below-viewport count in the footer", func() {
		body := make([]string, 30)
		for i := range body {
			body[i] = fmt.Sprintf("L%d", i)
		}
		m := send(newPagerModel(pagerContent{title: "t", body: body}), tea.WindowSizeMsg{Width: 80, Height: 12})
		Expect(m.footerInfo()).To(ContainSubstring("↓20")) // 30 lines − (offset 0 + height 10)
	})

	It("searches with / and commits on enter", func() {
		body := make([]string, 40)
		for i := range body {
			body[i] = fmt.Sprintf("line %d", i)
		}
		body[10] = "needle here"
		body[30] = "another needle"
		m := send(newPagerModel(pagerContent{title: "t", body: body}), tea.WindowSizeMsg{Width: 80, Height: 6})
		m = send(m, key("/"))
		Expect(m.search.searching()).To(BeTrue())
		m = send(m, key("n"), key("e"), key("e"), key("d"), key("l"), key("e")) // typed while searching
		Expect(m.footerInfo()).To(ContainSubstring("/needle"))
		m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.search.searching()).To(BeFalse())
		Expect(m.search.matches).To(Equal([]int{10, 30}))
		Expect(m.sv.YOffset()).To(Equal(10)) // jumped to the first match
		m = send(m, key("n"))                // next
		Expect(m.sv.YOffset()).To(Equal(30))
	})
})

var _ = Describe("pager navigation", func() {
	makeModel := func() pagerModel {
		body := make([]string, 50)
		for i := range body {
			body[i] = fmt.Sprintf("line %d", i+1)
		}
		m := newPagerModel(pagerContent{
			title: "t", body: body, number: true, gutterW: 5,
		})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		return nm.(pagerModel)
	}
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	It("jumps to a line with <n>g", func() {
		m := makeModel()
		a, _ := m.Update(key("5"))
		b, _ := a.(pagerModel).Update(key("g"))
		Expect(b.(pagerModel).sv.YOffset()).To(Equal(4)) // gotoLine(5) → offset 5-1
	})

	It("goes to the top with gg and the bottom with G", func() {
		m := makeModel()
		bottom, _ := m.Update(key("G"))
		Expect(bottom.(pagerModel).sv.YOffset()).To(BeNumerically(">", 0))

		g1, _ := bottom.(pagerModel).Update(key("g"))
		g2, _ := g1.(pagerModel).Update(key("g"))
		Expect(g2.(pagerModel).sv.YOffset()).To(Equal(0))
	})

	It("goes to the top with a single g too, as the list cockpit does", func() {
		m := makeModel()
		bottom, _ := m.Update(key("G"))
		Expect(bottom.(pagerModel).sv.YOffset()).To(BeNumerically(">", 0))

		top, _ := bottom.(pagerModel).Update(key("g"))
		Expect(top.(pagerModel).sv.YOffset()).To(Equal(0))
	})

	It("clamps <n>g past the last line to the bottom", func() {
		m := makeModel()
		a, _ := m.Update(key("9"))
		a, _ = a.(pagerModel).Update(key("9"))
		a, _ = a.(pagerModel).Update(key("9"))
		c, _ := a.(pagerModel).Update(key("g"))
		// 50 lines, viewport height 8 → max offset 42
		Expect(c.(pagerModel).sv.YOffset()).To(Equal(42))
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
		Expect(bottom.(pagerModel).sv.YOffset()).To(BeNumerically(">", 0))

		a, _ := bottom.(pagerModel).Update(key("5"))
		Expect(a.(pagerModel).footerInfo()).NotTo(ContainSubstring("5g"))
		b, _ := a.(pagerModel).Update(key("g"))
		// The digit is dropped, so the g is a bare g: top, not line 5.
		Expect(b.(pagerModel).sv.YOffset()).To(Equal(0))
	})

	It("keeps the standard gg/G motions", func() {
		m := makeModel()
		bottom, _ := m.Update(key("G"))
		Expect(bottom.(pagerModel).sv.YOffset()).To(BeNumerically(">", 0))
		g1, _ := bottom.(pagerModel).Update(key("g"))
		g2, _ := g1.(pagerModel).Update(key("g"))
		Expect(g2.(pagerModel).sv.YOffset()).To(Equal(0))
	})
})

var _ = Describe("pager visibleWindow", func() {
	makeModel := func() pagerModel {
		body := make([]string, 50)
		for i := range body {
			body[i] = fmt.Sprintf("line %d", i+1)
		}
		m := newPagerModel(pagerContent{title: "t", body: body})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		return nm.(pagerModel)
	}
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	It("returns the viewport-height slice of content at the top", func() {
		m := makeModel()
		w := m.visibleWindow()
		Expect(w).To(HaveLen(8)) // WindowSizeMsg height 10 − title − footer
		Expect(w[0]).To(Equal("line 1"))
	})

	It("tracks scroll position after jumping to the bottom", func() {
		m := makeModel()
		nm, _ := m.Update(key("G"))
		w := nm.(pagerModel).visibleWindow()
		Expect(w[len(w)-1]).To(Equal("line 50"))
	})

	It("returns nil before the first WindowSizeMsg", func() {
		m := newPagerModel(pagerContent{title: "t", body: []string{"x"}})
		Expect(m.visibleWindow()).To(BeNil())
	})
})

var _ = Describe("pager exit echo", func() {
	It("writes the visible window to the given writer", func() {
		body := make([]string, 50)
		for i := range body {
			body[i] = fmt.Sprintf("line %d", i+1)
		}
		m := newPagerModel(pagerContent{title: "t", body: body})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		var buf bytes.Buffer
		echoExit(&buf, nm.(pagerModel))
		Expect(buf.String()).To(ContainSubstring("line 1"))
		Expect(buf.String()).To(HaveSuffix("\n"))
	})

	It("writes nothing for a never-sized model", func() {
		var buf bytes.Buffer
		echoExit(&buf, newPagerModel(pagerContent{title: "t", body: []string{"x"}}))
		Expect(buf.String()).To(BeEmpty())
	})
})

var _ = Describe("pager help overlay", func() {
	It("lists the core keys", func() {
		out := strings.Join(pagerHelpRows(true), "\n")
		for _, k := range []string{"scroll", "search", "quit", "top/bottom"} {
			Expect(out).To(ContainSubstring(k))
		}
	})

	// Update drops the digits when there is no gutter, so advertising <n>g for
	// `kref search` promised a key that does nothing.
	It("offers <n>g only when there is a line-number gutter to aim at", func() {
		Expect(strings.Join(pagerHelpRows(true), "\n")).To(ContainSubstring("<n>g"))
		Expect(strings.Join(pagerHelpRows(false), "\n")).NotTo(ContainSubstring("<n>g"))
	})

	It("wires the gutter flag through from the content", func() {
		plain := newPagerModel(pagerContent{title: "t", body: []string{"a"}})
		numbered := newPagerModel(pagerContent{title: "t", body: []string{"a"}, number: true, gutterW: 4})
		plain.sv.Resize(80, 24)
		numbered.sv.Resize(80, 24)
		plain.sv.ToggleHelp()
		numbered.sv.ToggleHelp()
		Expect(plain.View()).NotTo(ContainSubstring("<n>g"))
		Expect(numbered.View()).To(ContainSubstring("<n>g"))
	})
})

var _ = Describe("pager help popup behavior", func() {
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	makeModel := func() pagerModel {
		m := newPagerModel(pagerContent{title: "t", body: []string{"a", "b", "c"}})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
		return nm.(pagerModel)
	}

	It("renders the overlay box when help is open", func() {
		m := makeModel()
		open, _ := m.Update(key("?"))
		Expect(open.(pagerModel).View()).To(ContainSubstring("scroll"))
	})

	It("closes the overlay on the next key without scrolling", func() {
		m := makeModel()
		open, _ := m.Update(key("?"))
		Expect(open.(pagerModel).sv.HelpOpen()).To(BeTrue())
		closed, _ := open.(pagerModel).Update(key("j"))
		Expect(closed.(pagerModel).sv.HelpOpen()).To(BeFalse())
		Expect(closed.(pagerModel).sv.YOffset()).To(Equal(0))
	})

	It("closes the overlay on q or esc without quitting the pager", func() {
		esc := tea.KeyMsg{Type: tea.KeyEsc}
		for _, dismiss := range []tea.KeyMsg{key("q"), esc} {
			m := makeModel()
			open, _ := m.Update(key("?"))
			Expect(open.(pagerModel).sv.HelpOpen()).To(BeTrue())
			closed, cmd := open.(pagerModel).Update(dismiss)
			Expect(closed.(pagerModel).sv.HelpOpen()).To(BeFalse())
			Expect(cmd).To(BeNil()) // dismissed the modal, did not quit
		}
	})

	It("hard-quits on ctrl+c even with the overlay open", func() {
		m := makeModel()
		open, _ := m.Update(key("?"))
		_, cmd := open.(pagerModel).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		Expect(cmd).NotTo(BeNil()) // tea.Quit
	})

	It("does not put the long help string in the footer", func() {
		m := makeModel()
		Expect(m.footerInfo()).NotTo(ContainSubstring("scroll"))
		Expect(m.footerInfo()).To(ContainSubstring("? keys"))
	})
})

var _ = Describe("pager horizontal scroll", func() {
	It("pans right to reveal the tail of a line wider than the viewport", func() {
		long := "START" + strings.Repeat("-", 100) + "END"
		m := newPagerModel(pagerContent{title: "t", body: []string{long}})
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 40, Height: 8})
		Expect(mm.(pagerModel).View()).NotTo(ContainSubstring("END"))
		for range 20 {
			mm, _ = mm.(pagerModel).Update(tea.KeyMsg{Type: tea.KeyRight})
		}
		Expect(mm.(pagerModel).View()).To(ContainSubstring("END"))
	})
})
