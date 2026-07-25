package tui_test

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

	It("clips the help overlay to the viewport so it never overflows", func() {
		sv := tui.NewScrollView("t")
		sv.SetStatus("ctx")
		sv.Resize(50, 16) // narrower + shorter than a natural 14-row/61-col help box
		sv.SetHelpRows([]string{
			"a very wide help row that plainly exceeds fifty display columns of width",
			"r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9", "r10", "r11", "r12", "r13", "r14",
		})
		sv.SetContent(strings.Repeat("body\n", 40))
		sv.ToggleHelp()
		out := sv.Render("footer")
		lines := strings.Split(out, "\n")
		Expect(len(lines)).To(BeNumerically("<=", 16)) // never taller than the terminal
		for _, ln := range lines {
			Expect(lipgloss.Width(ln)).To(BeNumerically("<=", 50)) // never wider
		}
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

// Chrome truncation is width-based, not byte-based: entry titles carry em-dashes
// and the todo cockpit's header arrives pre-rendered with SGR escapes, so byte
// slicing both under-filled the bar and cut through runes and escape sequences.
var _ = Describe("ScrollView chrome truncation", func() {
	row := func(out string, n int) string { return strings.Split(out, "\n")[n] }

	It("fills the width when the title is multi-byte, not stopping early", func() {
		sv := tui.NewScrollView(strings.Repeat("é", 30)) // 30 columns, 60 bytes
		sv.SetContent("body")
		sv.Resize(20, 6)
		title := row(sv.Render("f"), 0)
		Expect(ansi.StringWidth(title)).To(Equal(20))
		Expect(title).To(ContainSubstring("…"))
		Expect(utf8.ValidString(title)).To(BeTrue())
	})

	It("keeps the ellipsis when the cut lands on a multi-byte rune", func() {
		sv := tui.NewScrollView("Quarantine review — sub-project B: approve/reject")
		sv.SetContent("body")
		sv.Resize(21, 6) // the byte cut fell inside the em-dash and ate the ellipsis
		title := row(sv.Render("f"), 0)
		Expect(title).To(ContainSubstring("…"))
		Expect(utf8.ValidString(title)).To(BeTrue())
	})

	It("never leaves a partial escape sequence in a styled title", func() {
		sv := tui.NewScrollView("todo · 81be \x1b[31m◉ 0 awaiting you\x1b[0m   open 16")
		sv.SetContent("body")
		sv.Resize(18, 6)
		title := row(sv.Render("f"), 0)
		Expect(ansi.StringWidth(title)).To(Equal(18))
		Expect(title).NotTo(MatchRegexp(`\x1b\[[0-9;]*$`)) // no dangling CSI
	})

	It("does not count invisible escapes against the title's width budget", func() {
		plain := tui.NewScrollView("todo · 81be · awaiting you · open 16 · done 0")
		styled := tui.NewScrollView("todo · 81be · \x1b[31mawaiting you\x1b[0m · open 16 · done 0")
		for _, sv := range []*tui.ScrollView{&plain, &styled} {
			sv.SetContent("body")
			sv.Resize(40, 6)
		}
		Expect(ansi.StringWidth(row(styled.Render("f"), 0))).
			To(Equal(ansi.StringWidth(row(plain.Render("f"), 0))))
	})

	It("truncates an over-long footer instead of overflowing the pane", func() {
		sv := tui.NewScrollView("t")
		sv.SetContent("body")
		sv.Resize(30, 6)
		out := sv.Render(strings.Repeat("footer ", 20))
		lines := strings.Split(out, "\n")
		Expect(ansi.StringWidth(lines[len(lines)-1])).To(Equal(30))
	})

	It("truncates the status line to the width", func() {
		sv := tui.NewScrollView("t")
		sv.SetStatus(strings.Repeat("status ", 20))
		sv.SetContent("body")
		sv.Resize(30, 7)
		Expect(ansi.StringWidth(row(sv.Render("f"), 1))).To(Equal(30))
	})

	It("truncates the overlay footer too", func() {
		sv := tui.NewScrollView("t")
		sv.SetContent("body")
		sv.Resize(30, 6)
		out := sv.RenderOverlay(strings.Repeat("footer ", 20), "modal")
		lines := strings.Split(out, "\n")
		Expect(ansi.StringWidth(lines[len(lines)-1])).To(Equal(30))
	})
})

// clipBlock trimmed the placed box to the viewport with no signal, so on a short
// pane the popup silently lost its trailing rows — which is where the quit key
// lives. A help popup that hides how to quit is worse than none.
var _ = Describe("ScrollView help overlay clipping", func() {
	It("says so when the rows do not fit", func() {
		sv := tui.NewScrollView("t")
		sv.SetHelpRows([]string{"r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8"})
		sv.SetContent(strings.Repeat("body\n", 40))
		sv.Resize(40, 7) // a 5-row viewport cannot hold an 8-row box plus borders
		sv.ToggleHelp()
		Expect(sv.Render("f")).To(ContainSubstring("more"))
	})

	It("does not say so when every row fits", func() {
		sv := tui.NewScrollView("t")
		sv.SetHelpRows([]string{"r1", "r2"})
		sv.SetContent(strings.Repeat("body\n", 40))
		sv.Resize(40, 20)
		sv.ToggleHelp()
		Expect(sv.Render("f")).NotTo(ContainSubstring("more"))
	})

	It("still never overflows the viewport", func() {
		sv := tui.NewScrollView("t")
		sv.SetHelpRows([]string{"r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8"})
		sv.SetContent(strings.Repeat("body\n", 40))
		sv.Resize(40, 7)
		sv.ToggleHelp()
		for ln := range strings.SplitSeq(sv.Render("f"), "\n") {
			Expect(ansi.StringWidth(ln)).To(BeNumerically("<=", 40))
		}
	})
})

// A footer cannot wrap: reserved() is a fixed 2-3 chrome rows and the viewport
// height derives from it, so a second footer line would have to change the
// reservation — and it would change again whenever the search indicator appears,
// resizing the viewport under the reader. Hosts offer variants instead and the
// widest that fits wins.
var _ = Describe("ScrollView.Fit", func() {
	sized := func(w int) tui.ScrollView {
		sv := tui.NewScrollView("t")
		sv.SetContent("body")
		sv.Resize(w, 6)
		return sv
	}

	It("picks the richest variant that fits the width", func() {
		sv := sized(60)
		Expect(sv.Fit(strings.Repeat("x", 100), strings.Repeat("y", 50), "z")).
			To(Equal(strings.Repeat("y", 50)))
	})

	It("takes the first variant when the terminal is wide enough", func() {
		sv := sized(200)
		Expect(sv.Fit("rich hints here", "compact")).To(Equal("rich hints here"))
	})

	It("falls back to the last variant when none fit", func() {
		sv := sized(20)
		Expect(sv.Fit(strings.Repeat("x", 100), strings.Repeat("y", 50))).
			To(Equal(strings.Repeat("y", 50))) // Render truncates it
	})

	It("measures display width, not bytes, so ANSI does not cost a tier", func() {
		sv := sized(30)
		styled := "\x1b[31m" + strings.Repeat("x", 25) + "\x1b[0m" // 25 columns, 34 bytes
		Expect(sv.Fit(styled, "short")).To(Equal(styled))
	})

	It("budgets for the chrome padding", func() {
		sv := sized(20)
		Expect(sv.Fit(strings.Repeat("x", 19), "short")).To(Equal("short")) // 19 + 2 > 20
		Expect(sv.Fit(strings.Repeat("x", 18), "short")).To(Equal(strings.Repeat("x", 18)))
	})

	It("returns empty for no variants", func() {
		sv := sized(80)
		Expect(sv.Fit()).To(BeEmpty())
	})
})
