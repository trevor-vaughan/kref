package main

import (
	"strconv"
	"strings"
	"sync"
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
	MenuLabel   string // this action alone, for a menu row; Label may cover several
	Hidden      bool   // dispatched but never advertised (retired keys)
	Passthrough bool   // no handler: the viewport owns this key
	Enabled     func(m *viewerModel) bool
	// Detail explains why the action is unavailable. It is the disabled row's
	// dim note in a menu AND the notice a bare keypress leaves, so the two can
	// never give different reasons. An empty string means "fail silently".
	Detail func(m *viewerModel) string
	Do     func(m *viewerModel, key string) tea.Cmd
}

// viewerActionList is the canonical, ordered registry of every key the entry
// viewer binds — the single source of truth for dispatch, help and the menus.
// Order is help order.
//
// It is a function rather than a package var because the comment menu is built
// from this same table: a var whose closures reach openCommentMenu, which reads
// the var, is an initialization cycle. Dispatch reads the memoised index, so the
// slice is rebuilt only when a menu or the help popup is opened.
func viewerActionList() []action {
	return []action{
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
			// home rides this row rather than the viewport, matching G/end and the
			// list cockpit's "g", "home". Update clears numBuf for every key that is
			// neither a digit nor g, so home never arrives with a count pending.
			Keys: []string{"g", "home"}, Display: "g/G", HelpRow: "top-bottom", Group: "nav",
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
			Keys: []string{"a"}, Display: "a/A", HelpRow: "new-comment", Group: "comment",
			Label: "new comment / question", MenuLabel: "new comment",
			Enabled: canWrite,
			Do:      func(m *viewerModel, _ string) tea.Cmd { m.startNewComment(false); return nil },
		},
		{
			Keys: []string{"A"}, Display: "a/A", HelpRow: "new-comment", Group: "comment",
			Label: "new comment / question", MenuLabel: "new question",
			Enabled: canWrite,
			Do:      func(m *viewerModel, _ string) tea.Cmd { m.startNewComment(true); return nil },
		},
		{
			Keys: []string{"r"}, Display: "r/e/d/x", HelpRow: "comment", Group: "comment",
			Label: "reply / edit / delete / resolve↔reopen", MenuLabel: "reply",
			Enabled: func(m *viewerModel) bool { return canWrite(m) && m.selectedCommentID() != "" },
			Detail:  writeDetail("reply to"),
			Do:      func(m *viewerModel, _ string) tea.Cmd { m.startReply(); return nil },
		},
		{
			Keys: []string{"e"}, Display: "r/e/d/x", HelpRow: "comment", Group: "comment",
			Label: "reply / edit / delete / resolve↔reopen", MenuLabel: "edit",
			Enabled: func(m *viewerModel) bool { return canWrite(m) && m.liveComment() != nil },
			Detail:  writeDetail("edit"),
			Do:      func(m *viewerModel, _ string) tea.Cmd { m.startEdit(); return nil },
		},
		{
			Keys: []string{"d"}, Display: "r/e/d/x", HelpRow: "comment", Group: "comment",
			Label: "reply / edit / delete / resolve↔reopen", MenuLabel: "delete",
			Enabled: func(m *viewerModel) bool { return canWrite(m) && m.liveComment() != nil },
			Detail:  writeDetail("delete"),
			Do:      func(m *viewerModel, _ string) tea.Cmd { m.startDelete(); return nil },
		},
		{
			Keys: []string{"x"}, Display: "r/e/d/x", HelpRow: "comment", Group: "comment",
			Label: "reply / edit / delete / resolve↔reopen", MenuLabel: "resolve ↔ reopen",
			Enabled: func(m *viewerModel) bool {
				root := m.selectedThreadRoot()
				return canWrite(m) && root != nil && root.Question
			},
			Detail: func(m *viewerModel) string {
				if m.writer == nil {
					return ""
				}
				if root := m.selectedThreadRoot(); root != nil && !root.Question {
					return "only a question can be resolved — this thread is a plain comment"
				}
				return m.noCommentReason("resolve")
			},
			Do: func(m *viewerModel, _ string) tea.Cmd { return m.startResolve() },
		},
		{
			Keys: []string{"c"}, HelpRow: "comment-menu", Group: "view",
			Label:   "comment actions",
			Enabled: canWrite,
			Do:      func(m *viewerModel, _ string) tea.Cmd { m.openCommentMenu(); return nil },
		},
		{
			// Keyless, so it lives in the palette: expanding is a session action,
			// not a saved preference, which is what separates it from the settings
			// menu. It is also where the base header's "… +N more" link overflow
			// resolves.
			HelpRow: "expand", Group: "view",
			Label: "expand header", MenuLabel: "expand header (op-log + links)",
			Enabled: func(m *viewerModel) bool {
				_, ok := m.provider.(ExpandableHeader)
				return ok
			},
			Detail: func(_ *viewerModel) string { return "no expanded header here" },
			Do:     func(m *viewerModel, _ string) tea.Cmd { m.toggleExpandHeader(); return nil },
		},
		{
			// `/` is already incremental search over the body, so the palette takes
			// `:` — the other half of the vim pair, and free.
			Keys: []string{":"}, HelpRow: "palette", Group: "view",
			Label: "commands without a key",
			Do:    func(m *viewerModel, _ string) tea.Cmd { m.openPalette(); return nil },
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
			// Colour is a saved display preference, so it lives in the settings
			// menu, not the palette: `,` is settings, `:` is keyless commands, and
			// nothing belongs in both.
			Keys: []string{","}, HelpRow: "settings", Group: "view",
			Label: "view options",
			Do:    func(m *viewerModel, _ string) tea.Cmd { m.openSettings(); return nil },
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
			// `c` left this set when it became the comment menu key.
			Keys: []string{"o", "O", "C"}, Group: "view", Label: "retired fold keys (inert)",
			Hidden: true,
			Do:     func(_ *viewerModel, _ string) tea.Cmd { return nil },
		},
	}
}

// actionIndex maps every bound key to its action, built once on first use. The
// uniqueness the lookup depends on is asserted by the table's own spec.
var actionIndex = sync.OnceValue(func() map[string]action {
	list := viewerActionList()
	idx := make(map[string]action, len(list)*2)
	for _, a := range list {
		for _, k := range a.Keys {
			idx[k] = a
		}
	}
	return idx
})

func actionForKey(k string) (action, bool) {
	a, ok := actionIndex()[k]
	return a, ok
}

// canWrite reports whether the viewer has a comment writer at all. A read-only
// viewer silently ignores the write keys rather than explaining itself on every
// press, so the actions guarded by it carry no Detail for this case.
func canWrite(m *viewerModel) bool { return m.writer != nil }

// writeDetail builds the "why can't I" text for a cursor-dependent comment
// action, keeping one wording for the menu row and the bare keypress.
func writeDetail(verb string) func(*viewerModel) string {
	return func(m *viewerModel) string {
		if m.writer == nil {
			return ""
		}
		return m.noCommentReason(verb)
	}
}

// display returns the help spelling of an action's keys, or "" for a keyless
// action — one the palette carries and the help popup therefore cannot list.
func (a action) display() string {
	if a.Display != "" {
		return a.Display
	}
	if len(a.Keys) == 0 {
		return ""
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
	for _, a := range viewerActionList() {
		// Hidden actions are never advertised; keyless ones have no key to
		// advertise, and the palette (which is listed) is how they are found.
		if a.Hidden || a.display() == "" {
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
