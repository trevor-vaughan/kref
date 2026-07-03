package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1)
	footerStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
	promptStyle = lipgloss.NewStyle().Bold(true)
)

// pagerContent is the input to the pager: an un-numbered header block followed
// by a body that gets a line-number gutter when number is set.
type pagerContent struct {
	title   string
	header  []string                     // un-numbered lines (may be empty)
	body    []string                     // numbered lines when number == true
	number  bool                         // show the line-number gutter
	gutterW int                          // total gutter width (digits+3); 0 when number == false
	reload  func() (pagerContent, error) // optional: re-fetch content for the r hotkey
}

type pagerModel struct {
	title     string
	lines     []string // header ++ body, raw (no gutter): search and jumps use this
	bodyStart int      // index in lines where the body begins
	bodyCount int
	number    bool
	gutterW   int
	vp        viewport.Model
	ready     bool
	helpOpen  bool

	searching bool
	query     string
	matches   []int
	matchIdx  int

	numBuf   string // accumulated digits for <n>g (used by a later task)
	gPending bool   // first g of gg seen (used by a later task)

	reload func() (pagerContent, error) // optional r-hotkey re-fetch
	notice string                       // transient footer note (refresh result); cleared on next key
}

func newPagerModel(pc pagerContent) pagerModel {
	lines := make([]string, 0, len(pc.header)+len(pc.body))
	lines = append(lines, pc.header...)
	lines = append(lines, pc.body...)
	return pagerModel{
		title:     pc.title,
		lines:     lines,
		bodyStart: len(pc.header),
		bodyCount: len(pc.body),
		number:    pc.number,
		gutterW:   pc.gutterW,
		reload:    pc.reload,
	}
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
	lines := make([]string, 0, len(pc.header)+len(pc.body))
	lines = append(lines, pc.header...)
	lines = append(lines, pc.body...)
	m.title = pc.title
	m.lines = lines
	m.bodyStart = len(pc.header)
	m.bodyCount = len(pc.body)
	m.number = pc.number
	m.gutterW = pc.gutterW
	m.matches = searchMatches(m.lines, m.query)
	m.matchIdx = 0
	offset := m.vp.YOffset
	m.vp.SetContent(m.content())
	m.vp.SetYOffset(offset) // viewport clamps to the new max
	m.notice = "refreshed"
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

func (m pagerModel) Init() tea.Cmd { return nil }

func (m pagerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h := msg.Height - 2 // title + footer
		if h < 1 {
			h = 1
		}
		if !m.ready {
			m.vp = viewport.New(msg.Width, h)
			m.vp.SetContent(m.content())
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = h
		}
		return m, nil

	case tea.KeyMsg:
		if m.searching {
			return m.updateSearchInput(msg)
		}
		m.notice = ""
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			if m.reload != nil {
				m.numBuf, m.gPending = "", false
				m.refresh()
				return m, nil
			}
		case "?":
			m.helpOpen = !m.helpOpen
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
				m.vp.GotoTop()
			} else {
				m.gPending = true
			}
			return m, nil
		case "G", "end":
			m.numBuf, m.gPending = "", false
			m.vp.GotoBottom()
			return m, nil
		case "home":
			m.numBuf, m.gPending = "", false
			m.vp.GotoTop()
			return m, nil
		case "/":
			m.numBuf, m.gPending = "", false
			m.searching = true
			m.query = ""
			return m, nil
		case "n":
			m.numBuf, m.gPending = "", false
			m.jumpMatch(1)
			return m, nil
		case "N":
			m.numBuf, m.gPending = "", false
			m.jumpMatch(-1)
			return m, nil
		default:
			m.numBuf, m.gPending = "", false
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m pagerModel) updateSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.searching = false
		m.matches = searchMatches(m.lines, m.query)
		m.matchIdx = 0
		if len(m.matches) > 0 {
			m.vp.SetYOffset(m.matches[0])
		}
	case "esc":
		m.searching = false
		m.query = ""
	case "backspace":
		if m.query != "" {
			m.query = m.query[:len(m.query)-1]
		}
	default:
		if len(msg.Runes) > 0 {
			m.query += string(msg.Runes)
		}
	}
	return m, nil
}

func (m *pagerModel) jumpMatch(dir int) {
	if len(m.matches) == 0 {
		return
	}
	m.matchIdx = (m.matchIdx + dir + len(m.matches)) % len(m.matches)
	m.vp.SetYOffset(m.matches[m.matchIdx])
}

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
	m.vp.SetYOffset(m.bodyStart + n - 1)
}

func (m pagerModel) View() string {
	if !m.ready {
		return "\n  loading…"
	}
	title := titleStyle.Render(truncate(m.title, m.vp.Width))
	return fmt.Sprintf("%s\n%s\n%s", title, m.vp.View(), m.footer())
}

func (m pagerModel) footer() string {
	if m.searching {
		return promptStyle.Render("/" + m.query)
	}
	if m.helpOpen {
		help := "j/k ↑↓ scroll · ctrl+d/u page · gg/G top/bottom · <n>g line · / search · n/N match · q quit"
		if m.reload != nil {
			help = "j/k ↑↓ scroll · ctrl+d/u page · gg/G top/bottom · <n>g line · / search · n/N match · r refresh · q quit"
		}
		return footerStyle.Render(help)
	}
	info := fmt.Sprintf("%3.0f%%", m.vp.ScrollPercent()*100)
	if m.notice != "" {
		info = m.notice + " · " + info
	}
	if len(m.matches) > 0 {
		info = fmt.Sprintf("match %d/%d · %s", m.matchIdx+1, len(m.matches), info)
	}
	if m.numBuf != "" {
		info = m.numBuf + "g · " + info
	}
	return footerStyle.Render(info + "  ·  ? help · q quit")
}

func truncate(s string, w int) string {
	if w <= 0 || len(s) <= w {
		return s
	}
	if w <= 1 {
		return s[:w]
	}
	return s[:w-1] + "…"
}

// Page displays content in a full-screen scrollable pager with search and a
// line-number gutter. It runs on the alternate screen and returns when the user
// quits.
func Page(pc pagerContent) error {
	p := tea.NewProgram(
		newPagerModel(pc),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithOutput(os.Stdout),
	)
	_, err := p.Run()
	return err
}
