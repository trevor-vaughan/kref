package main

import (
	"errors"
	"strings"

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

// testReviewer stands in for the identity resolveActor hands the cockpit, so a
// spec can tell a real reviewer from a hardcoded placeholder.
const testReviewer = "Reviewer Name"

// twoEntryModel returns a reloaded, sized model with a quarantine row + two entries.
func twoEntryModel() (*listModel, *fakeActions) {
	f := newFake()
	f.queue = []store.QuarantineItem{{ID: "q111", HeldOp: true, OpKind: "set-body", Target: "aaaa", TargetTitle: "Alpha"}}
	f.entries = []*entry.Snapshot{
		{ID: "aaaa", Tier: "personal", TierType: "personal", Kind: "document", Status: "open", Title: "Alpha"},
		{ID: "bbbb", Tier: "personal", TierType: "personal", Kind: "todo", Status: "open", Title: "Beta"},
	}
	m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, true, store.ListFilter{}, testReviewer, "human")
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
	// who made the last decision, so a spec can prove the TUI records the
	// resolved reviewer rather than a placeholder
	approvedBy   string
	approvedKind string
	rejectedBy   string
	rejectedKind string
	archived     []string
	restored     []string
	statuses     map[string]string
	favs         map[string]string
	// injected read failures, so a spec can tell an unreadable store from an
	// empty one
	listErr   error
	queueErr  error
	detailErr error
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

func (f *fakeActions) QuarantineQueue() ([]store.QuarantineItem, error) {
	return f.queue, f.queueErr
}
func (f *fakeActions) QuarantineDetail(id entity.Id) (store.QuarantineDetail, error) {
	return f.details[id], f.detailErr
}
func (f *fakeActions) ListEntries() ([]*entry.Snapshot, error) { return f.entries, f.listErr }
func (f *fakeActions) ApproveQuarantine(id entity.Id, note, ap, k string) error {
	f.approved = append(f.approved, id.String())
	f.approvedBy, f.approvedKind = ap, k
	f.queue = removeQ(f.queue, id)
	return nil
}
func (f *fakeActions) RejectQuarantine(id entity.Id, note, rejecter, k string) (string, error) {
	f.rejected = append(f.rejected, id.String())
	f.rejectedBy, f.rejectedKind = rejecter, k
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
	// The query is now captured by the shared pagerSearch rather than a
	// textinput, so the spec types it the way a reader would.
	typeQuery := func(m *listModel, q string) {
		m.Update(key('/'))
		for _, r := range q {
			m.Update(key(r))
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}

	It("jumps the cursor to a match on / then enter", func() {
		m, _ := twoEntryModel() // rows: quarantine, aaaa "Alpha", bbbb "Beta"
		m.Update(key('/'))
		Expect(m.mode).To(Equal(listModeSearch))
		for _, r := range "Beta" {
			m.Update(key(r))
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.mode).To(Equal(listModeNone))
		Expect(m.rows[m.cursor].id).To(Equal(entity.Id("bbbb")))
	})

	It("reports no matches", func() {
		m, _ := twoEntryModel()
		typeQuery(m, "zzzznotfound")
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

var _ = Describe("list cockpit horizontal scroll", func() {
	It("pans right to reveal a title wider than the window", func() {
		f := newFake()
		f.entries = []*entry.Snapshot{
			{ID: "aaaa", Tier: "personal", TierType: "personal", Kind: "document", Status: "open",
				Title: "START" + strings.Repeat("-", 100) + "END"},
		}
		m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, true, store.ListFilter{}, testReviewer, "human")
		m.reload()
		m.sv.Resize(40, 12)
		m.syncContent()
		var mm tea.Model = m
		Expect(mm.(*listModel).View()).NotTo(ContainSubstring("END"))
		for range 20 {
			mm, _ = mm.(*listModel).Update(tea.KeyMsg{Type: tea.KeyRight})
		}
		Expect(mm.(*listModel).View()).To(ContainSubstring("END"))
	})
})

// A quarantine decision is the audit record of a human approval gate, so it has
// to name the human. Both TUIs used to pass the literal string "me" as the
// approver and an empty string as the rejecter, while the CLI resolved the real
// identity.
var _ = Describe("list cockpit decision attribution", func() {
	decide := func(k rune) *fakeActions {
		GinkgoHelper()
		m, f := twoEntryModel()
		m.cursor = 0 // the quarantine row
		m.Update(key(k))
		m.input.SetValue("a note")
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return f
	}

	It("approves as the resolved reviewer, not a placeholder", func() {
		f := decide('a')
		Expect(f.approvedBy).To(Equal(testReviewer))
		Expect(f.approvedKind).To(Equal("human"))
	})

	It("rejects as the resolved reviewer, not an empty string", func() {
		f := decide('r')
		Expect(f.rejectedBy).To(Equal(testReviewer))
		Expect(f.rejectedKind).To(Equal("human"))
	})
})

// A read failure must not read as an empty repository: "nothing here" and "the
// store is unreadable" are different facts, and the project rule is that a
// failure is never silent.
var _ = Describe("list cockpit reload errors", func() {
	It("reports an entry-list failure instead of showing an empty list", func() {
		f := newFake()
		f.listErr = errors.New("ref store unreadable")
		m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, false, store.ListFilter{}, testReviewer, "human")
		m.reload()
		m.sv.Resize(80, 24)
		Expect(m.View()).To(ContainSubstring("ref store unreadable"))
	})

	It("reports a quarantine-queue failure too", func() {
		f := newFake()
		f.queueErr = errors.New("queue unreadable")
		m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, false, store.ListFilter{}, testReviewer, "human")
		m.reload()
		m.sv.Resize(80, 24)
		Expect(m.View()).To(ContainSubstring("queue unreadable"))
	})
})

// e, x, u, s and f are advertised in the help popup unconditionally but did
// nothing at all with the cursor on a quarantine row, while a and r on an entry
// row already explained themselves. The rule the entry viewer's noComment
// follows applies here too: a key the interface advertises has to account for
// itself rather than look broken.
var _ = Describe("list cockpit key feedback", func() {
	entryOnlyKeys := []rune{'e', 'x', 'u', 's', 'f'}

	for _, k := range entryOnlyKeys {
		It("explains '"+string(k)+"' on a quarantine row instead of doing nothing", func() {
			m, _ := twoEntryModel() // cursor 0 is the quarantine row
			m.Update(key(k))
			Expect(m.err).To(ContainSubstring("quarantine"))
		})
	}

	It("still acts on an entry row", func() {
		m, f := twoEntryModel()
		m.Update(key('j')) // to an entry row
		m.Update(key('x'))
		Expect(f.archived).To(ContainElement("aaaa"))
		Expect(m.err).To(BeEmpty())
	})

	It("toggles colour on t, as the entry viewer does", func() {
		m, _ := twoEntryModel()
		before := m.sv.Plain()
		m.Update(key('t'))
		Expect(m.sv.Plain()).NotTo(Equal(before))
	})
})

// The list cockpit had its own search with no match counter, and rebuilt the
// model on every open/return round-trip, silently discarding it — after which n
// answered "no matches", which reads as "your query found nothing" rather than
// "your query is gone".
var _ = Describe("list cockpit search", func() {
	typeQuery := func(m *listModel, q string) {
		m.Update(key('/'))
		for _, r := range q {
			m.Update(key(r))
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}

	It("reports the match position in the footer", func() {
		m, _ := twoEntryModel()
		typeQuery(m, "Alpha")
		Expect(m.View()).To(MatchRegexp(`match \d+/\d+`))
	})

	It("moves the cursor to the match", func() {
		m, _ := twoEntryModel()
		typeQuery(m, "Beta")
		Expect(m.rows[m.cursor].line).To(ContainSubstring("Beta"))
	})

	It("says so when nothing matches", func() {
		m, _ := twoEntryModel()
		typeQuery(m, "nosuchthing")
		Expect(m.err).To(Equal("no matches"))
	})

	It("keeps the search across the model rebuild an open/return forces", func() {
		m, _ := twoEntryModel()
		typeQuery(m, "Alpha")
		carried := m.search

		m2, _ := twoEntryModel()
		m2.search = carried
		m2.search.refresh(m2.searchMatcher)
		m2.syncContent()
		m2.Update(key('n'))
		Expect(m2.err).To(BeEmpty(), "n must not claim 'no matches' on a carried search")
		Expect(m2.View()).To(MatchRegexp(`match \d+/\d+`))
	})
})
