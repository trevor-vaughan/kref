package main

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/git-bug/git-bug/entity"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("cockpitInputFor", func() {
	It("collapses Done unless --full", func() {
		in := cockpitInputFor("t", []string{"h"}, "## Done (compact)\n\n- [x] a\n", false, 80, false, nil)
		Expect(in.collapsed["Done (compact)"]).To(BeTrue())
		in2 := cockpitInputFor("t", []string{"h"}, "## Done (compact)\n\n- [x] a\n", false, 80, true, nil)
		Expect(in2.collapsed["Done (compact)"]).To(BeFalse())
	})

	It("sets title, header, body, color, width", func() {
		in := cockpitInputFor("my-title", []string{"line1", "line2"}, "body", true, 120, false, nil)
		Expect(in.title).To(Equal("my-title"))
		Expect(in.header).To(Equal([]string{"line1", "line2"}))
		Expect(in.body).To(Equal("body"))
		Expect(in.color).To(BeTrue())
		Expect(in.width).To(Equal(120))
	})
})

var _ = Describe("cockpitModel", func() {
	body := "## Open\n\n- [ ] alpha\n\n- [ ] bravo\n\n## Done\n\n- [x] gamma\n"

	newModel := func(doneCollapsed bool) cockpitModel {
		return newCockpitModel(cockpitInput{
			title:     "todo",
			header:    []string{"◉ 0 awaiting"},
			body:      body,
			color:     false,
			width:     60,
			collapsed: map[string]bool{"Done": doneCollapsed},
		})
	}
	send := func(m cockpitModel, msgs ...tea.Msg) cockpitModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(cockpitModel)
	}
	size := tea.WindowSizeMsg{Width: 60, Height: 20}

	It("shows an expanded section's items and hides a collapsed one's", func() {
		m := send(newModel(true), size)
		Expect(m.View()).To(ContainSubstring("alpha"))    // Open expanded
		Expect(m.View()).NotTo(ContainSubstring("gamma")) // Done collapsed
	})

	It("unfolds the Done section when toggled", func() {
		m := send(newModel(true), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})                       // cur -> Done
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}) // fold toggle
		Expect(m.View()).To(ContainSubstring("gamma"))
	})

	It("Tab moves the cursor to the next section", func() {
		m := send(newModel(false), size)
		Expect(m.items[m.cur].heading).To(Equal("Open"))
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})
		Expect(m.items[m.cur].heading).To(Equal("Done"))
	})

	It("quits on q", func() {
		_, cmd := newModel(true).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
		Expect(cmd).NotTo(BeNil())
	})

	It("makes a ### subsection a foldable cursor item", func() {
		sub := "## Open\n\n### Priority\n\n- [ ] a\n\n## Done (compact)\n"
		m := send(newCockpitModel(cockpitInput{
			title: "todo", header: []string{"h"}, body: sub, color: false,
			width: 80, collapsed: map[string]bool{},
		}), tea.WindowSizeMsg{Width: 80, Height: 24})
		var found bool
		for _, it := range m.items {
			if it.kind == itemSection && strings.Contains(it.headingText, "Priority") {
				found = true
			}
		}
		Expect(found).To(BeTrue()) // ### is now an item
	})

	rune1 := func(s string) tea.Msg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	It("o opens and c closes the current section; C collapses all, O expands all", func() {
		m := send(newModel(false), size) // cursor on the Open section
		cur := m.items[m.cur].heading
		m = send(m, rune1("c")) // close current
		Expect(m.collapsed[cur]).To(BeTrue())
		m = send(m, rune1("o")) // open current
		Expect(m.collapsed[cur]).To(BeFalse())
		m = send(m, rune1("C")) // collapse all
		Expect(m.collapsed[cur]).To(BeTrue())
		m = send(m, rune1("O")) // expand all
		Expect(m.collapsed[cur]).To(BeFalse())
	})

	It("scrolls one line with j/k, leaving the cursor put", func() {
		tall := "## Open\n\n" + strings.Repeat("- [ ] x\n", 30) + "\n## Done\n\n- [ ] last\n"
		m := send(newCockpitModel(cockpitInput{
			title: "t", header: []string{"h"}, body: tall, color: false,
			width: 80, collapsed: map[string]bool{},
		}), tea.WindowSizeMsg{Width: 80, Height: 10})
		cur0 := m.cur
		Expect(m.sv.YOffset()).To(Equal(0))
		m = send(m, rune1("j"))
		Expect(m.sv.YOffset()).To(Equal(1)) // scrolled one line, did not leap to the next item
		Expect(m.cur).To(Equal(cur0))       // cursor (action target) fixed, off-screen ok
		m = send(m, rune1("j"))
		Expect(m.sv.YOffset()).To(Equal(2))
		m = send(m, rune1("k"))
		Expect(m.sv.YOffset()).To(Equal(1)) // and back up
	})

	It("shows content lines below the cursor in the footer, shrinking as it descends", func() {
		m := send(newCockpitModel(cockpitInput{
			title: "t", header: []string{"h"}, body: "## A\n\nx\n\n## B\n\ny\n", color: false,
			width: 80, collapsed: map[string]bool{},
		}), tea.WindowSizeMsg{Width: 80, Height: 20})
		Expect(m.View()).To(ContainSubstring("↓5")) // 6 content lines, cursor on line 0
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})   // cursor → ## B (line 3)
		Expect(m.View()).To(ContainSubstring("↓2")) // 6 − 3 − 1
	})

	It("searches with / and scrolls to a match, cycling with n", func() {
		tall := "## Open\n\n" + strings.Repeat("- [ ] filler\n", 40) + "\n## Target\n\n- [ ] findme\n"
		m := send(newCockpitModel(cockpitInput{
			title: "t", header: []string{"h"}, body: tall, color: false,
			width: 80, collapsed: map[string]bool{},
		}), tea.WindowSizeMsg{Width: 80, Height: 10})
		before := m.sv.YOffset()
		m = send(m, rune1("/"))
		Expect(m.search.searching()).To(BeTrue())
		for _, r := range "findme" {
			m = send(m, rune1(string(r)))
		}
		m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.search.searching()).To(BeFalse())
		Expect(len(m.search.matches)).To(BeNumerically(">", 0))
		Expect(m.sv.YOffset()).To(BeNumerically(">", before)) // scrolled down to the match
	})

	It("expands folds on search so a hit in a collapsed section is found", func() {
		foldedBody := "## Open\n\n- [ ] a\n\n## Done (compact)\n\n- [ ] hiddengem\n"
		m := send(newCockpitModel(cockpitInput{
			title: "t", header: []string{"h"}, body: foldedBody, color: false,
			width: 80, collapsed: map[string]bool{"Done (compact)": true},
		}), tea.WindowSizeMsg{Width: 80, Height: 20})
		Expect(m.View()).NotTo(ContainSubstring("hiddengem")) // folded away
		m = send(m, rune1("/"))
		for _, r := range "hiddengem" {
			m = send(m, rune1(string(r)))
		}
		m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
		Expect(len(m.search.matches)).To(BeNumerically(">", 0)) // found after auto-expand
		Expect(m.View()).To(ContainSubstring("hiddengem"))      // now visible
	})
})

var _ = Describe("cockpitModel discussion zone", func() {
	body := "## Open\n\n- [ ] alpha\n\n## Done\n\n- [x] gamma\n"
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m cockpitModel, msgs ...tea.Msg) cockpitModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(cockpitModel)
	}
	newModel := func(comments []entry.Comment) cockpitModel {
		return newCockpitModel(cockpitInput{
			title: "todo", header: []string{"◉ 1 awaiting"}, body: body,
			color: false, width: 80, collapsed: map[string]bool{}, comments: comments,
		})
	}

	It("renders an open question thread with its ◉ glyph and body", func() {
		m := send(newModel([]entry.Comment{{ID: "q", Author: "ada", Body: "ship it?", Question: true}}), size)
		Expect(m.View()).To(ContainSubstring("◉"))
		Expect(m.View()).To(ContainSubstring("ship it?"))
		Expect(m.View()).To(ContainSubstring("alpha"))
	})

	It("renders a plain comment thread with a · glyph", func() {
		m := send(newModel([]entry.Comment{{ID: "p", Author: "ada", Body: "just a note"}}), size)
		Expect(m.View()).To(ContainSubstring("just a note"))
	})

	It("collapses a resolved question thread and expands it on space", func() {
		comments := []entry.Comment{
			{ID: "q", Author: "ada", Body: "old question?", Question: true, Resolved: true, ResolvedBy: "bob"},
			{ID: "r", Author: "bob", Body: "the reply text", ReplyTo: "q"},
		}
		m := send(newModel(comments), size)
		Expect(m.View()).To(ContainSubstring("old question?"))
		Expect(m.View()).NotTo(ContainSubstring("the reply text"))
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
		Expect(m.View()).To(ContainSubstring("the reply text"))
	})

	It("focuses the thread before the first section on Tab", func() {
		m := send(newModel([]entry.Comment{{ID: "q", Author: "ada", Body: "ship it?", Question: true}}), size)
		start := m.sv.YOffset()
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})
		Expect(m.sv.YOffset()).To(BeNumerically(">=", start))
	})

	It("renders identically to a bodyless-comment cockpit when there are no comments", func() {
		m := send(newModel(nil), size)
		Expect(m.View()).To(ContainSubstring("alpha"))
		Expect(m.View()).NotTo(ContainSubstring("Comments ("))
	})
})

var _ = Describe("cockpitModel cursor", func() {
	body := "## Open\n\n- [ ] alpha\n"
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m cockpitModel, msgs ...tea.Msg) cockpitModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(cockpitModel)
	}
	comments := []entry.Comment{
		{ID: "q", Author: "ada", Body: "root q?", Question: true},
		{ID: "r", Author: "bob", Body: "a reply", ReplyTo: "q"},
	}
	newModel := func() cockpitModel {
		return newCockpitModel(cockpitInput{
			title: "todo", header: []string{"h"}, body: body, color: false,
			width: 80, collapsed: map[string]bool{}, comments: comments,
		})
	}

	It("starts on the first comment and steps items with Tab", func() {
		m := send(newModel(), size)
		Expect(m.items[m.cur].commentID).To(Equal("q")) // root
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})
		Expect(m.items[m.cur].commentID).To(Equal("r")) // its reply
	})

	It("moves the cursor with Tab / Shift-Tab", func() {
		m := send(newModel(), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})
		Expect(m.items[m.cur].commentID).To(Equal("r")) // Tab = next item
		m = send(m, tea.KeyMsg{Type: tea.KeyShiftTab})
		Expect(m.items[m.cur].commentID).To(Equal("q")) // Shift-Tab = prev item
	})

	It("clamps the cursor at the last item", func() {
		m := send(newModel(), size)
		big := make([]tea.Msg, 20)
		for i := range big {
			big[i] = tea.KeyMsg{Type: tea.KeyTab}
		}
		m = send(m, big...)
		Expect(m.cur).To(Equal(len(m.items) - 1))
	})

	It("marks the cursor's line in the view", func() {
		m := send(newModel(), size)
		Expect(m.View()).To(ContainSubstring(cursorMarker))
	})

	It("Tab steps to the next item (the reply), not skipping it", func() {
		m := send(newModel(), size) // cursor on root q (item 0)
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})
		Expect(m.items[m.cur].commentID).To(Equal("r")) // next item is the reply
	})

	It("shows global context in the title and the cursor's local context in the status", func() {
		m := send(newModel(), size)
		Expect(m.View()).To(ContainSubstring("h"))            // global header text (title)
		Expect(m.View()).To(ContainSubstring("▸ thread 1/1")) // local context (status)
		// Tab past the root q and its reply r onto the ## Open section.
		m = send(m, tea.KeyMsg{Type: tea.KeyTab}, tea.KeyMsg{Type: tea.KeyTab})
		Expect(m.View()).To(ContainSubstring("▸ Open"))
	})

	It("folds the node under the cursor on space, hiding its replies", func() {
		m := send(newModel(), size) // cursor on root q, reply r visible
		Expect(m.View()).To(ContainSubstring("a reply"))
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
		Expect(m.View()).NotTo(ContainSubstring("a reply")) // replies hidden
		Expect(m.items[m.cur].commentID).To(Equal("q"))     // cursor stayed on the node
	})

	It("folds just the selected node's sub-thread (deep nesting), keeping the cursor there", func() {
		deep := []entry.Comment{
			{ID: "q", Author: "a", Body: "root-body"},
			{ID: "r", Author: "b", Body: "mid-body", ReplyTo: "q"},
			{ID: "s", Author: "c", Body: "leaf-body", ReplyTo: "r"},
		}
		m := send(newCockpitModel(cockpitInput{
			title: "todo", header: []string{"h"}, body: body, color: false,
			width: 80, collapsed: map[string]bool{}, comments: deep,
		}), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyTab}) // cursor onto the mid node r
		Expect(m.items[m.cur].commentID).To(Equal("r"))
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}) // fold r's sub-thread
		Expect(m.View()).To(ContainSubstring("root-body"))              // q still shown
		Expect(m.View()).To(ContainSubstring("mid-body"))               // r still shown
		Expect(m.View()).NotTo(ContainSubstring("leaf-body"))           // only s hidden
		Expect(m.items[m.cur].commentID).To(Equal("r"))                 // cursor stayed on r
	})

	It("→ goes into the reply and ← comes back out to the parent", func() {
		m := send(newModel(), size) // cursor on root q
		m = send(m, tea.KeyMsg{Type: tea.KeyRight})
		Expect(m.items[m.cur].commentID).To(Equal("r")) // into the child
		m = send(m, tea.KeyMsg{Type: tea.KeyLeft})
		Expect(m.items[m.cur].commentID).To(Equal("q")) // back out to the parent
	})

	It("l goes into the reply and h comes back out (vim keys)", func() {
		m := send(newModel(), size) // cursor on root q
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
		Expect(m.items[m.cur].commentID).To(Equal("r")) // l = into the child
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
		Expect(m.items[m.cur].commentID).To(Equal("q")) // h = out to the parent
	})

	It("puts the cursor marker on the section heading line, not the blank above it", func() {
		m := send(newModel(), size)
		// Tab past q and r onto the ## Open section.
		m = send(m, tea.KeyMsg{Type: tea.KeyTab}, tea.KeyMsg{Type: tea.KeyTab})
		var cursorLine string
		for ln := range strings.SplitSeq(m.View(), "\n") {
			if strings.Contains(ln, cursorMarker) {
				cursorLine = ln
				break
			}
		}
		Expect(cursorLine).To(ContainSubstring("Open")) // the marked line is the heading
	})

	It("does not fold on enter (only space folds)", func() {
		m := send(newModel(), size)
		before := m.View()
		m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.View()).To(Equal(before)) // enter is a no-op on view state
	})

	It("G jumps to the last item and gg back to the first", func() {
		m := send(newModel(), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
		Expect(m.cur).To(Equal(len(m.items) - 1))
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
		Expect(m.cur).To(Equal(0))
	})

	It("quits on q but not on esc", func() {
		_, qCmd := send(newModel(), size).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
		Expect(qCmd).NotTo(BeNil())
		_, escCmd := send(newModel(), size).Update(tea.KeyMsg{Type: tea.KeyEsc})
		Expect(escCmd).To(BeNil()) // esc is free for cancelling dialogs, not quitting
	})

	It("shows a scroll marker: 'all' when content fits, 'top' when it overflows", func() {
		m := send(newModel(), size) // small content in an 80x24 view → fits
		Expect(m.View()).To(ContainSubstring("all"))

		tall := "## Open\n\n" + strings.Repeat("- [ ] item\n", 60) + "\n## Done (compact)\n"
		big := send(newCockpitModel(cockpitInput{
			title: "t", header: []string{"h"}, body: tall, color: false,
			width: 80, collapsed: map[string]bool{},
		}), tea.WindowSizeMsg{Width: 80, Height: 10})
		Expect(big.View()).NotTo(ContainSubstring("all")) // overflows
		Expect(big.View()).To(ContainSubstring("top"))    // at the top
	})

	It("toggles content colour off and on with t", func() {
		q := []entry.Comment{{ID: "q", Author: "ada", Body: "q?", Question: true}} // open ◉ = red glyph
		m := send(newCockpitModel(cockpitInput{
			title: "todo", header: []string{"\x1b[31m◉ 1 awaiting\x1b[0m"},
			body: body, color: true, width: 80, collapsed: map[string]bool{}, comments: q,
		}), size)
		Expect(m.View()).To(ContainSubstring("\x1b[31m")) // red content (glyph + header) when on
		Expect(m.sv.Plain()).To(BeFalse())                // chrome styled when colour on
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
		Expect(m.color).To(BeFalse())
		Expect(m.View()).NotTo(ContainSubstring("\x1b[31m")) // no red content when off
		Expect(m.sv.Plain()).To(BeTrue())                    // chrome goes plain too
	})
})

type fakeWriter struct {
	addKind, addBody, addReplyTo string
	added                        bool
	editTarget, editBody         string
	edited                       bool
	deleteTarget                 string
	deleted                      bool
	resolveTarget                string
	resolved                     bool
}

func (f *fakeWriter) AddComment(id entity.Id, actorKind, body string, question bool, replyTo string) (string, error) {
	f.added, f.addKind, f.addBody, f.addReplyTo = true, actorKind, body, replyTo
	return "newid", nil
}
func (f *fakeWriter) ResolveComment(id entity.Id, target string) error {
	f.resolved, f.resolveTarget = true, target
	return nil
}
func (f *fakeWriter) EditComment(id entity.Id, target, body string) error {
	f.edited, f.editTarget, f.editBody = true, target, body
	return nil
}
func (f *fakeWriter) DeleteComment(id entity.Id, target string) error {
	f.deleted, f.deleteTarget = true, target
	return nil
}

var _ = Describe("cockpitModel reply", func() {
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m cockpitModel, msgs ...tea.Msg) cockpitModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(cockpitModel)
	}
	base := []entry.Comment{{ID: "q", Author: "ada", Body: "root q?", Question: true}}
	newModel := func(fw *fakeWriter, reloaded []entry.Comment) cockpitModel {
		return newCockpitModel(cockpitInput{
			title: "todo", header: []string{"h"}, body: "## Open\n\n- [ ] a\n",
			color: false, width: 80, collapsed: map[string]bool{}, comments: base,
			writer: fw, entryID: "deadbeef", actorKind: "human",
			reload: func() ([]string, []entry.Comment, error) { return []string{"h2"}, reloaded, nil },
		})
	}

	It("replies to the selected node and reloads", func() {
		fw := &fakeWriter{}
		reloaded := append(append([]entry.Comment{}, base...), entry.Comment{ID: "x", Author: "me", Body: "typed reply", ReplyTo: "q"})
		m := send(newModel(fw, reloaded), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
		m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
		Expect(fw.added).To(BeTrue())
		Expect(fw.addReplyTo).To(Equal("q"))
		Expect(fw.addKind).To(Equal("human"))
		Expect(fw.addBody).To(Equal("hi"))
		Expect(m.View()).To(ContainSubstring("typed reply"))
		Expect(m.View()).To(ContainSubstring("replied"))
	})

	It("cancels a reply on esc without writing", func() {
		fw := &fakeWriter{}
		m := send(newModel(fw, base), size)
		send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}, tea.KeyMsg{Type: tea.KeyEsc})
		Expect(fw.added).To(BeFalse())
	})

	It("refreshes on ctrl+r", func() {
		fw := &fakeWriter{}
		reloaded := append(append([]entry.Comment{}, base...), entry.Comment{ID: "z", Author: "them", Body: "arrived elsewhere"})
		m := send(newModel(fw, reloaded), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
		Expect(m.View()).To(ContainSubstring("arrived elsewhere"))
		Expect(m.View()).To(ContainSubstring("refreshed"))
	})

	It("is a no-op for r when there is no writer (3a read-only)", func() {
		m := send(newCockpitModel(cockpitInput{
			title: "t", header: []string{"h"}, body: "## Open\n\n- [ ] a\n", color: false,
			width: 80, collapsed: map[string]bool{}, comments: base,
		}), size)
		m2 := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		Expect(m2.mode).To(Equal(modeNone))
	})

	It("edits the comment under the cursor (prefilled body) on e then ctrl+s", func() {
		fw := &fakeWriter{}
		m := send(newModel(fw, base), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}) // prefills with "root q?"
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")}) // append a char
		m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
		Expect(fw.edited).To(BeTrue())
		Expect(fw.editTarget).To(Equal("q"))
		Expect(fw.editBody).To(Equal("root q?!"))
		Expect(m.View()).To(ContainSubstring("edited"))
	})

	It("deletes on d then y, and does not delete on d then n", func() {
		fw := &fakeWriter{}
		m := send(newModel(fw, base), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
		Expect(m.mode).To(Equal(modeConfirmDelete))
		send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
		Expect(fw.deleted).To(BeTrue())
		Expect(fw.deleteTarget).To(Equal("q"))

		fw2 := &fakeWriter{}
		m2 := send(newModel(fw2, base), size)
		send(m2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
		Expect(fw2.deleted).To(BeFalse())
	})

	It("resolves the thread's open-question root on x with an empty note", func() {
		fw := &fakeWriter{}
		m := send(newModel(fw, base), size) // cursor on q (open question)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		Expect(m.mode).To(Equal(modeResolveNote))
		send(m, tea.KeyMsg{Type: tea.KeyCtrlS}) // empty note → just resolve
		Expect(fw.resolved).To(BeTrue())
		Expect(fw.resolveTarget).To(Equal("q"))
		Expect(fw.added).To(BeFalse()) // no note reply
	})

	It("posts a note reply before resolving when the note is non-empty", func() {
		fw := &fakeWriter{}
		m := send(newModel(fw, base), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ok")})
		send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
		Expect(fw.added).To(BeTrue()) // note posted as a reply first
		Expect(fw.addReplyTo).To(Equal("q"))
		Expect(fw.addBody).To(Equal("ok"))
		Expect(fw.resolved).To(BeTrue())
	})

	It("x is a no-op on a non-question root", func() {
		fw := &fakeWriter{}
		plain := []entry.Comment{{ID: "p", Author: "ada", Body: "just a note"}}
		m := send(newCockpitModel(cockpitInput{
			title: "todo", header: []string{"h"}, body: "## Open\n\n- [ ] a\n",
			color: false, width: 80, collapsed: map[string]bool{}, comments: plain,
			writer: fw, entryID: "deadbeef", actorKind: "human",
			reload: func() ([]string, []entry.Comment, error) { return []string{"h"}, plain, nil },
		}), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		Expect(m.mode).To(Equal(modeNone))
		Expect(fw.resolved).To(BeFalse())
	})
})

var _ = Describe("cockpitModel $EDITOR escape", func() {
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m cockpitModel, msgs ...tea.Msg) cockpitModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(cockpitModel)
	}
	base := []entry.Comment{{ID: "q", Author: "ada", Body: "root q?", Question: true}}
	newModel := func(fw *fakeWriter) cockpitModel {
		return newCockpitModel(cockpitInput{
			title: "todo", header: []string{"h"}, body: "## Open\n\n- [ ] a\n",
			color: false, width: 80, collapsed: map[string]bool{}, comments: base,
			writer: fw, entryID: "deadbeef", actorKind: "human",
			reload: func() ([]string, []entry.Comment, error) { return []string{"h"}, base, nil },
		})
	}

	Describe("readEditorResult", func() {
		It("reads the edited body back (trimming trailing newlines) and removes the temp file", func() {
			path := filepath.Join(GinkgoT().TempDir(), "draft.md")
			Expect(os.WriteFile(path, []byte("edited in vim\n\n"), 0o600)).To(Succeed())
			msg := readEditorResult(path, nil)
			Expect(msg.err).To(BeNil())
			Expect(msg.body).To(Equal("edited in vim"))
			_, statErr := os.Stat(path)
			Expect(os.IsNotExist(statErr)).To(BeTrue()) // temp file cleaned up
		})

		It("surfaces the editor's run error and still removes the temp file", func() {
			path := filepath.Join(GinkgoT().TempDir(), "draft.md")
			Expect(os.WriteFile(path, []byte("half-typed"), 0o600)).To(Succeed())
			msg := readEditorResult(path, os.ErrClosed)
			Expect(msg.err).To(MatchError(os.ErrClosed))
			_, statErr := os.Stat(path)
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})

	It("ctrl+o in a textarea mode suspends to the editor, keeping the mode and draft", func() {
		GinkgoT().Setenv("XDG_CACHE_HOME", GinkgoT().TempDir()) // seed temp lands here, auto-cleaned
		fw := &fakeWriter{}
		m := send(newModel(fw), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})     // open reply
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("draft")}) // type a draft
		mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
		m = mm.(cockpitModel)
		Expect(cmd).NotTo(BeNil())              // an ExecProcess command was issued
		Expect(m.mode).To(Equal(modeReply))     // still composing the reply
		Expect(m.ta.Value()).To(Equal("draft")) // draft preserved, not sent
		Expect(fw.added).To(BeFalse())          // ctrl+o is not a submit
	})

	It("loads the editor's result into the textarea via editorFinishedMsg", func() {
		fw := &fakeWriter{}
		m := send(newModel(fw), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		m = send(m, editorFinishedMsg{body: "written in $EDITOR"})
		Expect(m.mode).To(Equal(modeReply))
		Expect(m.ta.Value()).To(Equal("written in $EDITOR"))
	})

	It("reports an editor failure and keeps the draft on editorFinishedMsg error", func() {
		fw := &fakeWriter{}
		m := send(newModel(fw), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("keep me")})
		m = send(m, editorFinishedMsg{err: os.ErrPermission})
		Expect(m.notice).To(ContainSubstring("editor failed"))
		Expect(m.ta.Value()).To(Equal("keep me")) // draft not clobbered
		Expect(m.mode).To(Equal(modeReply))
	})
})
