package main

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/store"
)

// key builds a rune KeyMsg for the given single character.
func key(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// twoEntryModel returns a reloaded, sized model with a quarantine row + two entries.
func twoEntryModel() (*listModel, *fakeActions) {
	f := newFake()
	f.queue = []store.QuarantineItem{{ID: "q111", HeldOp: true, OpKind: "set-body", Target: "aaaa", TargetTitle: "Alpha"}}
	f.entries = []*entry.Snapshot{
		{ID: "aaaa", Tier: "personal", TierType: "personal", Kind: "document", Status: "open", Title: "Alpha"},
		{ID: "bbbb", Tier: "personal", TierType: "personal", Kind: "todo", Status: "open", Title: "Beta"},
	}
	m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, true, store.ListFilter{})
	m.reload()
	m.sv.Resize(80, 24)
	m.syncContent()
	return m, f
}

// fakeActions is an in-memory listActions for headless model tests.
type fakeActions struct {
	queue    []store.QuarantineItem
	details  map[entity.Id]store.QuarantineDetail
	entries  []*entry.Snapshot
	approved []string
	rejected []string
	archived []string
	restored []string
	statuses map[string]string
	favs     map[string]string
}

func newFake() *fakeActions {
	return &fakeActions{
		details:  map[entity.Id]store.QuarantineDetail{},
		statuses: map[string]string{},
		favs:     map[string]string{},
	}
}

// removeQ returns q without the item whose ID matches id.
func removeQ(q []store.QuarantineItem, id entity.Id) []store.QuarantineItem {
	out := q[:0:0]
	for _, it := range q {
		if it.ID != id {
			out = append(out, it)
		}
	}
	return out
}

func (f *fakeActions) QuarantineQueue() ([]store.QuarantineItem, error) { return f.queue, nil }
func (f *fakeActions) QuarantineDetail(id entity.Id) (store.QuarantineDetail, error) {
	return f.details[id], nil
}
func (f *fakeActions) ListEntries() ([]*entry.Snapshot, error) { return f.entries, nil }
func (f *fakeActions) ApproveQuarantine(id entity.Id, note, ap, k string) error {
	f.approved = append(f.approved, id.String())
	f.queue = removeQ(f.queue, id)
	return nil
}
func (f *fakeActions) RejectQuarantine(id entity.Id, note, k string) (string, error) {
	f.rejected = append(f.rejected, id.String())
	f.queue = removeQ(f.queue, id)
	return "/tmp/rej", nil
}
func (f *fakeActions) Archive(id entity.Id) error {
	f.archived = append(f.archived, id.String())
	return nil
}
func (f *fakeActions) Unarchive(id entity.Id) error {
	f.restored = append(f.restored, id.String())
	return nil
}
func (f *fakeActions) SetStatus(id entity.Id, st string) error {
	f.statuses[id.String()] = st
	return nil
}
func (f *fakeActions) SetFavorite(name string, id entity.Id) error {
	f.favs[name] = id.String()
	return nil
}
func (f *fakeActions) RemoveFavorite(name string) error { delete(f.favs, name); return nil }
func (f *fakeActions) Favorites() map[string]string     { return f.favs }

var _ = Describe("buildCockpitRows", func() {
	It("puts the quarantine group first, then entry rows in display order", func() {
		q := []store.QuarantineItem{{ID: "q111", HeldOp: true, OpKind: "set-body", Target: "aaaa", TargetTitle: "Alpha"}}
		e := []*entry.Snapshot{{ID: "aaaa", Tier: "personal", TierType: "personal", Kind: "document", Status: "open", Title: "Alpha"}}
		rows := buildCockpitRows(q, e, render.ListOptions{Columns: render.DefaultColumns})
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].kind).To(Equal(rowQuarantine))
		Expect(rows[0].id).To(Equal(entity.Id("q111")))
		Expect(rows[1].kind).To(Equal(rowEntry))
		Expect(rows[1].id).To(Equal(entity.Id("aaaa")))
		Expect(rows[1].line).To(ContainSubstring("Alpha"))
	})
})

var _ = Describe("listModel navigation", func() {
	It("moves the cursor down and clamps at the ends", func() {
		m, _ := twoEntryModel()
		Expect(m.cursor).To(Equal(0))
		m.Update(key('j'))
		Expect(m.cursor).To(Equal(1))
		m.Update(key('j'))
		Expect(m.cursor).To(Equal(2)) // 3 rows: quarantine + 2 entries
		m.Update(key('j'))
		Expect(m.cursor).To(Equal(2)) // clamped at the bottom
		m.Update(key('k'))
		m.Update(key('k'))
		m.Update(key('k'))
		Expect(m.cursor).To(Equal(0)) // clamped at the top
	})

	It("renders the cursor marker on the selected row", func() {
		m, _ := twoEntryModel()
		Expect(m.View()).To(ContainSubstring(cursorMarker))
	})
})

var _ = Describe("listModel open/edit dispatch", func() {
	It("exits with an open result for the selected entry on enter", func() {
		m, _ := twoEntryModel()
		m.Update(key('j')) // cursor → first entry row (aaaa)
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(cmd).NotTo(BeNil()) // tea.Quit
		Expect(m.result.action).To(Equal("open"))
		Expect(m.result.id).To(Equal(entity.Id("aaaa")))
	})

	It("exits with an edit result on e", func() {
		m, _ := twoEntryModel()
		m.Update(key('j'))
		m.Update(key('e'))
		Expect(m.result.action).To(Equal("edit"))
		Expect(m.result.id).To(Equal(entity.Id("aaaa")))
	})

	It("opens the review view (not the raw target) on enter for a quarantine row", func() {
		m, _ := twoEntryModel() // cursor 0 = the held-op quarantine row
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(cmd).NotTo(BeNil())
		Expect(m.result.action).To(Equal("review"))
		Expect(m.result.id).To(Equal(entity.Id("q111"))) // the quarantine item id
	})

	It("does not edit a quarantine row", func() {
		m, _ := twoEntryModel()
		m.Update(key('e')) // cursor 0 = quarantine row
		Expect(m.result.action).To(Equal(""))
	})
})

var _ = Describe("listModel approve/reject", func() {
	It("approves the quarantine row on 'a' after entering a note", func() {
		m, f := twoEntryModel() // cursor 0 = the held-op quarantine row
		m.Update(key('a'))
		Expect(m.mode).To(Equal(listModeNote))
		m.input.SetValue("looks fine")
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(f.approved).To(ContainElement("q111"))
		Expect(m.mode).To(Equal(listModeNone))
	})

	It("rejects the quarantine row on 'r'", func() {
		m, f := twoEntryModel()
		m.Update(key('r'))
		Expect(m.mode).To(Equal(listModeNote))
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(f.rejected).To(ContainElement("q111"))
	})

	It("no-ops 'a' on a non-quarantine row", func() {
		m, f := twoEntryModel()
		m.Update(key('j')) // to an entry row
		m.Update(key('a'))
		Expect(m.mode).To(Equal(listModeNone))
		Expect(f.approved).To(BeEmpty())
	})

	It("cancels the note overlay on esc without acting", func() {
		m, f := twoEntryModel()
		m.Update(key('a'))
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		Expect(m.mode).To(Equal(listModeNone))
		Expect(f.approved).To(BeEmpty())
	})
})

var _ = Describe("listModel archive/unarchive", func() {
	It("archives the selected entry on 'x'", func() {
		m, f := twoEntryModel()
		m.Update(key('j')) // to entry row aaaa
		m.Update(key('x'))
		Expect(f.archived).To(ContainElement("aaaa"))
	})

	It("unarchives the selected entry on 'u'", func() {
		m, f := twoEntryModel()
		m.Update(key('j'))
		m.Update(key('u'))
		Expect(f.restored).To(ContainElement("aaaa"))
	})

	It("no-ops 'x' on a quarantine row", func() {
		m, f := twoEntryModel()
		m.Update(key('x')) // cursor 0 = quarantine row
		Expect(f.archived).To(BeEmpty())
	})
})

var _ = Describe("listModel status picker", func() {
	It("sets a chosen status on 's' then move + enter", func() {
		m, f := twoEntryModel()
		m.Update(key('j')) // to entry row aaaa (status open)
		m.Update(key('s'))
		Expect(m.mode).To(Equal(listModeStatus))
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(f.statuses).To(HaveKey("aaaa"))
		Expect(m.mode).To(Equal(listModeNone))
	})

	It("no-ops 's' on a quarantine row", func() {
		m, _ := twoEntryModel()
		m.Update(key('s')) // cursor 0 = quarantine row
		Expect(m.mode).To(Equal(listModeNone))
	})
})

var _ = Describe("listModel alias overlay", func() {
	It("sets an alias on 'f' then a name", func() {
		m, f := twoEntryModel()
		m.Update(key('j')) // entry aaaa
		m.Update(key('f'))
		Expect(m.mode).To(Equal(listModeFav))
		m.input.SetValue("alpha-notes")
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(f.favs).To(HaveKeyWithValue("alpha-notes", "aaaa"))
		Expect(m.mode).To(Equal(listModeNone))
	})

	It("prefills an existing alias and removes it on empty save", func() {
		m, f := twoEntryModel()
		f.favs["alpha"] = "aaaa"
		m.Update(key('j'))
		m.Update(key('f'))
		Expect(m.input.Value()).To(Equal("alpha"))
		m.input.SetValue("")
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(f.favs).NotTo(HaveKey("alpha"))
	})

	It("no-ops 'f' on a quarantine row", func() {
		m, _ := twoEntryModel()
		m.Update(key('f'))
		Expect(m.mode).To(Equal(listModeNone))
	})
})

var _ = Describe("listModel search", func() {
	It("jumps the cursor to a match on / then enter", func() {
		m, _ := twoEntryModel() // rows: quarantine, aaaa "Alpha", bbbb "Beta"
		m.Update(key('/'))
		Expect(m.mode).To(Equal(listModeSearch))
		m.input.SetValue("Beta")
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.mode).To(Equal(listModeNone))
		Expect(m.rows[m.cursor].id).To(Equal(entity.Id("bbbb")))
	})

	It("reports no matches", func() {
		m, _ := twoEntryModel()
		m.Update(key('/'))
		m.input.SetValue("zzzznotfound")
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.err).To(ContainSubstring("no match"))
	})
})

var _ = Describe("listModel help dismiss", func() {
	It("closes the help overlay on esc without quitting", func() {
		m, _ := twoEntryModel()
		m.Update(key('?'))
		Expect(m.sv.HelpOpen()).To(BeTrue())
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		Expect(m.sv.HelpOpen()).To(BeFalse())
		Expect(cmd).To(BeNil()) // dismissed the popup, did not quit
	})

	It("dismisses help on any key without acting on the list underneath", func() {
		m, _ := twoEntryModel()
		m.Update(key('?'))
		m.Update(key('j')) // closes help; must not move the cursor
		Expect(m.sv.HelpOpen()).To(BeFalse())
		Expect(m.cursor).To(Equal(0))
	})

	It("still quits on esc when nothing is open", func() {
		m, _ := twoEntryModel()
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		Expect(cmd).NotTo(BeNil()) // tea.Quit
	})
})
