package main

import (
	"bytes"
	"errors"
	"fmt"
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

// echoText is m's exit echo as a string.
func echoText(m *listModel) string {
	var buf bytes.Buffer
	m.echoResults(&buf)
	return buf.String()
}

// echoRows is the row block of an exit echo: everything between the column
// header that opens it and the blank line + tally that close a search echo.
func echoRows(out string) []string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 {
		return nil
	}
	lines = lines[1:]
	if n := len(lines); n >= 2 && lines[n-2] == "" {
		lines = lines[:n-2]
	}
	return lines
}

// twoEntryModel returns a reloaded, sized model with a quarantine row + two entries.
func twoEntryModel() (*listModel, *fakeActions) {
	f := newFake()
	f.queue = []store.QuarantineItem{{ID: "q111", HeldOp: true, OpKind: "set-body", Target: "aaaa", TargetTitle: "Alpha"}}
	f.entries = []*entry.Snapshot{
		{ID: "aaaa", Tier: "personal", TierType: "personal", Kind: "document", Status: "open", Title: "Alpha"},
		{ID: "bbbb", Tier: "personal", TierType: "personal", Kind: "todo", Status: "open", Title: "Beta"},
	}
	m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, true, store.ListFilter{}, testReviewer, "human",
		cockpitProfile{title: "kref", queue: true})
	m.reload()
	m.sv.Resize(80, 24)
	m.syncContent()
	return m, f
}

// fakeActions is an in-memory listActions for headless model tests.
type fakeActions struct {
	queue []store.QuarantineItem
	// how many times the model asked for the queue, so a spec can prove a
	// queueless profile never issues the call at all
	queueCalls int
	details    map[entity.Id]store.QuarantineDetail
	entries    []*entry.Snapshot
	matches    map[string]int
	approved   []string
	rejected   []string
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
	f.queueCalls++
	return f.queue, f.queueErr
}
func (f *fakeActions) QuarantineDetail(id entity.Id) (store.QuarantineDetail, error) {
	return f.details[id], f.detailErr
}
func (f *fakeActions) ListEntries() ([]*entry.Snapshot, map[string]int, error) {
	return f.entries, f.matches, f.listErr
}
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
		header, rows := buildCockpitRows(q, e, render.ListOptions{Columns: render.DefaultColumns})
		Expect(header).To(ContainSubstring("TITLE"))
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
		m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, true, store.ListFilter{}, testReviewer, "human",
			cockpitProfile{title: "kref", queue: true})
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
		m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, false, store.ListFilter{}, testReviewer, "human",
			cockpitProfile{title: "kref", queue: true})
		m.reload()
		m.sv.Resize(80, 24)
		Expect(m.View()).To(ContainSubstring("ref store unreadable"))
	})

	It("reports a quarantine-queue failure too", func() {
		f := newFake()
		f.queueErr = errors.New("queue unreadable")
		m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, false, store.ListFilter{}, testReviewer, "human",
			cockpitProfile{title: "kref", queue: true})
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

	It("toggles colour from the view-options menu and persists it", func() {
		m, _ := twoEntryModel()
		before := m.sv.Plain()
		m.Update(key(','))
		Expect(m.View()).To(ContainSubstring("view options"))
		Expect(m.View()).To(ContainSubstring("colour"))

		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.sv.Plain()).NotTo(Equal(before))
		_, uc, err := loadUserConfigForEdit()
		Expect(err).NotTo(HaveOccurred())
		Expect(uc.Color).NotTo(BeNil())
		Expect(*uc.Color).To(Equal(m.color))

		// The menu stays open, so changing your mind is one more keypress.
		Expect(m.View()).To(ContainSubstring("view options"))
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.sv.Plain()).To(Equal(before))
		m.Update(key(','))
		Expect(m.View()).NotTo(ContainSubstring("view options"))
	})

	It("no longer treats t as a key", func() {
		// Colour is a saved preference now; it lives behind `,` on every surface.
		m, _ := twoEntryModel()
		before := m.sv.Plain()
		m.Update(key('t'))
		Expect(m.sv.Plain()).To(Equal(before))
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

var _ = Describe("cockpit match counts", func() {
	It("renders the MATCHES column from the counts ListEntries returns", func() {
		f := newFake()
		f.entries = []*entry.Snapshot{
			{ID: "aaaa", Tier: "shared", TierType: "shared",
				Kind: "spec", Status: "open", Title: "Alpha"},
			{ID: "bbbb", Tier: "shared", TierType: "shared",
				Kind: "note", Status: "open", Title: "Beta"},
		}
		f.matches = map[string]int{"aaaa": 14, "bbbb": 6}
		m := newListModel(f, render.ListOptions{
			Columns:       render.SearchColumns,
			ShowAll:       true,
			PreserveOrder: true,
		}, true, store.ListFilter{Search: "x"}, testReviewer, "human",
			cockpitProfile{title: "kref search — x"})
		m.reload()
		m.sv.Resize(80, 24)
		m.syncContent()

		Expect(m.rows).To(HaveLen(2))
		Expect(m.rows[0].line).To(HavePrefix("     14  "))
		Expect(m.rows[1].line).To(HavePrefix("      6  "))
	})
})

var _ = Describe("cockpit profiles", func() {
	// searchModel is the search-profile counterpart of twoEntryModel: the same
	// shape of data, a non-empty quarantine queue, and the search profile.
	searchModel := func() (*listModel, *fakeActions) {
		f := newFake()
		f.queue = []store.QuarantineItem{{
			ID: "q111", HeldOp: true, OpKind: "set-body",
			Target: "aaaa", TargetTitle: "Alpha",
		}}
		f.entries = []*entry.Snapshot{
			{ID: "aaaa", Tier: "shared", TierType: "shared",
				Kind: "spec", Status: "open", Title: "Alpha"},
		}
		f.matches = map[string]int{"aaaa": 3}
		m := newListModel(f, render.ListOptions{
			Columns: render.SearchColumns, ShowAll: true, PreserveOrder: true,
		}, true, store.ListFilter{Search: "x"}, testReviewer, "human",
			cockpitProfile{title: "kref search — x", echoOnExit: true})
		m.reload()
		m.sv.Resize(80, 24)
		m.syncContent()
		return m, f
	}

	It("omits quarantine rows even when the queue is non-empty", func() {
		m, _ := searchModel()
		Expect(m.rows).To(HaveLen(1))
		Expect(m.rows[0].kind).To(Equal(rowEntry))
	})

	It("never asks the store for the queue", func() {
		m, f := searchModel()
		Expect(m.rows).To(HaveLen(1))
		Expect(f.queueCalls).To(Equal(0))
	})

	It("omits the approve/reject row from its help", func() {
		Expect(listHelpRows(false)).NotTo(ContainElement(ContainSubstring("approve/reject")))
		Expect(listHelpRows(true)).To(ContainElement(ContainSubstring("approve/reject")))
	})

	It("still advertises edit on a surface without the queue", func() {
		Expect(listHelpRows(false)).To(ContainElement(ContainSubstring("edit")))
	})

	It("explains a rather than claiming the row is not a quarantine item", func() {
		m, _ := searchModel()
		m.Update(key('a'))
		Expect(m.err).To(ContainSubstring("no review queue here"))
		Expect(m.err).NotTo(ContainSubstring("not a quarantine item"))
	})

	It("still says not a quarantine item in the bare cockpit", func() {
		m, _ := twoEntryModel()
		m.cursor = 1 // an entry row, not the queue row
		m.Update(key('a'))
		Expect(m.err).To(Equal("not a quarantine item"))
	})

	It("titles the bare cockpit kref and the search cockpit with its query", func() {
		bare, _ := twoEntryModel()
		Expect(bare.sv.Render("")).To(ContainSubstring("kref"))
		Expect(bare.sv.Render("")).NotTo(ContainSubstring("kref search"))

		m, _ := searchModel()
		Expect(m.sv.Render("")).To(ContainSubstring("kref search — x"))
	})
})

var _ = Describe("cockpit exit echo", func() {
	// fortyRowSearchModel is a search cockpit with more rows than fit, so the
	// echo has a window to pick from and an offset to respect.
	fortyRowSearchModel := func(height int) *listModel {
		GinkgoHelper()
		f := newFake()
		f.matches = map[string]int{}
		for i := range 40 {
			id := entity.Id(fmt.Sprintf("e%03d", i))
			f.entries = append(f.entries, &entry.Snapshot{
				ID: id, Tier: "shared", TierType: "shared",
				Kind: "spec", Status: "open", Title: fmt.Sprintf("Entry %d", i),
			})
			f.matches[id.String()] = 2
		}
		m := newListModel(f, searchListOptions(true), true,
			store.ListFilter{Search: "x"}, testReviewer, "human",
			cockpitProfile{title: "kref search — x", echoOnExit: true})
		m.reload()
		m.sv.Resize(80, height)
		m.syncContent()
		return m
	}

	It("echoes the visible rows so results stay in scrollback", func() {
		m, _ := twoEntryModel()
		var buf bytes.Buffer
		m.echoResults(&buf)
		Expect(buf.String()).To(ContainSubstring("Alpha"))
		Expect(buf.String()).To(ContainSubstring("Beta"))
	})

	It("echoes nothing before the first size message", func() {
		f := newFake()
		f.entries = []*entry.Snapshot{
			{ID: "aaaa", Tier: "shared", TierType: "shared",
				Kind: "spec", Status: "open", Title: "Alpha"},
		}
		m := newListModel(f, render.ListOptions{Columns: render.DefaultColumns}, true,
			store.ListFilter{}, testReviewer, "human",
			cockpitProfile{title: "kref", queue: true})
		m.reload()
		var buf bytes.Buffer
		m.echoResults(&buf)
		Expect(buf.String()).To(BeEmpty())
	})

	It("echoes only the rows inside the viewport, not the whole list", func() {
		m := fortyRowSearchModel(12)
		Expect(echoRows(echoText(m))).To(HaveLen(m.sv.Height()))
	})

	It("echoes the rows at the current offset, not always the first ones", func() {
		m := fortyRowSearchModel(12)
		m.Update(key('G')) // to the bottom, so the window is no longer at offset 0
		Expect(m.sv.YOffset()).To(BeNumerically(">", 0))

		out := echoText(m)
		rows := echoRows(out)
		Expect(rows).To(HaveLen(m.sv.Height()))
		// The window starts where the viewport does and ends on the last row.
		Expect(rows[0]).To(Equal(m.rows[m.sv.YOffset()].line))
		Expect(rows[len(rows)-1]).To(Equal(m.rows[len(m.rows)-1].line))
		Expect(out).NotTo(ContainSubstring("Entry 0 "))
	})

	It("frames the echo with the column header and the tally", func() {
		m := fortyRowSearchModel(12)
		out := strings.Split(strings.TrimRight(echoText(m), "\n"), "\n")
		Expect(out).NotTo(BeEmpty())
		// The bare rows are a leading column of integers; the header is what
		// says the column is MATCHES.
		Expect(out[0]).To(ContainSubstring("MATCHES"))
		Expect(out[0]).To(ContainSubstring("TITLE"))
		// The tally describes the whole result set, not the visible window.
		Expect(out[len(out)-1]).To(Equal("40 entries, 80 matches"))
	})

	It("omits the tally on a cockpit that is not a search", func() {
		m, _ := twoEntryModel()
		Expect(echoText(m)).NotTo(ContainSubstring("entries,"))
	})
})

var _ = Describe("search cockpit key consistency", func() {
	searchModelWithRows := func(n int) *listModel {
		f := newFake()
		f.matches = map[string]int{}
		for i := range n {
			id := entity.Id(fmt.Sprintf("e%03d", i))
			f.entries = append(f.entries, &entry.Snapshot{
				ID: id, Tier: "shared", TierType: "shared",
				Kind: "spec", Status: "open", Title: fmt.Sprintf("Entry %d", i),
			})
			f.matches[id.String()] = n - i
		}
		m := newListModel(f, render.ListOptions{
			Columns: render.SearchColumns, ShowAll: true, PreserveOrder: true,
		}, true, store.ListFilter{Search: "x"}, testReviewer, "human",
			cockpitProfile{title: "kref search — x", echoOnExit: true})
		m.reload()
		m.sv.Resize(80, 10)
		m.syncContent()
		return m
	}

	It("moves to the bottom on G and back to the top on g and home", func() {
		m := searchModelWithRows(30)
		m.Update(key('G'))
		Expect(m.cursor).To(Equal(29))
		m.Update(key('g'))
		Expect(m.cursor).To(Equal(0))
		m.Update(key('G'))
		m.Update(tea.KeyMsg{Type: tea.KeyHome})
		Expect(m.cursor).To(Equal(0))
		m.Update(tea.KeyMsg{Type: tea.KeyEnd})
		Expect(m.cursor).To(Equal(29))
	})

	It("opens the help popup on ? and the settings overlay on ,", func() {
		m := searchModelWithRows(3)
		m.Update(key('?'))
		Expect(m.sv.HelpOpen()).To(BeTrue())
		m.Update(key('?')) // any key dismisses
		Expect(m.sv.HelpOpen()).To(BeFalse())
		m.Update(key(','))
		Expect(m.settings.isOpen()).To(BeTrue())
	})

	It("exits with an open result when enter lands on a hit", func() {
		m := searchModelWithRows(3)
		m.Update(key('j'))
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(cmd).NotTo(BeNil()) // tea.Quit
		Expect(m.result.action).To(Equal("open"))
		Expect(m.result.id).To(Equal(entity.Id("e001")))
		Expect(m.result.cursor).To(Equal(1))
	})

	It("ranks by matches, with no favorite pinning to disturb it", func() {
		m := searchModelWithRows(4)
		Expect(m.rows[0].id).To(Equal(entity.Id("e000"))) // 4 matches
		Expect(m.rows[3].id).To(Equal(entity.Id("e003"))) // 1 match
	})

	It("finds a row on / and selects it", func() {
		m := searchModelWithRows(20)
		m.Update(key('/'))
		Expect(m.mode).To(Equal(listModeSearch))
		for _, r := range "Entry 7" {
			m.Update(key(r))
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(m.mode).To(Equal(listModeNone))
		Expect(m.rows[m.cursor].line).To(ContainSubstring("Entry 7"))
	})
})

var _ = Describe("cockpit footer hints", func() {
	It("omits the a/r hint on a surface with no review queue", func() {
		f := newFake()
		f.entries = []*entry.Snapshot{
			{ID: "aaaa", Tier: "shared", TierType: "shared",
				Kind: "spec", Status: "open", Title: "Alpha"},
		}
		f.matches = map[string]int{"aaaa": 3}
		m := newListModel(f, render.ListOptions{
			Columns: render.SearchColumns, ShowAll: true, PreserveOrder: true,
		}, true, store.ListFilter{Search: "x"}, testReviewer, "human",
			cockpitProfile{title: "kref search — x", echoOnExit: true})
		m.reload()
		m.sv.Resize(200, 24) // wide, so the fullest variant is the one chosen
		m.syncContent()
		Expect(m.footer()).NotTo(ContainSubstring("a/r review"))
		Expect(m.footer()).To(ContainSubstring("e edit"))
	})

	It("keeps the a/r hint in the bare cockpit", func() {
		m, _ := twoEntryModel()
		m.sv.Resize(200, 24)
		m.syncContent()
		Expect(m.footer()).To(ContainSubstring("a/r review"))
	})
})

var _ = Describe("searchListOptions", func() {
	// runSearchBrowse renders through this, so asserting it here asserts the
	// production path. A spec that builds its own ListOptions literal instead
	// proves only that the renderer honours those options — drop PreserveOrder
	// from the caller and relevance ranking silently reverts to
	// tier→kind→title with the whole suite still green.
	It("preserves the ranking and lets nothing outrank it", func() {
		opts := searchListOptions(true)
		Expect(opts.Columns).To(Equal(render.SearchColumns))
		Expect(opts.PreserveOrder).To(BeTrue())
		Expect(opts.ShowAll).To(BeTrue())
		Expect(opts.Favorites).To(BeNil())
		Expect(opts.Sort).To(BeNil())
		Expect(opts.Color).To(BeTrue())
	})

	It("carries the caller's colour decision through", func() {
		Expect(searchListOptions(false).Color).To(BeFalse())
	})
})

// listCockpitActions is the cockpit's only data path — the fake used everywhere
// above stands in for it, so nothing else reaches the List/Search branch, the
// --sort reapplication, or the match map the MATCHES column and the ranking are
// rendered from.
var _ = Describe("listCockpitActions.ListEntries", func() {
	// seeded opens a real store holding two entries that both match "auth",
	// with the one that matches more sorting later by title — so a
	// matches-ranked order and a title-ranked order are distinguishable.
	seeded := func() *store.Store {
		GinkgoHelper()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--kind", "spec", "--title", "Zebra auth", "--body", "auth auth")
		run("--dir", dir, "new", "--kind", "spec", "--title", "Alpha auth", "--body", "unrelated")
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(s.Close)
		return s
	}

	titles := func(items []*entry.Snapshot) []string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.Title
		}
		return out
	}

	It("ranks by match count and keys the counts by the id the renderer looks up", func() {
		acts := listCockpitActions{s: seeded(), filter: store.ListFilter{Search: "auth"}}
		items, matches, err := acts.ListEntries()
		Expect(err).NotTo(HaveOccurred())
		Expect(titles(items)).To(Equal([]string{"Zebra auth", "Alpha auth"}))

		// render.ListLines reads the map by snap.ID.String(); a map keyed any
		// other way renders an empty MATCHES column rather than failing.
		Expect(matches).To(HaveLen(2))
		for _, it := range items {
			Expect(matches).To(HaveKey(it.ID.String()))
		}
		Expect(matches[items[0].ID.String()]).
			To(BeNumerically(">", matches[items[1].ID.String()]))
	})

	It("reapplies --sort, so the order survives the reload after a mutation", func() {
		acts := listCockpitActions{
			s: seeded(), filter: store.ListFilter{Search: "auth"}, sortBy: "title",
		}
		items, _, err := acts.ListEntries()
		Expect(err).NotTo(HaveOccurred())
		Expect(titles(items)).To(Equal([]string{"Alpha auth", "Zebra auth"}))
	})

	It("reports a bad --sort rather than silently keeping the default order", func() {
		acts := listCockpitActions{
			s: seeded(), filter: store.ListFilter{Search: "auth"}, sortBy: "nosuchfield",
		}
		_, _, err := acts.ListEntries()
		Expect(err).To(HaveOccurred())
	})

	It("takes the List branch with no counts when the filter carries no query", func() {
		acts := listCockpitActions{s: seeded(), filter: store.ListFilter{}}
		items, matches, err := acts.ListEntries()
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(2))
		// nil, not empty: the MATCHES column is a search artifact.
		Expect(matches).To(BeNil())
	})

	It("serves a primed search once, then reads the store", func() {
		s := seeded()
		primed, err := s.Search(store.ListFilter{Search: "auth"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sortSearchResults(primed, "title")).To(Succeed())

		// sortBy stays empty, so a second call cannot reproduce the primed
		// title order — which is what makes the handover observable.
		acts := listCockpitActions{
			s: s, filter: store.ListFilter{Search: "auth"},
			primed: &primedSearch{results: primed},
		}
		first, _, err := acts.ListEntries()
		Expect(err).NotTo(HaveOccurred())
		Expect(titles(first)).To(Equal([]string{"Alpha auth", "Zebra auth"}))

		second, _, err := acts.ListEntries()
		Expect(err).NotTo(HaveOccurred())
		Expect(titles(second)).To(Equal([]string{"Zebra auth", "Alpha auth"}))
	})

	It("reads the store when the primed set is nil rather than rendering empty", func() {
		acts := listCockpitActions{
			s: seeded(), filter: store.ListFilter{Search: "auth"},
			primed: &primedSearch{results: nil},
		}
		items, _, err := acts.ListEntries()
		Expect(err).NotTo(HaveOccurred())
		Expect(titles(items)).To(Equal([]string{"Zebra auth", "Alpha auth"}))
	})
})
