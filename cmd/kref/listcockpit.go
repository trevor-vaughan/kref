package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/git-bug/git-bug/entity"
	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/store"
	"github.com/trevor-vaughan/kref/internal/tui"
)

type rowKind int

const (
	rowQuarantine rowKind = iota
	rowEntry
)

// cockpitRow is one selectable line in the interactive list: a quarantine-queue
// item (approve/reject) or an entry (open/edit/archive/status/alias).
type cockpitRow struct {
	kind  rowKind
	id    entity.Id             // entry id (rowEntry) or quarantine item id (rowQuarantine)
	line  string                // rendered display line (no cursor marker)
	snap  *entry.Snapshot       // rowEntry: for status/archive/edit/open dispatch
	qitem *store.QuarantineItem // rowQuarantine: for approve/reject and open-target
}

// listActions is the store subset the list cockpit reads and mutates (mirrors
// commentWriter). A fake implements it in tests.
type listActions interface {
	QuarantineQueue() ([]store.QuarantineItem, error)
	QuarantineDetail(id entity.Id) (store.QuarantineDetail, error)
	// ListEntries returns the rows and, for a search-filtered cockpit, each
	// entry's match count keyed by id string (nil otherwise). One call returns
	// both so a reload after a mutation re-derives the ranking instead of
	// leaving stale counts behind.
	ListEntries() ([]*entry.Snapshot, map[string]int, error)
	ApproveQuarantine(id entity.Id, note, approver, actorKind string) error
	RejectQuarantine(id entity.Id, note, rejecter, actorKind string) (string, error)
	Archive(id entity.Id) error
	Unarchive(id entity.Id) error
	SetStatus(id entity.Id, status string) error
	SetFavorite(name string, id entity.Id) error
	RemoveFavorite(name string) error
	Favorites() map[string]string
}

// buildCockpitRows renders the quarantine group (top) then the entry rows, using
// the same formatter as the static table so a row looks identical. The column
// header comes back with them: the rows are a bare column of values, and what
// names them is needed by the scrollback echo.
func buildCockpitRows(queue []store.QuarantineItem, entries []*entry.Snapshot, opts render.ListOptions) (string, []cockpitRow) {
	var rows []cockpitRow
	now := time.Now()
	for i := range queue {
		q := queue[i]
		rows = append(rows, cockpitRow{
			kind:  rowQuarantine,
			id:    q.ID,
			line:  "⚠ " + strings.TrimSpace(quarantineLine(q, now)),
			qitem: &queue[i],
		})
	}
	header, lines, ids := render.ListLines(entries, opts)
	byID := make(map[entity.Id]*entry.Snapshot, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	for i, ln := range lines {
		rows = append(rows, cockpitRow{kind: rowEntry, id: ids[i], line: ln, snap: byID[ids[i]]})
	}
	return header, rows
}

type listInputMode int

const (
	listModeNone   listInputMode = iota
	listModeNote                 // approve/reject note
	listModeFav                  // favorite/alias name
	listModeSearch               // / search
	listModeStatus               // status picker
)

// cockpitProfile is what distinguishes the two surfaces that share this model:
// bare `kref` (the whole store, with the quarantine review queue on top) and
// `kref search` (relevance-ranked hits, no queue, results echoed on exit).
// Everything else — keys, actions, reload semantics — is identical by design.
type cockpitProfile struct {
	title      string // ScrollView title
	queue      bool   // fetch and render the quarantine review queue
	echoOnExit bool   // reprint the visible rows to scrollback on quit
}

// listModel is the interactive list cockpit. In-place actions mutate through
// acts and reload; open/edit exit with a result the RunE loop dispatches.
type listModel struct {
	sv     tui.ScrollView
	acts   listActions
	opts   render.ListOptions
	filter store.ListFilter
	color  bool

	// the resolved reviewer recorded on an approve/reject; a quarantine
	// decision is an audit record and has to name who made it
	actor     string
	actorKind string

	rows   []cockpitRow
	header string // the column header the rows are aligned to, for the exit echo
	cursor int

	mode      listInputMode
	input     textinput.Model
	statusIdx int
	err       string // transient footer message

	settings viewSettings // the `,` view-options overlay

	search pagerSearch // shared incremental search (/, n/N) — same as the viewer and pager

	profile cockpitProfile // which surface this is: bare `kref` or `kref search`

	noteApprove bool       // note mode: approve (true) vs reject
	result      listResult // set on exit for a full-screen action
}

// listResult is what the model exits with: a full-screen action to run, or quit.
type listResult struct {
	action string // "" quit, "open", "edit", "review" (a held write)
	id     entity.Id
	cursor int
}

func newListModel(acts listActions, opts render.ListOptions, color bool, filter store.ListFilter, actor, actorKind string, profile cockpitProfile) *listModel {
	// The cockpit is what bare `kref` opens (`kref list` is the static table)
	// and what `kref search` opens over its hits; profile says which.
	sv := tui.NewScrollView(profile.title)
	sv.SetPlain(!color)
	sv.SetHelpRows(listHelpRows(profile.queue))
	sv.SetHorizontalStep(8) // ←/h →/l pan to read titles wider than the window
	return &listModel{
		sv: sv, acts: acts, opts: opts, filter: filter, color: color,
		actor: actor, actorKind: actorKind, input: textinput.New(),
		profile: profile,
	}
}

// reload refetches the queue + entries, rebuilds the rows, and keeps the cursor
// on the same id when it survives.
func (m *listModel) reload() {
	// A surface without the review queue does not ask for it: `kref search`
	// answers a question about entries, and a failed queue read must not become
	// an error message on a view that would never have shown the queue anyway.
	var q []store.QuarantineItem
	var qErr error
	if m.profile.queue {
		q, qErr = m.acts.QuarantineQueue()
	}
	e, matches, lErr := m.acts.ListEntries()
	// An unreadable store must not render as an empty repository: "nothing here"
	// and "the read failed" are different facts, and the reader needs to know
	// which one they are looking at. The entry list is the headline, so it wins
	// when both fail.
	switch {
	case lErr != nil:
		m.err = lErr.Error()
	case qErr != nil:
		m.err = qErr.Error()
	}
	var keep entity.Id
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		keep = m.rows[m.cursor].id
	}
	m.opts.Matches = matches
	m.header, m.rows = buildCockpitRows(q, e, m.opts)
	m.cursor = 0
	for i, r := range m.rows {
		if r.id == keep {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = max(len(m.rows)-1, 0)
	}
	m.syncContent()
}

// syncContent renders rows (with the cursor marker) into the ScrollView and sets
// the sticky status line to the selected row's context.
func (m *listModel) syncContent() {
	var b strings.Builder
	for i, r := range m.rows {
		if i == m.cursor {
			fmt.Fprintf(&b, "%s %s\n", cursorMarker, r.line)
		} else {
			fmt.Fprintf(&b, "  %s\n", r.line)
		}
	}
	m.sv.SetContent(strings.TrimRight(b.String(), "\n"))
	m.sv.SetStatus(m.statusLine())
}

// visibleWindow returns the rendered rows currently shown in the viewport, so
// `kref search` can leave its results in scrollback on quit the way the text
// pager it replaced did. nil before the first size message.
func (m *listModel) visibleWindow() []string {
	if !m.sv.Ready() {
		return nil
	}
	top := max(m.sv.YOffset(), 0)
	end := min(top+m.sv.Height(), len(m.rows))
	if top > end {
		top = end
	}
	out := make([]string, 0, end-top)
	for _, r := range m.rows[top:end] {
		out = append(out, r.line)
	}
	return out
}

// echoResults prints the parting block `kref search` leaves in scrollback: the
// column header, the rows still on screen, and the tally. The rows alone are a
// column of bare integers with nothing naming them — the text pager this
// replaced echoed the framed table, and the docs still promise that.
func (m *listModel) echoResults(w io.Writer) {
	win := m.visibleWindow()
	if len(win) == 0 {
		return
	}
	if m.header != "" {
		fmt.Fprintln(w, m.header)
	}
	fmt.Fprintln(w, strings.Join(win, "\n"))
	// Matches is set only on a search, and only a search has a tally to print.
	if m.opts.Matches != nil {
		entries, total := 0, 0
		for _, r := range m.rows {
			if r.kind != rowEntry {
				continue
			}
			entries++
			total += m.opts.Matches[r.id.String()]
		}
		fmt.Fprintf(w, "\n%s\n", render.SearchTally(entries, total))
	}
}

func (m *listModel) statusLine() string {
	if len(m.rows) == 0 {
		return "nothing here"
	}
	r := m.rows[m.cursor]
	if r.kind == rowQuarantine {
		return "quarantine · a approve · r reject · enter view"
	}
	return fmt.Sprintf("%s · %s · enter open · e edit · x archive · s status · f alias", r.snap.Kind, r.snap.Status)
}

func (m *listModel) Init() tea.Cmd { return textinput.Blink }

func (m *listModel) selected() (cockpitRow, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return cockpitRow{}, false
	}
	return m.rows[m.cursor], true
}

// entryRow returns the selected row when it is an entry, and otherwise records
// why the key did nothing. The footer and the help popup advertise these keys
// unconditionally, so on a quarantine row they have to account for themselves
// rather than look broken — the rule the entry viewer's noComment follows.
func (m *listModel) entryRow(verb string) (cockpitRow, bool) {
	r, ok := m.selected()
	if !ok {
		m.err = "nothing selected"
		return cockpitRow{}, false
	}
	if r.kind != rowEntry {
		m.err = "this is a quarantine item — " + verb + " applies to an entry; a/r decide it, enter reviews it"
		return cockpitRow{}, false
	}
	return r, true
}

// mutate records an in-place action's error on the footer, or reloads the rows
// on success (so counts and the queue stay live).
func (m *listModel) mutate(err error) {
	if err != nil {
		m.err = err.Error()
		return
	}
	m.reload()
}

func (m *listModel) moveCursor(d int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor = clamp(m.cursor+d, 0, len(m.rows)-1)
	m.followCursor()
	m.syncContent()
}

// followCursor keeps the selected row within the viewport.
func (m *listModel) followCursor() {
	top := m.sv.YOffset()
	h := m.sv.Height()
	if h <= 0 {
		return
	}
	if m.cursor < top {
		m.sv.SetYOffset(m.cursor)
	} else if m.cursor >= top+h {
		m.sv.SetYOffset(m.cursor - h + 1)
	}
}

func (m *listModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); !ok {
		switch m.mode {
		case listModeNote:
			return m.updateNote(msg)
		case listModeStatus:
			return m.updateStatus(msg)
		case listModeFav:
			return m.updateFav(msg)
		case listModeSearch:
			return m.updateSearch(msg)
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.sv.Resize(msg.Width, msg.Height)
		m.syncContent()
		return m, nil
	case tea.MouseMsg:
		// Mouse capture is enabled; forwarding the wheel is what pays for it.
		return m, m.sv.PassKey(msg)
	case tea.KeyMsg:
		m.err = "" // clear a transient error on the next keypress
		// A dialog/popup swallows the next key and closes; only ctrl+c (the hard
		// quit) still exits. Mirrors the show pager and todo cockpit so esc (and
		// any key) dismisses an overlay rather than quitting the app.
		if m.sv.HelpOpen() {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			m.sv.CloseHelp()
			return m, nil
		}
		if m.settings.isOpen() {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			if id := m.settings.key(msg); id != "" {
				m.toggleSetting(id)
				m.settings.refresh(m.settingRows())
			}
			return m, nil
		}
		if m.mode == listModeNone {
			switch msg.String() {
			case "j", "down":
				m.moveCursor(1)
				return m, nil
			case "k", "up":
				m.moveCursor(-1)
				return m, nil
			case "g", "home":
				m.cursor = 0
				m.sv.GotoTop()
				m.syncContent()
				return m, nil
			case "G", "end":
				m.cursor = max(len(m.rows)-1, 0)
				m.sv.GotoBottom()
				m.syncContent()
				return m, nil
			case "enter":
				if r, ok := m.selected(); ok {
					// A quarantine row opens its review (findings + proposed change,
					// approve/reject in place); an entry row opens the entry.
					act := "open"
					if r.kind == rowQuarantine {
						act = "review"
					}
					m.result = listResult{action: act, id: r.id, cursor: m.cursor}
					return m, tea.Quit
				}
			case "e":
				if r, ok := m.entryRow("edit"); ok {
					m.result = listResult{action: "edit", id: r.id, cursor: m.cursor}
					return m, tea.Quit
				}
				return m, nil
			case "a", "r":
				if r, ok := m.selected(); ok && r.kind == rowQuarantine {
					m.noteApprove = msg.String() == "a"
					m.mode = listModeNote
					m.input.SetValue("")
					m.input.Focus()
					return m, textinput.Blink
				}
				if !m.profile.queue {
					m.err = "no review queue here — run `kref` or `kref quarantine` to decide held writes"
					return m, nil
				}
				m.err = "not a quarantine item"
				return m, nil
			case "x":
				if r, ok := m.entryRow("archive"); ok {
					m.mutate(m.acts.Archive(r.id))
				}
				return m, nil
			case "u":
				if r, ok := m.entryRow("unarchive"); ok {
					m.mutate(m.acts.Unarchive(r.id))
				}
				return m, nil
			case "s":
				if r, ok := m.entryRow("status"); ok {
					m.mode = listModeStatus
					m.statusIdx = statusIndex(r.snap.Status)
				}
				return m, nil
			case "f":
				if r, ok := m.entryRow("alias"); ok {
					m.mode = listModeFav
					m.input.SetValue(existingFavName(m.acts, r.id))
					m.input.CursorEnd()
					m.input.Focus()
					return m, textinput.Blink
				}
				return m, nil
			case "/":
				m.mode = listModeSearch
				m.search.start()
				m.sv.SetStatus("/")
				return m, nil
			case "left", "h", "right", "l":
				// Pan horizontally to read a title wider than the window; the
				// viewport handles the offset (rows stay one line each).
				return m, m.sv.PassKey(msg)
			case "n":
				m.jumpMatch(1)
				return m, nil
			case "N":
				m.jumpMatch(-1)
				return m, nil
			case ",":
				m.settings.open(m.settingRows())
				return m, nil
			case "?":
				m.sv.ToggleHelp()
				return m, nil
			case "q", "esc", "ctrl+c":
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// updateNote routes keys while the approve/reject note overlay is open.
func (m *listModel) updateNote(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.Type {
		case tea.KeyEsc:
			m.mode = listModeNone
			return m, nil
		case tea.KeyEnter:
			note := strings.TrimSpace(m.input.Value())
			r, _ := m.selected()
			var err error
			if m.noteApprove {
				err = m.acts.ApproveQuarantine(r.id, note, m.actor, m.actorKind)
			} else {
				_, err = m.acts.RejectQuarantine(r.id, note, m.actor, m.actorKind)
			}
			m.mode = listModeNone
			if err != nil {
				m.err = err.Error()
			} else {
				m.reload()
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// updateSearch routes keys while the / search input is active, through the same
// pagerSearch the entry viewer and pager use — so the query line, the wrapping
// n/N cycle and the "match i/N" counter behave identically on every surface.
func (m *listModel) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	m.search.input(km, m.searchMatcher, &m.sv)
	if m.search.searching() {
		m.sv.SetStatus("/" + m.search.query)
		return m, nil
	}
	// The query line closed: enter committed it (idx is already 0, so land on
	// that first hit) or esc cancelled it.
	m.mode = listModeNone
	if _, hit := m.search.current(); hit {
		m.moveToMatch()
	} else {
		if m.search.query != "" {
			m.err = "no matches"
		}
		m.sv.SetStatus(m.statusLine())
	}
	return m, nil
}

// searchMatcher returns the row indices whose rendered line contains q. The
// cockpit renders exactly one line per row, so a content-line offset is a row
// index.
func (m *listModel) searchMatcher(q string) []int {
	lines := make([]string, len(m.rows))
	for i, r := range m.rows {
		lines[i] = r.line
	}
	return searchMatches(lines, q)
}

// moveToMatch puts the cursor on the current match. The list selects a row
// rather than scrolling to an offset, so it drives the cursor itself instead of
// using pagerSearch.jump.
func (m *listModel) moveToMatch() {
	row, ok := m.search.current()
	if !ok {
		m.err = "no matches"
		m.sv.SetStatus(m.statusLine())
		return
	}
	m.cursor = clamp(row, 0, max(len(m.rows)-1, 0))
	m.followCursor()
	m.syncContent()
}

// jumpMatch cycles to the next/previous match (wrapping) and selects it.
func (m *listModel) jumpMatch(dir int) {
	if _, ok := m.search.current(); !ok {
		m.err = "no matches"
		m.sv.SetStatus(m.statusLine())
		return
	}
	m.search.cycle(dir, &m.sv)
	m.moveToMatch()
}

// updateFav routes keys while the alias (favorite) input overlay is open. An
// empty save clears an existing alias; a non-empty save sets it.
func (m *listModel) updateFav(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.Type {
		case tea.KeyEsc:
			m.mode = listModeNone
			return m, nil
		case tea.KeyEnter:
			r, _ := m.selected()
			name := strings.TrimSpace(m.input.Value())
			m.mode = listModeNone
			if name == "" {
				if old := existingFavName(m.acts, r.id); old != "" {
					m.mutate(m.acts.RemoveFavorite(old))
				}
			} else {
				m.mutate(m.acts.SetFavorite(name, r.id))
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// existingFavName returns one favorite name pointing at id, or "".
func existingFavName(acts listActions, id entity.Id) string {
	if names := favoritesFor(acts.Favorites(), id); len(names) > 0 {
		return names[0]
	}
	return ""
}

// updateStatus routes keys while the status picker is open.
func (m *listModel) updateStatus(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// Switch on the key string rather than the type so k/j sit beside up/down.
	// The picker takes no text, so the letters are free — and every other
	// surface accepts both, because the viewport's keymap binds them together.
	switch km.String() {
	case "esc":
		m.mode = listModeNone
	case "up", "k":
		m.statusIdx = clamp(m.statusIdx-1, 0, len(statusValues)-1)
	case "down", "j":
		m.statusIdx = clamp(m.statusIdx+1, 0, len(statusValues)-1)
	case "enter":
		r, _ := m.selected()
		m.mode = listModeNone
		m.mutate(m.acts.SetStatus(r.id, statusValues[m.statusIdx]))
	}
	return m, nil
}

func statusIndex(s string) int {
	for i, v := range statusValues {
		if v == s {
			return i
		}
	}
	return 0
}

// statusPicker renders the status-choice modal.
func (m *listModel) statusPicker() string {
	var b strings.Builder
	b.WriteString("status\n")
	for i, v := range statusValues {
		marker := "  "
		if i == m.statusIdx {
			marker = cursorMarker + " "
		}
		fmt.Fprintf(&b, "%s%s\n", marker, v)
	}
	b.WriteString("↑↓ choose · enter set · esc cancel")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(b.String())
}

// settingRows and toggleSetting are the cockpit's half of the `,` menu. Colour
// is the only display setting here: the rows are one line each, so there is no
// gutter to hide.
func (m *listModel) settingRows() []tui.MenuRow { return []tui.MenuRow{colorRow(m.color)} }

// toggleSetting applies a setting to the live view and saves it to the user
// config. A failed write is a footer notice, not a crash — the view has already
// changed, and losing the preference is not worth losing the session over.
func (m *listModel) toggleSetting(id string) {
	if id != settingColor {
		return
	}
	m.color = !m.color
	m.sv.SetPlain(!m.color)
	// The rows carry their own colour, so they have to be rebuilt: chrome alone
	// would leave a plain frame around coloured tier glyphs.
	m.opts.Color = m.color
	m.reload()
	if err := setUserColor(m.color); err != nil {
		m.err = "preference not saved: " + err.Error()
	}
}

// overlayBox renders the active modal (note / favorite input).
func (m *listModel) overlayBox() string {
	var title string
	switch m.mode {
	case listModeNote:
		if m.noteApprove {
			title = "approve — optional note"
		} else {
			title = "reject — reason"
		}
	case listModeFav:
		title = "favorite — alias name"
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).
		Render(title + "\n" + m.input.View() + "\nenter save · esc cancel")
}

func (m *listModel) View() string {
	if m.settings.isOpen() {
		return m.sv.RenderOverlay(m.footer(), m.settings.render(m.sv.Width(), m.color))
	}
	switch m.mode {
	case listModeNote, listModeFav:
		return m.sv.RenderOverlay(m.footer(), m.overlayBox())
	case listModeStatus:
		return m.sv.RenderOverlay(m.footer(), m.statusPicker())
	}
	return m.sv.Render(m.footer())
}

func (m *listModel) footer() string {
	if m.err != "" {
		return m.err + " · " + m.sv.ScrollLabel()
	}
	pos := m.sv.ScrollLabel()
	if f := m.search.footer(); f != "" {
		pos = f + "  ·  " + pos
	}
	head := fmt.Sprintf("%d/%d  ·  %s  ·  ", m.cursor+1, max(len(m.rows), 1), pos)
	// Spend whatever width the terminal has. The old footer spelled every key
	// unconditionally and needed 111 columns, so on an 80-column terminal it was
	// clipped and both "? keys" and "q quit" fell off the end; capping it at the
	// narrow form instead would have starved wide terminals of the same hints.
	// The a/r hint follows the same rule as the help popup: a surface without
	// the review queue must not advertise keys that have nothing to act on.
	review := ""
	if m.profile.queue {
		review = "a/r review · "
	}
	return m.sv.Fit(withHead(head,
		"↑↓ move · enter open · "+review+"e edit · x/u arch · s status · f alias · / search · ? keys · q quit",
		"↑↓ move · enter open · "+review+"e edit · / search · ? keys · q quit",
		"↑↓ move · enter open · / search · ? keys · q quit",
		"? keys · q quit",
	)...)
}

// withHead prefixes each footer variant with the position head and appends a
// bare last resort that drops the head entirely. Fit measures whole rows, so the
// head has to be part of each candidate — and on a very narrow terminal the
// position is what gives, never the two hints that say how to get help and out.
func withHead(head string, variants ...string) []string {
	out := make([]string, 0, len(variants)+1)
	for _, v := range variants {
		out = append(out, head+v)
	}
	return append(out, variants[len(variants)-1])
}

// listHelpRows returns the help key rows. The approve/reject row appears only
// on a surface that carries the review queue: `kref search` has no quarantine
// rows, so advertising a/r there would promise keys with nothing to act on.
func listHelpRows(queue bool) []string {
	rows := []string{
		"↑/↓ j/k   move             enter   open (show/todo)",
		"←/→ h/l   pan title        g / G   top / bottom",
	}
	if queue {
		rows = append(rows, "a / r     approve/reject   e       edit ($EDITOR)")
	} else {
		rows = append(rows, "e         edit ($EDITOR)")
	}
	return append(rows,
		"x / u     archive/restore  s       status",
		"f         alias            /       search   n/N next/prev",
		",         view options",
		"? q esc   keys / quit",
	)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// runListCockpit runs the interactive list, looping: run the model, and when it
// exits for a full-screen action (open/edit) dispatch to the real viewer/editor
// via handle, then re-enter at the saved cursor. Quit ends the loop.
func runListCockpit(acts listActions, opts render.ListOptions, color bool, filter store.ListFilter, actor, actorKind string, profile cockpitProfile, handle func(res listResult) error) error {
	cursor := 0
	// The model is rebuilt on every return from a full-screen action, which used
	// to discard the search silently — after which n reported "no matches",
	// reading as "your query found nothing" rather than "your query is gone".
	var search pagerSearch
	for {
		m := newListModel(acts, opts, color, filter, actor, actorKind, profile)
		m.reload()
		m.cursor = clamp(cursor, 0, max(len(m.rows)-1, 0))
		m.search = search
		m.search.refresh(m.searchMatcher)
		m.syncContent()
		out, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(os.Stdout)).Run()
		if err != nil {
			return err
		}
		fm, ok := out.(*listModel)
		if !ok || fm.result.action == "" {
			// `kref search` replaced a pager that left its results on screen;
			// echoing the last window keeps that, so quitting does not wipe
			// the answer you just asked for.
			if ok && profile.echoOnExit {
				fm.echoResults(os.Stdout)
			}
			return nil // quit
		}
		cursor, search = fm.result.cursor, fm.search
		if herr := handle(fm.result); herr != nil {
			return herr
		}
	}
}

// runRootBrowse is bare `kref`: the interactive cockpit on a terminal, the same
// help as ever anywhere else. --json and --plain are machine contracts a TUI
// cannot honour, so they are refused by name, pointing at the subcommand that
// can.
func runRootBrowse(cmd *cobra.Command, dir *string, sel *listSelection) error {
	if jsonMode(cmd) {
		return errors.New("--json needs a subcommand (try `kref list --json`)")
	}
	if plainMode(cmd) {
		return errors.New("--plain needs a subcommand (try `kref list --plain`)")
	}
	if !usePager(cmd) {
		cmd.HelpFunc()(cmd, nil)
		return nil
	}
	// Parsed before the store is opened so a bad --sort reports itself rather
	// than whatever the store has to say first.
	sortSpec, err := sel.sortSpec()
	if err != nil {
		return err
	}
	s, err := store.Open(*dir)
	if err != nil {
		return err
	}
	defer s.Close()
	lf, err := sel.filter(s)
	if err != nil {
		return err
	}
	return runBrowse(cmd, dir, s, lf, sortSpec, sel.all)
}

// runBrowse opens the interactive list cockpit over lf and carries out whatever
// the user selected on the way out: the quarantine review queue, the entry
// viewer, or $EDITOR. The cockpit has no column control of its own, so it always
// renders the default columns.
func runBrowse(cmd *cobra.Command, dir *string, s *store.Store, lf store.ListFilter, sortSpec *render.SortSpec, all bool) error {
	// Favorited entries pin to the top of the view; the id-set is the values of
	// the merged (user + shared) favorites map.
	favIDs := map[string]bool{}
	for _, id := range s.Favorites() {
		favIDs[id] = true
	}
	color := resolveColor(cmd, s.EffectiveConfig())
	opts := render.ListOptions{
		Columns: render.DefaultColumns, Color: color, ShowAll: all, Sort: sortSpec, Favorites: favIDs,
	}
	acts := listCockpitActions{s: s, filter: lf}
	actor, actorKind := resolveActor(cmd, s)
	profile := cockpitProfile{title: "kref", queue: true}
	return runListCockpit(acts, opts, color, lf, actor, actorKind, profile, func(res listResult) error {
		// The review queue is bare `kref`'s alone; everything else is the
		// entry dispatch both cockpits share.
		if res.action == "review" {
			queue, qerr := acts.QuarantineQueue()
			if qerr != nil {
				return qerr
			}
			start := 0
			for i, it := range queue {
				if it.ID == res.id {
					start = i
					break
				}
			}
			rr, rerr := runReviewModel(acts, queue, start, color, ttyWidth(), actor, actorKind)
			if rerr != nil {
				return rerr
			}
			if rr.action == "open" {
				snap, gerr := s.Get(rr.target)
				if gerr != nil {
					return gerr
				}
				return openEntry(cmd, dir, s, snap)
			}
			return nil
		}
		return dispatchEntryAction(cmd, dir, s, res)
	})
}

// dispatchEntryAction carries out the full-screen action a cockpit exited for:
// open the entry in its viewer, or hand it to $EDITOR. Shared by bare `kref`
// and `kref search`; the quarantine review case stays in runBrowse, the only
// surface that carries the queue.
func dispatchEntryAction(cmd *cobra.Command, dir *string, s *store.Store, res listResult) error {
	switch res.action {
	case "open":
		snap, err := s.Get(res.id)
		if err != nil {
			return err
		}
		return openEntry(cmd, dir, s, snap)
	case "edit":
		return editEntry(cmd, s, res.id)
	}
	return nil
}

// runSearchBrowse opens the interactive cockpit over a query's hits: the same
// model bare `kref` uses, with the relevance column, no review queue, and the
// results echoed to scrollback on exit so quitting does not wipe the answer.
func runSearchBrowse(cmd *cobra.Command, dir *string, s *store.Store, lf store.ListFilter, sortBy, query string, primed []store.SearchResult) error {
	acts := listCockpitActions{s: s, filter: lf, sortBy: sortBy, primed: &primedSearch{results: primed}}
	color := resolveColor(cmd, s.EffectiveConfig())
	opts := searchListOptions(color)
	actor, actorKind := resolveActor(cmd, s)
	profile := cockpitProfile{title: "kref search — " + query, echoOnExit: true}
	return runListCockpit(acts, opts, color, lf, actor, actorKind, profile,
		func(res listResult) error {
			return dispatchEntryAction(cmd, dir, s, res)
		})
}

// searchListOptions is how the search cockpit renders: the relevance column, and
// the ranking ListEntries computed left exactly as it came. It is a function
// rather than a literal inside runSearchBrowse so a spec can assert the option
// set the production path actually uses — asserting a copy of it proves only
// that the renderer honours those options, never that this caller still passes
// them.
func searchListOptions(color bool) render.ListOptions {
	return render.ListOptions{
		Columns: render.SearchColumns,
		Color:   color,
		// Search shows every hit: no same-title collapsing, no superseded drop.
		ShowAll: true,
		// PreserveOrder keeps the ranking ListEntries computed. A nil Sort is
		// NOT enough — sortListRows falls through to the default
		// tier→kind→title order without it. Favorites stays nil, so pinning
		// cannot outrank relevance.
		PreserveOrder: true,
	}
}

// primedSearch is a search the caller already ran, handed to the cockpit so the
// first paint does not repeat the scan. `kref search` has to run the query
// anyway to know whether there is anything to open, and store.Search re-reads
// and recompiles every entry in the store.
//
// Consumed once: every reload after that goes back to the store, which is what
// makes a mutation show up. Held by pointer because listCockpitActions is copied
// by value, and the copies have to agree on whether it has been spent.
type primedSearch struct {
	results []store.SearchResult
	used    bool
}

// listCockpitActions adapts *store.Store to listActions. Favorites are user-scope
// config writes, so those delegate to the fav.go helpers, not the store.
//
// sortBy is the raw --sort value for a search cockpit. It is reapplied on every
// reload so a mutation does not silently revert the ordering to matches:desc.
type listCockpitActions struct {
	s      *store.Store
	filter store.ListFilter
	sortBy string
	primed *primedSearch
}

func (a listCockpitActions) QuarantineQueue() ([]store.QuarantineItem, error) {
	return a.s.QuarantineQueue()
}
func (a listCockpitActions) QuarantineDetail(id entity.Id) (store.QuarantineDetail, error) {
	return a.s.QuarantineDetail(id)
}

// ListEntries serves the plain cockpit from List and a search cockpit from
// Search, which is List plus a per-entry match count and a matches:desc order.
// The --sort override is reapplied here rather than only at startup, so it
// survives the reload that follows every mutation.
func (a listCockpitActions) ListEntries() ([]*entry.Snapshot, map[string]int, error) {
	if a.filter.Search == "" {
		items, err := a.s.List(a.filter)
		return items, nil, err
	}
	var results []store.SearchResult
	// A nil result slice is "not primed", never "primed with nothing": an empty
	// search never opens a cockpit, so honouring it would render a store full of
	// matches as one with none.
	if a.primed != nil && !a.primed.used && a.primed.results != nil {
		// Already sorted by the caller that ran it; re-sorting is not merely
		// redundant, it would apply --sort a second time.
		a.primed.used, results = true, a.primed.results
	} else {
		var err error
		if results, err = a.s.Search(a.filter); err != nil {
			return nil, nil, err
		}
		if err := sortSearchResults(results, a.sortBy); err != nil {
			return nil, nil, err
		}
	}
	items := make([]*entry.Snapshot, len(results))
	matches := make(map[string]int, len(results))
	for i, r := range results {
		items[i] = r.Snapshot
		matches[r.ID.String()] = r.Matches
	}
	return items, matches, nil
}
func (a listCockpitActions) ApproveQuarantine(id entity.Id, note, ap, k string) error {
	return a.s.ApproveQuarantine(id, note, ap, k)
}
func (a listCockpitActions) RejectQuarantine(id entity.Id, note, rejecter, k string) (string, error) {
	return a.s.RejectQuarantine(id, note, rejecter, k)
}
func (a listCockpitActions) Archive(id entity.Id) error              { return a.s.Archive(id) }
func (a listCockpitActions) Unarchive(id entity.Id) error            { return a.s.Unarchive(id) }
func (a listCockpitActions) SetStatus(id entity.Id, st string) error { return a.s.SetStatus(id, st) }
func (a listCockpitActions) SetFavorite(name string, id entity.Id) error {
	return setUserFavorite(name, id)
}
func (a listCockpitActions) RemoveFavorite(name string) error { return removeUserFavorite(name) }
func (a listCockpitActions) Favorites() map[string]string     { return a.s.Favorites() }
