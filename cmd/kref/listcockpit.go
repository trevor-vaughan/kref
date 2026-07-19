package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/git-bug/git-bug/entity"

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
	ListEntries() ([]*entry.Snapshot, error)
	ApproveQuarantine(id entity.Id, note, approver, actorKind string) error
	RejectQuarantine(id entity.Id, note, actorKind string) (string, error)
	Archive(id entity.Id) error
	Unarchive(id entity.Id) error
	SetStatus(id entity.Id, status string) error
	SetFavorite(name string, id entity.Id) error
	RemoveFavorite(name string) error
	Favorites() map[string]string
}

// buildCockpitRows renders the quarantine group (top) then the entry rows, using
// the same formatter as the static table so a row looks identical.
func buildCockpitRows(queue []store.QuarantineItem, entries []*entry.Snapshot, opts render.ListOptions) []cockpitRow {
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
	_, lines, ids := render.ListLines(entries, opts)
	byID := make(map[entity.Id]*entry.Snapshot, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	for i, ln := range lines {
		rows = append(rows, cockpitRow{kind: rowEntry, id: ids[i], line: ln, snap: byID[ids[i]]})
	}
	return rows
}

type listInputMode int

const (
	listModeNone   listInputMode = iota
	listModeNote                 // approve/reject note
	listModeFav                  // favorite/alias name
	listModeSearch               // / search
	listModeStatus               // status picker
)

// listModel is the interactive list cockpit. In-place actions mutate through
// acts and reload; open/edit exit with a result the RunE loop dispatches.
type listModel struct {
	sv     tui.ScrollView
	acts   listActions
	opts   render.ListOptions
	filter store.ListFilter
	color  bool

	rows   []cockpitRow
	cursor int

	mode      listInputMode
	input     textinput.Model
	statusIdx int
	err       string // transient footer message

	matches  []int
	matchPos int

	noteApprove bool       // note mode: approve (true) vs reject
	result      listResult // set on exit for a full-screen action
}

// listResult is what the model exits with: a full-screen action to run, or quit.
type listResult struct {
	action string // "" quit, "open", "edit"
	id     entity.Id
	cursor int
}

func newListModel(acts listActions, opts render.ListOptions, color bool, filter store.ListFilter) *listModel {
	sv := tui.NewScrollView("kref list")
	sv.SetPlain(!color)
	sv.SetHelpRows(listHelpRows())
	return &listModel{sv: sv, acts: acts, opts: opts, filter: filter, color: color, input: textinput.New()}
}

// reload refetches the queue + entries, rebuilds the rows, and keeps the cursor
// on the same id when it survives.
func (m *listModel) reload() {
	q, _ := m.acts.QuarantineQueue()
	e, _ := m.acts.ListEntries()
	var keep entity.Id
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		keep = m.rows[m.cursor].id
	}
	m.rows = buildCockpitRows(q, e, m.opts)
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
				if r, ok := m.selected(); ok && r.kind == rowEntry {
					m.result = listResult{action: "edit", id: r.id, cursor: m.cursor}
					return m, tea.Quit
				}
			case "a", "r":
				if r, ok := m.selected(); ok && r.kind == rowQuarantine {
					m.noteApprove = msg.String() == "a"
					m.mode = listModeNote
					m.input.SetValue("")
					m.input.Focus()
					return m, textinput.Blink
				}
				m.err = "not a quarantine item"
				return m, nil
			case "x":
				if r, ok := m.selected(); ok && r.kind == rowEntry {
					m.mutate(m.acts.Archive(r.id))
				}
				return m, nil
			case "u":
				if r, ok := m.selected(); ok && r.kind == rowEntry {
					m.mutate(m.acts.Unarchive(r.id))
				}
				return m, nil
			case "s":
				if r, ok := m.selected(); ok && r.kind == rowEntry {
					m.mode = listModeStatus
					m.statusIdx = statusIndex(r.snap.Status)
				}
				return m, nil
			case "f":
				if r, ok := m.selected(); ok && r.kind == rowEntry {
					m.mode = listModeFav
					m.input.SetValue(existingFavName(m.acts, r.id))
					m.input.CursorEnd()
					m.input.Focus()
					return m, textinput.Blink
				}
				return m, nil
			case "/":
				m.mode = listModeSearch
				m.input.SetValue("")
				m.input.Focus()
				return m, textinput.Blink
			case "n":
				m.jumpMatch(1)
				return m, nil
			case "N":
				m.jumpMatch(-1)
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
				err = m.acts.ApproveQuarantine(r.id, note, "me", "human")
			} else {
				_, err = m.acts.RejectQuarantine(r.id, note, "human")
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

// updateSearch routes keys while the / search input is active. Enter runs the
// match and jumps to the first hit; esc cancels.
func (m *listModel) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.Type {
		case tea.KeyEsc:
			m.mode = listModeNone
			m.sv.SetStatus(m.statusLine())
			return m, nil
		case tea.KeyEnter:
			m.mode = listModeNone
			m.computeMatches(m.input.Value())
			m.matchPos = -1
			m.jumpMatch(1)
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.sv.SetStatus("/" + m.input.Value())
	return m, cmd
}

// computeMatches records the row indices whose rendered line contains query.
func (m *listModel) computeMatches(query string) {
	m.matches = nil
	ql := strings.ToLower(strings.TrimSpace(query))
	if ql == "" {
		return
	}
	for i, r := range m.rows {
		if strings.Contains(strings.ToLower(r.line), ql) {
			m.matches = append(m.matches, i)
		}
	}
}

// jumpMatch moves the cursor to the next/previous match (wrapping).
func (m *listModel) jumpMatch(dir int) {
	if len(m.matches) == 0 {
		m.err = "no matches"
		m.sv.SetStatus(m.statusLine())
		return
	}
	m.matchPos = (m.matchPos + dir + len(m.matches)) % len(m.matches)
	m.cursor = m.matches[m.matchPos]
	m.followCursor()
	m.syncContent()
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
	switch km.Type {
	case tea.KeyEsc:
		m.mode = listModeNone
	case tea.KeyUp:
		m.statusIdx = clamp(m.statusIdx-1, 0, len(statusValues)-1)
	case tea.KeyDown:
		m.statusIdx = clamp(m.statusIdx+1, 0, len(statusValues)-1)
	case tea.KeyEnter:
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
	return "↑↓ move · enter open · a/r review · e edit · x/u arch · s status · f alias · / search · ? keys · q quit · " + m.sv.ScrollLabel()
}

func listHelpRows() []string {
	return []string{
		"↑/↓ j/k  move        enter  open (show/todo)",
		"a / r    approve/rej e      edit ($EDITOR)",
		"x / u    archive/res s      status",
		"f        alias        /     search  n/N next/prev",
		"g / G    top/bottom   q     quit",
	}
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
func runListCockpit(acts listActions, opts render.ListOptions, color bool, filter store.ListFilter, handle func(res listResult) error) error {
	cursor := 0
	for {
		m := newListModel(acts, opts, color, filter)
		m.reload()
		m.cursor = clamp(cursor, 0, max(len(m.rows)-1, 0))
		m.syncContent()
		out, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(os.Stdout)).Run()
		if err != nil {
			return err
		}
		fm, ok := out.(*listModel)
		if !ok || fm.result.action == "" {
			return nil // quit
		}
		cursor = fm.result.cursor
		if herr := handle(fm.result); herr != nil {
			return herr
		}
	}
}

// listCockpitActions adapts *store.Store to listActions. Favorites are user-scope
// config writes, so those delegate to the fav.go helpers, not the store.
type listCockpitActions struct {
	s      *store.Store
	filter store.ListFilter
}

func (a listCockpitActions) QuarantineQueue() ([]store.QuarantineItem, error) {
	return a.s.QuarantineQueue()
}
func (a listCockpitActions) QuarantineDetail(id entity.Id) (store.QuarantineDetail, error) {
	return a.s.QuarantineDetail(id)
}
func (a listCockpitActions) ListEntries() ([]*entry.Snapshot, error) { return a.s.List(a.filter) }
func (a listCockpitActions) ApproveQuarantine(id entity.Id, note, ap, k string) error {
	return a.s.ApproveQuarantine(id, note, ap, k)
}
func (a listCockpitActions) RejectQuarantine(id entity.Id, note, k string) (string, error) {
	return a.s.RejectQuarantine(id, note, k)
}
func (a listCockpitActions) Archive(id entity.Id) error              { return a.s.Archive(id) }
func (a listCockpitActions) Unarchive(id entity.Id) error            { return a.s.Unarchive(id) }
func (a listCockpitActions) SetStatus(id entity.Id, st string) error { return a.s.SetStatus(id, st) }
func (a listCockpitActions) SetFavorite(name string, id entity.Id) error {
	return setUserFavorite(name, id)
}
func (a listCockpitActions) RemoveFavorite(name string) error { return removeUserFavorite(name) }
func (a listCockpitActions) Favorites() map[string]string     { return a.s.Favorites() }
