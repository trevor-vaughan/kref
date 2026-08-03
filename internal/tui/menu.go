package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

var (
	menuBoxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	menuTitleStyle    = lipgloss.NewStyle().Bold(true)
	menuSubtitleStyle = lipgloss.NewStyle().Faint(true)
	menuDimStyle      = lipgloss.NewStyle().Faint(true)
)

// MenuRow is one choice in a Menu. Key is the accelerator that fires the same
// action outside the menu, so the menu doubles as the documentation for it — an
// empty Key means the row is reachable only from here. A disabled row stays
// visible and carries its reason in Detail: showing why something is unavailable
// beats hiding it and leaving the reader to guess.
//
// ID is the host's opaque handle for the row. It exists because Key cannot serve
// as identity: a keyless row still has to be firable once selected.
type MenuRow struct {
	ID      string
	Key     string
	Label   string
	Detail  string
	Enabled bool
}

// Menu is a passive selectable overlay, in the same spirit as ScrollView: it
// owns no application state and performs no actions. The host supplies rows,
// routes keys to Move/ByKey, and acts on whatever Selected reports.
type Menu struct {
	title    string
	subtitle string
	rows     []MenuRow
	filter   string
	cur      int // index into the filtered rows
}

func NewMenu(title string) *Menu { return &Menu{title: title} }

// SetRows replaces the menu's rows and selects the first enabled one.
func (m *Menu) SetRows(rows []MenuRow) {
	m.rows = rows
	m.resetCursor()
}

func (m *Menu) SetSubtitle(s string) { m.subtitle = s }

// SetFilter narrows the menu to rows whose label or key contains s
// (case-insensitive), and re-selects the first enabled match.
func (m *Menu) SetFilter(s string) {
	m.filter = s
	m.resetCursor()
}

func (m *Menu) Filter() string { return m.filter }

// Rows returns the currently visible rows, after any filter.
func (m *Menu) Rows() []MenuRow {
	if m.filter == "" {
		return m.rows
	}
	needle := strings.ToLower(m.filter)
	var out []MenuRow
	for _, r := range m.rows {
		if strings.Contains(strings.ToLower(r.Label), needle) ||
			strings.Contains(strings.ToLower(r.Key), needle) {
			out = append(out, r)
		}
	}
	return out
}

// resetCursor puts the selection on the first enabled visible row, or past the
// end when there is none.
func (m *Menu) resetCursor() {
	rows := m.Rows()
	for i, r := range rows {
		if r.Enabled {
			m.cur = i
			return
		}
	}
	m.cur = len(rows)
}

// Move steps the selection by d, skipping disabled rows and stopping at the
// ends. Wrapping would let a held key cycle silently; stopping says "that's all".
func (m *Menu) Move(d int) {
	rows := m.Rows()
	if d == 0 {
		return
	}
	step := 1
	if d < 0 {
		step = -1
	}
	steps := abs(d)
	for range steps {
		i := m.cur + step
		for i >= 0 && i < len(rows) && !rows[i].Enabled {
			i += step
		}
		if i < 0 || i >= len(rows) {
			return // nothing further in that direction
		}
		m.cur = i
	}
}

// Selected returns the highlighted row, or false when nothing is selectable.
func (m *Menu) Selected() (MenuRow, bool) {
	rows := m.Rows()
	if m.cur < 0 || m.cur >= len(rows) || !rows[m.cur].Enabled {
		return MenuRow{}, false
	}
	return rows[m.cur], true
}

// ByKey returns the visible, enabled row bound to k. A disabled row reports
// false: its key is real but cannot act right now, and the host must not fire it.
func (m *Menu) ByKey(k string) (MenuRow, bool) {
	if k == "" {
		return MenuRow{}, false
	}
	for _, r := range m.Rows() {
		if r.Key == k && r.Enabled {
			return r, true
		}
	}
	return MenuRow{}, false
}

// Render draws the menu as a bordered box sized to width. Disabled rows and the
// hint are dimmed only when colour is on, so plain output stays ANSI-free and
// deterministic for tests and pipes.
func (m *Menu) Render(width int, color bool) string {
	rows := m.Rows()
	keyW := 0
	for _, r := range rows {
		if n := utf8.RuneCountInString(r.Key); n > keyW {
			keyW = n
		}
	}

	var b strings.Builder
	b.WriteString(styled(menuTitleStyle, m.title, color))
	if m.subtitle != "" {
		b.WriteString(" · " + styled(menuSubtitleStyle, m.subtitle, color))
	}
	if m.filter != "" {
		b.WriteString("  /" + m.filter)
	}
	b.WriteString("\n")

	for i, r := range rows {
		marker := "  "
		if i == m.cur && r.Enabled {
			marker = cursorMark + " "
		}
		line := marker + pad(r.Key, keyW) + "  " + r.Label
		if r.Detail != "" {
			line += "  " + r.Detail
		}
		if !r.Enabled {
			line = styled(menuDimStyle, line, color)
		}
		b.WriteString(line + "\n")
	}
	if len(rows) == 0 {
		b.WriteString(styled(menuDimStyle, "  no matches", color) + "\n")
	}
	b.WriteString(styled(menuHintStyle(), "↑↓ choose · enter do · esc cancel", color))

	return menuBoxStyle.Width(max(20, min(width-4, 72))).Render(b.String())
}

// cursorMark flags the highlighted row, matching the cockpits' selection marker.
const cursorMark = "❯"

func menuHintStyle() lipgloss.Style { return menuSubtitleStyle }

// styled applies a lipgloss style only when colour is on.
func styled(s lipgloss.Style, text string, color bool) string {
	if !color {
		return text
	}
	return s.Render(text)
}

func pad(s string, w int) string {
	if n := w - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
