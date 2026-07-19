package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/trevor-vaughan/kref/internal/outline"
	"github.com/trevor-vaughan/kref/internal/tui"
)

// searchMatches returns the indices of lines that contain query
// (case-insensitive), in order. A blank query matches nothing.
func searchMatches(lines []string, query string) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var out []int
	for i, ln := range lines {
		if strings.Contains(strings.ToLower(ln), q) {
			out = append(out, i)
		}
	}
	return out
}

var promptStyle = lipgloss.NewStyle().Bold(true)

// pagerContent is the input to the pager: an un-numbered header block followed
// by a body that gets a line-number gutter when number is set.
type pagerContent struct {
	title   string
	header  []string                     // un-numbered lines (may be empty)
	body    []string                     // numbered lines when number == true
	number  bool                         // show the line-number gutter
	gutterW int                          // total gutter width (digits+3); 0 when number == false
	reload  func() (pagerContent, error) // optional: re-fetch content for the r hotkey
	expand  func() ([]string, error)     // optional: fetch expanded-header rows for the e hotkey

	// Fold (markdown only): rawBody is the un-rendered markdown whose heading tree
	// drives folding; foldRender re-renders body+comments for a fold state (nil for
	// non-markdown). markdown gates the fold keys.
	rawBody     string
	markdown    bool
	hasComments bool // the body has a trailing (foldable) Comments block
	foldRender  func(folded map[string]bool) foldedBody
}

// commentsFoldPath is the synthetic fold key for the pager's trailing Comments
// block — folded as one whole section (per-thread folding stays cockpit-only). The
// NUL prefix keeps it distinct from any real heading Path.
const commentsFoldPath = "\x00comments"

// renderedHeading records a surviving heading's Path and its body-relative offset
// in the rendered (folded) body lines — the pager maps a viewport line to its
// section through these.
type renderedHeading struct {
	path string
	line int
}

// foldedBody is the pager body re-rendered for a fold state: the display lines
// plus each surviving heading's rendered offset. Because glamour reflows the
// markdown, the offsets cannot be derived from the raw source — foldRender renders
// block-by-block and records them.
type foldedBody struct {
	lines    []string
	headings []renderedHeading
}

type pagerModel struct {
	sv        tui.ScrollView
	lines     []string // header ++ body, raw (no gutter): search and jumps use this
	bodyStart int      // index in lines where the body begins
	bodyCount int
	number    bool
	gutterW   int

	search pagerSearch // shared incremental search (/, n/N)

	numBuf   string // accumulated digits for <n>g (used by a later task)
	gPending bool   // first g of gg seen (used by a later task)

	reload func() (pagerContent, error) // optional r-hotkey re-fetch
	notice string                       // transient footer note (refresh result); cleared on next key

	expand   func() ([]string, error) // optional e-hotkey fetch
	baseHdr  []string                 // the collapsed header block
	extRows  []string                 // cached expanded-header rows
	extLoad  bool                     // extRows fetched successfully
	expanded bool                     // showing the expanded header

	outline     *outline.Outline                        // heading tree of rawBody (nil for non-markdown)
	folded      map[string]bool                         // fold state by heading Path
	foldRender  func(folded map[string]bool) foldedBody // re-render body+comments for a fold state
	headings    []renderedHeading                       // rendered offsets of surviving headings
	hasComments bool                                    // a foldable Comments block trails the body
}

func newPagerModel(pc pagerContent) pagerModel {
	m := pagerModel{
		sv:      tui.NewScrollView(pc.title),
		number:  pc.number,
		gutterW: pc.gutterW,
		reload:  pc.reload,
		expand:  pc.expand,
		baseHdr: pc.header,
	}
	body := m.initFold(pc)
	lines := make([]string, 0, len(pc.header)+len(body))
	lines = append(lines, pc.header...)
	lines = append(lines, body...)
	m.lines = lines
	m.bodyStart = len(pc.header)
	m.bodyCount = len(body)
	m.updateHelpRows()
	return m
}

// initFold configures the fold state from pc — the outline, an empty fold set,
// and the foldRender hook for a markdown entry, or clears it for non-markdown —
// and returns the body lines to display: foldRender({}) for a fold entry (so lines
// and heading offsets agree), else pc.body. Shared by newPagerModel and refresh.
func (m *pagerModel) initFold(pc pagerContent) []string {
	if pc.markdown && pc.foldRender != nil {
		m.outline = outline.Parse(pc.rawBody)
		m.folded = map[string]bool{}
		m.foldRender = pc.foldRender
		m.hasComments = pc.hasComments
		fb := pc.foldRender(m.folded)
		m.headings = fb.headings
		return fb.lines
	}
	m.outline, m.folded, m.foldRender, m.headings = nil, nil, nil, nil
	m.hasComments = false
	return pc.body
}

// pagerHelpRows returns the help key rows; hasReload/hasExpand/hasFold gate the
// hooked keys.
func pagerHelpRows(hasReload, hasExpand, hasFold bool) []string {
	rows := []string{
		"j/k  ↑↓      scroll",
		"^d/^u        page",
		"gg/G         top/bottom",
		"<n>g         goto line",
		"/  n/N       search / next-prev",
	}
	if hasFold {
		rows = append(rows,
			"tab/S-tab    jump to next/prev heading",
			"space o/c    fold top section · open/close",
			"O/C          open/close all sections")
	}
	if hasExpand {
		rows = append(rows, "e            expand header")
	}
	if hasReload {
		rows = append(rows, "r  ctrl+r    refresh")
	}
	return append(rows, "?            toggle this help", "q  esc       quit")
}

func (m *pagerModel) updateHelpRows() {
	m.sv.SetHelpRows(pagerHelpRows(m.reload != nil, m.expand != nil, m.outline != nil))
}

// refresh re-fetches content via the reload hook and swaps it in, preserving
// the scroll position (clamped by the viewport) and any active search query's
// matches against the new lines. A failed reload keeps the current content and
// surfaces the error in the footer.
func (m *pagerModel) refresh() {
	pc, err := m.reload()
	if err != nil {
		m.notice = "refresh failed: " + err.Error()
		return
	}
	m.sv.SetTitle(pc.title)
	m.number = pc.number
	m.gutterW = pc.gutterW
	body := m.initFold(pc) // re-seeds (or clears) fold state for the fresh content
	lines := make([]string, 0, len(pc.header)+len(body))
	lines = append(lines, pc.header...)
	lines = append(lines, body...)
	m.lines = lines
	m.bodyStart = len(pc.header)
	m.bodyCount = len(body)
	m.search.refresh(m.searchMatcher)
	// Reset the expanded-header state to the fresh base: the cached rows and the
	// old base header now describe stale content.
	m.baseHdr = pc.header
	m.expanded = false
	m.extLoad = false
	m.extRows = nil
	offset := m.sv.YOffset()
	m.sv.SetContent(m.content())
	m.sv.SetYOffset(offset) // viewport clamps to the new max
	m.notice = "refreshed"
	m.updateHelpRows()
}

// applyFold re-renders the body for the current fold state via foldRender and
// swaps it in, keeping the header and preserving the scroll offset (the viewport
// re-clamps). It refreshes the heading offsets used by topSection.
func (m *pagerModel) applyFold() {
	fb := m.foldRender(m.folded)
	m.headings = fb.headings
	lines := make([]string, 0, m.bodyStart+len(fb.lines))
	lines = append(lines, m.lines[:m.bodyStart]...)
	lines = append(lines, fb.lines...)
	m.lines = lines
	m.bodyCount = len(fb.lines)
	offset := m.sv.YOffset()
	m.sv.SetContent(m.content())
	m.sv.SetYOffset(offset)
}

// topSection returns the Path of the section at the viewport top — the innermost
// heading whose rendered span contains the top body line. Returns ("", false)
// for a non-markdown entry or when the top is above the first heading (preamble),
// so space can fall back to paging there.
func (m *pagerModel) topSection() (string, bool) {
	if m.outline == nil || len(m.headings) == 0 {
		return "", false
	}
	top := max(m.sv.YOffset()-m.bodyStart, 0)
	best, found := "", false
	for _, h := range m.headings {
		if h.line <= top {
			best, found = h.path, true // later headings are deeper/lower → keep the last
		}
	}
	return best, found
}

// setHeader rebuilds lines with hdr as the header block, keeping the body and
// preserving the scroll offset (the viewport re-clamps).
func (m *pagerModel) setHeader(hdr []string) {
	body := m.lines[m.bodyStart:]
	lines := make([]string, 0, len(hdr)+len(body))
	lines = append(lines, hdr...)
	lines = append(lines, body...)
	m.lines = lines
	m.bodyStart = len(hdr)
	offset := m.sv.YOffset()
	m.sv.SetContent(m.content())
	m.sv.SetYOffset(offset)
}

// toggleExpand fetches (once) and shows the expanded header, or restores the
// base header. A fetch error is surfaced in the footer and leaves it collapsed.
func (m *pagerModel) toggleExpand() {
	if m.expanded {
		m.setHeader(m.baseHdr)
		m.expanded = false
		return
	}
	if !m.extLoad {
		rows, err := m.expand()
		if err != nil {
			m.notice = "expand failed: " + err.Error()
			return
		}
		m.extRows = rows
		m.extLoad = true
	}
	m.setHeader(m.extRows)
	m.expanded = true
}

// content joins the lines for the viewport, prefixing a line-number gutter when
// numbering is on: blank for header lines, right-aligned numbers for body lines.
func (m pagerModel) content() string {
	if !m.number {
		return strings.Join(m.lines, "\n")
	}
	d := m.gutterW - 3
	var b strings.Builder
	for i, ln := range m.lines {
		if i < m.bodyStart {
			b.WriteString(strings.Repeat(" ", d))
			b.WriteString(" │ ")
		} else {
			fmt.Fprintf(&b, "%*d │ ", d, i-m.bodyStart+1)
		}
		b.WriteString(ln)
		if i < len(m.lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// visibleWindow returns the content lines currently shown in the viewport — the
// gutter-and-color-rendered slice from the scroll offset for one viewport
// height, clamped to the content. It returns nil before the first size message.
// Used to echo the last view to normal stdout on quit so it stays in scrollback.
func (m pagerModel) visibleWindow() []string {
	if !m.sv.Ready() {
		return nil
	}
	lines := strings.Split(m.content(), "\n")
	top := max(m.sv.YOffset(), 0)
	end := min(top+m.sv.Height(), len(lines))
	if top > end {
		top = end
	}
	return lines[top:end]
}

func (m pagerModel) Init() tea.Cmd { return nil }

func (m pagerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.sv.Resize(msg.Width, msg.Height)
		m.sv.SetContent(m.content())
		return m, nil

	case tea.KeyMsg:
		if m.search.searching() {
			if msg.String() == "enter" && m.outline != nil && len(m.folded) > 0 {
				m.folded = map[string]bool{} // expand every section so a fold never hides a hit
				m.applyFold()
			}
			m.search.input(msg, m.searchMatcher, &m.sv)
			return m, nil
		}
		m.notice = ""
		if m.sv.HelpOpen() {
			// Any key dismisses the popup and is swallowed; only ctrl+c, the
			// hard quit, still exits the pager.
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			m.sv.CloseHelp()
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q", "esc":
			// esc and q behave identically: dismiss the topmost layer. With the
			// help popup already handled above, that means collapse the expanded
			// header if it is open, otherwise quit.
			if m.expanded {
				m.numBuf, m.gPending = "", false
				m.toggleExpand()
				return m, nil
			}
			return m, tea.Quit
		case "r", "ctrl+r":
			// ctrl+r is the shared reload key (the cockpit uses it because r is
			// reply there); the pager keeps r as an alias for muscle memory.
			if m.reload != nil {
				m.numBuf, m.gPending = "", false
				m.refresh()
				return m, nil
			}
		case "e":
			if m.expand != nil {
				m.numBuf, m.gPending = "", false
				m.toggleExpand()
				return m, nil
			}
		case "?":
			m.sv.ToggleHelp()
			return m, nil
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if !m.number {
				// Lean mode: without the gutter there are no visible line
				// targets, so <n>g is not offered. numBuf stays empty, which
				// also makes the g-case's jump branch unreachable.
				break
			}
			m.numBuf += msg.String()
			m.gPending = false
			return m, nil
		case "g":
			if m.numBuf != "" {
				n, _ := strconv.Atoi(m.numBuf)
				m.numBuf = ""
				m.gPending = false
				m.gotoBodyLine(n)
			} else if m.gPending {
				m.gPending = false
				m.sv.GotoTop()
			} else {
				m.gPending = true
			}
			return m, nil
		case "G", "end":
			m.numBuf, m.gPending = "", false
			m.sv.GotoBottom()
			return m, nil
		case "home":
			m.numBuf, m.gPending = "", false
			m.sv.GotoTop()
			return m, nil
		case "/":
			m.numBuf, m.gPending = "", false
			m.search.start()
			return m, nil
		case "n":
			m.numBuf, m.gPending = "", false
			m.search.cycle(1, &m.sv)
			return m, nil
		case "N":
			m.numBuf, m.gPending = "", false
			m.search.cycle(-1, &m.sv)
			return m, nil
		case " ":
			m.numBuf, m.gPending = "", false
			if p, ok := m.topSection(); ok {
				m.folded[p] = !m.folded[p]
				m.applyFold()
				return m, nil
			}
			// No section at the viewport top (preamble or non-markdown): fall
			// through to page down.
		case "o", "c":
			m.numBuf, m.gPending = "", false
			if p, ok := m.topSection(); ok {
				m.folded[p] = msg.String() == "c"
				m.applyFold()
			}
			return m, nil
		case "tab":
			m.numBuf, m.gPending = "", false
			m.jumpSection(1)
			return m, nil
		case "shift+tab":
			m.numBuf, m.gPending = "", false
			m.jumpSection(-1)
			return m, nil
		case "O":
			m.numBuf, m.gPending = "", false
			if m.outline != nil {
				m.folded = map[string]bool{}
				m.applyFold()
				return m, nil
			}
		case "C":
			m.numBuf, m.gPending = "", false
			if m.outline != nil {
				for _, p := range m.outline.AllPaths() {
					m.folded[p] = true
				}
				if m.hasComments {
					m.folded[commentsFoldPath] = true
				}
				m.applyFold()
				return m, nil
			}
		default:
			m.numBuf, m.gPending = "", false
		}
	}
	cmd := m.sv.PassKey(msg)
	return m, cmd
}

// jumpSection scrolls so the next (dir=1) / previous (dir=-1) heading relative to
// the viewport top sits at the top. No-op for a non-markdown entry (no headings).
func (m *pagerModel) jumpSection(dir int) {
	if len(m.headings) == 0 {
		return
	}
	top := m.sv.YOffset() - m.bodyStart
	target := -1
	if dir > 0 {
		for _, h := range m.headings {
			if h.line > top {
				target = h.line
				break
			}
		}
	} else {
		for _, h := range m.headings {
			if h.line < top {
				target = h.line // keep the last heading before the top
			}
		}
	}
	if target >= 0 {
		m.sv.SetYOffset(m.bodyStart + target)
	}
}

// linesBelow returns the number of content lines below the visible window — how
// much is left to scroll, shown in the footer.
func (m *pagerModel) linesBelow() int {
	return max(0, len(m.lines)-(m.sv.YOffset()+m.sv.Height()))
}

// searchMatcher returns the offsets of raw (gutter-free) body/header lines that
// contain q — the pager's match source, kept gutter-free so a numeric query does
// not match the line-number gutter.
func (m *pagerModel) searchMatcher(q string) []int { return searchMatches(m.lines, q) }

// gotoBodyLine scrolls so body line n (1-based) is at the top, clamped to the
// body range. The viewport additionally clamps to its own max offset.
func (m *pagerModel) gotoBodyLine(n int) {
	if m.bodyCount == 0 {
		return
	}
	if n < 1 {
		n = 1
	}
	if n > m.bodyCount {
		n = m.bodyCount
	}
	m.sv.SetYOffset(m.bodyStart + n - 1)
}

// footerInfo returns the plain-text footer content for the non-search case.
// ScrollView.Render calls this to compose the styled footer line.
func (m pagerModel) footerInfo() string {
	info := m.sv.ScrollLabel()
	if b := m.linesBelow(); b > 0 {
		info = fmt.Sprintf("%s ↓%d", info, b)
	}
	if m.notice != "" {
		info = m.notice + " · " + info
	}
	if f := m.search.footer(); f != "" {
		info = f + " · " + info // "match i/N" (never active here — View handles the prompt)
	}
	if m.numBuf != "" {
		info = m.numBuf + "g · " + info
	}
	return info + "  ·  ? help · q quit"
}

// footer returns the composed footer string (styled for search, plain otherwise).
// Tests assert substrings against this value.
func (m pagerModel) footer() string {
	if m.search.searching() {
		return promptStyle.Render(m.search.footer())
	}
	return m.footerInfo()
}

func (m pagerModel) View() string {
	if !m.sv.Ready() {
		return "\n  loading…"
	}
	if m.search.searching() {
		// Compose the frame manually so the search prompt stays bold and is not
		// routed through sv.Render's faint footer path.
		title := m.sv.Render("") // use sv for title+body chrome only
		// Strip the sv-rendered footer (last line) and append the bold prompt.
		frame := strings.TrimRight(title, "\n")
		// sv.Render returns "title\nbody\nfooter" — we need to replace the footer.
		lines := strings.Split(frame, "\n")
		if len(lines) > 0 {
			lines[len(lines)-1] = promptStyle.Render(m.search.footer())
		}
		return strings.Join(lines, "\n")
	}
	return m.sv.Render(m.footerInfo())
}

// echoExit prints the pager's last visible window to w so the content stays in
// the terminal scrollback after the alt-screen is torn down. Silent when there
// is nothing to show.
func echoExit(w io.Writer, m pagerModel) {
	win := m.visibleWindow()
	if len(win) == 0 {
		return
	}
	fmt.Fprintln(w, strings.Join(win, "\n"))
}

func Page(pc pagerContent) error {
	p := tea.NewProgram(
		newPagerModel(pc),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithOutput(os.Stdout),
	)
	final, err := p.Run()
	if err != nil {
		return err
	}
	if m, ok := final.(pagerModel); ok {
		echoExit(os.Stdout, m)
	}
	return nil
}
