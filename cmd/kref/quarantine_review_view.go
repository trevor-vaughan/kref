package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/store"
	"github.com/trevor-vaughan/kref/internal/tui"
)

// reviewResult is what the review viewer exits with: "open" to open the target
// entry, or "" (back / a decision was made in-view).
type reviewResult struct {
	action string
	target entity.Id
}

// reviewModel is the shared interactive review viewer for one quarantine item,
// used by both `kref quarantine show` and the list cockpit's Enter. It shows the
// proposed change (renderQuarantineReview) on the shared ScrollView and lets the
// reviewer approve/reject (a/r), open the target entry (o), or go back (q) — so a
// decision never requires leaving to type a command.
type reviewModel struct {
	sv     tui.ScrollView
	acts   listActions
	queue  []store.QuarantineItem
	idx    int
	detail store.QuarantineDetail

	color bool
	width int

	mode        listInputMode
	input       textinput.Model
	noteApprove bool
	err         string

	result reviewResult
}

func newReviewModel(acts listActions, queue []store.QuarantineItem, startIndex int, color bool, width int) *reviewModel {
	sv := tui.NewScrollView("quarantine review")
	sv.SetPlain(!color)
	sv.SetHelpRows([]string{
		"a / r   approve / reject",
		"n / p   next / prev held write",
		"o       open the target entry",
		"j/k     scroll        q  back",
	})
	m := &reviewModel{sv: sv, acts: acts, queue: queue, idx: startIndex, color: color, width: width, input: textinput.New()}
	m.loadDetail()
	return m
}

// loadDetail loads the current item's detail into the ScrollView, or a "clear"
// message when the queue is empty. Clamps idx into range.
func (m *reviewModel) loadDetail() {
	if len(m.queue) == 0 {
		m.detail = store.QuarantineDetail{}
		m.sv.SetContent("review queue is clear — nothing awaiting review.")
		return
	}
	if m.idx < 0 {
		m.idx = 0
	}
	if m.idx >= len(m.queue) {
		m.idx = len(m.queue) - 1
	}
	d, err := m.acts.QuarantineDetail(m.queue[m.idx].ID)
	if err != nil {
		m.err = err.Error()
		return
	}
	m.detail = d
	m.sv.SetContent(m.content())
}

func (m *reviewModel) content() string {
	var b strings.Builder
	renderQuarantineReview(&b, m.detail, m.color, m.width)
	return strings.TrimRight(b.String(), "\n")
}

func (m *reviewModel) Init() tea.Cmd {
	return textinput.Blink
}

// target is the entry to open for the o key: a held op's live target, else the
// item (a draft) itself.
func (m *reviewModel) target() entity.Id {
	if len(m.queue) == 0 {
		return ""
	}
	if m.detail.Item.HeldOp && m.detail.Item.Target != "" {
		return m.detail.Item.Target
	}
	return m.detail.Item.ID
}

func (m *reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); !ok && m.mode == listModeNote {
		return m.updateNote(msg)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.sv.Resize(msg.Width, msg.Height)
		m.width = msg.Width
		m.sv.SetContent(m.content())
		return m, nil
	case tea.KeyMsg:
		m.err = ""
		if m.sv.HelpOpen() {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			m.sv.CloseHelp()
			return m, nil
		}
		switch msg.String() {
		case "a", "r":
			if len(m.queue) == 0 {
				return m, nil
			}
			m.noteApprove = msg.String() == "a"
			m.mode = listModeNote
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		case "n":
			if m.idx < len(m.queue)-1 {
				m.idx++
				m.loadDetail()
			}
			return m, nil
		case "p":
			if m.idx > 0 {
				m.idx--
				m.loadDetail()
			}
			return m, nil
		case "o":
			if len(m.queue) == 0 {
				return m, nil
			}
			m.result = reviewResult{action: "open", target: m.target()}
			return m, tea.Quit
		case "?":
			m.sv.ToggleHelp()
			return m, nil
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
		return m, m.sv.PassKey(msg) // scroll the content
	}
	return m, nil
}

// updateNote routes keys while the approve/reject note overlay is open; a
// decision closes the viewer (the caller reloads).
func (m *reviewModel) updateNote(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.Type {
		case tea.KeyEsc:
			m.mode = listModeNone
			return m, nil
		case tea.KeyEnter:
			note := strings.TrimSpace(m.input.Value())
			id := m.detail.Item.ID
			var err error
			if m.noteApprove {
				err = m.acts.ApproveQuarantine(id, note, "me", "human")
			} else {
				_, err = m.acts.RejectQuarantine(id, note, "human")
			}
			m.mode = listModeNone
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if q, qerr := m.acts.QuarantineQueue(); qerr == nil {
				m.queue = q
			}
			m.loadDetail() // decided item gone; idx now points at the next (clamped)
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *reviewModel) View() string {
	if m.mode == listModeNote {
		title := "approve — optional note"
		if !m.noteApprove {
			title = "reject — reason"
		}
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).
			Render(title + "\n" + m.input.View() + "\nenter save · esc cancel")
		return m.sv.RenderOverlay(m.footer(), box)
	}
	return m.sv.Render(m.footer())
}

func (m *reviewModel) footer() string {
	pos := ""
	if len(m.queue) > 0 {
		pos = fmt.Sprintf("item %d/%d · ", m.idx+1, len(m.queue))
	}
	if m.err != "" {
		return m.err + " · " + m.sv.ScrollLabel()
	}
	return pos + "a approve · r reject · n/p next/prev · o open · ? keys · q back · " + m.sv.ScrollLabel()
}

// runReviewModel runs the review viewer for one quarantine item and returns its
// result (open the target, or back).
func runReviewModel(acts listActions, queue []store.QuarantineItem, startIndex int, color bool, width int) (reviewResult, error) {
	m := newReviewModel(acts, queue, startIndex, color, width)
	out, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(os.Stdout)).Run()
	if err != nil {
		return reviewResult{}, err
	}
	fm, ok := out.(*reviewModel)
	if !ok {
		return reviewResult{}, nil
	}
	return fm.result, nil
}
