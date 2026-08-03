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
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

// stubProvider is a test HeaderProvider: static header + fold, canned reload.
type stubProvider struct {
	header []string
	fold   map[string]bool
	reload func() ([]string, []entry.Comment, error)
}

func (p stubProvider) HeaderLines() []string { return p.header }

func (p stubProvider) InitialFold() map[string]bool {
	if p.fold == nil {
		return map[string]bool{}
	}
	return p.fold
}

func (p stubProvider) Reload() ([]string, []entry.Comment, error) {
	if p.reload == nil {
		return p.header, nil, nil
	}
	return p.reload()
}

var _ = Describe("todoInitialFold", func() {
	It("collapses Done unless --full", func() {
		body := "## Done (compact)\n\n- [x] a\n"
		Expect(todoInitialFold(body, false)["Done (compact)"]).To(BeTrue())
		Expect(todoInitialFold(body, true)["Done (compact)"]).To(BeFalse())
	})
})

var _ = Describe("viewerModel", func() {
	body := "## Open\n\n- [ ] alpha\n\n- [ ] bravo\n\n## Done\n\n- [x] gamma\n"

	newModel := func(doneCollapsed bool) viewerModel {
		return newViewerModel(viewerInput{
			title: "todo",
			body:  body,
			color: false,
			width: 60,
			provider: stubProvider{
				header: []string{"◉ 0 awaiting"},
				fold:   map[string]bool{"Done": doneCollapsed},
			},
		})
	}
	send := func(m viewerModel, msgs ...tea.Msg) viewerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(viewerModel)
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
		m := send(newViewerModel(viewerInput{
			title: "todo", body: sub, color: false,
			width:    80,
			provider: stubProvider{header: []string{"h"}},
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

	// ^space is ctrl+space, which terminals send as NUL — bubbletea names that
	// key "ctrl+@".
	ctrlSpace := tea.KeyMsg{Type: tea.KeyCtrlAt}

	It("ignores the retired o/c O/C fold keys", func() {
		m := send(newModel(false), size) // cursor on the Open section
		cur := m.items[m.cur].heading
		for _, k := range []string{"c", "o", "C", "O"} {
			m = send(m, rune1(k))
			Expect(m.collapsed[cur]).To(BeFalse(), "%q must not fold anything", k)
		}
		Expect(m.View()).To(ContainSubstring("alpha")) // nothing folded
	})

	It("folds every section with ^space, and unfolds them on the next press", func() {
		m := send(newModel(false), size)
		m = send(m, ctrlSpace)
		Expect(m.collapsed["Open"]).To(BeTrue())
		Expect(m.collapsed["Done"]).To(BeTrue())
		Expect(m.View()).NotTo(ContainSubstring("alpha"))
		m = send(m, ctrlSpace)
		Expect(m.collapsed).To(BeEmpty())
		Expect(m.View()).To(ContainSubstring("alpha"))
	})

	It("unfolds everything with ^space when only some sections are folded", func() {
		m := send(newModel(true), size) // Done starts folded
		m = send(m, ctrlSpace)
		Expect(m.collapsed).To(BeEmpty())
		Expect(m.View()).To(ContainSubstring("gamma"))
	})

	It("advertises ^space, not o/c, in the key help", func() {
		m := send(newModel(false), tea.WindowSizeMsg{Width: 100, Height: 24}, rune1("?"))
		Expect(m.View()).To(ContainSubstring("^space"))
		Expect(m.View()).NotTo(ContainSubstring("o/c"))
	})

	It("scrolls one line with j/k, leaving the cursor put", func() {
		tall := "## Open\n\n" + strings.Repeat("- [ ] x\n", 30) + "\n## Done\n\n- [ ] last\n"
		m := send(newViewerModel(viewerInput{
			title: "t", body: tall, color: false,
			width:    80,
			provider: stubProvider{header: []string{"h"}},
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
		m := send(newViewerModel(viewerInput{
			title: "t", body: "## A\n\nx\n\n## B\n\ny\n", color: false,
			width:    80,
			provider: stubProvider{header: []string{"h"}},
		}), tea.WindowSizeMsg{Width: 80, Height: 20})
		Expect(m.View()).To(ContainSubstring("↓5")) // 6 content lines, cursor on line 0
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})   // cursor → ## B (line 3)
		Expect(m.View()).To(ContainSubstring("↓2")) // 6 − 3 − 1
	})

	It("searches with / and scrolls to a match, cycling with n", func() {
		tall := "## Open\n\n" + strings.Repeat("- [ ] filler\n", 40) + "\n## Target\n\n- [ ] findme\n"
		m := send(newViewerModel(viewerInput{
			title: "t", body: tall, color: false,
			width:    80,
			provider: stubProvider{header: []string{"h"}},
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
		m := send(newViewerModel(viewerInput{
			title: "t", body: foldedBody, color: false,
			width: 80,
			provider: stubProvider{
				header: []string{"h"},
				fold:   map[string]bool{"Done (compact)": true},
			},
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

var _ = Describe("viewerModel discussion zone", func() {
	body := "## Open\n\n- [ ] alpha\n\n## Done\n\n- [x] gamma\n"
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m viewerModel, msgs ...tea.Msg) viewerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(viewerModel)
	}
	newModel := func(comments []entry.Comment) viewerModel {
		return newViewerModel(viewerInput{
			title: "todo", body: body,
			color: false, width: 80, comments: comments,
			provider: stubProvider{header: []string{"◉ 1 awaiting"}},
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

var _ = Describe("viewerModel cursor", func() {
	body := "## Open\n\n- [ ] alpha\n"
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m viewerModel, msgs ...tea.Msg) viewerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(viewerModel)
	}
	comments := []entry.Comment{
		{ID: "q", Author: "ada", Body: "root q?", Question: true},
		{ID: "r", Author: "bob", Body: "a reply", ReplyTo: "q"},
	}
	newModel := func() viewerModel {
		return newViewerModel(viewerInput{
			title: "todo", body: body, color: false,
			width: 80, comments: comments,
			provider: stubProvider{header: []string{"h"}},
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
		m := send(newViewerModel(viewerInput{
			title: "todo", body: body, color: false,
			width: 80, comments: deep,
			provider: stubProvider{header: []string{"h"}},
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

	// G scrolls to the end of the content (matching the pager and list) and the
	// cursor follows to the item owning the new top line. On a body short enough
	// that the last item is already on screen at the bottom, that item is the
	// selection — but the contract under test is the scroll position, not the
	// index.
	It("G scrolls to the bottom and gg goes back to the top", func() {
		m := send(newModel(), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
		Expect(m.sv.ScrollLabel()).To(BeElementOf("bot", "all"))
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
		Expect(m.cur).To(Equal(0))
	})

	It("a single g also jumps to the first item, as it does in the list cockpit", func() {
		m := send(newModel(), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
		Expect(m.cur).To(Equal(0))
	})

	It("quits on q, and on esc once there is nothing left to dismiss", func() {
		_, qCmd := send(newModel(), size).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
		Expect(qCmd).NotTo(BeNil())
		// esc is a layered dismiss: it closes a modal, then the help popup, then
		// a committed search, and only quits at the base layer — the same shape
		// the pager, list and review viewer already had.
		_, escCmd := send(newModel(), size).Update(tea.KeyMsg{Type: tea.KeyEsc})
		Expect(escCmd).NotTo(BeNil())
	})

	It("shows a scroll marker: 'all' when content fits, 'top' when it overflows", func() {
		m := send(newModel(), size) // small content in an 80x24 view → fits
		Expect(m.View()).To(ContainSubstring("all"))

		tall := "## Open\n\n" + strings.Repeat("- [ ] item\n", 60) + "\n## Done (compact)\n"
		big := send(newViewerModel(viewerInput{
			title: "t", body: tall, color: false,
			width:    80,
			provider: stubProvider{header: []string{"h"}},
		}), tea.WindowSizeMsg{Width: 80, Height: 10})
		Expect(big.View()).NotTo(ContainSubstring("all")) // overflows
		Expect(big.View()).To(ContainSubstring("top"))    // at the top
	})

	It("toggles content colour off and on with t", func() {
		q := []entry.Comment{{ID: "q", Author: "ada", Body: "q?", Question: true}} // open ◉ = red glyph
		m := send(newViewerModel(viewerInput{
			title: "todo",
			body:  body, color: true, width: 80, comments: q,
			provider: stubProvider{header: []string{"\x1b[31m◉ 1 awaiting\x1b[0m"}},
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
	addQuestion                  bool
	added                        bool
	editTarget, editBody         string
	edited                       bool
	deleteTarget                 string
	deleted                      bool
	resolveTarget, resolveNote   string
	resolved                     bool
	unresolved                   bool
	unresolveTarget              string
	result                       writeResult // outcome the next body-bearing write returns
}

func (f *fakeWriter) AddComment(id entity.Id, actor, actorKind, body string, question bool, replyTo string) (writeResult, error) {
	f.added, f.addKind, f.addBody, f.addReplyTo, f.addQuestion = true, actorKind, body, replyTo, question
	res := f.result
	if res.CommentID == "" && res.Parked == nil {
		res.CommentID = "newid"
	}
	return res, nil
}
func (f *fakeWriter) ResolveWithNote(id entity.Id, target, note string) (writeResult, error) {
	f.resolved, f.resolveTarget, f.resolveNote = true, target, note
	return f.result, nil
}
func (f *fakeWriter) EditComment(id entity.Id, target, body string) (writeResult, error) {
	f.edited, f.editTarget, f.editBody = true, target, body
	return f.result, nil
}
func (f *fakeWriter) DeleteComment(id entity.Id, target string) error {
	f.deleted, f.deleteTarget = true, target
	return nil
}
func (f *fakeWriter) UnresolveComment(id entity.Id, target string) error {
	f.unresolved, f.unresolveTarget = true, target
	return nil
}

var _ = Describe("viewerModel reply", func() {
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m viewerModel, msgs ...tea.Msg) viewerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(viewerModel)
	}
	base := []entry.Comment{{ID: "q", Author: "ada", Body: "root q?", Question: true}}
	newModel := func(fw *fakeWriter, reloaded []entry.Comment) viewerModel {
		return newViewerModel(viewerInput{
			title: "todo", body: "## Open\n\n- [ ] a\n",
			color: false, width: 80, comments: base,
			writer: fw, entryID: "deadbeef", actorKind: "human",
			provider: stubProvider{
				header: []string{"h"},
				reload: func() ([]string, []entry.Comment, error) { return []string{"h2"}, reloaded, nil },
			},
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
		m := send(newViewerModel(viewerInput{
			title: "t", body: "## Open\n\n- [ ] a\n", color: false,
			width: 80, comments: base,
			provider: stubProvider{header: []string{"h"}},
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
		Expect(fw.resolveNote).To(BeEmpty())
		Expect(fw.added).To(BeFalse()) // the note never goes through AddComment
	})

	It("passes a non-empty note to the writer with the resolve", func() {
		fw := &fakeWriter{}
		m := send(newModel(fw, base), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ok")})
		send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
		Expect(fw.resolved).To(BeTrue())
		Expect(fw.resolveTarget).To(Equal("q"))
		Expect(fw.resolveNote).To(Equal("ok"))
		Expect(fw.added).To(BeFalse()) // one guarded call, not note-then-resolve
	})

	It("reports a parked write as held for review and does not claim success", func() {
		fw := &fakeWriter{result: writeResult{Parked: &store.Parked{
			ItemID:   "abcd1234abcd",
			Findings: []scan.Finding{{RuleID: "github-pat", StartLine: 1, Description: "token"}},
		}}}
		m := send(newModel(fw, base), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
		m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
		Expect(m.notice).To(ContainSubstring("held for review"))
		Expect(m.notice).NotTo(ContainSubstring("replied"))
	})

	It("flags an unscanned write in the notice", func() {
		fw := &fakeWriter{result: writeResult{CommentID: "newid", Unscanned: true}}
		m := send(newModel(fw, base), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
		m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
		Expect(m.notice).To(ContainSubstring("UNSCANNED"))
	})

	It("x reopens a resolved question and expands its thread", func() {
		fw := &fakeWriter{}
		resolved := []entry.Comment{
			{ID: "q", Author: "ada", Body: "root q?", Question: true, Resolved: true, ResolvedBy: "bob"},
			{ID: "r", Author: "bob", Body: "the reply", ReplyTo: "q"},
		}
		reopened := []entry.Comment{
			{ID: "q", Author: "ada", Body: "root q?", Question: true},
			{ID: "r", Author: "bob", Body: "the reply", ReplyTo: "q"},
		}
		m := newViewerModel(viewerInput{
			title: "todo", body: "## Open\n\n- [ ] a\n",
			color: false, width: 80, comments: resolved,
			writer: fw, entryID: "deadbeef", actorKind: "human",
			provider: stubProvider{
				header: []string{"h"},
				reload: func() ([]string, []entry.Comment, error) { return []string{"h"}, reopened, nil },
			},
		})
		m = send(m, size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		Expect(fw.unresolved).To(BeTrue())
		Expect(fw.unresolveTarget).To(Equal("q"))
		Expect(m.nodeCollapsed["q"]).To(BeFalse())
		Expect(m.View()).To(ContainSubstring("the reply"))
		Expect(m.View()).To(ContainSubstring("reopened"))
	})

	It("x on an open question still enters resolve mode, not reopen", func() {
		fw := &fakeWriter{}
		m := send(newModel(fw, base), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		Expect(m.mode).To(Equal(modeResolveNote))
		Expect(fw.unresolved).To(BeFalse())
	})

	It("x is a no-op on a non-question root", func() {
		fw := &fakeWriter{}
		plain := []entry.Comment{{ID: "p", Author: "ada", Body: "just a note"}}
		m := send(newViewerModel(viewerInput{
			title: "todo", body: "## Open\n\n- [ ] a\n",
			color: false, width: 80, comments: plain,
			writer: fw, entryID: "deadbeef", actorKind: "human",
			provider: stubProvider{
				header: []string{"h"},
				reload: func() ([]string, []entry.Comment, error) { return []string{"h"}, plain, nil },
			},
		}), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		Expect(m.mode).To(Equal(modeNone))
		Expect(fw.resolved).To(BeFalse())
	})
})

var _ = Describe("viewerModel inapplicable write keys", func() {
	// The help popup advertises r/e/d/x unconditionally, so a key that cannot
	// act must say why rather than appear broken. The list cockpit already does
	// this ("not a quarantine item"); the viewer owes the reader the same.
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m viewerModel, msgs ...tea.Msg) viewerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(viewerModel)
	}
	withComments := func(cs []entry.Comment) viewerModel {
		return newViewerModel(viewerInput{
			title: "spec", body: "## Open\n\n- [ ] a\n",
			color: false, width: 80, comments: cs,
			writer: &fakeWriter{}, entryID: "deadbeef", actorKind: "human",
			provider: stubProvider{header: []string{"h"}},
		})
	}

	DescribeTable("says no comment is selected on an entry with none",
		func(key string) {
			m := send(withComments(nil), size)
			m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			Expect(m.mode).To(Equal(modeNone))
			Expect(m.View()).To(ContainSubstring("no comment selected"))
		},
		Entry("reply", "r"),
		Entry("edit", "e"),
		Entry("delete", "d"),
		Entry("resolve", "x"),
	)

	It("says x needs a question when the thread is a plain comment", func() {
		m := send(withComments([]entry.Comment{{ID: "p", Author: "ada", Body: "just a note"}}), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		Expect(m.mode).To(Equal(modeNone))
		Expect(m.View()).To(ContainSubstring("only a question can be resolved"))
	})

	It("clears the notice on the next keypress", func() {
		m := send(withComments(nil), size)
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		Expect(m.View()).To(ContainSubstring("no comment selected"))
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		Expect(m.View()).NotTo(ContainSubstring("no comment selected"))
	})
})

var _ = Describe("viewerModel $EDITOR escape", func() {
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m viewerModel, msgs ...tea.Msg) viewerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(viewerModel)
	}
	base := []entry.Comment{{ID: "q", Author: "ada", Body: "root q?", Question: true}}
	newModel := func(fw *fakeWriter) viewerModel {
		return newViewerModel(viewerInput{
			title: "todo", body: "## Open\n\n- [ ] a\n",
			color: false, width: 80, comments: base,
			writer: fw, entryID: "deadbeef", actorKind: "human",
			provider: stubProvider{
				header: []string{"h"},
				reload: func() ([]string, []entry.Comment, error) { return []string{"h"}, base, nil },
			},
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
		m = mm.(viewerModel)
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

var _ = Describe("viewerModel content type", func() {
	It("renders a non-markdown body as one block with no section items", func() {
		code := "package main\n\n# not a heading in Go\nfunc main() {}\n"
		m := newViewerModel(viewerInput{
			title: "code · abc", body: code, color: false, width: 80,
			contentType: "text/x-go",
			provider:    stubProvider{header: []string{"◆ shared"}},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		vm := mm.(viewerModel)
		Expect(vm.View()).To(ContainSubstring("func main() {}"))
		for _, it := range vm.items {
			Expect(it.kind).NotTo(Equal(itemSection))
		}
	})

	It("defaults an empty content type to markdown (folds headings)", func() {
		m := newViewerModel(viewerInput{
			title: "t", body: "## A\n\nx\n", color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		vm := mm.(viewerModel)
		hasSection := false
		for _, it := range vm.items {
			if it.kind == itemSection {
				hasSection = true
			}
		}
		Expect(hasSection).To(BeTrue())
	})
})

var _ = Describe("viewerModel genericity", func() {
	It("renders a non-todo entry through a plain provider", func() {
		body := "## Notes\n\nsome prose about the design.\n"
		comments := []entry.Comment{
			{ID: "c1", Author: "alice", Body: "first thought"},
		}
		m := newViewerModel(viewerInput{
			title:    "note · abc123",
			body:     body,
			color:    false,
			width:    80,
			comments: comments,
			provider: stubProvider{header: []string{"◆ shared"}},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		view := mm.(viewerModel).View()
		Expect(view).To(ContainSubstring("◆ shared"))      // provider header
		Expect(view).To(ContainSubstring("first thought")) // comment thread
		Expect(view).To(ContainSubstring("Notes"))         // body section heading
	})
})

var _ = Describe("viewerModel goto line", func() {
	It("<n>g scrolls so body line n is at the viewport top", func() {
		body := "## H\n\n" + strings.Repeat("x\n\n", 40)
		m := newViewerModel(viewerInput{
			title: "t", body: body, color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("5")})
		mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
		vm := mm.(viewerModel)
		Expect(vm.sv.YOffset()).To(Equal(vm.bodyStartLine + 4)) // body line 5 → bodyStart+4
	})

	It("gg still jumps the cursor to the first item", func() {
		m := newViewerModel(viewerInput{
			title: "t", body: "## A\n\nx\n\n## B\n\ny\n", color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
		mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
		mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
		Expect(mm.(viewerModel).cur).To(Equal(0))
	})
})

var _ = Describe("viewerModel numbered gutter", func() {
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	It("numbers body lines and leaves comments un-numbered", func() {
		m := newViewerModel(viewerInput{
			title: "t", body: "## Notes\n\nalpha line\n", color: false, width: 80,
			comments: []entry.Comment{{ID: "c1", Author: "ada", Body: "a remark"}},
			provider: stubProvider{header: []string{"h"}},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(size)
		view := mm.(viewerModel).View()
		Expect(view).To(ContainSubstring(" │ ")) // a number column separator is present
		Expect(view).To(ContainSubstring("alpha line"))
		Expect(view).To(ContainSubstring("a remark"))
	})

	It("numeric search matches body text, not the line-number gutter", func() {
		body := "## H\n\nalpha\n\nbravo 42 charlie\n"
		m := newViewerModel(viewerInput{
			title: "t", body: body, color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(size)
		vm := mm.(viewerModel)
		hits := vm.searchMatcher("42")
		Expect(hits).To(HaveLen(1)) // only the 'bravo 42 charlie' line, not a gutter number
		Expect(vm.contentRaw[hits[0]]).To(ContainSubstring("bravo 42 charlie"))
	})
})

var _ = Describe("showHeaderProvider", func() {
	It("returns the header, an empty fold, and reloads", func() {
		reloaded := []entry.Comment{{ID: "c9", Author: "z", Body: "later"}}
		p := showHeaderProvider{
			header: []string{"◆ shared", "kind: note"},
			reload: func() ([]string, []entry.Comment, error) {
				return []string{"◆ shared", "reloaded"}, reloaded, nil
			},
		}
		Expect(p.HeaderLines()).To(Equal([]string{"◆ shared", "kind: note"}))
		Expect(p.InitialFold()).To(BeEmpty())
		h, c, err := p.Reload()
		Expect(err).NotTo(HaveOccurred())
		Expect(h).To(ContainElement("reloaded"))
		Expect(c).To(Equal(reloaded))
	})

	It("drives a viewer that can reply to a comment on a non-todo entry", func() {
		fw := &fakeWriter{}
		base := []entry.Comment{{ID: "q", Author: "ada", Body: "root?", Question: true}}
		reloaded := append(append([]entry.Comment{}, base...), entry.Comment{ID: "r1", Author: "me", Body: "typed", ReplyTo: "q"})
		m := newViewerModel(viewerInput{
			title: "note · abc", body: "## Notes\n\nx\n", color: false, width: 80,
			contentType: "text/markdown", comments: base,
			writer: fw, entryID: "deadbeef", actorKind: "human",
			provider: showHeaderProvider{
				header: []string{"◆ shared"},
				reload: func() ([]string, []entry.Comment, error) { return []string{"◆ shared"}, reloaded, nil },
			},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
		mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyCtrlS})
		vm := mm.(viewerModel)
		Expect(fw.added).To(BeTrue())
		Expect(fw.addReplyTo).To(Equal("q"))
		Expect(vm.View()).To(ContainSubstring("typed"))
	})
})

var _ = Describe("viewerModel marker follows scroll", func() {
	It("moves the select marker to the top-of-view section when scrolling both ways", func() {
		body := "## Alpha\n\n" + strings.Repeat("a\n\n", 20) + "## Bravo\n\n" + strings.Repeat("b\n\n", 20)
		m := newViewerModel(viewerInput{
			title: "t", body: body, color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		Expect(mm.(viewerModel).items[mm.(viewerModel).cur].headingText).To(Equal("Alpha"))
		for range 100 { // scroll to the bottom → Bravo owns the top line
			mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		}
		down := mm.(viewerModel)
		Expect(down.items[down.cur].headingText).To(Equal("Bravo"))
		for range 100 { // scroll back to the top → Alpha again
			mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
		}
		up := mm.(viewerModel)
		Expect(up.items[up.cur].headingText).To(Equal("Alpha"))
	})
})

var _ = Describe("viewerModel cursor stays anchored", func() {
	It("does not pop the cursor off a still-visible section when scrolling", func() {
		body := "## Alpha\n\n" + strings.Repeat("a\n\n", 20) + "## Bravo\n\n" + strings.Repeat("b\n\n", 20)
		m := newViewerModel(viewerInput{
			title: "t", body: body, color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		})
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")}) // cursor → Bravo
		Expect(mm.(viewerModel).items[mm.(viewerModel).cur].headingText).To(Equal("Bravo"))
		for range 5 { // j while Bravo is still visible must NOT jump to Alpha
			mm, _ = mm.(viewerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		}
		Expect(mm.(viewerModel).items[mm.(viewerModel).cur].headingText).To(Equal("Bravo"))
	})
})

// ctrl+c is the terminal's universal abort. Every modal used to swallow it: the
// delete confirmation accepted only y/n/esc, and the textarea modes forwarded it
// to the textarea, which ignores it — so the one place the reflex matters most,
// a destructive prompt covering the content, was the one place the app could not
// be interrupted.
var _ = Describe("viewerModel ctrl+c escape hatch", func() {
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	send := func(m viewerModel, msgs ...tea.Msg) (viewerModel, tea.Cmd) {
		var mm tea.Model = m
		var cmd tea.Cmd
		for _, msg := range msgs {
			mm, cmd = mm.Update(msg)
		}
		return mm.(viewerModel), cmd
	}
	base := []entry.Comment{{ID: "q", Author: "ada", Body: "root q?", Question: true}}
	newModel := func() viewerModel {
		return newViewerModel(viewerInput{
			title: "t", body: "## Open\n\n- [ ] a\n", color: false, width: 80,
			comments: base, writer: &fakeWriter{}, entryID: "deadbeef", actorKind: "human",
			provider: stubProvider{header: []string{"h"}},
		})
	}

	It("quits from the delete-confirm modal", func() {
		m, _ := send(newModel(), size)
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
		Expect(m.mode).To(Equal(modeConfirmDelete))
		_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
		Expect(cmd).NotTo(BeNil())
		Expect(cmd()).To(Equal(tea.Quit()))
	})

	It("quits from a draft modal", func() {
		m, _ := send(newModel(), size)
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")},
			tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("half a thought")})
		_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
		Expect(cmd).NotTo(BeNil())
		Expect(cmd()).To(Equal(tea.Quit()))
	})

	It("preserves a non-empty draft on the way out", func() {
		m, _ := send(newModel(), size)
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")},
			tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("half a thought")})
		out, _ := send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
		Expect(out.spilled).NotTo(BeEmpty())
		body, err := os.ReadFile(out.spilled)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("half a thought"))
		Expect(os.Remove(out.spilled)).To(Succeed())
	})

	It("does not spill an empty draft", func() {
		m, _ := send(newModel(), size)
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		out, _ := send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
		Expect(out.spilled).To(BeEmpty())
	})

	It("does not spill from the delete confirm, which has no draft", func() {
		m, _ := send(newModel(), size)
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
		out, _ := send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
		Expect(out.spilled).To(BeEmpty())
	})
})

// G meant "last cursor item" here but "bottom" in the pager and the list. On a
// body whose final heading is not its final line that left content below with no
// single key to reach it.
var _ = Describe("viewerModel G reaches the end", func() {
	size := tea.WindowSizeMsg{Width: 80, Height: 12}
	send := func(m viewerModel, msgs ...tea.Msg) viewerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(viewerModel)
	}
	// The last heading sits well above the last line, so "last item" and "bottom"
	// are different places.
	body := "## One\n\nalpha\n\n## Two\n\n" + strings.Repeat("trailing line\n", 30)

	It("scrolls to the bottom of the content, not just the last item", func() {
		m := send(newViewerModel(viewerInput{
			title: "t", body: body, color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		}), size, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
		Expect(m.sv.ScrollLabel()).To(Equal("bot"))
	})

	It("still lands at the top on g", func() {
		m := send(newViewerModel(viewerInput{
			title: "t", body: body, color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		}), size,
			tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")},
			tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
		Expect(m.sv.ScrollLabel()).To(Equal("top"))
	})
})

// esc quit the pager, list and review viewer but did nothing at all here, so the
// same key ended the session on three surfaces and was inert on the fourth. It
// is now a layered dismiss: a committed search first, then quit.
var _ = Describe("viewerModel esc layering", func() {
	size := tea.WindowSizeMsg{Width: 80, Height: 20}
	send := func(m viewerModel, msgs ...tea.Msg) (viewerModel, tea.Cmd) {
		var mm tea.Model = m
		var cmd tea.Cmd
		for _, msg := range msgs {
			mm, cmd = mm.Update(msg)
		}
		return mm.(viewerModel), cmd
	}
	body := "## One\n\nalpha line\n\n## Two\n\nbeta line\n"
	newModel := func() viewerModel {
		return newViewerModel(viewerInput{
			title: "t", body: body, color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		})
	}
	runSearch := func(m viewerModel, q string) viewerModel {
		msgs := []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")}}
		for _, r := range q {
			msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		msgs = append(msgs, tea.KeyMsg{Type: tea.KeyEnter})
		out, _ := send(m, msgs...)
		return out
	}

	It("clears a committed search before quitting", func() {
		m, _ := send(newModel(), size)
		m = runSearch(m, "line")
		Expect(m.search.footer()).NotTo(BeEmpty())
		out, cmd := send(m, tea.KeyMsg{Type: tea.KeyEsc})
		Expect(cmd).To(BeNil(), "the first esc dismisses the search, it does not quit")
		Expect(out.search.footer()).To(BeEmpty())
	})

	It("quits on esc at the base layer", func() {
		m, _ := send(newModel(), size)
		_, cmd := send(m, tea.KeyMsg{Type: tea.KeyEsc})
		Expect(cmd).NotTo(BeNil())
		Expect(cmd()).To(Equal(tea.Quit()))
	})

	It("restores the folds a search expanded", func() {
		m, _ := send(newModel(), size)
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyCtrlAt}) // ^space: collapse all
		Expect(m.collapsed).NotTo(BeEmpty())
		m = runSearch(m, "line")
		out, _ := send(m, tea.KeyMsg{Type: tea.KeyEsc})
		Expect(out.collapsed).NotTo(BeEmpty(), "esc must put the reader's folds back")
	})
})

// The confirmation floats over the content it is asking about, so "Delete this
// comment?" alone gave the reader nothing to check a destructive action against.
var _ = Describe("viewerModel delete confirmation", func() {
	size := tea.WindowSizeMsg{Width: 100, Height: 24}
	send := func(m viewerModel, msgs ...tea.Msg) viewerModel {
		var mm tea.Model = m
		for _, msg := range msgs {
			mm, _ = mm.Update(msg)
		}
		return mm.(viewerModel)
	}
	newModel := func() viewerModel {
		return newViewerModel(viewerInput{
			title: "t", body: "## Open\n\n- [ ] a\n", color: false, width: 100,
			comments: []entry.Comment{{ID: "c1", Author: "ada", Body: "the first line\nand a second"}},
			writer:   &fakeWriter{}, entryID: "deadbeef", actorKind: "human",
			provider: stubProvider{header: []string{"h"}},
		})
	}

	It("names the author and quotes the comment it will delete", func() {
		m := send(newModel(), size, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
		Expect(m.mode).To(Equal(modeConfirmDelete))
		box := m.inputBox()
		Expect(box).To(ContainSubstring("ada"))
		Expect(box).To(ContainSubstring("the first line"))
		Expect(box).NotTo(ContainSubstring("and a second"), "one line is enough for a prompt")
	})
})

var _ = Describe("viewerModel exit echo", func() {
	It("does not leak the cursor marker into scrollback", func() {
		m := newViewerModel(viewerInput{
			title: "t", body: "## One\n\nalpha\n", color: false, width: 80,
			provider: stubProvider{header: []string{"h"}},
		})
		out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
		fm := out.(viewerModel)
		Expect(fm.sv.VisibleWindow()).To(ContainElement(ContainSubstring(cursorMarker)))
		Expect(strings.Join(fm.echoLines(), "\n")).NotTo(ContainSubstring(cursorMarker))
	})
})
