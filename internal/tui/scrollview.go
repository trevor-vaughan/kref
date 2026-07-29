package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1)
	statusStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
	footerStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
	// plainChromeStyle drops the colour/emphasis attributes (reverse/faint/bold)
	// for the "colour off" toggle, keeping only the 1-column padding so widths and
	// sticky-row layout are byte-for-byte identical to the styled chrome.
	plainChromeStyle = lipgloss.NewStyle().Padding(0, 1)
	helpBoxStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// ScrollView is a passive scroll+chrome component: a titled, footered viewport
// with a centered help overlay and a scrollback-preserving exit window. It holds
// NO key policy — the host model routes keys and calls these methods.
type ScrollView struct {
	title         string
	status        string // optional second sticky line under the title (local context)
	vp            viewport.Model
	w, h          int
	ready         bool
	plain         bool // render chrome without colour/emphasis (the host's "colour off")
	helpOpen      bool
	helpRows      []string
	pendingBody   string   // buffered content set before first Resize
	contentLines  []string // the current content, split into lines (for Matches)
	contentHeight int      // real content height for ScrollLabel; -1 = use len(contentLines)
	hStep         int      // horizontal pan step; re-applied whenever the viewport is (re)built
}

// SetPlain toggles plain chrome rendering: when true, the title/status/footer are
// drawn without colour or emphasis (reverse/faint/bold), matching a host that has
// turned colour off. The layout (padding, row count) is unchanged.
func (s *ScrollView) SetPlain(p bool) { s.plain = p }

// Plain reports whether chrome is rendered without colour.
func (s ScrollView) Plain() bool { return s.plain }

// chrome returns the style to use for a chrome row: the given styled variant, or
// the attribute-free plain style when the plain toggle is on.
func (s *ScrollView) chrome(styled lipgloss.Style) lipgloss.Style {
	if s.plain {
		return plainChromeStyle
	}
	return styled
}

// Fit returns the first variant that fits a chrome row at the current width —
// so a host can offer a rich hint line for a wide terminal and progressively
// terser ones for narrow terminals without doing its own width arithmetic.
//
// Chrome cannot wrap: reserved() is a fixed 2-3 rows and the viewport height
// derives from it, so a second footer line would have to change that
// reservation — and would change it again whenever a search indicator appears,
// resizing the viewport under the reader mid-scroll. Choosing a variant keeps
// the row count fixed while still spending whatever width is available.
//
// The last variant is the fallback; Render truncates it if even that overflows.
func (s *ScrollView) Fit(variants ...string) string {
	if len(variants) == 0 {
		return ""
	}
	budget := s.vp.Width - 2 // the chrome styles add Padding(0, 1)
	for _, v := range variants {
		if ansi.StringWidth(v) <= budget {
			return v
		}
	}
	return variants[len(variants)-1]
}

// chromeRow renders one sticky row (title, status, footer) at exactly the
// viewport width. Every chrome style carries Padding(0, 1), which is applied
// *after* truncation, so the text budget is the width less those two columns —
// otherwise a full-width row overflows by two and the terminal wraps it, pushing
// the layout down a line.
func (s *ScrollView) chromeRow(styled lipgloss.Style, text string) string {
	return s.chrome(styled).Render(truncate(text, max(s.vp.Width-2, 0)))
}

// NewScrollView returns a new ScrollView with the given title.
func NewScrollView(title string) ScrollView { return ScrollView{title: title, contentHeight: -1} }

func (s *ScrollView) SetTitle(t string) { s.title = t }
func (s ScrollView) Title() string      { return s.title }

// SetStatus sets an optional second sticky line rendered under the title (e.g.
// local/context info). An empty string hides it. Setting it re-fits the viewport
// so the extra row is reserved.
func (s *ScrollView) SetStatus(st string) {
	had := s.status != ""
	s.status = st
	if s.ready && had != (st != "") {
		s.vp.Height = max(s.h-s.reserved(), 1)
	}
}

// reserved is the number of chrome rows (title, optional status, footer).
func (s *ScrollView) reserved() int {
	if s.status != "" {
		return 3
	}
	return 2
}
func (s *ScrollView) SetHelpRows(r []string) { s.helpRows = r }

// SetHorizontalStep enables horizontal scrolling: the underlying viewport pans
// left/right by n columns on ←/h and →/l (routed through PassKey), so lines wider
// than the viewport (e.g. long list/search titles) can be read without wrapping.
// n == 0 (the default) leaves horizontal scrolling off.
func (s *ScrollView) SetHorizontalStep(n int) {
	s.hStep = n
	if s.ready {
		s.vp.SetHorizontalStep(n)
	}
}
func (s ScrollView) Ready() bool            { return s.ready }
func (s ScrollView) HelpOpen() bool         { return s.helpOpen }
func (s *ScrollView) ToggleHelp()           { s.helpOpen = !s.helpOpen }
func (s *ScrollView) CloseHelp()            { s.helpOpen = false }
func (s ScrollView) Width() int             { return s.vp.Width }
func (s ScrollView) Height() int            { return s.vp.Height }
func (s ScrollView) YOffset() int           { return s.vp.YOffset }
func (s *ScrollView) SetYOffset(n int)      { s.vp.SetYOffset(n) }
func (s *ScrollView) GotoTop()              { s.vp.GotoTop() }
func (s *ScrollView) GotoBottom()           { s.vp.GotoBottom() }
func (s ScrollView) ScrollPercent() float64 { return s.vp.ScrollPercent() }

// Resize (re)sizes the viewport, reserving one row each for title and footer.
// If SetContent was called before the first Resize, the buffered content is
// applied now so callers can call SetContent → Resize in any order.
func (s *ScrollView) Resize(w, h int) {
	s.w, s.h = w, h
	bodyH := max(h-s.reserved(), 1)
	if !s.ready {
		s.vp = viewport.New(w, bodyH)
		s.vp.SetHorizontalStep(s.hStep) // survive the viewport (re)build
		if s.pendingBody != "" {
			s.vp.SetContent(s.pendingBody)
			s.pendingBody = ""
		}
		s.ready = true
		return
	}
	s.vp.Width = w
	s.vp.Height = bodyH
}

// SetContent sets the viewport content. If called before the first Resize, the
// content is buffered and applied when Resize is called.
func (s *ScrollView) SetContent(content string) {
	s.contentLines = strings.Split(content, "\n")
	s.contentHeight = -1 // default: real height = line count, until the host overrides
	if !s.ready {
		s.pendingBody = content
		return
	}
	s.vp.SetContent(content)
}

// SetContentHeight records the real (unpadded) content height for ScrollLabel.
// Hosts that append reachability padding to the content (e.g. the cockpit) set
// this so the scroll marker reflects the true content, not the padding. Reset to
// the line count on the next SetContent.
func (s *ScrollView) SetContentHeight(n int) { s.contentHeight = n }

// ScrollLabel reports the vertical scroll position over the real content height:
// "all" when it fits, else "top" / "NN%" / "bot". Mirrors a viewer's position
// indicator.
func (s *ScrollView) ScrollLabel() string {
	ch := s.contentHeight
	if ch < 0 {
		ch = len(s.contentLines)
	}
	h := s.vp.Height
	if h <= 0 || ch <= h {
		return "all"
	}
	top := s.vp.YOffset
	if top <= 0 {
		return "top"
	}
	if top+h >= ch {
		return "bot"
	}
	return fmt.Sprintf("%d%%", top*100/(ch-h))
}

// Matches returns the offsets of content lines containing query
// (case-insensitive), in order — a passive search primitive the host uses to
// drive scroll-to. A blank query matches nothing.
func (s *ScrollView) Matches(query string) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var out []int
	for i, ln := range s.contentLines {
		if strings.Contains(strings.ToLower(ln), q) {
			out = append(out, i)
		}
	}
	return out
}

// PassKey forwards a message to the viewport (default scrolling: arrows, pgup/pgdn,
// ctrl+d/u, space/b, j/k). Returns any command the viewport emits.
func (s *ScrollView) PassKey(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	return cmd
}

// VisibleWindow returns the currently-visible content lines (to echo on exit so
// the last frame stays in scrollback). nil before the first Resize.
func (s *ScrollView) VisibleWindow() []string {
	if !s.ready {
		return nil
	}
	return strings.Split(s.vp.View(), "\n")
}

// Render composes title + (help overlay | viewport) + footer.
func (s *ScrollView) Render(footerInfo string) string {
	if !s.ready {
		return "\n  loading…"
	}
	title := s.chromeRow(titleStyle, s.title)
	footer := s.chromeRow(footerStyle, footerInfo)
	body := s.vp.View()
	if s.helpOpen {
		body = s.helpOverlay()
	}
	if s.status != "" {
		return fmt.Sprintf("%s\n%s\n%s\n%s", title, s.chromeRow(statusStyle, s.status), body, footer)
	}
	return fmt.Sprintf("%s\n%s\n%s", title, body, footer)
}

// RenderOverlay composes title + (optional status) + the given content centered
// over the viewport region + footer. Callers use it to float a modal (e.g. an
// input box) over the content while keeping the sticky header and footer.
func (s *ScrollView) RenderOverlay(footerInfo, content string) string {
	if !s.ready {
		return "\n  loading…"
	}
	title := s.chromeRow(titleStyle, s.title)
	footer := s.chromeRow(footerStyle, footerInfo)
	body := lipgloss.Place(s.vp.Width, s.vp.Height, lipgloss.Center, lipgloss.Center, content)
	if s.status != "" {
		return fmt.Sprintf("%s\n%s\n%s\n%s", title, s.chromeRow(statusStyle, s.status), body, footer)
	}
	return fmt.Sprintf("%s\n%s\n%s", title, body, footer)
}

func (s *ScrollView) helpOverlay() string {
	// Bound each row to the viewport width (border 2 + padding 2) so the box is
	// never wider than the screen, then hard-clip the placed block to the viewport
	// in both dimensions — lipgloss.Place does not clip oversized content, so an
	// unbounded box would spill past the alt-screen edges and garble.
	inner := max(1, s.vp.Width-4)
	// A short pane cannot show every row. Drop the overflow here and say so,
	// rather than letting clipBlock swallow rows silently — the last row is
	// usually the quit key, and a help popup that hides how to leave is worse
	// than no popup at all.
	rows := s.helpRows
	fits := max(1, s.vp.Height-3) // 2 border rows + the "keys" caption
	trimmed := false
	if len(rows) > fits {
		rows, trimmed = rows[:max(fits-1, 1)], true
	}
	out := make([]string, 0, len(rows)+1)
	for _, r := range rows {
		out = append(out, truncate(r, inner))
	}
	if trimmed {
		out = append(out, truncate("… more keys — enlarge the window", inner))
	}
	box := helpBoxStyle.Render("keys\n" + strings.Join(out, "\n"))
	placed := lipgloss.Place(s.vp.Width, s.vp.Height, lipgloss.Center, lipgloss.Center, box)
	return clipBlock(placed, s.vp.Width, s.vp.Height)
}

// clipBlock hard-limits a rendered block to w columns and h rows, so an oversized
// overlay can never spill past the viewport edges.
func clipBlock(block string, w, h int) string {
	lines := strings.Split(block, "\n")
	if h >= 0 && len(lines) > h {
		lines = lines[:h]
	}
	for i, ln := range lines {
		lines[i] = ansi.Truncate(ln, w, "")
	}
	return strings.Join(lines, "\n")
}

// truncate shortens str to w display columns with an ellipsis, measuring
// rendered width rather than bytes: a multi-byte rune costs one column and an
// SGR escape costs none. ansi.Truncate also closes any style it cuts through,
// so a chrome row can never emit a half rune or a dangling escape sequence.
func truncate(str string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(str) <= w {
		return str
	}
	if w == 1 {
		return "…"
	}
	return ansi.Truncate(str, w, "…")
}
