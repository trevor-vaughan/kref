package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/git-bug/git-bug/entity"
	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/store"
	"github.com/trevor-vaughan/kref/internal/todo"
	"github.com/trevor-vaughan/kref/internal/tui"
)

// todoCandidate is one todo the picker can act on: the identity it hands back
// and the two presentations it needs — Line for the row, Title for the subject
// it names in the subtitle and the hint.
type todoCandidate struct {
	ID    entity.Id
	Title string
	Line  string
}

// todoPickResult is what the picker hands to its loop. An empty verb means the
// reader quit; cursor is where to re-enter after a full-screen action, the same
// contract listResult carries.
type todoPickResult struct {
	verb   string // "" quit, "show", "fmt", "lint"
	id     entity.Id
	cursor int
}

const (
	pickerTodos   = "TODOS"
	pickerActions = "ACTIONS"
)

// pickerVerbs are the ACTIONS rows. show is deliberately absent: enter on a todo
// already means show, and a row for it would be a second way to say the same
// thing.
var pickerVerbs = []struct{ key, verb, detail string }{
	{"f", "fmt", "auto-arrange"},
	{"l", "lint", "check grammar"},
}

// todoPickerModel is the grouped picker: a TODOS section over an ACTIONS
// section, where the highlighted todo is the subject and the verbs act on it.
//
// It owns no store: rows arrive built, and the chosen verb is dispatched outside
// the TUI by runTodoPicker — the same split runListCockpit uses. That is what
// lets fmt print to scrollback and lint set an exit status at all.
type todoPickerModel struct {
	menu      *tui.Menu
	cands     []todoCandidate
	verb      string // fixed by the command line; "" offers the ACTIONS section
	subject   int    // index into cands: the todo the verbs act on
	color     bool
	width     int
	filtering bool
	result    todoPickResult
}

func newTodoPickerModel(cands []todoCandidate, verb string, color bool, width int) *todoPickerModel {
	title := "todo"
	if verb != "" {
		title = verb + " · which todo?"
	}
	m := &todoPickerModel{
		menu:  tui.NewMenu(title),
		cands: cands,
		verb:  verb,
		color: color,
		width: width,
	}
	m.menu.SetRows(m.rows())
	m.refresh()
	return m
}

// rows builds the menu rows. With a verb fixed on the command line there is
// nothing to choose but the target, so the sections collapse to a plain list: a
// lone "TODOS" heading over the only section there is would be chrome.
func (m *todoPickerModel) rows() []tui.MenuRow {
	section := pickerTodos
	if m.verb != "" {
		section = ""
	}
	rows := make([]tui.MenuRow, 0, len(m.cands)+len(pickerVerbs))
	for i, c := range m.cands {
		rows = append(rows, tui.MenuRow{
			ID: strconv.Itoa(i), Section: section, Label: c.Line, Enabled: true,
		})
	}
	if m.verb != "" {
		return rows
	}
	// The verb labels are padded here rather than by the menu: Menu lines up a
	// Value column but not a Detail one, and teaching it to would re-align the
	// four overlays that already ship Details on their disabled rows.
	verbW := 0
	for _, v := range pickerVerbs {
		verbW = max(verbW, len(v.verb))
	}
	for _, v := range pickerVerbs {
		rows = append(rows, tui.MenuRow{
			ID: "verb:" + v.verb, Section: pickerActions, Key: v.key,
			Label: v.verb + strings.Repeat(" ", verbW-len(v.verb)), Detail: v.detail, Enabled: true,
		})
	}
	return rows
}

// refresh re-reads the selection and updates the subject, subtitle and hint. The
// subject follows the cursor only while it is on a todo, which is what keeps the
// verbs acting on the todo you left when you arrow down into ACTIONS.
func (m *todoPickerModel) refresh() {
	if len(m.cands) == 0 {
		return
	}
	verb := m.pendingVerb()
	if sel, ok := m.menu.Selected(); ok {
		if i, err := strconv.Atoi(sel.ID); err == nil {
			m.subject = i
		}
	}
	if m.subject >= len(m.cands) {
		m.subject = 0
	}
	title := m.cands[m.subject].Title
	m.menu.SetSubtitle("on: " + title)
	m.menu.SetHint(fmt.Sprintf("↑↓ choose · enter %s ‹%s› · esc cancel", verb, title))
}

// pendingVerb is what enter would do right now: the verb under the cursor when
// the cursor is in ACTIONS, else the command-line verb, else show.
func (m *todoPickerModel) pendingVerb() string {
	if sel, ok := m.menu.Selected(); ok {
		if v, found := strings.CutPrefix(sel.ID, "verb:"); found {
			return v
		}
	}
	if m.verb != "" {
		return m.verb
	}
	return "show"
}

// fire records the decision and quits. The verb is dispatched by the loop, not
// here: a write or a lint report cannot happen under the alt screen.
func (m *todoPickerModel) fire(verb string) tea.Cmd {
	if len(m.cands) == 0 {
		return tea.Quit
	}
	m.result = todoPickResult{verb: verb, id: m.cands[m.subject].ID, cursor: m.subject}
	return tea.Quit
}

func (m *todoPickerModel) Init() tea.Cmd { return nil }

func (m *todoPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		if m.filtering {
			m.updateFilter(msg)
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			m.menu.Move(-1)
			m.refresh()
		case "down", "j":
			m.menu.Move(1)
			m.refresh()
		case "/":
			m.filtering = true
		case "enter":
			if _, ok := m.menu.Selected(); !ok {
				return m, nil
			}
			return m, m.fire(m.pendingVerb())
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		default:
			if row, ok := m.menu.ByKey(msg.String()); ok {
				if v, found := strings.CutPrefix(row.ID, "verb:"); found {
					return m, m.fire(v)
				}
			}
		}
	}
	return m, nil
}

// updateFilter routes keys to the incremental filter. Letters type rather than
// fire here — the same rule the viewer's palette follows, so a query containing
// q or f cannot quit or format behind the reader's back. esc clears the filter
// instead of quitting, so the way out of a typo is not the way out of the app.
func (m *todoPickerModel) updateFilter(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEsc:
		m.filtering = false
		m.menu.SetFilter("")
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyBackspace:
		if f := m.menu.Filter(); f != "" {
			m.menu.SetFilter(f[:len(f)-1])
		}
	case tea.KeyRunes:
		m.menu.SetFilter(m.menu.Filter() + string(msg.Runes))
	default:
		return
	}
	m.refresh()
}

func (m *todoPickerModel) View() string { return m.menu.Render(m.width, m.color) }

// todoCandidates builds a picker row per kind:todo entry. store.List already
// returns fully compiled snapshots with bodies and comments, so each row costs
// one Summarize over data the caller has in hand — not N extra Gets.
//
// The signal is Summarize's Awaiting (the todo's unanswered questions), the same
// number the cockpit strip leads with. It is deliberately NOT the watermark
// delta's ToReview: that counts what changed since you last looked, which is a
// different question, and one glyph must not mean two things.
func todoCandidates(s *store.Store, theme string) ([]todoCandidate, error) {
	snaps, err := s.List(store.ListFilter{Kind: "todo"})
	if err != nil {
		return nil, err
	}
	items := make([]render.TodoPickerItem, 0, len(snaps))
	for _, snap := range snaps {
		c := todo.Summarize(snap.Body, snap.Comments)
		items = append(items, render.TodoPickerItem{
			ID: snap.ID, Title: snap.Title, Awaiting: c.Awaiting, Open: c.Open, Edited: snap.EditedAt,
		})
	}
	lines := render.TodoPickerLines(items, theme, time.Now())
	cands := make([]todoCandidate, 0, len(snaps))
	for i, snap := range snaps {
		cands = append(cands, todoCandidate{ID: snap.ID, Title: snap.Title, Line: lines[i]})
	}
	return cands, nil
}

// runTodoPicker resolves an ambiguous todo target interactively: run the picker,
// dispatch the chosen verb OUTSIDE the TUI, then re-enter — the same split
// runListCockpit uses.
//
// show re-enters at the saved cursor, because it is the full-screen action the
// way open and edit are in the list cockpit. fmt and lint end the loop: both
// print to scrollback, and lint's exit status is a contract lefthook consumes,
// so neither can be swallowed by an alt-screen re-entry.
//
// verb fixes the picker to a single action chosen on the command line; the loop
// then runs exactly once, since there is nothing left to come back for.
func runTodoPicker(cmd *cobra.Command, s *store.Store, verb string, full bool) error {
	cfg := s.EffectiveConfig()
	color := resolveColor(cmd, cfg)
	theme := cfg.GlyphTheme()
	cursor := 0
	for {
		cands, err := todoCandidates(s, theme)
		if err != nil {
			return err
		}
		if len(cands) == 0 {
			return errors.New("no kind:todo entry found; pass an id")
		}
		m := newTodoPickerModel(cands, verb, color, ttyWidth())
		m.menu.Move(min(cursor, len(cands)-1))
		m.refresh()
		out, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(os.Stdout)).Run()
		if err != nil {
			return err
		}
		fm, ok := out.(*todoPickerModel)
		if !ok || fm.result.verb == "" {
			return nil // quit
		}
		cursor = fm.result.cursor
		snap, err := s.Get(fm.result.id)
		if err != nil {
			return err
		}
		switch fm.result.verb {
		case "fmt":
			return formatTodo(cmd, s, snap)
		case "lint":
			return lintTodo(cmd, s, snap, false)
		default:
			// A switch request needs no special handling: the loop is already
			// about to give the reader the picker back.
			if _, err := showTodoCockpit(cmd, s, snap, full, false); err != nil {
				return err
			}
		}
		if verb != "" {
			return nil
		}
	}
}
