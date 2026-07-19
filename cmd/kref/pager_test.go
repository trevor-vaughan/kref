package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/git-bug/git-bug/entity"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/outline"
	"github.com/trevor-vaughan/kref/internal/render"
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

// pagerWith builds a markdown pager model over body, with a real foldRender that
// folds the raw markdown via outline before rendering (raw lines, no glamour, no
// gutter), so a heading's rendered offset is its folded-line index. Used across
// the fold tests.
func pagerWith(body string) pagerModel {
	fr := func(folded map[string]bool) foldedBody {
		src := outline.Parse(body).Render(folded)
		var rh []renderedHeading
		for _, h := range outline.Parse(src).Headings() {
			rh = append(rh, renderedHeading{path: h.Path, line: h.Line})
		}
		return foldedBody{lines: strings.Split(src, "\n"), headings: rh}
	}
	pc := pagerContent{title: "t", rawBody: body, markdown: true, foldRender: fr}
	pc.body = fr(map[string]bool{}).lines
	return newPagerModel(pc)
}

var _ = Describe("pager fold", func() {
	send := func(m pagerModel, msgs ...tea.Msg) pagerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(pagerModel)
	}
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	size := tea.WindowSizeMsg{Width: 80, Height: 12}

	It("parses an outline from the raw markdown body", func() {
		m := pagerWith("# Doc\n\n## A\n\nbody\n\n## B\n\nmore\n")
		Expect(m.outline).NotTo(BeNil())
		Expect(m.outline.Headings()).To(HaveLen(3))
	})

	It("has no outline for non-markdown content", func() {
		m := newPagerModel(pagerContent{title: "t", body: []string{"plain"}})
		Expect(m.outline).To(BeNil())
	})

	It("space folds the section at the viewport top", func() {
		m := send(pagerWith("## A\n\nalpha\n\n## B\n\nbravo\n"), size)
		Expect(strings.Join(m.sv.VisibleWindow(), "\n")).To(ContainSubstring("alpha"))
		m = send(m, key(" ")) // cursor at top → ## A folds
		win := strings.Join(m.sv.VisibleWindow(), "\n")
		Expect(win).NotTo(ContainSubstring("alpha")) // A's content hidden
		Expect(win).To(ContainSubstring("bravo"))    // B still open
	})

	It("C collapses all sections and O expands them", func() {
		m := send(pagerWith("## A\n\nalpha\n\n## B\n\nbravo\n"), size)
		m = send(m, key("C"))
		win := strings.Join(m.sv.VisibleWindow(), "\n")
		Expect(win).NotTo(ContainSubstring("alpha"))
		Expect(win).NotTo(ContainSubstring("bravo"))
		m = send(m, key("O"))
		win = strings.Join(m.sv.VisibleWindow(), "\n")
		Expect(win).To(ContainSubstring("alpha"))
		Expect(win).To(ContainSubstring("bravo"))
	})

	It("c closes and o opens the top-visible section", func() {
		m := send(pagerWith("## A\n\nalpha\n\n## B\n\nbravo\n"), size)
		m = send(m, key("c")) // close ## A
		Expect(strings.Join(m.sv.VisibleWindow(), "\n")).NotTo(ContainSubstring("alpha"))
		m = send(m, key("o")) // open it again
		Expect(strings.Join(m.sv.VisibleWindow(), "\n")).To(ContainSubstring("alpha"))
	})

	It("does not fold non-markdown content; space pages down", func() {
		body := make([]string, 50)
		for i := range body {
			body[i] = fmt.Sprintf("line %d", i)
		}
		m := send(newPagerModel(pagerContent{title: "t", body: body}), tea.WindowSizeMsg{Width: 80, Height: 10})
		before := m.sv.YOffset()
		m = send(m, key(" "))
		Expect(m.sv.YOffset()).To(BeNumerically(">", before)) // space paged, did not fold
	})

	It("Tab jumps to the next heading and Shift-Tab to the previous", func() {
		m := send(pagerWith("## A\n\na1\na2\na3\n\n## B\n\nb1\nb2\n\n## C\n\nc1\n"), tea.WindowSizeMsg{Width: 80, Height: 4})
		Expect(m.sv.VisibleWindow()[0]).To(ContainSubstring("A")) // start at the top
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})
		Expect(m.sv.VisibleWindow()[0]).To(ContainSubstring("B")) // jumped to ## B
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})
		Expect(m.sv.VisibleWindow()[0]).To(ContainSubstring("C")) // then ## C
		m = send(m, tea.KeyMsg{Type: tea.KeyShiftTab})
		Expect(m.sv.VisibleWindow()[0]).To(ContainSubstring("B")) // back to ## B
	})

	It("Tab is a no-op on non-markdown content", func() {
		m := send(newPagerModel(pagerContent{title: "t", body: []string{"a", "b", "c"}}), tea.WindowSizeMsg{Width: 80, Height: 6})
		before := m.sv.YOffset()
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})
		Expect(m.sv.YOffset()).To(Equal(before))
	})

	It("shows the lines-below-viewport count in the footer", func() {
		body := make([]string, 30)
		for i := range body {
			body[i] = fmt.Sprintf("L%d", i)
		}
		m := send(newPagerModel(pagerContent{title: "t", body: body}), tea.WindowSizeMsg{Width: 80, Height: 12})
		Expect(m.footer()).To(ContainSubstring("↓20")) // 30 lines − (offset 0 + height 10)
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
		Expect(m.footer()).To(ContainSubstring("/needle"))
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
		Expect(b.(pagerModel).sv.YOffset()).To(Equal(6)) // bodyStart 2 + (5-1)
	})

	It("goes to the top with gg and the bottom with G", func() {
		m := makeModel()
		bottom, _ := m.Update(key("G"))
		Expect(bottom.(pagerModel).sv.YOffset()).To(BeNumerically(">", 0))

		g1, _ := bottom.(pagerModel).Update(key("g"))
		g2, _ := g1.(pagerModel).Update(key("g"))
		Expect(g2.(pagerModel).sv.YOffset()).To(Equal(0))
	})

	It("clamps <n>g past the last body line to the bottom", func() {
		m := makeModel()
		a, _ := m.Update(key("9"))
		a, _ = a.(pagerModel).Update(key("9"))
		a, _ = a.(pagerModel).Update(key("9"))
		c, _ := a.(pagerModel).Update(key("g"))
		// total 52 lines, viewport height 8 → max offset 44
		Expect(c.(pagerModel).sv.YOffset()).To(Equal(44))
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
		was := bottom.(pagerModel).sv.YOffset()
		Expect(was).To(BeNumerically(">", 0))

		a, _ := bottom.(pagerModel).Update(key("5"))
		Expect(a.(pagerModel).footer()).NotTo(ContainSubstring("5g"))
		b, _ := a.(pagerModel).Update(key("g"))
		Expect(b.(pagerModel).sv.YOffset()).To(Equal(was)) // no jump to line 5
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
		Expect(rm.sv.Title()).To(Equal("t2"))
		Expect(rm.footer()).To(ContainSubstring("refreshed"))
	})

	It("also refreshes on ctrl+r (keymap parity with the cockpit)", func() {
		m := makeModel(func() (pagerContent, error) {
			return pagerContent{title: "t2", header: []string{"H2", ""}, body: []string{"fresh"}, number: true, gutterW: 4}, nil
		})
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
		Expect(nm.(pagerModel).content()).To(ContainSubstring("fresh"))
	})

	It("keeps the old content and reports the error when reload fails", func() {
		m := makeModel(func() (pagerContent, error) {
			return pagerContent{}, errors.New("entry vanished")
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

var _ = Describe("pager help overlay", func() {
	It("lists the core keys", func() {
		out := strings.Join(pagerHelpRows(false, false, false), "\n")
		for _, k := range []string{"scroll", "search", "quit", "top/bottom"} {
			Expect(out).To(ContainSubstring(k))
		}
	})
	It("shows refresh only when reload is wired", func() {
		Expect(strings.Join(pagerHelpRows(false, false, false), "\n")).NotTo(ContainSubstring("refresh"))
		Expect(strings.Join(pagerHelpRows(true, false, false), "\n")).To(ContainSubstring("refresh"))
	})
	It("shows expand only when the expand hook is wired", func() {
		Expect(strings.Join(pagerHelpRows(false, false, false), "\n")).NotTo(ContainSubstring("expand"))
		Expect(strings.Join(pagerHelpRows(false, true, false), "\n")).To(ContainSubstring("expand"))
	})
	It("shows fold keys only for a markdown entry", func() {
		Expect(strings.Join(pagerHelpRows(false, false, false), "\n")).NotTo(ContainSubstring("fold"))
		Expect(strings.Join(pagerHelpRows(false, false, true), "\n")).To(ContainSubstring("fold"))
	})
})

var _ = Describe("pager help popup behavior", func() {
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	makeModel := func() pagerModel {
		m := newPagerModel(pagerContent{title: "t", header: []string{"H", ""}, body: []string{"a", "b", "c"}})
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
		Expect(m.footer()).NotTo(ContainSubstring("scroll"))
		Expect(m.footer()).To(ContainSubstring("? help"))
	})
})

var _ = Describe("pager expand header", func() {
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	makeModel := func(expand func() ([]string, error)) pagerModel {
		m := newPagerModel(pagerContent{
			title:  "t",
			header: []string{"ID  x", ""},
			body:   []string{"body one", "body two"},
			expand: expand,
		})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
		return nm.(pagerModel)
	}

	It("inserts the fetched rows on e and removes them on a second e", func() {
		m := makeModel(func() ([]string, error) { return []string{"Editors  Trevor (2)", ""}, nil })
		open, _ := m.Update(key("e"))
		Expect(open.(pagerModel).content()).To(ContainSubstring("Editors  Trevor (2)"))
		closed, _ := open.(pagerModel).Update(key("e"))
		Expect(closed.(pagerModel).content()).NotTo(ContainSubstring("Editors  Trevor (2)"))
		Expect(closed.(pagerModel).content()).To(ContainSubstring("body one"))
	})

	It("reports a fetch error and stays collapsed", func() {
		m := makeModel(func() ([]string, error) { return nil, errors.New("boom") })
		nm, _ := m.Update(key("e"))
		Expect(nm.(pagerModel).footer()).To(ContainSubstring("boom"))
		Expect(nm.(pagerModel).content()).NotTo(ContainSubstring("Editors"))
	})

	It("does nothing on e when no expand hook is wired", func() {
		m := makeModel(nil)
		before := m.content()
		nm, _ := m.Update(key("e"))
		Expect(nm.(pagerModel).content()).To(Equal(before))
	})

	It("caches the fetch — the hook runs at most once", func() {
		calls := 0
		m := makeModel(func() ([]string, error) { calls++; return []string{"Editors  x", ""}, nil })
		a, _ := m.Update(key("e"))
		b, _ := a.(pagerModel).Update(key("e"))
		c, _ := b.(pagerModel).Update(key("e"))
		_ = c
		Expect(calls).To(Equal(1))
	})
})

var _ = Describe("pager esc/q collapse the expanded header", func() {
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	makeModel := func() pagerModel {
		m := newPagerModel(pagerContent{
			title:  "t",
			header: []string{"ID x", ""},
			body:   []string{"one", "two"},
			expand: func() ([]string, error) { return []string{"Editors  X", ""}, nil },
		})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
		return nm.(pagerModel)
	}

	It("collapses the expanded header on esc or q instead of quitting", func() {
		for _, dismiss := range []tea.KeyMsg{esc, key("q")} {
			m := makeModel()
			open, _ := m.Update(key("e"))
			Expect(open.(pagerModel).expanded).To(BeTrue())
			collapsed, cmd := open.(pagerModel).Update(dismiss)
			Expect(collapsed.(pagerModel).expanded).To(BeFalse())
			Expect(cmd).To(BeNil()) // collapsed, did not quit
		}
	})

	It("quits on esc or q when the header is not expanded", func() {
		for _, dismiss := range []tea.KeyMsg{esc, key("q")} {
			m := makeModel()
			_, cmd := m.Update(dismiss)
			Expect(cmd).NotTo(BeNil()) // tea.Quit
		}
	})
})

var _ = Describe("showPagerContent includes comments", func() {
	snap := func() *entry.Snapshot {
		return &entry.Snapshot{
			ID:   entity.Id("fdd23cc786c4ff4b732b38773a69a55cbc70aab1"), // DevSkim: ignore DS173237
			Tier: "private", TierType: "private",
			Kind: "note", Status: "open",
			Title: "pagetitle", Body: "pagebody",
			Comments: []entry.Comment{
				{ID: "aaaa1111", Author: "x", Body: "pagercomment", Question: true},
			},
		}
	}

	It("body includes comment text in non-raw pager mode", func() {
		pc := showPagerContent(snap(), render.ShowOptions{NoHeader: true, Color: false, Width: 80})
		joined := strings.Join(pc.body, "\n")
		Expect(joined).To(ContainSubstring("pagercomment"), "comment body must appear in pager body")
		Expect(joined).To(ContainSubstring("Comments (1)"), "comment header must appear in pager body")
	})

	It("body includes comment text in raw pager mode (--plain)", func() {
		pc := showPagerContent(snap(), render.ShowOptions{NoHeader: true, Raw: true, Color: false, Width: 80})
		joined := strings.Join(pc.body, "\n")
		Expect(joined).To(ContainSubstring("pagercomment"), "comment body must appear in raw pager body")
		Expect(joined).To(ContainSubstring("Comments (1)"), "comment header must appear in raw pager body")
	})

	It("folds a markdown section through the real glamour render path", func() {
		s := &entry.Snapshot{
			ID:   entity.Id("fdd23cc786c4ff4b732b38773a69a55cbc70aab1"), // DevSkim: ignore DS173237
			Tier: "private", TierType: "private", Kind: "note", Status: "open",
			Title: "t", ContentType: "text/markdown",
			Body: "## Alpha\n\napple\n\n## Bravo\n\nbanana\n",
		}
		pc := showPagerContent(s, render.ShowOptions{NoHeader: true, Color: false, Width: 80})
		Expect(pc.markdown).To(BeTrue())
		var mm tea.Model = newPagerModel(pc)
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
		pm := mm.(pagerModel)
		Expect(strings.Join(pm.sv.VisibleWindow(), "\n")).To(ContainSubstring("apple"))
		mm, _ = pm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}) // fold top section (Alpha)
		pm = mm.(pagerModel)
		win := strings.Join(pm.sv.VisibleWindow(), "\n")
		Expect(win).NotTo(ContainSubstring("apple")) // Alpha's content hidden
		Expect(win).To(ContainSubstring("banana"))   // Bravo still open
	})

	It("folds the pager Comments block as one section", func() {
		s := &entry.Snapshot{
			ID:   entity.Id("fdd23cc786c4ff4b732b38773a69a55cbc70aab1"), // DevSkim: ignore DS173237
			Tier: "private", TierType: "private", Kind: "note", Status: "open",
			Title: "t", ContentType: "text/markdown",
			Body:     "## Open\n\n- [ ] a\n",
			Comments: []entry.Comment{{ID: "c1", Author: "x", Body: "discuss this"}},
		}
		pc := showPagerContent(s, render.ShowOptions{NoHeader: true, Color: false, Width: 80})
		var mm tea.Model = newPagerModel(pc)
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
		pm := mm.(pagerModel)
		Expect(strings.Join(pm.sv.VisibleWindow(), "\n")).To(ContainSubstring("discuss this"))
		mm, _ = pm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")}) // collapse all incl comments
		pm = mm.(pagerModel)
		win := strings.Join(pm.sv.VisibleWindow(), "\n")
		Expect(win).NotTo(ContainSubstring("discuss this")) // comment threads folded
		Expect(win).To(ContainSubstring("Comments (1)"))    // header stays as the fold affordance
	})
})

var _ = Describe("pager refresh resets the expanded header", func() {
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	It("collapses to the fresh header and re-fetches on the next expand", func() {
		extCalls := 0
		m := newPagerModel(pagerContent{
			title:  "t",
			header: []string{"OLD", ""},
			body:   []string{"b"},
			expand: func() ([]string, error) { extCalls++; return []string{"Editors  X", ""}, nil },
			reload: func() (pagerContent, error) {
				return pagerContent{title: "t", header: []string{"NEW", ""}, body: []string{"b2"}}, nil
			},
		})
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})

		e1, _ := nm.(pagerModel).Update(key("e"))
		Expect(e1.(pagerModel).expanded).To(BeTrue())

		r1, _ := e1.(pagerModel).Update(key("r"))
		rm := r1.(pagerModel)
		Expect(rm.expanded).To(BeFalse())
		Expect(rm.content()).To(ContainSubstring("NEW"))
		Expect(rm.content()).NotTo(ContainSubstring("Editors  X"))

		e2, _ := rm.Update(key("e"))
		Expect(e2.(pagerModel).content()).To(ContainSubstring("Editors  X"))
		Expect(extCalls).To(Equal(2)) // cache was invalidated by refresh
	})
})
