package main

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/trevor-vaughan/kref/internal/tui"
)

// viewSettings is the `,` view-options overlay for the surfaces whose menu holds
// nothing but display toggles: the list cockpit, the search/diff pager and the
// quarantine review view. The entry viewer keeps its own, because its menu shares
// an input-mode enum with five other overlays and its rows come from the action
// table.
//
// Passive in the same way tui.Menu is — it owns the selection and the keys, the
// host owns the settings themselves and what changing one means.
type viewSettings struct{ menu *tui.Menu }

// open shows the overlay with the rows the host built.
func (v *viewSettings) open(rows []tui.MenuRow) {
	menu := tui.NewMenu("view options")
	menu.SetRows(rows)
	v.menu = menu
}

func (v *viewSettings) isOpen() bool { return v.menu != nil }

func (v *viewSettings) close() { v.menu = nil }

// key routes one keypress and returns the ID of a row the reader chose, or ""
// when the key moved the selection, closed the overlay, or meant nothing here.
// A choice deliberately leaves the overlay open: changing two settings should be
// one visit, and changing your mind about one is then a single keypress.
func (v *viewSettings) key(msg tea.KeyMsg) string {
	switch msg.String() {
	case "esc", ",", "q":
		v.close()
	case "up", "k":
		v.menu.Move(-1)
	case "down", "j":
		v.menu.Move(1)
	case "enter", " ":
		if row, ok := v.menu.Selected(); ok {
			return row.ID
		}
	}
	return ""
}

// refresh redraws the rows with their new values, keeping the selection on the
// row that was just changed.
func (v *viewSettings) refresh(rows []tui.MenuRow) { v.menu.RefreshRows(rows) }

func (v *viewSettings) render(width int, color bool) string { return v.menu.Render(width, color) }

// colorRow is the one setting every surface with a `,` menu carries.
func colorRow(on bool) tui.MenuRow {
	return tui.MenuRow{ID: settingColor, Label: "colour", Value: onOff(on), Enabled: true}
}
