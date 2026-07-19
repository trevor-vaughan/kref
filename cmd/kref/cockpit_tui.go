package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/git-bug/git-bug/entity"
	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/outline"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/todo"
	"github.com/trevor-vaughan/kref/internal/tui"
	"github.com/trevor-vaughan/kref/internal/xdg"
)

type cockpitInput struct {
	title     string
	header    []string
	body      string
	color     bool
	width     int
	collapsed map[string]bool
	comments  []entry.Comment
	writer    commentWriter
	entryID   entity.Id
	actorKind string
	reload    func() (header []string, comments []entry.Comment, err error)
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
	AddComment(id entity.Id, actorKind, body string, question bool, replyTo string) (string, error)
	ResolveComment(id entity.Id, target string) error
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

type cockpitModel struct {
	sv            tui.ScrollView
	title         string // entry identity ("todo · <id>") — prefixes the global header
	header        []string
	body          string
	color         bool
	width         int
	sections      []todo.Section
	collapsed     map[string]bool // section fold state, by heading
	comments      []entry.Comment
	nodeCollapsed map[string]bool // fold state by comment-node id (hides that node's replies)
	offsets       []int           // rendered-line offset of each item, aligned with items
	items         []cursorItem    // the flat selectable list (comments + sections)
	cur           int             // index of the selection cursor within items
	contentLines  int             // real content height (excludes scroll padding)
	gPending      bool            // first 'g' of a gg (go-to-top) chord seen
	writer        commentWriter
	entryID       entity.Id
	actorKind     string
	reload        func() ([]string, []entry.Comment, error)
	height        int
	mode          inputMode
	ta            textarea.Model
	target        string
	notice        string
	search        pagerSearch // shared incremental search (/, n/N)
}

func newCockpitModel(in cockpitInput) cockpitModel {
	sv := tui.NewScrollView(in.title)
	sv.SetHelpRows([]string{
		"↑/↓ or j/k  scroll one line",
		"tab/S-tab   move the cursor to next/prev item",
		"→/← or l/h  cursor into a reply / out to the parent",
		"gg/G        cursor to first / last item",
		"space       fold replies under the cursor (or a section)",
		"o/c  O/C    open/close the current section · all sections",
		"/  n/N      search · next/prev match",
		"r/e/d       reply · edit · delete the comment",
		"x           resolve the thread's question",
		"ctrl+r      refresh   ·   t  toggle colour",
		"pgup/pgdn   scroll a page   ·   ^d/^u  half",
		"?           toggle this help",
		"q           quit",
	})
	col := in.collapsed
	if col == nil {
		col = map[string]bool{}
	}
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	return cockpitModel{
		sv:            sv,
		title:         in.title,
		header:        in.header,
		body:          in.body,
		color:         in.color,
		width:         in.width,
		sections:      todo.Sections(in.body),
		collapsed:     col,
		comments:      in.comments,
		nodeCollapsed: resolvedQuestionRoots(in.comments),
		writer:        in.writer,
		entryID:       in.entryID,
		actorKind:     in.actorKind,
		reload:        in.reload,
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

// renderContent rebuilds the viewport content — the discussion zone (comment
// threads) above the body sections — and the parallel item/offset lists the
// single cursor navigates. Every content line carries a 2-column gutter so the
// cursor marker never shifts indentation.
//
// Padding: (viewportHeight - 1) blank lines are appended so every item offset is
// reachable by SetYOffset even when the content is shorter than the viewport.
func (m *cockpitModel) renderContent() {
	var lines []string
	m.offsets = m.offsets[:0]
	m.items = m.items[:0]

	// Discussion zone: one cursor item per comment node; a blank line separates
	// adjacent threads.
	threads := render.RenderCommentThreads(m.comments, m.color, m.nodeCollapsed, m.width-2)
	for ti, t := range threads {
		if ti > 0 {
			lines = append(lines, "")
		}
		for _, n := range t.Nodes {
			m.offsets = append(m.offsets, len(lines))
			m.items = append(m.items, cursorItem{kind: itemComment, commentID: n.ID, rootID: t.RootID, depth: n.Depth})
			isCursor := len(m.items)-1 == m.cur
			for li, ln := range n.Lines {
				lines = append(lines, gutterFor(isCursor && li == 0)+ln)
			}
		}
	}
	if len(threads) > 0 {
		lines = append(lines, "") // separate the zone from the body
	}

	// Body zone: one cursor item per heading of any level. The outline collapses
	// folded sections to a "▸ N lines" hint (keyed by heading Path); re-parsing the
	// folded body recovers the surviving headings and their positions, which split
	// it into per-heading blocks so each heading gets a stable cursor offset.
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
			// Glamour-render the markdown (styled headings/bold/lists).
			var rb bytes.Buffer
			render.RenderBody(&rb, src, "text/markdown", true, m.width)
			rendered = strings.Split(strings.TrimRight(rb.String(), "\n"), "\n")
			// RenderBody prepends blank line(s) as markdown top-margin; drop them for
			// a section so the heading is the first line and the cursor marker +
			// offset land on the heading, not one line above it.
			if isSection {
				for len(rendered) > 1 && strings.TrimSpace(rendered[0]) == "" {
					rendered = rendered[1:]
				}
			}
		} else {
			// Colour off: show the raw markdown source (like `show --plain`) rather
			// than flattening its structure through the styled renderer.
			rendered = strings.Split(strings.TrimRight(src, "\n"), "\n")
		}

		isCursor := false
		if isSection {
			m.offsets = append(m.offsets, len(lines))
			m.items = append(m.items, cursorItem{
				kind: itemSection, heading: span.heading.Path,
				level: span.heading.Level, headingText: span.heading.Text,
			})
			isCursor = len(m.items)-1 == m.cur
		}
		for li, ln := range rendered {
			lines = append(lines, gutterFor(isCursor && li == 0)+ln)
		}
	}

	// Sticky two-line header: global signal on the title row, local focus on the
	// status row — both stay put while the zone/body scroll. Colour off also kills
	// the chrome colour so the whole view is plain, not just the content.
	m.sv.SetTitle(m.globalContext())
	m.sv.SetStatus(m.localContext(threads))
	m.sv.SetPlain(!m.color)

	m.contentLines = len(lines) // real content height, before the scroll padding

	if h := m.sv.Height(); h > 1 {
		for range h - 1 {
			lines = append(lines, "")
		}
	}
	m.sv.SetContent(strings.Join(lines, "\n"))
	m.sv.SetContentHeight(m.contentLines) // scroll marker ignores the reachability padding
}

// scrollLines scrolls the viewport by dy lines for reading, leaving the cursor
// (the reply/edit/fold target) where it is — clamped to the real content so it
// never scrolls into the reachability padding.
func (m *cockpitModel) scrollLines(dy int) {
	maxOff := max(0, m.contentLines-m.sv.Height())
	m.sv.SetYOffset(clampInt(m.sv.YOffset()+dy, 0, maxOff))
}

// linesBelowCursor returns the number of real content lines below the cursor's
// line (ignoring the reachability padding) — how much content is below the
// selection, shown in the footer.
func (m *cockpitModel) linesBelowCursor() int {
	if m.cur < 0 || m.cur >= len(m.offsets) {
		return 0
	}
	return max(0, m.contentLines-m.offsets[m.cur]-1)
}

// globalContext joins the entry identity and the non-empty header signal lines
// into the single sticky title line (awaiting-you count, open/done, version).
func (m *cockpitModel) globalContext() string {
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
func (m *cockpitModel) localContext(threads []render.CommentThread) string {
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

func (m cockpitModel) Init() tea.Cmd { return nil }

func (m cockpitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	case tea.KeyMsg:
		m.notice = ""
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
				m.collapseAll(false) // expand every section so a fold never hides a hit
			}
			m.search.input(msg, m.searchMatcher, &m.sv)
			return m, nil
		}
		wasG := m.gPending
		m.gPending = false
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "?":
			m.sv.ToggleHelp()
			return m, nil
		case "g":
			if wasG {
				m.gotoItem(0) // gg → top
			} else {
				m.gPending = true
			}
			return m, nil
		case "G", "end":
			m.gotoItem(len(m.items) - 1) // G → bottom
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
			if root == nil || !root.Question || root.Resolved {
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
		case "o", "c":
			if p, ok := m.currentSectionPath(); ok {
				m.setFold(p, msg.String() == "c")
			}
			return m, nil
		case "O":
			m.collapseAll(false)
			return m, nil
		case "C":
			m.collapseAll(true)
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
		// page or half-page; the cursor stays where it is.
		return m, m.sv.PassKey(msg)
	}
	return m, nil
}

func (m cockpitModel) View() string {
	if !m.sv.Ready() {
		return "\n  loading…"
	}
	total := max(len(m.items), 1)
	pos := m.sv.ScrollLabel()
	if b := m.linesBelowCursor(); b > 0 {
		pos = fmt.Sprintf("%s ↓%d", pos, b)
	}
	footer := fmt.Sprintf("%d/%d  ·  %s  ·  ? help · q quit", m.cur+1, total, pos)
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
func (m *cockpitModel) inputBox() string {
	if m.mode == modeConfirmDelete {
		return modalStyle.Render(
			modalTitleStyle.Render("Delete this comment?") + "\n\n" +
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
func (m *cockpitModel) sizeInput() {
	m.ta.SetWidth(max(20, min(m.width-12, 70)))
	m.ta.SetHeight(6)
}

// searchMatcher returns the offsets of viewport content lines containing q — the
// cockpit's match source (its ScrollView content, gutter and all).
func (m *cockpitModel) searchMatcher(q string) []int { return m.sv.Matches(q) }

// selectedCommentID returns the comment id under the cursor, or "" when the
// cursor is on a section heading.
func (m *cockpitModel) selectedCommentID() string {
	if m.cur >= 0 && m.cur < len(m.items) && m.items[m.cur].kind == itemComment {
		return m.items[m.cur].commentID
	}
	return ""
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
func (m *cockpitModel) openEditor() tea.Cmd {
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
func (m cockpitModel) submitInput() (tea.Model, tea.Cmd) {
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
		if _, err := m.writer.AddComment(m.entryID, m.actorKind, body, false, m.target); err != nil {
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
			if _, err := m.writer.AddComment(m.entryID, m.actorKind, body, false, m.target); err != nil {
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

// confirmDelete tombstones the comment named by m.target (invoked on 'y' in the
// delete-confirm prompt), then reloads.
func (m cockpitModel) confirmDelete() (tea.Model, tea.Cmd) {
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
func (m *cockpitModel) commentByID(id string) *entry.Comment {
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
func (m *cockpitModel) selectedThreadRoot() *entry.Comment {
	if m.cur < 0 || m.cur >= len(m.items) || m.items[m.cur].kind != itemComment {
		return nil
	}
	return m.commentByID(m.items[m.cur].rootID)
}

// doReload re-fetches header+comments and re-renders, setting a footer notice.
func (m *cockpitModel) doReload(note string) {
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
func (m *cockpitModel) applyViewport() {
	m.sizeInput()
	m.sv.Resize(m.width, m.height)
	m.renderContent()
	m.ensureVisible()
}

// moveCursor moves the single selection cursor by d items and keeps it visible.
func (m *cockpitModel) moveCursor(d int) {
	if len(m.items) == 0 {
		return
	}
	m.cur = clampInt(m.cur+d, 0, len(m.items)-1)
	m.renderContent()
	m.ensureVisible()
}

// gotoItem jumps the cursor to item i (clamped) — the gg/G top/bottom shortcuts.
func (m *cockpitModel) gotoItem(i int) {
	if len(m.items) == 0 {
		return
	}
	m.cur = clampInt(i, 0, len(m.items)-1)
	m.renderContent()
	m.ensureVisible()
}

// toggleFold folds/unfolds under the cursor. On a comment it hides just that
// node's replies (so a deep sub-thread can be folded from any node); the cursor
// stays on the node, which remains visible. On a section it folds the section
// and re-homes the cursor on the heading.
func (m *cockpitModel) toggleFold() {
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

// currentSectionPath returns the outline Path of the section under the cursor, or
// ("", false) when the cursor is on a comment (o/c are section-only).
func (m *cockpitModel) currentSectionPath() (string, bool) {
	if m.cur >= 0 && m.cur < len(m.items) && m.items[m.cur].kind == itemSection {
		return m.items[m.cur].heading, true
	}
	return "", false
}

// setFold sets one section's fold state by Path, re-renders, and re-homes the
// cursor on that heading (folding changes which items exist).
func (m *cockpitModel) setFold(path string, collapsed bool) {
	m.collapsed[path] = collapsed
	m.renderContent()
	m.cursorTo(func(c cursorItem) bool { return c.kind == itemSection && c.heading == path })
	m.renderContent()
	m.ensureVisible()
}

// collapseAll folds (collapsed=true) or unfolds every body section. Unfold clears
// the fold set; fold marks every outline heading Path. The cursor is clamped since
// folding an ancestor removes its nested-heading items.
func (m *cockpitModel) collapseAll(collapsed bool) {
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
func (m *cockpitModel) cursorInto() {
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
func (m *cockpitModel) cursorOut() {
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
func (m *cockpitModel) cursorTo(pred func(cursorItem) bool) {
	for i, it := range m.items {
		if pred(it) {
			m.cur = i
			return
		}
	}
	m.cur = clampInt(m.cur, 0, max(len(m.items)-1, 0))
}

// ensureVisible scrolls the viewport so the cursor's line is on screen.
func (m *cockpitModel) ensureVisible() {
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

// cockpitInputFor assembles the interactive-cockpit input. The Done section
// starts collapsed unless full is set, matching --full flag behaviour.
func cockpitInputFor(title string, header []string, body string, color bool, width int, full bool, comments []entry.Comment) cockpitInput {
	collapsed := map[string]bool{}
	if !full {
		for _, h := range outline.Parse(body).Headings() {
			if h.Text == "Done (compact)" {
				collapsed[h.Path] = true
			}
		}
	}
	return cockpitInput{title: title, header: header, body: body, color: color, width: width, collapsed: collapsed, comments: comments}
}

// RunCockpit runs the interactive todo cockpit and echoes the last frame to
// stdout on exit (so it stays in scrollback), mirroring the show pager's Page.
func RunCockpit(in cockpitInput) error {
	m := newCockpitModel(in)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(os.Stdout))
	final, err := p.Run()
	if err != nil {
		return err
	}
	if fm, ok := final.(cockpitModel); ok {
		if win := fm.sv.VisibleWindow(); len(win) > 0 {
			fmt.Fprintln(os.Stdout, strings.Join(win, "\n"))
		}
	}
	return nil
}
