package main

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/git-bug/git-bug/entity"
	"github.com/trevor-vaughan/kref/internal/content"
	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/outline"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/todoguard"
	"github.com/trevor-vaughan/kref/internal/tui"
	"github.com/trevor-vaughan/kref/internal/xdg"
)

// HeaderProvider supplies the viewer's dynamic, entry-semantic inputs: the
// sticky header signal lines, the sections collapsed on first render (by outline
// heading Path), and the ctrl+r refresh. The comment write path uses the
// separate commentWriter interface. An optional ExpandableHeader capability
// (type-asserted) for the show e-expand is deferred to the config-menu work.
type HeaderProvider interface {
	HeaderLines() []string
	InitialFold() map[string]bool
	Reload() (header []string, comments []entry.Comment, err error)
}

type viewerInput struct {
	title       string
	body        string
	contentType string
	color       bool
	width       int
	comments    []entry.Comment
	writer      commentWriter
	entryID     entity.Id
	actor       string
	actorKind   string
	provider    HeaderProvider
}

const (
	itemComment = iota
	itemSection
)

// cursorMarker flags the line the single selection cursor is on.
const cursorMarker = "❯"

var (
	modalStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)
	modalTitleStyle = lipgloss.NewStyle().Bold(true)
	modalHintStyle  = lipgloss.NewStyle().Faint(true)
)

type inputMode int

const (
	modeNone inputMode = iota
	modeReply
	modeEdit
	modeResolveNote
	modeConfirmDelete
)

// commentWriter is the subset of *store.Store the cockpit uses to write comments.
type commentWriter interface {
	AddComment(id entity.Id, actor, actorKind, body string, question bool, replyTo string) (string, error)
	ResolveComment(id entity.Id, target string) error
	UnresolveComment(id entity.Id, target string) error
	EditComment(id entity.Id, target, body string) error
	DeleteComment(id entity.Id, target string) error
}

// cursorItem is one selectable line in the cockpit: a comment node (actionable —
// reply, fold its thread) or a body ## section heading (foldable). A single
// cursor moves across these with no modes.
type cursorItem struct {
	kind        int
	commentID   string // comment node id (itemComment)
	rootID      string // the node's thread root — fold + reply-thread key (itemComment)
	depth       int    // nesting depth within the thread (itemComment) — for ←/→
	heading     string // outline Path — stable fold key (itemSection)
	level       int    // ATX level 1..6 of the heading (itemSection)
	headingText string // heading text for display (itemSection)
}

// sectionMark records a body ## section heading emitted by renderBodyBlocks: its
// outline Path (fold key), ATX level, display text, and the index of its heading
// line within the returned bodyLines. renderContent turns each into a cursor item
// at an absolute offset.
type sectionMark struct {
	path     string
	level    int
	text     string
	bodyLine int
}

type viewerModel struct {
	sv            tui.ScrollView
	title         string // entry identity ("todo · <id>") — prefixes the global header
	header        []string
	body          string
	contentType   string
	color         bool
	width         int
	collapsed     map[string]bool // section fold state, by heading
	foldSnapshot  map[string]bool // folds as they were before a search expanded them
	comments      []entry.Comment
	nodeCollapsed map[string]bool // fold state by comment-node id (hides that node's replies)
	offsets       []int           // rendered-line offset of each item, aligned with items
	items         []cursorItem    // the flat selectable list (comments + sections)
	cur           int             // index of the selection cursor within items
	contentLines  int             // real content height (excludes scroll padding)
	numBuf        string          // accumulated digits for <n>g
	writer        commentWriter
	entryID       entity.Id
	actor         string
	actorKind     string
	reload        func() ([]string, []entry.Comment, error)
	height        int
	mode          inputMode
	ta            textarea.Model
	target        string
	notice        string
	spilled       string      // where a ctrl+c-interrupted draft was preserved
	search        pagerSearch // shared incremental search (/, n/N)
	bodyStartLine int         // content-line index where the body zone begins (for <n>g)
	bodyLineCount int         // rendered body line count (for <n>g clamping)
	contentRaw    []string    // gutter-free content lines, parallel to the ScrollView content (for search)
}

func newViewerModel(in viewerInput) viewerModel {
	sv := tui.NewScrollView(in.title)
	sv.SetHelpRows([]string{
		"j/k ↑/↓       scroll a line",
		"pgup/pgdn     scroll a page",
		"ctrl+d/u      scroll a half page",
		"tab/S-tab     next/prev item",
		"→/← l/h       into a reply / out to parent",
		"g/G           top / bottom",
		"<n>g          goto body line n",
		"space         fold the current section",
		"^space        fold / unfold everything",
		"/ n/N         search / next / prev",
		"r/e/d/x       reply / edit / delete / resolve↔reopen",
		"ctrl+r        refresh",
		"t             toggle colour",
		"? q esc       help / quit",
	})
	col := in.provider.InitialFold()
	if col == nil {
		col = map[string]bool{}
	}
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ct := in.contentType
	if ct == "" {
		ct = "text/markdown"
	}
	return viewerModel{
		sv:            sv,
		title:         in.title,
		header:        in.provider.HeaderLines(),
		body:          in.body,
		contentType:   ct,
		color:         in.color,
		width:         in.width,
		collapsed:     col,
		comments:      in.comments,
		nodeCollapsed: resolvedQuestionRoots(in.comments),
		writer:        in.writer,
		entryID:       in.entryID,
		actor:         in.actor,
		actorKind:     in.actorKind,
		reload:        in.provider.Reload,
		ta:            ta,
	}
}

// injectMarker inserts a fold marker (▾ open / ▸ collapsed) after the leading #s
// of an ATX heading line at the given level.
func injectMarker(headLine string, level int, collapsed bool) string {
	marker := "▾"
	if collapsed {
		marker = "▸"
	}
	prefix := strings.Repeat("#", level) + " "
	return prefix + marker + " " + strings.TrimPrefix(headLine, prefix)
}

// headingSpan is a contiguous run of body lines [start,end): the preamble
// (heading == nil) before the first heading, or one heading with its content.
type headingSpan struct {
	start, end int
	heading    *outline.Heading
}

// headingBlocks partitions nLines of a (folded) body into a leading preamble span
// plus one span per heading, each running to the next heading of any level or the
// end. Heading pointers index into hs.
func headingBlocks(hs []outline.Heading, nLines int) []headingSpan {
	var out []headingSpan
	if len(hs) == 0 {
		if nLines > 0 {
			out = append(out, headingSpan{0, nLines, nil})
		}
		return out
	}
	if hs[0].Line > 0 {
		out = append(out, headingSpan{0, hs[0].Line, nil})
	}
	for i := range hs {
		end := nLines
		if i+1 < len(hs) {
			end = hs[i+1].Line
		}
		out = append(out, headingSpan{hs[i].Line, end, &hs[i]})
	}
	return out
}

// gutterFor returns the fixed 2-column left gutter: the cursor marker on the
// cursor's own line, blanks otherwise. The fixed width keeps the cursor from
// ever shifting the content's indentation.
func gutterFor(isCursor bool) string {
	if isCursor {
		return cursorMarker + " "
	}
	return "  "
}

// numberCol returns the gutterW-wide number column: a right-aligned 1-based body
// line number followed by " │ " for a body line (lineNo >= 1), or gutterW spaces
// for a non-body line (comments, preamble, separators). gutterW is numDigits+3.
func numberCol(lineNo, gutterW int) string {
	if lineNo < 1 {
		return strings.Repeat(" ", gutterW)
	}
	return fmt.Sprintf("%*d │ ", gutterW-3, lineNo)
}

// renderBodyBlocks renders m.body (folded per m.collapsed) into gutter-free
// display lines at wrapWidth, plus a sectionMark per surviving ## heading. It is
// the body zone of the viewport; renderContent prepends gutters and turns the
// marks into cursor items. Markdown folds block-by-block; a heading's block runs
// to the next heading of any level.
func (m *viewerModel) renderBodyBlocks(wrapWidth int) (bodyLines []string, sections []sectionMark) {
	if wrapWidth < 1 {
		wrapWidth = 1
	}
	if !content.IsMarkdown(m.contentType) {
		// Non-markdown (code, JSON, …): render the whole body once, no fold, no
		// section marks. outline is skipped so '#' lines aren't read as headings.
		var b bytes.Buffer
		render.RenderBody(&b, m.body, m.contentType, m.color, wrapWidth)
		return strings.Split(strings.TrimRight(b.String(), "\n"), "\n"), nil
	}
	folded := outline.Parse(m.body).Render(m.collapsed)
	foldedLines := strings.Split(folded, "\n")
	hs := outline.Parse(folded).Headings()
	for _, span := range headingBlocks(hs, len(foldedLines)) {
		blockLines := append([]string(nil), foldedLines[span.start:span.end]...)
		isSection := span.heading != nil
		if isSection {
			blockLines[0] = injectMarker(blockLines[0], span.heading.Level, m.collapsed[span.heading.Path])
		}
		src := strings.Join(blockLines, "\n")

		var rendered []string
		if m.color {
			var rb bytes.Buffer
			render.RenderBody(&rb, src, m.contentType, true, wrapWidth)
			rendered = strings.Split(strings.TrimRight(rb.String(), "\n"), "\n")
			if isSection {
				for len(rendered) > 1 && strings.TrimSpace(rendered[0]) == "" {
					rendered = rendered[1:]
				}
			}
		} else {
			rendered = strings.Split(strings.TrimRight(src, "\n"), "\n")
		}

		if isSection {
			sections = append(sections, sectionMark{
				path: span.heading.Path, level: span.heading.Level,
				text: span.heading.Text, bodyLine: len(bodyLines),
			})
		}
		bodyLines = append(bodyLines, rendered...)
	}
	return bodyLines, sections
}

// renderContent rebuilds the viewport content — the discussion zone (comment
// threads) above the body sections — and the parallel item/offset lists the
// single cursor navigates. Every content line carries a 2-column gutter so the
// cursor marker never shifts indentation.
//
// Padding: (viewportHeight - 1) blank lines are appended so every item offset is
// reachable by SetYOffset even when the content is shorter than the viewport.
func (m *viewerModel) renderContent() {
	// Settle the number-gutter width from the body line count (line count depends
	// on wrap width depends on gutter width — a bounded fixed point like the
	// pager's foldedPagerBody).
	w := max(1, m.width-2)
	first, _ := m.renderBodyBlocks(w)
	d := numDigits(len(first))
	var bodyLines []string
	var sections []sectionMark
	for range 4 {
		bodyLines, sections = m.renderBodyBlocks(max(1, w-(d+3)))
		nd := numDigits(len(bodyLines))
		if nd == d {
			break
		}
		d = nd
	}
	gutterW := d + 3

	var lines []string
	m.contentRaw = m.contentRaw[:0]
	m.offsets = m.offsets[:0]
	m.items = m.items[:0]

	emit := func(raw string, isCursor bool, bodyLineNo int) {
		m.contentRaw = append(m.contentRaw, raw)
		lines = append(lines, gutterFor(isCursor)+numberCol(bodyLineNo, gutterW)+raw)
	}

	// Discussion zone: comment nodes, un-numbered (bodyLineNo 0), blank separators.
	threads := render.RenderCommentThreads(m.comments, m.color, m.nodeCollapsed, max(1, m.width-2-gutterW))
	for ti, t := range threads {
		if ti > 0 {
			emit("", false, 0)
		}
		for _, n := range t.Nodes {
			m.offsets = append(m.offsets, len(lines))
			m.items = append(m.items, cursorItem{kind: itemComment, commentID: n.ID, rootID: t.RootID, depth: n.Depth})
			isCursor := len(m.items)-1 == m.cur
			for li, ln := range n.Lines {
				emit(ln, isCursor && li == 0, 0)
			}
		}
	}
	if len(threads) > 0 {
		emit("", false, 0) // separate the zone from the body
	}

	// Body zone: numbered 1..N; section headings become cursor items.
	m.bodyStartLine = len(lines)
	m.bodyLineCount = len(bodyLines)
	cursorLine := map[int]bool{}
	for _, s := range sections {
		m.offsets = append(m.offsets, m.bodyStartLine+s.bodyLine)
		m.items = append(m.items, cursorItem{kind: itemSection, heading: s.path, level: s.level, headingText: s.text})
		if len(m.items)-1 == m.cur {
			cursorLine[s.bodyLine] = true
		}
	}
	for bi, ln := range bodyLines {
		emit(ln, cursorLine[bi], bi+1)
	}

	// Sticky two-line header + plain toggle (unchanged).
	m.sv.SetTitle(m.globalContext())
	m.sv.SetStatus(m.localContext(threads))
	m.sv.SetPlain(!m.color)

	m.contentLines = len(lines)
	if h := m.sv.Height(); h > 1 {
		for range h - 1 {
			lines = append(lines, "")
		}
	}
	m.sv.SetContent(strings.Join(lines, "\n"))
	m.sv.SetContentHeight(m.contentLines)
}

// scrollLines scrolls the viewport by dy lines for reading, leaving the cursor
// (the reply/edit/fold target) where it is — clamped to the real content so it
// never scrolls into the reachability padding.
func (m *viewerModel) scrollLines(dy int) {
	maxOff := max(0, m.contentLines-m.sv.Height())
	m.sv.SetYOffset(clampInt(m.sv.YOffset()+dy, 0, maxOff))
	m.syncCursorToScroll()
}

// syncCursorToScroll moves the selection cursor to the item at the top of the
// viewport — the item whose span contains the top visible line — so a fold or
// reply acts on what the reader is looking at. Called after any scroll (in either
// direction); it re-renders to move the marker but does not change the scroll
// position. A no-op when the top item is already selected.
func (m *viewerModel) syncCursorToScroll() {
	n := len(m.offsets)
	if n == 0 || m.cur < 0 || m.cur >= n {
		return
	}
	top := m.sv.YOffset()
	h := m.sv.Height()
	// Anchored: while the cursor's item line is still on screen, leave the cursor
	// where it is — an explicit tab/gg/G selection sticks, and scrolling that is a
	// no-op at the top/bottom never moves it. Only when a scroll pushes the
	// cursor's line off the viewport does the cursor follow to the item owning the
	// new top line.
	if off := m.offsets[m.cur]; off >= top && off < top+h {
		return
	}
	cur := 0
	for i, off := range m.offsets {
		if off <= top {
			cur = i
		} else {
			break
		}
	}
	if cur != m.cur {
		m.cur = cur
		m.renderContent()
	}
}

// linesBelowCursor returns the number of real content lines below the cursor's
// line (ignoring the reachability padding) — how much content is below the
// selection, shown in the footer.
func (m *viewerModel) linesBelowCursor() int {
	if m.cur < 0 || m.cur >= len(m.offsets) {
		return 0
	}
	return max(0, m.contentLines-m.offsets[m.cur]-1)
}

// globalContext joins the entry identity and the non-empty header signal lines
// into the single sticky title line (awaiting-you count, open/done, version).
func (m *viewerModel) globalContext() string {
	parts := []string{}
	if m.title != "" {
		parts = append(parts, m.title)
	}
	for _, h := range m.header {
		if s := strings.TrimSpace(h); s != "" {
			parts = append(parts, s)
		}
	}
	line := strings.Join(parts, "  ·  ")
	if !m.color {
		line = ansiRe.ReplaceAllString(line, "") // the header is pre-rendered; strip color when off
	}
	return line
}

// ansiRe matches SGR color escape sequences, for stripping color from the
// pre-rendered header when the live color toggle is off.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// localContext describes what the cursor is on for the sticky status line.
func (m *viewerModel) localContext(threads []render.CommentThread) string {
	if m.cur < 0 || m.cur >= len(m.items) {
		return ""
	}
	it := m.items[m.cur]
	if it.kind == itemSection {
		return "▸ " + it.headingText
	}
	for ti, th := range threads {
		if th.RootID != it.rootID {
			continue
		}
		for ni, n := range th.Nodes {
			if n.ID == it.commentID {
				return fmt.Sprintf("▸ thread %d/%d · comment %d/%d (depth %d)", ti+1, len(threads), ni+1, len(th.Nodes), n.Depth)
			}
		}
	}
	return ""
}

func (m viewerModel) Init() tea.Cmd { return nil }

func (m viewerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.applyViewport()
		return m, nil
	case editorFinishedMsg:
		if msg.err != nil {
			m.notice = "editor failed: " + msg.err.Error()
			return m, nil
		}
		m.ta.SetValue(msg.body)
		return m, nil
	case tea.MouseMsg:
		// The program enables mouse capture, which costs the terminal its own
		// select-to-copy; forwarding the wheel is what pays for that.
		cmd := m.sv.PassKey(msg)
		m.syncCursorToScroll()
		return m, cmd
	case tea.KeyMsg:
		m.notice = ""
		if msg.String() == "ctrl+c" {
			return m.interrupt()
		}
		if m.mode == modeConfirmDelete {
			switch msg.String() {
			case "y", "Y":
				return m.confirmDelete()
			case "n", "N", "esc":
				m.mode = modeNone
				m.applyViewport()
			}
			return m, nil
		}
		if m.mode != modeNone {
			switch msg.String() {
			case "esc":
				m.mode = modeNone
				m.ta.Reset()
				m.applyViewport()
				return m, nil
			case "ctrl+s":
				return m.submitInput()
			case "ctrl+o":
				return m, m.openEditor()
			}
			var cmd tea.Cmd
			m.ta, cmd = m.ta.Update(msg)
			return m, cmd
		}
		if m.sv.HelpOpen() {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			m.sv.CloseHelp()
			return m, nil
		}
		if m.search.searching() {
			if msg.String() == "enter" && len(m.collapsed) > 0 {
				// Remember the reader's folds so esc can put them back. Expanding
				// is right — a fold must not hide a hit — but discarding a
				// hand-collapsed view with no way back is not.
				m.foldSnapshot = maps.Clone(m.collapsed)
				m.collapseAll(false) // expand every section so a fold never hides a hit
			}
			m.search.input(msg, m.searchMatcher, &m.sv)
			return m, nil
		}
		if !isDigit(msg.String()) && msg.String() != "g" {
			m.numBuf = ""
		}
		switch msg.String() {
		case "esc":
			// Layered dismiss. A modal and the help popup are handled above, so
			// by here the only thing left to step back from is a committed
			// search; with nothing to dismiss, esc quits — as it already did in
			// the pager, the list cockpit and the review viewer.
			if m.search.footer() != "" {
				m.search = pagerSearch{}
				if m.foldSnapshot != nil {
					m.collapsed, m.foldSnapshot = m.foldSnapshot, nil
				}
				m.applyViewport()
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+c", "q":
			return m, tea.Quit
		case "?":
			m.sv.ToggleHelp()
			return m, nil
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
			m.numBuf += msg.String()
			return m, nil
		case "g":
			if m.numBuf != "" {
				n, _ := strconv.Atoi(m.numBuf)
				m.numBuf = ""
				m.gotoBodyLine(n)
			} else {
				// Bare g goes to the top, and gg lands there too (the second g
				// is a no-op) — so the vim chord and the list cockpit's single
				// g both work, in every viewer.
				m.gotoItem(0)
			}
			return m, nil
		case "G", "end":
			m.gotoBottom()
			return m, nil
		case "up", "k":
			m.scrollLines(-1)
			return m, nil
		case "down", "j":
			m.scrollLines(1)
			return m, nil
		case "tab":
			m.moveCursor(1)
			return m, nil
		case "shift+tab":
			m.moveCursor(-1)
			return m, nil
		case "right", "l":
			m.cursorInto()
			return m, nil
		case "left", "h":
			m.cursorOut()
			return m, nil
		case " ":
			m.toggleFold()
			return m, nil
		case "r":
			if m.writer == nil {
				return m, nil
			}
			id := m.selectedCommentID()
			if id == "" {
				m.noComment("reply to")
				return m, nil
			}
			m.target = id
			m.mode = modeReply
			m.ta.Reset()
			m.ta.Focus()
			m.applyViewport()
			return m, nil
		case "e":
			if m.writer == nil {
				return m, nil
			}
			c := m.commentByID(m.selectedCommentID())
			if c == nil || c.Deleted {
				m.noComment("edit")
				return m, nil
			}
			m.target = c.ID
			m.mode = modeEdit
			m.ta.Reset()
			m.ta.SetValue(c.Body)
			m.ta.Focus()
			m.applyViewport()
			return m, nil
		case "d":
			if m.writer == nil {
				return m, nil
			}
			c := m.commentByID(m.selectedCommentID())
			if c == nil || c.Deleted {
				m.noComment("delete")
				return m, nil
			}
			m.target = c.ID
			m.mode = modeConfirmDelete
			m.applyViewport()
			return m, nil
		case "x":
			if m.writer == nil {
				return m, nil
			}
			root := m.selectedThreadRoot()
			if root == nil {
				m.noComment("resolve")
				return m, nil
			}
			if !root.Question {
				m.notice = "only a question can be resolved — this thread is a plain comment"
				return m, nil
			}
			if root.Resolved {
				if err := m.writer.UnresolveComment(m.entryID, root.ID); err != nil {
					m.notice = "reopen failed: " + err.Error()
					m.applyViewport()
					return m, nil
				}
				m.nodeCollapsed[root.ID] = false // show the reopened thread
				m.doReload("reopened")
				return m, nil
			}
			m.target = root.ID
			m.mode = modeResolveNote
			m.ta.Reset()
			m.ta.Focus()
			m.applyViewport()
			return m, nil
		case "ctrl+r":
			if m.reload != nil {
				m.doReload("refreshed")
			}
			return m, nil
		case "ctrl+@": // ^space — terminals send NUL for ctrl+space
			m.toggleFoldAll()
			return m, nil
		case "o", "c", "O", "C":
			// Retired fold keys, held inert rather than dropped: space folds the
			// section under the cursor and ^space folds everything, so a stale
			// finger must not fall through to the viewport (and `o` stays free
			// for the open-an-entry gesture the other cockpits give it).
			return m, nil
		case "t":
			m.color = !m.color
			m.applyViewport()
			return m, nil
		case "/":
			m.search.start()
			return m, nil
		case "n":
			m.search.cycle(1, &m.sv)
			return m, nil
		case "N":
			m.search.cycle(-1, &m.sv)
			return m, nil
		}
		// Everything else (pgup/pgdn, ctrl+d/u, home/end) scrolls the viewport by a
		// page or half-page; the cursor follows to the item at the new top.
		cmd := m.sv.PassKey(msg)
		m.syncCursorToScroll()
		return m, cmd
	}
	return m, nil
}

func (m viewerModel) View() string {
	if !m.sv.Ready() {
		return "\n  loading…"
	}
	total := max(len(m.items), 1)
	pos := m.sv.ScrollLabel()
	if b := m.linesBelowCursor(); b > 0 {
		pos = fmt.Sprintf("%s ↓%d", pos, b)
	}
	footer := fmt.Sprintf("%d/%d  ·  %s  ·  ? keys · q quit", m.cur+1, total, pos)
	if f := m.search.footer(); f != "" {
		footer = f + "  ·  " + footer // "/query" while typing, else "match i/N"
	}
	if m.notice != "" {
		footer = m.notice + "  ·  " + footer
	}
	if m.mode != modeNone {
		return m.sv.RenderOverlay(footer, m.inputBox())
	}
	return m.sv.Render(footer)
}

// inputBox renders the active input mode as a centered modal: a bordered box with
// a title, the textarea (or the delete confirm), and a key hint.
func (m *viewerModel) inputBox() string {
	if m.mode == modeConfirmDelete {
		// Name the target. The modal floats over the very comment it is asking
		// about, so without an excerpt the reader has nothing to check a
		// destructive action against.
		desc := "this comment"
		if c := m.commentByID(m.target); c != nil {
			// Name it the way the thread above does, so the prompt and the
			// content agree on who wrote what.
			desc = render.CommentAuthor(*c) + ": " +
				strings.TrimSpace(strings.SplitN(c.Body, "\n", 2)[0])
		}
		return modalStyle.Render(
			modalTitleStyle.Render("Delete this comment?") + "\n\n" +
				ansi.Truncate(desc, max(20, min(m.width-16, 60)), "…") + "\n\n" +
				modalHintStyle.Render("(y) delete    (n) cancel"))
	}
	var title, hint string
	switch m.mode {
	case modeReply:
		title, hint = "Reply", "ctrl+s send · ctrl+o editor · esc cancel"
	case modeEdit:
		title, hint = "Edit comment", "ctrl+s save · ctrl+o editor · esc cancel"
	case modeResolveNote:
		title, hint = "Resolve — optional closing note", "ctrl+s resolve · ctrl+o editor · esc cancel"
	}
	return modalStyle.Render(
		modalTitleStyle.Render(title) + "\n\n" + m.ta.View() + "\n\n" + modalHintStyle.Render(hint))
}

// sizeInput sizes the textarea to fit the modal (a fraction of the screen width).
func (m *viewerModel) sizeInput() {
	m.ta.SetWidth(max(20, min(m.width-12, 70)))
	m.ta.SetHeight(6)
}

// searchMatcher returns the offsets of gutter-free content lines containing q
// (case-insensitive) — so a numeric query matches body text, not the line-number
// gutter. A blank query matches nothing.
func (m *viewerModel) searchMatcher(q string) []int {
	needle := strings.ToLower(strings.TrimSpace(q))
	if needle == "" {
		return nil
	}
	var out []int
	for i, ln := range m.contentRaw {
		if strings.Contains(strings.ToLower(ln), needle) {
			out = append(out, i)
		}
	}
	return out
}

// selectedCommentID returns the comment id under the cursor, or "" when the
// cursor is on a section heading.
func (m *viewerModel) selectedCommentID() string {
	if m.cur >= 0 && m.cur < len(m.items) && m.items[m.cur].kind == itemComment {
		return m.items[m.cur].commentID
	}
	return ""
}

// noComment sets the footer notice for a write key pressed while the cursor is
// not on a comment. The help popup lists r/e/d/x unconditionally, so the keys
// have to explain themselves rather than look broken; verb names the action the
// reader was reaching for ("reply to", "edit", "delete", "resolve").
func (m *viewerModel) noComment(verb string) {
	if len(m.comments) == 0 {
		m.notice = "no comment selected — this entry has none yet; r replies once there is one"
		return
	}
	m.notice = "no comment selected — tab to a comment to " + verb + " it"
}

// editorFinishedMsg carries the result of the $EDITOR escape back into the event
// loop: the edited body, or the error from launching/reading the editor.
type editorFinishedMsg struct {
	body string
	err  error
}

// openEditor suspends the cockpit and opens $EDITOR on a temp file seeded with
// the current draft, for composing a long comment outside the small textarea
// (ctrl+o). The temp file lives under kref's user-owned cache tree (not the
// shared system temp dir) because a draft may carry private-tier text. On the
// editor's exit the edited body is fed back via editorFinishedMsg.
func (m *viewerModel) openEditor() tea.Cmd {
	f, err := os.CreateTemp(xdg.CacheTempDir(), "kref-comment-*.md")
	if err != nil {
		return func() tea.Msg { return editorFinishedMsg{err: err} }
	}
	path := f.Name()
	if _, err := f.WriteString(m.ta.Value()); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return func() tea.Msg { return editorFinishedMsg{err: err} }
	}
	_ = f.Close()
	ed := resolveEditor()
	c := exec.Command(ed[0], append(ed[1:], path)...)
	return tea.ExecProcess(c, func(runErr error) tea.Msg { return readEditorResult(path, runErr) })
}

// readEditorResult reads the edited temp file back, removing it, and packages the
// body (trailing newlines trimmed, matching submitInput) or an error as an
// editorFinishedMsg. The editor's own run error takes precedence.
func readEditorResult(path string, runErr error) editorFinishedMsg {
	defer func() { _ = os.Remove(path) }()
	if runErr != nil {
		return editorFinishedMsg{err: runErr}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return editorFinishedMsg{err: err}
	}
	return editorFinishedMsg{body: strings.TrimRight(string(raw), "\n")}
}

// submitInput performs the pending write for a textarea mode (reply/edit/resolve
// note), then reloads. On error it keeps the mode/draft and shows a failure
// notice. Empty reply/edit is discarded; an empty resolve note just resolves.
func (m viewerModel) submitInput() (tea.Model, tea.Cmd) {
	body := strings.TrimRight(m.ta.Value(), "\n")
	switch m.mode {
	case modeReply:
		if strings.TrimSpace(body) == "" {
			m.notice = "empty reply — nothing sent"
			m.mode = modeNone
			m.ta.Reset()
			m.applyViewport()
			return m, nil
		}
		if _, err := m.writer.AddComment(m.entryID, m.actor, m.actorKind, body, false, m.target); err != nil {
			m.notice = "write failed: " + err.Error()
			return m, nil
		}
		m.mode = modeNone
		m.ta.Reset()
		m.doReload("replied")
		return m, nil
	case modeEdit:
		if strings.TrimSpace(body) == "" {
			m.notice = "empty — edit discarded"
			m.mode = modeNone
			m.ta.Reset()
			m.applyViewport()
			return m, nil
		}
		if err := m.writer.EditComment(m.entryID, m.target, body); err != nil {
			m.notice = "write failed: " + err.Error()
			return m, nil
		}
		m.mode = modeNone
		m.ta.Reset()
		m.doReload("edited")
		return m, nil
	case modeResolveNote:
		if strings.TrimSpace(body) != "" {
			if _, err := m.writer.AddComment(m.entryID, m.actor, m.actorKind, body, false, m.target); err != nil {
				m.notice = "write failed: " + err.Error()
				return m, nil
			}
		}
		if err := m.writer.ResolveComment(m.entryID, m.target); err != nil {
			m.notice = "write failed: " + err.Error()
			return m, nil
		}
		m.mode = modeNone
		m.ta.Reset()
		m.doReload("resolved")
		return m, nil
	default:
		m.mode = modeNone
		m.applyViewport()
		return m, nil
	}
}

// interrupt handles ctrl+c from any layer. ctrl+c is the terminal's universal
// abort and has to work everywhere — but a modal may be holding text the author
// has not sent, and losing that is the outcome AGENTS.md rules out. A non-empty
// draft goes to the same recovery tree a lint-rejected todo body uses, and
// RunViewer prints the path on the way out. The delete confirm holds no draft.
func (m viewerModel) interrupt() (tea.Model, tea.Cmd) {
	if m.mode != modeNone && m.mode != modeConfirmDelete {
		if draft := strings.TrimRight(m.ta.Value(), "\n"); strings.TrimSpace(draft) != "" {
			if path, err := todoguard.WriteRejected("draft-"+render.ShortID(m.entryID), draft); err == nil {
				m.spilled = path
			}
		}
	}
	return m, tea.Quit
}

// confirmDelete tombstones the comment named by m.target (invoked on 'y' in the
// delete-confirm prompt), then reloads.
func (m viewerModel) confirmDelete() (tea.Model, tea.Cmd) {
	if err := m.writer.DeleteComment(m.entryID, m.target); err != nil {
		m.notice = "write failed: " + err.Error()
		m.mode = modeNone
		m.applyViewport()
		return m, nil
	}
	m.mode = modeNone
	m.doReload("deleted")
	return m, nil
}

// commentByID returns a pointer to the comment with the given id, or nil.
func (m *viewerModel) commentByID(id string) *entry.Comment {
	if id == "" {
		return nil
	}
	for i := range m.comments {
		if m.comments[i].ID == id {
			return &m.comments[i]
		}
	}
	return nil
}

// selectedThreadRoot returns the root comment of the thread the cursor is in, or
// nil when the cursor is not on a comment.
func (m *viewerModel) selectedThreadRoot() *entry.Comment {
	if m.cur < 0 || m.cur >= len(m.items) || m.items[m.cur].kind != itemComment {
		return nil
	}
	return m.commentByID(m.items[m.cur].rootID)
}

// doReload re-fetches header+comments and re-renders, setting a footer notice.
func (m *viewerModel) doReload(note string) {
	if m.reload == nil {
		return
	}
	header, comments, err := m.reload()
	if err != nil {
		m.notice = "refresh failed: " + err.Error()
		m.applyViewport()
		return
	}
	m.header, m.comments = header, comments
	m.notice = note
	m.applyViewport()
}

// applyViewport resizes the ScrollView to leave room for the input (when active),
// sizes the textarea to the width, and re-renders the content.
// applyViewport sizes the textarea and (re)renders. The input modal floats over
// the viewport (RenderOverlay), so the viewport keeps its full height.
func (m *viewerModel) applyViewport() {
	m.sizeInput()
	m.sv.Resize(m.width, m.height)
	m.renderContent()
	m.ensureVisible()
}

// moveCursor moves the single selection cursor by d items and keeps it visible.
func (m *viewerModel) moveCursor(d int) {
	if len(m.items) == 0 {
		return
	}
	m.cur = clampInt(m.cur+d, 0, len(m.items)-1)
	m.renderContent()
	m.ensureVisible()
}

// gotoItem jumps the cursor to item i (clamped) — the gg/G top/bottom shortcuts.
func (m *viewerModel) gotoItem(i int) {
	if len(m.items) == 0 {
		return
	}
	m.cur = clampInt(i, 0, len(m.items)-1)
	m.renderContent()
	m.ensureVisible()
}

// gotoBottom scrolls to the end of the real content — never into the
// reachability padding — and lets the cursor follow, so G means "end of the
// document" here exactly as it does in the pager and the list cockpit. Jumping
// to the last cursor item instead left everything below the final heading
// unreachable in one key.
func (m *viewerModel) gotoBottom() {
	m.sv.SetYOffset(max(0, m.contentLines-m.sv.Height()))
	m.syncCursorToScroll()
}

// gotoBodyLine scrolls so rendered body line n (1-based) is at the viewport top,
// clamped to the body range; the cursor follows to the item at the new top.
func (m *viewerModel) gotoBodyLine(n int) {
	if m.bodyLineCount == 0 {
		return
	}
	n = clampInt(n, 1, m.bodyLineCount)
	maxOff := max(0, m.contentLines-m.sv.Height())
	m.sv.SetYOffset(clampInt(m.bodyStartLine+n-1, 0, maxOff))
	m.syncCursorToScroll()
}

// toggleFold folds/unfolds under the cursor. On a comment it hides just that
// node's replies (so a deep sub-thread can be folded from any node); the cursor
// stays on the node, which remains visible. On a section it folds the section
// and re-homes the cursor on the heading.
func (m *viewerModel) toggleFold() {
	if m.cur < 0 || m.cur >= len(m.items) {
		return
	}
	it := m.items[m.cur]
	switch it.kind {
	case itemComment:
		m.nodeCollapsed[it.commentID] = !m.nodeCollapsed[it.commentID]
		m.renderContent()
		m.ensureVisible()
	case itemSection:
		m.setFold(it.heading, !m.collapsed[it.heading])
	}
}

// toggleFoldAll is ^space: unfold every section when anything is folded, and
// fold every section otherwise. Biasing the mixed state towards "show me
// everything" makes the key a reliable way out of a view the reader has lost
// track of, whoever folded it — the initial fold, a search, or their own space.
func (m *viewerModel) toggleFoldAll() {
	for _, folded := range m.collapsed {
		if folded {
			m.collapseAll(false)
			return
		}
	}
	m.collapseAll(true)
}

// setFold sets one section's fold state by Path, re-renders, and re-homes the
// cursor on that heading (folding changes which items exist).
func (m *viewerModel) setFold(path string, collapsed bool) {
	m.collapsed[path] = collapsed
	m.renderContent()
	m.cursorTo(func(c cursorItem) bool { return c.kind == itemSection && c.heading == path })
	m.renderContent()
	m.ensureVisible()
}

// collapseAll folds (collapsed=true) or unfolds every body section. Unfold clears
// the fold set; fold marks every outline heading Path. The cursor is clamped since
// folding an ancestor removes its nested-heading items.
func (m *viewerModel) collapseAll(collapsed bool) {
	if collapsed {
		for _, p := range outline.Parse(m.body).AllPaths() {
			m.collapsed[p] = true
		}
	} else {
		m.collapsed = map[string]bool{}
	}
	m.renderContent()
	m.cur = clampInt(m.cur, 0, max(len(m.items)-1, 0))
	m.renderContent()
	m.ensureVisible()
}

// cursorInto moves the cursor into the selected comment's first reply (one depth
// deeper in the same thread), if it has one — the "→ jumps in" gesture.
func (m *viewerModel) cursorInto() {
	if m.cur < 0 || m.cur >= len(m.items) {
		return
	}
	it := m.items[m.cur]
	if it.kind != itemComment {
		return
	}
	if next := m.cur + 1; next < len(m.items) {
		if n := m.items[next]; n.kind == itemComment && n.rootID == it.rootID && n.depth == it.depth+1 {
			m.cur = next
			m.renderContent()
			m.ensureVisible()
		}
	}
}

// cursorOut moves the cursor out to the selected comment's parent (one depth
// shallower in the same thread), if it has one — the "← jumps out" gesture.
func (m *viewerModel) cursorOut() {
	if m.cur < 0 || m.cur >= len(m.items) {
		return
	}
	it := m.items[m.cur]
	if it.kind != itemComment || it.depth == 0 {
		return
	}
	for i := m.cur - 1; i >= 0; i-- {
		if p := m.items[i]; p.kind == itemComment && p.rootID == it.rootID && p.depth == it.depth-1 {
			m.cur = i
			m.renderContent()
			m.ensureVisible()
			return
		}
	}
}

// cursorTo places the cursor on the first item matching pred, clamping if none.
func (m *viewerModel) cursorTo(pred func(cursorItem) bool) {
	for i, it := range m.items {
		if pred(it) {
			m.cur = i
			return
		}
	}
	m.cur = clampInt(m.cur, 0, max(len(m.items)-1, 0))
}

// ensureVisible scrolls the viewport so the cursor's line is on screen.
func (m *viewerModel) ensureVisible() {
	if m.cur < 0 || m.cur >= len(m.offsets) {
		return
	}
	off := m.offsets[m.cur]
	top := m.sv.YOffset()
	if off < top {
		m.sv.SetYOffset(off)
		return
	}
	if h := m.sv.Height(); h > 0 && off >= top+h {
		m.sv.SetYOffset(off - h + 1)
	}
}

// isDigit reports whether s is a single ASCII digit "0".."9".
func isDigit(s string) bool { return len(s) == 1 && s[0] >= '0' && s[0] <= '9' }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// resolvedQuestionRoots returns the set of root comment ids that are resolved
// questions — the threads the cockpit collapses by default.
func resolvedQuestionRoots(comments []entry.Comment) map[string]bool {
	present := make(map[string]bool, len(comments))
	for _, c := range comments {
		present[c.ID] = true
	}
	out := map[string]bool{}
	for _, c := range comments {
		isRoot := c.ReplyTo == "" || !present[c.ReplyTo]
		if isRoot && c.Question && c.Resolved {
			out[c.ID] = true
		}
	}
	return out
}

// echoLines is the last frame to leave in scrollback on exit, with the cursor
// marker stripped: it is a live-selection affordance, not part of the text, and
// text copied out of the terminal should not carry it.
func (m viewerModel) echoLines() []string {
	win := m.sv.VisibleWindow()
	out := make([]string, len(win))
	for i, ln := range win {
		out[i] = strings.Replace(ln, cursorMarker+" ", "  ", 1)
	}
	return out
}

// RunViewer runs the interactive entry viewer and echoes the last frame to
// stdout on exit (so it stays in scrollback), mirroring the show pager's Page.
func RunViewer(in viewerInput) error {
	m := newViewerModel(in)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(os.Stdout))
	final, err := p.Run()
	if err != nil {
		return err
	}
	if fm, ok := final.(viewerModel); ok {
		if win := fm.echoLines(); len(win) > 0 {
			fmt.Fprintln(os.Stdout, strings.Join(win, "\n"))
		}
		if fm.spilled != "" {
			fmt.Fprintf(os.Stderr, "interrupted: your unsent draft was saved to %s\n", fm.spilled)
		}
	}
	return nil
}
