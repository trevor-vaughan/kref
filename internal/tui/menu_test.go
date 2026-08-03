package tui_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/tui"
)

var _ = Describe("Menu", func() {
	rows := []tui.MenuRow{
		{Key: "a", Label: "new comment", Enabled: true},
		{Key: "r", Label: "reply", Enabled: false, Detail: "tab to a comment"},
		{Key: "e", Label: "edit", Enabled: true},
	}

	newMenu := func() *tui.Menu {
		m := tui.NewMenu("comment")
		m.SetRows(rows)
		return m
	}

	It("starts on the first enabled row", func() {
		sel, ok := newMenu().Selected()
		Expect(ok).To(BeTrue())
		Expect(sel.Key).To(Equal("a"))
	})

	It("skips disabled rows when moving", func() {
		m := newMenu()
		m.Move(1) // a -> e, stepping over the disabled r
		sel, ok := m.Selected()
		Expect(ok).To(BeTrue())
		Expect(sel.Key).To(Equal("e"))
	})

	It("stops at the ends rather than wrapping", func() {
		m := newMenu()
		m.Move(-1)
		sel, _ := m.Selected()
		Expect(sel.Key).To(Equal("a"))
		m.Move(1)
		m.Move(1)
		sel, _ = m.Selected()
		Expect(sel.Key).To(Equal("e"))
	})

	It("looks up an enabled row by key", func() {
		got, ok := newMenu().ByKey("e")
		Expect(ok).To(BeTrue())
		Expect(got.Label).To(Equal("edit"))
	})

	It("refuses to select a disabled row by key", func() {
		// The key is real but unavailable right now; the caller must not fire it.
		_, ok := newMenu().ByKey("r")
		Expect(ok).To(BeFalse())
	})

	It("reports no selection when every row is disabled", func() {
		m := tui.NewMenu("comment")
		m.SetRows([]tui.MenuRow{{Key: "r", Label: "reply"}})
		_, ok := m.Selected()
		Expect(ok).To(BeFalse())
	})

	It("filters rows by label and resets the selection", func() {
		m := newMenu()
		m.SetFilter("edit")
		Expect(m.Rows()).To(HaveLen(1))
		sel, ok := m.Selected()
		Expect(ok).To(BeTrue())
		Expect(sel.Key).To(Equal("e"))
	})

	It("renders every row, the title and the subtitle", func() {
		m := newMenu()
		m.SetSubtitle(`on "ada: needs a repro"`)
		out := m.Render(60, false)
		Expect(out).To(ContainSubstring("comment"))
		Expect(out).To(ContainSubstring(`on "ada: needs a repro"`))
		Expect(out).To(ContainSubstring("new comment"))
		Expect(out).To(ContainSubstring("reply"))
		Expect(out).To(ContainSubstring("tab to a comment")) // the reason it is dim
	})

	It("marks the selected row and emits no colour when plain", func() {
		out := newMenu().Render(60, false)
		Expect(out).To(ContainSubstring("❯"))
		Expect(out).NotTo(ContainSubstring("\x1b["))
	})
})
