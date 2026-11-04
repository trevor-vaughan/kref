package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

var (
	menuBoxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	menuTitleStyle    = lipgloss.NewStyle().Bold(true)
	menuSectionStyle  = lipgloss.NewStyle().Bold(true)
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
//
// Section groups rows under a heading at render time. It is not a row: nothing
// in Move, Selected or ByKey knows about it, so a filter that empties a section
// removes its heading for free and a caller that sets no sections gets exactly
// the flat menu it had before.
type MenuRow struct {
	ID      string
	Key     string
	Section string
	Label   string
	Value   string // current setting for a toggle row ("on"/"off"/…); "" for actions
	Detail  string
	Enabled bool
}

// Menu is a passive selectable overlay, in the same spirit as ScrollView: it
// owns no application state and performs no actions. The host supplies rows,
// routes keys to Move/ByKey, and acts on whatever Selected reports.
type Menu struct {
	title    string
	subtitle string
	hint     string
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

// RefreshRows replaces the rows and keeps the selection where it is — for a menu
// whose rows carry live values and are re-rendered after every change. Resetting
// there would send the next keypress to a row the reader never chose. The
// selection falls back to the first enabled row if the refresh dropped or
// disabled the one it was on.
func (m *Menu) RefreshRows(rows []MenuRow) {
	m.rows = rows
	if _, ok := m.Selected(); !ok {
		m.resetCursor()
	}
}

func (m *Menu) SetSubtitle(s string) { m.subtitle = s }

// SetHint replaces the footer line. The default names the keys every menu binds;
// a host that gives enter a specific verb, and a subject to apply it to, says so
// there instead of leaving the reader to infer it.
func (m *Menu) SetHint(s string) { m.hint = s }

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
	keyW, labelW := 0, 0
	valued := false
	for _, r := range rows {
		if n := utf8.RuneCountInString(r.Key); n > keyW {
			keyW = n
		}
		if n := utf8.RuneCountInString(r.Label); n > labelW {
			labelW = n
		}
		if r.Value != "" {
			valued = true
		}
	}

	// Rows are truncated to the box rather than left to wrap: a long reason that
	// wraps mid-phrase splits across lines and breaks the frame, the same trap
	// the help overlay already avoids.
	boxW := max(20, min(width-4, 72))
	inner := max(1, boxW-2)

	var b strings.Builder
	b.WriteString(styled(menuTitleStyle, m.title, color))
	if m.subtitle != "" {
		b.WriteString(" · " + styled(menuSubtitleStyle, m.subtitle, color))
	}
	if m.filter != "" {
		b.WriteString("  /" + m.filter)
	}
	b.WriteString("\n")

	prevSection := ""
	for i, r := range rows {
		if r.Section != "" && r.Section != prevSection {
			// A blank line before every heading, including the first: it separates
			// the first group from the title the same way it separates the groups
			// from each other.
			b.WriteString("\n")
			b.WriteString(styled(menuSectionStyle, truncate(r.Section, inner), color) + "\n")
		}
		prevSection = r.Section

		marker := "  "
		if i == m.cur && r.Enabled {
			marker = cursorMark + " "
		}
		label := r.Label
		if valued {
			label = pad(label, labelW) // line the value column up
		}
		line := marker + pad(r.Key, keyW) + "  " + label
		if r.Value != "" {
			line += "  " + r.Value
		}
		if r.Detail != "" {
			line += "  " + r.Detail
		}
		line = truncate(line, inner)
		if !r.Enabled {
			line = styled(menuDimStyle, line, color)
		}
		b.WriteString(line + "\n")
	}
	if len(rows) == 0 {
		b.WriteString(styled(menuDimStyle, "  no matches", color) + "\n")
	}
	hint := m.hint
	if hint == "" {
		hint = "↑↓ choose · enter do · esc cancel"
	}
	b.WriteString(styled(menuHintStyle(), hint, color))

	return menuBoxStyle.Width(boxW).Render(b.String())
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
