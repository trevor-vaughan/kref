package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// pagerContent is the input to the generic text pager: a titled block of display
// lines, optionally with a line-number gutter. Used by kref search and kref diff.
type pagerContent struct {
	title   string
	body    []string // display lines; numbered when number == true
	number  bool     // show the line-number gutter
	gutterW int      // total gutter width (digits+3); 0 when number == false
}

type pagerModel struct {
	sv      tui.ScrollView
	lines   []string // display lines, raw (no gutter): search and goto use this
	number  bool
	gutterW int

	search pagerSearch // shared incremental search (/, n/N)

	numBuf string // accumulated digits for <n>g
	color  bool   // chrome colour, toggled with t
}

func newPagerModel(pc pagerContent) pagerModel {
	m := pagerModel{
		sv:      tui.NewScrollView(pc.title),
		lines:   pc.body,
		number:  pc.number,
		gutterW: pc.gutterW,
	}
	m.color = true
	m.sv.SetHelpRows(pagerHelpRows(pc.number))
	m.sv.SetHorizontalStep(8) // ←/h →/l pan long lines (titles, wide diffs)
	return m
}

// pagerHelpRows returns the help key rows for the text pager. The goto-line row
// appears only with a number gutter: without visible line numbers there is
// nothing to aim <n>g at, and Update drops the digits — so advertising it for
// `kref search` promised a key that does nothing.
func pagerHelpRows(numbered bool) []string {
	rows := []string{
		"j/k  ↑↓      scroll",
		"^d/^u        page",
		"g/G          top/bottom",
		"←/h →/l      pan left/right",
	}
	if numbered {
		rows = append(rows, "<n>g         goto line")
	}
	return append(rows,
		"/  n/N       search / next-prev",
		"t            toggle colour",
		"?            toggle this help",
		"q  esc       quit",
	)
}

// content joins the lines for the viewport, prefixing a right-aligned line-number
// gutter when numbering is on.
func (m pagerModel) content() string {
	if !m.number {
		return strings.Join(m.lines, "\n")
	}
	d := m.gutterW - 3
	var b strings.Builder
	for i, ln := range m.lines {
		fmt.Fprintf(&b, "%*d │ ", d, i+1)
		b.WriteString(ln)
		if i < len(m.lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// visibleWindow returns the content lines currently shown in the viewport, to
// echo on quit so the content stays in scrollback. nil before the first size.
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
			m.search.input(msg, m.searchMatcher, &m.sv)
			return m, nil
		}
		if m.sv.HelpOpen() {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			m.sv.CloseHelp()
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "t":
			m.color = !m.color
			m.sv.SetPlain(!m.color)
			return m, nil
		case "?":
			m.sv.ToggleHelp()
			return m, nil
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if !m.number {
				break // no visible line targets without the gutter
			}
			m.numBuf += msg.String()
			return m, nil
		case "g":
			if m.numBuf != "" {
				n, _ := strconv.Atoi(m.numBuf)
				m.numBuf = ""
				m.gotoLine(n)
			} else {
				// Bare g goes to the top, and gg lands there too (the second g
				// is a no-op) — so the vim chord and the list cockpit's single
				// g both work, in every viewer.
				m.sv.GotoTop()
			}
			return m, nil
		case "G", "end":
			m.numBuf = ""
			m.sv.GotoBottom()
			return m, nil
		case "home":
			m.numBuf = ""
			m.sv.GotoTop()
			return m, nil
		case "/":
			m.numBuf = ""
			m.search.start()
			return m, nil
		case "n":
			m.numBuf = ""
			m.search.cycle(1, &m.sv)
			return m, nil
		case "N":
			m.numBuf = ""
			m.search.cycle(-1, &m.sv)
			return m, nil
		default:
			m.numBuf = ""
		}
	}
	cmd := m.sv.PassKey(msg)
	return m, cmd
}

// linesBelow returns the number of content lines below the visible window.
func (m *pagerModel) linesBelow() int {
	return max(0, len(m.lines)-(m.sv.YOffset()+m.sv.Height()))
}

// searchMatcher returns the offsets of raw (gutter-free) lines containing q, so a
// numeric query does not match the line-number gutter.
func (m *pagerModel) searchMatcher(q string) []int { return searchMatches(m.lines, q) }

// gotoLine scrolls so line n (1-based) is at the top, clamped to the range.
func (m *pagerModel) gotoLine(n int) {
	if len(m.lines) == 0 {
		return
	}
	if n < 1 {
		n = 1
	}
	if n > len(m.lines) {
		n = len(m.lines)
	}
	m.sv.SetYOffset(n - 1)
}

// footerInfo returns the plain-text footer content for the non-search case.
func (m pagerModel) footerInfo() string {
	info := m.sv.ScrollLabel()
	if b := m.linesBelow(); b > 0 {
		info = fmt.Sprintf("%s ↓%d", info, b)
	}
	if f := m.search.footer(); f != "" {
		info = f + " · " + info
	}
	if m.numBuf != "" {
		info = m.numBuf + "g · " + info
	}
	return info + "  ·  ? keys · q quit"
}

func (m pagerModel) View() string {
	if !m.sv.Ready() {
		return "\n  loading…"
	}
	if m.search.searching() {
		frame := strings.TrimRight(m.sv.Render(""), "\n")
		lines := strings.Split(frame, "\n")
		if len(lines) > 0 {
			lines[len(lines)-1] = promptStyle.Render(m.search.footer())
		}
		return strings.Join(lines, "\n")
	}
	return m.sv.Render(m.footerInfo())
}

// echoExit prints the pager's last visible window to w so the content stays in
// scrollback after the alt-screen is torn down.
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
