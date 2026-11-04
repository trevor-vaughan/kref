package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/git-bug/git-bug/entity"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("todo picker", func() {
	idA := entity.Id("1111111111111111111111111111111111111111")
	idB := entity.Id("2222222222222222222222222222222222222222")

	cands := []todoCandidate{
		{ID: idA, Title: "kref (this project)", Line: "111111111111  kref (this project)  ◉2   11  1w ago"},
		{ID: idB, Title: "infra migration", Line: "222222222222  infra migration           4  2d ago"},
	}

	newPicker := func() *todoPickerModel { return newTodoPickerModel(cands, "", false, 80) }

	It("shows both sections and starts on the first todo", func() {
		out := newPicker().View()
		Expect(out).To(ContainSubstring("TODOS"))
		Expect(out).To(ContainSubstring("ACTIONS"))
		Expect(out).To(ContainSubstring("on: kref (this project)"))
	})

	It("returns show for enter on a todo", func() {
		m := newPicker()
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.result.verb).To(Equal("show"))
		Expect(m.result.id).To(Equal(idB))
	})

	It("keeps the todo as the subject after moving into ACTIONS", func() {
		m := newPicker()
		m.Update(tea.KeyMsg{Type: tea.KeyDown}) // infra migration
		m.Update(tea.KeyMsg{Type: tea.KeyDown}) // fmt
		Expect(m.View()).To(ContainSubstring("on: infra migration"))
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.result.verb).To(Equal("fmt"))
		Expect(m.result.id).To(Equal(idB))
	})

	It("names the pending verb and its subject in the hint", func() {
		m := newPicker()
		Expect(m.View()).To(ContainSubstring("enter show ‹kref (this project)›"))
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		Expect(m.View()).To(ContainSubstring("enter fmt ‹infra migration›"))
	})

	It("fires a verb accelerator against the highlighted todo", func() {
		m := newPicker()
		m.Update(key('l'))
		Expect(m.result.verb).To(Equal("lint"))
		Expect(m.result.id).To(Equal(idA))
	})

	It("quits empty on esc", func() {
		m := newPicker()
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		Expect(m.result.verb).To(BeEmpty())
	})

	It("narrows to matching todos while filtering", func() {
		m := newPicker()
		m.Update(key('/'))
		m.Update(key('i'))
		m.Update(key('n'))
		m.Update(key('f'))
		out := m.View()
		Expect(out).To(ContainSubstring("infra migration"))
		Expect(out).NotTo(ContainSubstring("kref (this project)  ◉2"))
	})

	It("types q and f into the filter instead of quitting or formatting", func() {
		m := newPicker()
		m.Update(key('/'))
		m.Update(key('q'))
		m.Update(key('f'))
		Expect(m.result.verb).To(BeEmpty())
		Expect(m.View()).To(ContainSubstring("/qf"))
	})

	It("clears the filter on esc rather than quitting out of it", func() {
		m := newPicker()
		m.Update(key('/'))
		m.Update(key('z'))
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		Expect(m.result.verb).To(BeEmpty())
		Expect(m.View()).To(ContainSubstring("kref (this project)"))
	})

	Describe("with the verb fixed on the command line", func() {
		It("offers no ACTIONS section and returns that verb", func() {
			m := newTodoPickerModel(cands, "fmt", false, 80)
			out := m.View()
			Expect(out).NotTo(ContainSubstring("ACTIONS"))
			Expect(out).To(ContainSubstring("fmt · which todo?"))
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(m.result.verb).To(Equal("fmt"))
			Expect(m.result.id).To(Equal(idA))
		})
	})
})

var _ = Describe("todoHeaderProvider switching", func() {
	It("advertises the capability and records a request", func() {
		p := &todoHeaderProvider{}
		var _ Switchable = p // the capability the palette type-asserts for
		Expect(p.switchRequested).To(BeFalse())
		p.RequestSwitch()
		Expect(p.switchRequested).To(BeTrue())
	})
})
