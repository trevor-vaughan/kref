package main

import (
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// action is one key binding, declared once and read by everything that needs to
// know about keys: dispatch, the help popup, and (from the comment menu on) the
// menus. The help popup used to be a hand-written slice beside a 25-case switch,
// with nothing keeping the two in agreement — which is how it came to advertise
// keys whose behaviour had moved on.
//
// Do receives the pressed key because a few actions care which one it was (the
// digit accumulator). A nil Do means the viewport handles the key: the row
// exists to document it, and Passthrough must be set so dispatch lets it fall
// through rather than swallowing it.
type action struct {
	Keys        []string
	Display     string // how the keys read in help; defaults to Keys[0]
	HelpRow     string // actions sharing a HelpRow merge into one help line
	Group       string // semantic group: nav | view | search | comment
	Label       string
	Hidden      bool // dispatched but never advertised (retired keys)
	Passthrough bool // no handler: the viewport owns this key
	Enabled     func(m *viewerModel) bool
	Do          func(m *viewerModel, key string) tea.Cmd
}

// viewerActions is the canonical, ordered registry of every key the entry viewer
// binds — the single source of truth for dispatch and help. Order is help order.
var viewerActions = []action{
	{
		Keys: []string{"up", "k"}, Display: "j/k ↑/↓", HelpRow: "scroll-line", Group: "nav",
		Label: "scroll a line",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.scrollLines(-1); return nil },
	},
	{
		Keys: []string{"down", "j"}, Display: "j/k ↑/↓", HelpRow: "scroll-line", Group: "nav",
		Label: "scroll a line",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.scrollLines(1); return nil },
	},
	{
		Keys: []string{"pgup", "pgdown"}, Display: "pgup/pgdn", HelpRow: "scroll-page", Group: "nav",
		Label: "scroll a page", Passthrough: true,
	},
	{
		Keys: []string{"ctrl+d", "ctrl+u"}, Display: "ctrl+d/u", HelpRow: "scroll-half", Group: "nav",
		Label: "scroll a half page", Passthrough: true,
	},
	{
		Keys: []string{"tab"}, Display: "tab/S-tab", HelpRow: "cursor", Group: "nav",
		Label: "next/prev item",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.moveCursor(1); return nil },
	},
	{
		Keys: []string{"shift+tab"}, Display: "tab/S-tab", HelpRow: "cursor", Group: "nav",
		Label: "next/prev item",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.moveCursor(-1); return nil },
	},
	{
		Keys: []string{"right", "l"}, Display: "→/← l/h", HelpRow: "into-out", Group: "nav",
		Label: "into a reply / out to parent",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.cursorInto(); return nil },
	},
	{
		Keys: []string{"left", "h"}, Display: "→/← l/h", HelpRow: "into-out", Group: "nav",
		Label: "into a reply / out to parent",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.cursorOut(); return nil },
	},
	{
		Keys: []string{"g"}, Display: "g/G", HelpRow: "top-bottom", Group: "nav",
		Label: "top / bottom",
		Do: func(m *viewerModel, _ string) tea.Cmd {
			if m.numBuf != "" {
				n, _ := strconv.Atoi(m.numBuf)
				m.numBuf = ""
				m.gotoBodyLine(n)
				return nil
			}
			// Bare g goes to the top, and gg lands there too (the second g is a
			// no-op) — so the vim chord and the list cockpit's single g both work,
			// in every viewer.
			m.gotoItem(0)
			return nil
		},
	},
	{
		Keys: []string{"G", "end"}, Display: "g/G", HelpRow: "top-bottom", Group: "nav",
		Label: "top / bottom",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.gotoBottom(); return nil },
	},
	{
		Keys:    []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"},
		Display: "<n>g", HelpRow: "goto-line", Group: "nav", Label: "goto body line n",
		Do: func(m *viewerModel, key string) tea.Cmd { m.numBuf += key; return nil },
	},
	{
		Keys: []string{" "}, Display: "space", HelpRow: "fold", Group: "view",
		Label: "fold the current section",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.toggleFold(); return nil },
	},
	{
		// ^space — terminals send NUL for ctrl+space.
		Keys: []string{"ctrl+@"}, Display: "^space", HelpRow: "fold-all", Group: "view",
		Label: "fold / unfold everything",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.toggleFoldAll(); return nil },
	},
	{
		Keys: []string{"/"}, Display: "/ n/N", HelpRow: "search", Group: "search",
		Label: "search / next / prev",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.search.start(); return nil },
	},
	{
		Keys: []string{"n"}, Display: "/ n/N", HelpRow: "search", Group: "search",
		Label: "search / next / prev",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.search.cycle(1, &m.sv); return nil },
	},
	{
		Keys: []string{"N"}, Display: "/ n/N", HelpRow: "search", Group: "search",
		Label: "search / next / prev",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.search.cycle(-1, &m.sv); return nil },
	},
	{
		Keys: []string{"r"}, Display: "r/e/d/x", HelpRow: "comment", Group: "comment",
		Label:   "reply / edit / delete / resolve↔reopen",
		Enabled: func(m *viewerModel) bool { return m.writer != nil },
		Do:      func(m *viewerModel, _ string) tea.Cmd { m.startReply(); return nil },
	},
	{
		Keys: []string{"e"}, Display: "r/e/d/x", HelpRow: "comment", Group: "comment",
		Label:   "reply / edit / delete / resolve↔reopen",
		Enabled: func(m *viewerModel) bool { return m.writer != nil },
		Do:      func(m *viewerModel, _ string) tea.Cmd { m.startEdit(); return nil },
	},
	{
		Keys: []string{"d"}, Display: "r/e/d/x", HelpRow: "comment", Group: "comment",
		Label:   "reply / edit / delete / resolve↔reopen",
		Enabled: func(m *viewerModel) bool { return m.writer != nil },
		Do:      func(m *viewerModel, _ string) tea.Cmd { m.startDelete(); return nil },
	},
	{
		Keys: []string{"x"}, Display: "r/e/d/x", HelpRow: "comment", Group: "comment",
		Label:   "reply / edit / delete / resolve↔reopen",
		Enabled: func(m *viewerModel) bool { return m.writer != nil },
		Do:      func(m *viewerModel, _ string) tea.Cmd { return m.startResolve() },
	},
	{
		Keys: []string{"ctrl+r"}, HelpRow: "refresh", Group: "view", Label: "refresh",
		Do: func(m *viewerModel, _ string) tea.Cmd {
			if m.reload != nil {
				m.doReload("refreshed")
			}
			return nil
		},
	},
	{
		Keys: []string{"t"}, HelpRow: "colour", Group: "view", Label: "toggle colour",
		Do: func(m *viewerModel, _ string) tea.Cmd {
			m.color = !m.color
			m.applyViewport()
			return nil
		},
	},
	{
		Keys: []string{"?"}, Display: "? q esc", HelpRow: "meta", Group: "view",
		Label: "help / quit",
		Do:    func(m *viewerModel, _ string) tea.Cmd { m.sv.ToggleHelp(); return nil },
	},
	{
		Keys: []string{"ctrl+c", "q"}, Display: "? q esc", HelpRow: "meta", Group: "view",
		Label: "help / quit",
		Do:    func(_ *viewerModel, _ string) tea.Cmd { return tea.Quit },
	},
	{
		Keys: []string{"esc"}, Display: "? q esc", HelpRow: "meta", Group: "view",
		Label: "help / quit",
		Do:    func(m *viewerModel, _ string) tea.Cmd { return m.dismiss() },
	},
	{
		// Retired fold keys, held inert rather than dropped: space folds the section
		// under the cursor and ^space folds everything, so a stale finger must not
		// fall through to the viewport (and `o` stays free for the open-an-entry
		// gesture the other cockpits give it).
		Keys: []string{"o", "c", "O", "C"}, Group: "view", Label: "retired fold keys (inert)",
		Hidden: true,
		Do:     func(_ *viewerModel, _ string) tea.Cmd { return nil },
	},
}

// actionIndex maps every bound key to its action. Built once at init; the
// uniqueness the lookup depends on is asserted by the table's own spec.
var actionIndex = func() map[string]action {
	idx := make(map[string]action, len(viewerActions)*2)
	for _, a := range viewerActions {
		for _, k := range a.Keys {
			idx[k] = a
		}
	}
	return idx
}()

func actionForKey(k string) (action, bool) {
	a, ok := actionIndex[k]
	return a, ok
}

// display returns the help spelling of an action's keys.
func (a action) display() string {
	if a.Display != "" {
		return a.Display
	}
	return a.Keys[0]
}

// helpRowWidth is the column the help labels start at, matching the layout the
// popup used when these rows were written by hand.
const helpRowWidth = 14

// helpRows renders the help popup from the action table. Actions sharing a
// HelpRow merge into one line — r/e/d/x is four dispatchable actions but one
// thing to say — so the popup stays as short as it was when hand-written, while
// still being derived from what dispatch actually binds.
func helpRows() []string {
	var order []string
	byRow := map[string]action{}
	for _, a := range viewerActions {
		if a.Hidden {
			continue
		}
		if _, seen := byRow[a.HelpRow]; !seen {
			order = append(order, a.HelpRow)
			byRow[a.HelpRow] = a
		}
	}
	rows := make([]string, 0, len(order))
	for _, key := range order {
		a := byRow[key]
		disp := a.display()
		pad := max(helpRowWidth-utf8.RuneCountInString(disp), 1)
		rows = append(rows, disp+strings.Repeat(" ", pad)+a.Label)
	}
	return rows
}
