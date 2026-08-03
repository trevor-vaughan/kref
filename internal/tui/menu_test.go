package tui_test

import (
	"strings"
	"unicode/utf8"

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

	It("refreshes row values without moving the selection", func() {
		// A settings menu re-renders its rows after every change. Snapping back to
		// the top would send the next keypress to a row the reader never chose.
		m := newMenu()
		m.Move(1) // a -> e
		m.RefreshRows([]tui.MenuRow{
			{Key: "a", Label: "new comment", Enabled: true},
			{Key: "r", Label: "reply", Enabled: false, Detail: "tab to a comment"},
			{Key: "e", Label: "edit", Enabled: true, Value: "changed"},
		})
		sel, ok := m.Selected()
		Expect(ok).To(BeTrue())
		Expect(sel.Key).To(Equal("e"))
		Expect(sel.Value).To(Equal("changed"))
	})

	It("falls back to the first enabled row when a refresh drops the selected one", func() {
		m := newMenu()
		m.Move(1) // a -> e
		m.RefreshRows([]tui.MenuRow{{Key: "a", Label: "new comment", Enabled: true}})
		sel, ok := m.Selected()
		Expect(ok).To(BeTrue())
		Expect(sel.Key).To(Equal("a"))
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

var _ = Describe("Menu rendering width", func() {
	It("truncates a long row instead of letting it wrap and break the box", func() {
		m := tui.NewMenu("commands")
		m.SetRows([]tui.MenuRow{{
			Label:   "expand header (op-log + all links)",
			Detail:  "this view has no expanded header at all, not even a little bit",
			Enabled: false,
		}})
		out := m.Render(48, false)
		for line := range strings.SplitSeq(out, "\n") {
			Expect(utf8.RuneCountInString(line)).To(BeNumerically("<=", 48))
		}
	})

	It("shows a setting's current value", func() {
		m := tui.NewMenu("view options")
		m.SetRows([]tui.MenuRow{{Label: "line numbers", Value: "on", Enabled: true}})
		Expect(m.Render(60, false)).To(ContainSubstring("on"))
	})
})
