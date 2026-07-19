package tui_test

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/tui"
)

var _ = Describe("ScrollView", func() {
	It("renders title, content, and footer", func() {
		sv := tui.NewScrollView("MyTitle")
		sv.SetContent(strings.Join([]string{"line1", "line2", "line3"}, "\n"))
		sv.Resize(40, 6)
		out := sv.Render("footer-info")
		Expect(out).To(ContainSubstring("MyTitle"))
		Expect(out).To(ContainSubstring("line1"))
		Expect(out).To(ContainSubstring("footer-info"))
	})

	It("is not ready before the first Resize", func() {
		Expect(tui.NewScrollView("T").Ready()).To(BeFalse())
	})

	It("returns content-line offsets that contain the query (case-insensitive)", func() {
		sv := tui.NewScrollView("T")
		sv.SetContent("Alpha\nbravo\nAlpha again\n")
		sv.Resize(40, 6)
		Expect(sv.Matches("alpha")).To(Equal([]int{0, 2}))
		Expect(sv.Matches("")).To(BeEmpty())
		Expect(sv.Matches("zeta")).To(BeEmpty())
	})

	It("matches on content buffered before the first Resize", func() {
		sv := tui.NewScrollView("T")
		sv.SetContent("one\ntwo\n") // buffered (not yet Resized)
		Expect(sv.Matches("two")).To(Equal([]int{1}))
	})

	It("reports the scroll position as all/top/NN%/bot", func() {
		sv := tui.NewScrollView("T")
		sv.SetContent(strings.TrimRight(strings.Repeat("x\n", 20), "\n"))
		sv.Resize(40, 5) // viewport height 3, content 20
		Expect(sv.ScrollLabel()).To(Equal("top"))
		sv.SetYOffset(8)
		Expect(sv.ScrollLabel()).To(HaveSuffix("%"))
		sv.SetYOffset(100) // clamps to the bottom
		Expect(sv.ScrollLabel()).To(Equal("bot"))

		fits := tui.NewScrollView("T")
		fits.SetContent("a\nb")
		fits.Resize(40, 10)
		Expect(fits.ScrollLabel()).To(Equal("all"))
	})

	It("ScrollLabel uses the real content height set by the host (ignores padding)", func() {
		sv := tui.NewScrollView("T")
		sv.SetContent(strings.TrimRight(strings.Repeat("x\n", 20), "\n")) // 20 padded lines
		sv.SetContentHeight(3)                                            // only 3 are real
		sv.Resize(40, 12)                                                 // viewport height 10 > 3
		Expect(sv.ScrollLabel()).To(Equal("all"))
	})

	It("drops the chrome colour attributes when set plain", func() {
		// lipgloss neutralises styling on a non-TTY, so force a colour profile to
		// observe the chrome attributes; restore it after.
		old := lipgloss.ColorProfile()
		lipgloss.SetColorProfile(termenv.TrueColor)
		DeferCleanup(func() { lipgloss.SetColorProfile(old) })

		sv := tui.NewScrollView("MyTitle")
		sv.SetContent("body")
		sv.SetStatus("local ctx")
		sv.Resize(40, 8)

		styled := sv.Render("footer-info")
		Expect(styled).To(ContainSubstring("\x1b[7m")) // reverse title bar when styled

		sv.SetPlain(true)
		plain := sv.Render("footer-info")
		Expect(plain).NotTo(ContainSubstring("\x1b[7m")) // no reverse title
		Expect(plain).NotTo(ContainSubstring("\x1b[2m")) // no faint status/footer
		Expect(plain).To(ContainSubstring("MyTitle"))    // text and layout intact
		Expect(plain).To(ContainSubstring("footer-info"))
		Expect(plain).To(ContainSubstring("local ctx"))
	})

	It("replaces the body with the help overlay when open", func() {
		sv := tui.NewScrollView("T")
		sv.SetContent("body-text")
		sv.SetHelpRows([]string{"x  do thing"})
		sv.Resize(40, 10)
		sv.ToggleHelp()
		out := sv.Render("f")
		Expect(out).NotTo(ContainSubstring("body-text"))
		Expect(out).To(ContainSubstring("do thing"))
	})

	It("returns a visible window after Resize", func() {
		sv := tui.NewScrollView("T")
		sv.SetContent(strings.Join([]string{"a", "b", "c", "d"}, "\n"))
		sv.Resize(20, 4)
		Expect(sv.VisibleWindow()).NotTo(BeEmpty())
	})

	It("renders a sticky status line under the title when set", func() {
		sv := tui.NewScrollView("MyTitle")
		sv.SetContent(strings.Join([]string{"a", "b", "c"}, "\n"))
		sv.Resize(40, 8)
		sv.SetStatus("local-context")
		out := sv.Render("f")
		Expect(out).To(ContainSubstring("MyTitle"))
		Expect(out).To(ContainSubstring("local-context"))
		// The status row is reserved from the body: title+status+footer = 3 rows.
		Expect(sv.Height()).To(Equal(5))
	})

	It("scrolls the viewport on a forwarded key", func() {
		sv := tui.NewScrollView("T")
		sv.SetContent(strings.Join([]string{"1", "2", "3", "4", "5", "6", "7", "8"}, "\n"))
		sv.Resize(20, 4)
		before := sv.YOffset()
		sv.PassKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		sv.PassKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		Expect(sv.YOffset()).NotTo(Equal(before))
	})
})
