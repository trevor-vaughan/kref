// Package render produces the human-readable presentation of kref entries.
// Every function writes to an io.Writer and takes an explicit color flag, so it
// stays unit-testable with a bytes.Buffer. Body rendering uses glamour (markdown)
// and chroma (syntax highlighting); TTY detection, the --json decision, and the
// interactive pager live in cmd/kref, not here.
package render

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"github.com/alecthomas/chroma/v2/quick"
	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/content"
	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/textdiff"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
)

// ShortID is the canonical 12-character entry id used by the listing and the
// action confirmations. The detail view (Show) keeps the full id.
func ShortID(id entity.Id) string {
	r := []rune(id.String())
	if len(r) > 12 {
		return string(r[:12])
	}
	return string(r)
}

// tierGlyph keys off the tier's TYPE (private|personal|shared); custom tiers
// borrow their type's glyph.
func tierGlyph(typ string) string {
	switch typ {
	case string(entry.TierPrivate):
		return "●"
	case string(entry.TierPersonal):
		return "◐"
	case string(entry.TierShared):
		return "○"
	default:
		return "•"
	}
}

// tierColor keys off the tier's TYPE (private|personal|shared); custom tiers
// borrow their type's color.
func tierColor(typ string) string {
	switch typ {
	case string(entry.TierPrivate):
		return ansiRed
	case string(entry.TierPersonal):
		return ansiYellow
	case string(entry.TierShared):
		return ansiGreen
	default:
		return ""
	}
}

func tierPlain(tier, typ string) string { return tierGlyph(typ) + " " + tier }

// Tier renders a glyph-prefixed tier badge ("● private"). The glyph and color
// follow the tier's TYPE; the word is the tier's name. The glyph prints
// regardless of color so the visibility signal survives NO_COLOR and piping.
func Tier(tier, typ string, color bool) string {
	s := tierPlain(tier, typ)
	if color {
		if c := tierColor(typ); c != "" {
			return c + s + ansiReset
		}
	}
	return s
}

// pad right-pads s with spaces to w columns, counting runes (the tier glyphs
// are multi-byte, so byte-based padding would misalign).
func pad(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return s + spaces(w-n)
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// tierRank keys off the tier's TYPE (private|personal|shared); custom tiers
// rank with their type group.
func tierRank(typ string) int {
	switch typ {
	case string(entry.TierPrivate):
		return 0
	case string(entry.TierPersonal):
		return 1
	case string(entry.TierShared):
		return 2
	default:
		return 3
	}
}

// tierLess orders snapshots by tier: type rank first (private < personal-typed
// < shared-typed), the built-in leading its type group, then name.
func tierLess(a, b *entry.Snapshot) bool {
	if ra, rb := tierRank(a.TierType), tierRank(b.TierType); ra != rb {
		return ra < rb
	}
	if ab, bb := a.Tier == a.TierType, b.Tier == b.TierType; ab != bb {
		return ab // builtin (name==type) leads its type group
	}
	return a.Tier < b.Tier
}

type listRow struct {
	snap  *entry.Snapshot
	count int
}

// Column is a selectable list column.
type Column string

const (
	ColTier    Column = "tier"
	ColID      Column = "id"
	ColFullID  Column = "fullid"
	ColKind    Column = "kind"
	ColStatus  Column = "status"
	ColTitle   Column = "title"
	ColAuthor  Column = "author"
	ColEmail   Column = "email"
	ColCreated Column = "created"
	ColUpdated Column = "updated"
	ColEdited  Column = "edited"
	ColLabels  Column = "labels"
	ColTracked Column = "tracked"
	ColPath    Column = "path"
	ColSource  Column = "source"
)

// AllColumns is the canonical, ordered registry of every column — the single
// source of truth checked by the registry-consistency specs.
var AllColumns = []Column{
	ColTier, ColID, ColFullID, ColKind, ColStatus, ColTitle,
	ColAuthor, ColEmail, ColCreated, ColUpdated, ColEdited, ColLabels, ColTracked, ColPath, ColSource,
}

// DefaultColumns reproduces the existing 5-column table layout exactly.
var DefaultColumns = []Column{ColTier, ColID, ColKind, ColStatus, ColTitle}

// WideColumns adds author and edited to the default set.
var WideColumns = []Column{ColTier, ColID, ColKind, ColStatus, ColAuthor, ColEdited, ColTitle}

var columnHeaders = map[Column]string{
	ColTier: "TIER", ColID: "ID", ColFullID: "ID", ColKind: "KIND", ColStatus: "STATUS",
	ColTitle: "TITLE", ColAuthor: "AUTHOR", ColEmail: "EMAIL", ColCreated: "CREATED",
	ColUpdated: "UPDATED", ColEdited: "EDITED", ColLabels: "LABELS", ColTracked: "TRACKED", ColPath: "PATH",
	ColSource: "SOURCE",
}

// HeaderFor returns the column header label (exported for the consistency specs).
func HeaderFor(c Column) string { return columnHeaders[c] }

// columnDescriptions documents each column for `kref list --columns` (bare). Its
// coverage of AllColumns is enforced by the ColumnHelp spec, so a new column
// cannot ship without a description.
var columnDescriptions = map[Column]string{
	ColTier:    "visibility tier (● private / ◐ personal / ○ shared)",
	ColID:      "12-character short id",
	ColFullID:  "full 64-character id",
	ColKind:    "entry kind (spec, plan, adr, memory, reference, document)",
	ColStatus:  "lifecycle status (open, active, accepted, superseded, obsolete)",
	ColTitle:   "title",
	ColAuthor:  "author display name",
	ColEmail:   "author email",
	ColCreated: "creation date (YYYY-MM-DD)",
	ColUpdated: "last-updated date (YYYY-MM-DD)",
	ColEdited:  "last body-edit date (YYYY-MM-DD)",
	ColLabels:  "comma-separated labels",
	ColTracked: "yes/no — kept in sync with a local file via `kref track`",
	ColPath:    "tracked file path (set by `kref track`; empty for one-shot ingests)",
	ColSource:  "origin source path from provenance (where it was ingested/created from)",
}

// ColumnHelp renders the available columns and their descriptions, for the bare
// `kref list --columns` discovery form.
func ColumnHelp() string {
	width := 0
	for _, c := range AllColumns {
		if n := len(string(c)); n > width {
			width = n
		}
	}
	var b strings.Builder
	b.WriteString("Available columns (select with `kref list --columns=a,b,c`, or use `--wide`):\n")
	for _, c := range AllColumns {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, string(c), columnDescriptions[c])
	}
	return b.String()
}

// ColumnDescription returns a column's one-line description (empty for an
// unknown column). Shell completion uses it as the candidate description.
func ColumnDescription(c Column) string { return columnDescriptions[c] }

func validColumns() string {
	names := make([]string, len(AllColumns))
	for i, c := range AllColumns {
		names[i] = string(c)
	}
	return strings.Join(names, " ")
}

// ParseColumns turns "a,b,c" into ordered columns, erroring on unknown tokens.
func ParseColumns(s string) ([]Column, error) {
	parts := strings.Split(s, ",")
	cols := make([]Column, 0, len(parts))
	for _, p := range parts {
		c := Column(strings.TrimSpace(p))
		if _, ok := columnHeaders[c]; !ok {
			return nil, fmt.Errorf("unknown column %q (valid: %s)", p, validColumns())
		}
		cols = append(cols, c)
	}
	return cols, nil
}

// ListOptions configures RenderList. Columns defaults to DefaultColumns when nil.
type ListOptions struct {
	Columns   []Column
	Plain     bool
	Color     bool
	ShowAll   bool
	Sort      *SortSpec       // nil = the default order (table: tier→kind→title; plain: store order)
	Favorites map[string]bool // favorited entry ids (id string → true); these pin above every other row
}

// SortSpec is a parsed --sort value: the field to order by and a direction.
type SortSpec struct {
	Key  Column
	Desc bool
}

// sortableColumns are the fields --sort accepts. Composite/derived columns
// (labels, tracked, path, source) are excluded: they have no total order a
// user would predict.
var sortableColumns = []Column{ColTier, ColID, ColKind, ColStatus, ColTitle, ColAuthor, ColCreated, ColUpdated, ColEdited}

// SortKeys returns the accepted --sort keys in display order (for completion
// and error text).
func SortKeys() []string {
	out := make([]string, len(sortableColumns))
	for i, c := range sortableColumns {
		out[i] = string(c)
	}
	return out
}

// sortDefaultsDesc reports whether a bare sort key defaults to descending.
// The date fields do: a recency sort wants the newest at the top.
func sortDefaultsDesc(key Column) bool {
	return key == ColCreated || key == ColUpdated || key == ColEdited
}

// SortBareDesc reports whether a bare --sort key (no :direction suffix)
// defaults to descending — completion uses it to offer the non-default suffix.
func SortBareDesc(key string) bool { return sortDefaultsDesc(Column(key)) }

// ParseSort parses a --sort value: "key" or "key:asc"/"key:desc". A bare key
// sorts ascending, except the date fields (created, updated), which default
// to descending so the newest entries land at the top.
func ParseSort(s string) (*SortSpec, error) {
	key, dir, hasDir := strings.Cut(strings.TrimSpace(s), ":")
	spec := &SortSpec{Key: Column(key)}
	valid := slices.Contains(sortableColumns, spec.Key)
	if !valid {
		return nil, fmt.Errorf("unknown sort key %q (valid: %s; append :desc to reverse)", key, strings.Join(SortKeys(), " "))
	}
	switch {
	case !hasDir:
		spec.Desc = sortDefaultsDesc(spec.Key)
	case dir == "asc":
	case dir == "desc":
		spec.Desc = true
	default:
		return nil, fmt.Errorf("unknown sort direction %q (want asc or desc)", dir)
	}
	return spec, nil
}

// snapLess compares two snapshots on key, ascending. Strings compare
// case-insensitively so "alpha" and "Alpha" interleave the way a human sorts.
func snapLess(a, b *entry.Snapshot, key Column) bool {
	switch key {
	case ColTier:
		return tierLess(a, b)
	case ColID:
		return a.ID.String() < b.ID.String()
	case ColKind:
		return a.Kind < b.Kind
	case ColStatus:
		return a.Status < b.Status
	case ColTitle:
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	case ColAuthor:
		return strings.ToLower(a.CreatedBy) < strings.ToLower(b.CreatedBy)
	case ColCreated:
		return a.CreatedAt.Before(b.CreatedAt)
	case ColUpdated:
		return a.UpdatedAt.Before(b.UpdatedAt)
	case ColEdited:
		return a.EditedAt.Before(b.EditedAt)
	}
	return false
}

// Less reports whether a orders before b under the spec (direction included).
func (s *SortSpec) Less(a, b *entry.Snapshot) bool {
	if s.Desc {
		return snapLess(b, a, s.Key)
	}
	return snapLess(a, b, s.Key)
}

// favFirst reports the favorites-before-rest ordering of a and b. ok is true
// when the two differ in favorite membership (and less gives the order); when
// they share it, ok is false and the caller falls through to its secondary
// comparator. An empty favs makes ok always false, so favorite pinning is
// inert unless the caller supplies a set.
func favFirst(favs map[string]bool, a, b *entry.Snapshot) (less, ok bool) {
	fa, fb := favs[a.ID.String()], favs[b.ID.String()]
	if fa == fb {
		return false, false
	}
	return fa, true // a is favorited → a sorts first
}

// SortSnapshots orders items in place per spec (stable). Favorited ids (favs)
// float to the top regardless of spec; within the favorite and non-favorite
// groups the spec order (or, for a nil spec, the incoming store order) holds.
// A nil spec with empty favs is a no-op so callers can pass the parsed flag
// through unconditionally.
func SortSnapshots(items []*entry.Snapshot, spec *SortSpec, favs map[string]bool) {
	if spec == nil && len(favs) == 0 {
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		if less, ok := favFirst(favs, items[i], items[j]); ok {
			return less
		}
		if spec == nil {
			return false
		}
		return spec.Less(items[i], items[j])
	})
}

// tableCell returns the display string for a column in aligned-table mode.
// For the tier column it returns the plain glyph+word badge; for title it
// appends the decorators ((deleted), [labels], ◆ merged, ×N count).
func tableCell(col Column, r listRow) string {
	it := r.snap
	switch col {
	case ColTier:
		return tierPlain(it.Tier, it.TierType)
	case ColTitle:
		title := it.Title
		if it.Deleted {
			title += "  (deleted)"
		}
		if it.Archived {
			title += "  (archived)"
		}
		if len(it.Labels) > 0 {
			title += "  [" + strings.Join(it.Labels, ", ") + "]"
		}
		if it.Merged {
			title += "  ◆ merged"
		}
		if r.count > 1 {
			title += fmt.Sprintf("  (×%d)", r.count)
		}
		return title
	default:
		return plainCell(col, r)
	}
}

// plainCell returns the bare TSV value for a column with no decorators.
func plainCell(col Column, r listRow) string {
	it := r.snap
	switch col {
	case ColTier:
		return it.Tier
	case ColID:
		return ShortID(it.ID)
	case ColFullID:
		return it.ID.String()
	case ColKind:
		return it.Kind
	case ColStatus:
		return it.Status
	case ColTitle:
		return it.Title
	case ColAuthor:
		return it.CreatedBy
	case ColEmail:
		return it.CreatedByEmail
	case ColCreated:
		return it.CreatedAt.Format("2006-01-02")
	case ColUpdated:
		return it.UpdatedAt.Format("2006-01-02")
	case ColEdited:
		return it.EditedAt.Format("2006-01-02")
	case ColLabels:
		return strings.Join(it.Labels, ", ")
	case ColTracked:
		if it.Tracked {
			return "yes"
		}
		return "no"
	case ColPath:
		return it.TrackedPath
	case ColSource:
		// the most recent provenance event that carries a source path
		src := ""
		for _, o := range it.Provenance {
			if o.SourcePath != "" {
				src = o.SourcePath
			}
		}
		return src
	}
	return ""
}

// List renders the default tier-sorted, collapsed table (unchanged behavior).
func List(w io.Writer, items []*entry.Snapshot, color, showAll bool) {
	RenderList(w, items, ListOptions{Columns: DefaultColumns, Color: color, ShowAll: showAll})
}

// RenderList renders items per opts: a TSV plain mode or the aligned table.
func RenderList(w io.Writer, items []*entry.Snapshot, opts ListOptions) {
	cols := opts.Columns
	if len(cols) == 0 {
		cols = DefaultColumns
	}
	if opts.Plain {
		renderPlain(w, items, cols, opts.ShowAll)
		return
	}
	renderTable(w, items, cols, opts.Color, opts.ShowAll, opts.Sort, opts.Favorites)
}

// renderPlain emits one tab-separated line per entry, uncollapsed, no chrome.
// Superseded entries drop unless showAll.
func renderPlain(w io.Writer, items []*entry.Snapshot, cols []Column, showAll bool) {
	for _, it := range items {
		if !showAll && it.Status == "superseded" {
			continue
		}
		r := listRow{snap: it, count: 1}
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = plainCell(c, r)
		}
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
}

// renderTable reproduces the aligned, collapsed table for arbitrary columns.
// A non-nil sortSpec replaces the default tier→kind→title order; it applies to
// the post-collapse rows (each group is placed by its representative). Favorited
// ids (favs) pin their rows to the top ahead of either ordering.
func renderTable(w io.Writer, items []*entry.Snapshot, cols []Column, color, showAll bool, sortSpec *SortSpec, favs map[string]bool) {
	rows := listRows(items, showAll)
	if len(rows) == 0 {
		fmt.Fprintln(w, "no entries")
		return
	}
	sortListRows(rows, sortSpec, favs)
	widths := columnWidths(rows, cols)
	fmt.Fprintln(w, strings.Join(headerCells(cols, widths), "  "))
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(rowCells(r, cols, widths, color), "  "))
	}
	noun := "entries"
	if len(rows) == 1 {
		noun = "entry"
	}
	fmt.Fprintf(w, "\n%d %s\n", len(rows), noun)
}

// ListLines renders the collapsed, sorted entry table as a header row and one
// line per entry, returning the entry ids in the same display order. RenderList
// and the interactive list cockpit share it so a row's rendered form is defined
// once. The plain/machine format stays in RenderList; opts.Plain is ignored here.
func ListLines(items []*entry.Snapshot, opts ListOptions) (header string, lines []string, ids []entity.Id) {
	cols := opts.Columns
	if len(cols) == 0 {
		cols = DefaultColumns
	}
	rows := listRows(items, opts.ShowAll)
	sortListRows(rows, opts.Sort, opts.Favorites)
	widths := columnWidths(rows, cols)
	header = strings.Join(headerCells(cols, widths), "  ")
	for _, r := range rows {
		lines = append(lines, strings.Join(rowCells(r, cols, widths, opts.Color), "  "))
		ids = append(ids, r.snap.ID)
	}
	return header, lines, ids
}

// sortListRows orders rows in place: favorited ids pin to the top, then sortSpec
// (if any), else the default tier→kind→title→id order.
func sortListRows(rows []listRow, sortSpec *SortSpec, favs map[string]bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].snap, rows[j].snap
		if less, ok := favFirst(favs, a, b); ok {
			return less
		}
		if sortSpec != nil {
			if sortSpec.Desc {
				return snapLess(b, a, sortSpec.Key)
			}
			return snapLess(a, b, sortSpec.Key)
		}
		if a.Tier != b.Tier {
			return tierLess(a, b)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID.String() < b.ID.String()
	})
}

// columnWidths returns the display width of each column: the header width grown
// to fit the widest cell in that column.
func columnWidths(rows []listRow, cols []Column) []int {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(columnHeaders[c])
	}
	for _, r := range rows {
		for i, c := range cols {
			if n := utf8.RuneCountInString(tableCell(c, r)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	return widths
}

// headerCells returns the padded column-header cells (the last column is not padded).
func headerCells(cols []Column, widths []int) []string {
	hdr := make([]string, len(cols))
	for i, c := range cols {
		if i == len(cols)-1 {
			hdr[i] = columnHeaders[c]
		} else {
			hdr[i] = pad(columnHeaders[c], widths[i])
		}
	}
	return hdr
}

// rowCells returns the padded cells for one row (tier cell is colorized; the last
// column is not padded).
func rowCells(r listRow, cols []Column, widths []int, color bool) []string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		last := i == len(cols)-1
		if c == ColTier {
			wdt := widths[i]
			if last {
				wdt = 0
			}
			cells[i] = tierCell(r.snap.Tier, r.snap.TierType, wdt, color)
		} else if last {
			cells[i] = tableCell(c, r)
		} else {
			cells[i] = pad(tableCell(c, r), widths[i])
		}
	}
	return cells
}

// listRows applies the clean-view transforms. With showAll, every item is its
// own row. Otherwise superseded entries drop out and entries sharing a
// normalized title collapse to one row (representative = most recently updated,
// tie-broken by id) carrying the group count.
func listRows(items []*entry.Snapshot, showAll bool) []listRow {
	if showAll {
		rows := make([]listRow, 0, len(items))
		for _, it := range items {
			rows = append(rows, listRow{snap: it, count: 1})
		}
		return rows
	}
	groups := map[string][]*entry.Snapshot{}
	var order []string
	for _, it := range items {
		if it.Status == "superseded" {
			continue
		}
		key := entry.NormalizeTitle(it.Title)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], it)
	}
	rows := make([]listRow, 0, len(order))
	for _, key := range order {
		g := groups[key]
		rep := g[0]
		for _, it := range g[1:] {
			if it.UpdatedAt.After(rep.UpdatedAt) ||
				(it.UpdatedAt.Equal(rep.UpdatedAt) && it.ID.String() < rep.ID.String()) {
				rep = it
			}
		}
		rows = append(rows, listRow{snap: rep, count: len(g)})
	}
	return rows
}

// SearchHit is one row of the search table: a snapshot plus the number of
// query occurrences in its title and body.
type SearchHit struct {
	Snap    *entry.Snapshot
	Matches int
}

// SearchResults renders the `kref search` table: a right-aligned MATCHES
// column ahead of the familiar tier/id/kind/title columns, with a footer
// tallying entries and total matches. Rows arrive pre-sorted (most matches
// first); no collapsing — search shows every hit.
func SearchResults(w io.Writer, hits []SearchHit, color bool) {
	if len(hits) == 0 {
		fmt.Fprintln(w, "no matches")
		return
	}
	cols := []Column{ColTier, ColID, ColKind, ColTitle}
	mw := len("MATCHES")
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(columnHeaders[c])
	}
	for _, h := range hits {
		r := listRow{snap: h.Snap, count: 1}
		for i, c := range cols {
			if n := utf8.RuneCountInString(tableCell(c, r)); n > widths[i] {
				widths[i] = n
			}
		}
	}

	hdr := make([]string, 0, len(cols)+1)
	hdr = append(hdr, pad("MATCHES", mw))
	for i, c := range cols {
		if i == len(cols)-1 {
			hdr = append(hdr, columnHeaders[c])
		} else {
			hdr = append(hdr, pad(columnHeaders[c], widths[i]))
		}
	}
	fmt.Fprintln(w, strings.Join(hdr, "  "))

	total := 0
	for _, h := range hits {
		total += h.Matches
		r := listRow{snap: h.Snap, count: 1}
		cells := []string{fmt.Sprintf("%*d", mw, h.Matches)}
		for i, c := range cols {
			last := i == len(cols)-1
			switch {
			case c == ColTier:
				wdt := widths[i]
				if last {
					wdt = 0
				}
				cells = append(cells, tierCell(h.Snap.Tier, h.Snap.TierType, wdt, color))
			case last:
				cells = append(cells, tableCell(c, r))
			default:
				cells = append(cells, pad(tableCell(c, r), widths[i]))
			}
		}
		fmt.Fprintln(w, strings.Join(cells, "  "))
	}

	entriesNoun, matchesNoun := "entries", "matches"
	if len(hits) == 1 {
		entriesNoun = "entry"
	}
	if total == 1 {
		matchesNoun = "match"
	}
	fmt.Fprintf(w, "\n%d %s, %d %s\n", len(hits), entriesNoun, total, matchesNoun)
}

// PlainSearchResults emits one tab-separated row per hit — matches, tier, id,
// kind, title — with no header or footer, mirroring `list --plain` for
// grep/cut/xargs pipelines.
func PlainSearchResults(w io.Writer, hits []SearchHit) {
	for _, h := range hits {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
			h.Matches, h.Snap.Tier, ShortID(h.Snap.ID), h.Snap.Kind, h.Snap.Title)
	}
}

// tierCell pads the plain badge to width first (so column alignment is computed
// on visible runes), then wraps the padded cell in color. tabwriter is avoided
// because it counts ANSI escape bytes as visible width and would misalign.
func tierCell(tier, typ string, w int, color bool) string {
	cell := pad(tierPlain(tier, typ), w)
	if color {
		if c := tierColor(typ); c != "" {
			return c + cell + ansiReset
		}
	}
	return cell
}

// ShowOptions controls how Show composes an entry's detail view.
type ShowOptions struct {
	Raw         bool     // emit the stored body verbatim instead of rendering it
	NoHeader    bool     // omit the metadata header block
	HeaderOnly  bool     // render only the metadata header block (no body)
	Color       bool     // ANSI color (human + interactive only; resolved by cmd/kref)
	Width       int      // markdown wrap width; 0 = no hard wrap (pipe-safe default)
	TrackedNote string   // preformatted "<path> [<drift>]"; empty = no Tracked row
	Favorites   []string // favorite names pointing at this entry; empty = no row
}

// plainMarkdownStyle is a glamour style that strips heading markers and
// produces no ANSI escapes, making output deterministic and pipe-safe.
var plainMarkdownStyle = ansi.StyleConfig{
	Document: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{},
		Margin:         new(uint),
	},
	Heading: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{Bold: new(false)},
		Margin:         new(uint),
	},
	Paragraph: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{},
	},
	Text: ansi.StylePrimitive{},
}

// RenderBody writes body rendered according to contentType: markdown through
// glamour, recognized code/structured text through chroma (color only), and
// everything else verbatim. width>0 wraps markdown to that column count; width==0
// leaves it unwrapped (except colored output, which wraps at 80 to match the
// old glamour.Render default). Color off keeps output ANSI-free and deterministic.
func RenderBody(w io.Writer, body, contentType string, color bool, width int) {
	if content.IsMarkdown(contentType) {
		body = UnwrapMarkdown(body)
		style := glamour.WithStyles(plainMarkdownStyle)
		if color {
			style = glamour.WithStandardStyle("dark")
		}
		wrap := width
		if color && wrap == 0 {
			wrap = 80
		}
		if r, err := glamour.NewTermRenderer(style, glamour.WithWordWrap(wrap)); err == nil {
			if out, rerr := r.Render(body); rerr == nil {
				fmt.Fprint(w, out)
				return
			}
		}
		// glamour failure falls through to verbatim so output is never lost.
	}
	if lexer := content.Lexer(contentType); lexer != "" && color {
		if err := quick.Highlight(w, body, lexer, "terminal256", "monokai"); err == nil {
			return
		}
	}
	fmt.Fprintln(w, body)
}

// ShowHeader writes the metadata block as an aligned key/value table: id,
// tier/status, title, author, labels, merged note, provenance, and (when set)
// the tracked-file note. trackedNote is preformatted "<path> [<drift>]"; the
// command layer computes drift so render keeps no dependency on bridge.
// hdrRow is one label/value line of a show header. vw is the value's visible
// width (runes, ANSI-free) used to size the closing rule.
type hdrRow struct {
	label, value string
	vw           int
}

// baseHeaderRows builds the standard metadata rows (ID … Tracked).
func baseHeaderRows(snap *entry.Snapshot, color bool, trackedNote string, favorites []string) []hdrRow {
	rc := utf8.RuneCountInString
	var rows []hdrRow
	add := func(label, value string, vw int) { rows = append(rows, hdrRow{label, value, vw}) }

	id := snap.ID.String()
	add("ID", id, rc(id))
	statusPlain := tierPlain(snap.Tier, snap.TierType) + " / " + snap.Status
	add("Status", Tier(snap.Tier, snap.TierType, color)+" / "+snap.Status, rc(statusPlain))
	add("Title", snap.Title, rc(snap.Title))
	author := fmt.Sprintf("%s <%s>", snap.CreatedBy, snap.CreatedByEmail)
	add("Author", author, rc(author))
	if len(snap.Labels) > 0 {
		v := strings.Join(snap.Labels, ", ")
		add("Labels", v, rc(v))
	}
	if len(favorites) > 0 {
		v := strings.Join(favorites, ", ")
		add("Favorites", v, rc(v))
	}
	if snap.Merged {
		v := "◆ merged — concurrent edits auto-merged; review with `kref diff`, clear with `kref resolve`"
		add("Merged", v, rc(v))
	}
	for _, o := range snap.Provenance {
		v := fmt.Sprintf("%s by %s (%s)", o.Trigger, o.Actor, o.ActorKind)
		if o.SourcePath != "" {
			v += " from " + o.SourcePath
		}
		add("Origin", v, rc(v))
	}
	if trackedNote != "" {
		add("Tracked", trackedNote, rc(trackedNote))
	}
	return rows
}

// writeHeaderRows renders label-padded rows followed by a rule sized to the
// widest rendered row.
func writeHeaderRows(w io.Writer, rows []hdrRow) {
	rc := utf8.RuneCountInString
	labelW := 0
	for _, r := range rows {
		if n := rc(r.label); n > labelW {
			labelW = n
		}
	}
	ruleW := 0
	for _, r := range rows {
		fmt.Fprintf(w, "%s%s\n", pad(r.label, labelW+2), r.value)
		if rw := labelW + 2 + r.vw; rw > ruleW {
			ruleW = rw
		}
	}
	fmt.Fprintln(w, strings.Repeat("─", ruleW))
}

func ShowHeader(w io.Writer, snap *entry.Snapshot, color bool, trackedNote string, favorites []string) {
	writeHeaderRows(w, baseHeaderRows(snap, color, trackedNote, favorites))
}

// Show renders the full detail view of one entry per opts. The full id is
// intentional: Show is the canonical reference surface.
func Show(w io.Writer, snap *entry.Snapshot, opts ShowOptions) {
	if !opts.NoHeader {
		ShowHeader(w, snap, opts.Color, opts.TrackedNote, opts.Favorites)
		if opts.HeaderOnly {
			return
		}
		fmt.Fprintln(w)
	}
	if opts.Raw {
		fmt.Fprintln(w, snap.Body)
	} else {
		RenderBody(w, snap.Body, snap.ContentType, opts.Color, opts.Width)
	}
	if len(snap.Comments) > 0 {
		fmt.Fprintln(w)
		RenderComments(w, snap.Comments, opts.Color, opts.Width)
	}
}

// CommentNode is one comment within a thread: its own rendered lines plus its
// id and depth, so callers can address/select individual nodes.
type CommentNode struct {
	ID    string
	Depth int
	Lines []string
}

// CommentThread is one top-level thread's rendered lines: the root plus, unless
// the root is collapsed, its nested replies (depth-first).
type CommentThread struct {
	RootID string
	Lines  []string
	Nodes  []CommentNode
}

// RenderCommentThreads renders each top-level comment thread to its own line
// group. Any node whose id is in collapsed keeps its head+body but hides its
// replies (a one-line "▸ N replies" hint takes their place); collapsed==nil
// expands everything. width>0 word-wraps comment bodies to that column count.
// This is the shared tree-walk behind RenderComments/RenderCommentsCollapsed
// (the flat show forms) and the todo cockpit (which needs per-node groups to
// place the cursor).
// wrapText greedily word-wraps s to width columns, hard-breaking any word longer
// than width. width <= 0 returns s unwrapped; a whitespace-only line is preserved.
func wrapText(s string, width int) []string {
	words := strings.Fields(s)
	if width <= 0 || len(words) == 0 {
		return []string{s}
	}
	var out []string
	cur := ""
	for _, w := range words {
		for len([]rune(w)) > width {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			rw := []rune(w)
			out = append(out, string(rw[:width]))
			w = string(rw[width:])
		}
		switch {
		case cur == "":
			cur = w
		case len([]rune(cur))+1+len([]rune(w)) <= width:
			cur += " " + w
		default:
			out = append(out, cur)
			cur = w
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func RenderCommentThreads(comments []entry.Comment, color bool, collapsed map[string]bool, width int) []CommentThread {
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + ansiReset
	}

	present := make(map[string]bool, len(comments))
	for _, c := range comments {
		present[c.ID] = true
	}
	children := make(map[string][]entry.Comment)
	var roots []entry.Comment
	for _, c := range comments {
		if c.ReplyTo != "" && present[c.ReplyTo] {
			children[c.ReplyTo] = append(children[c.ReplyTo], c)
		} else {
			roots = append(roots, c)
		}
	}

	now := time.Now()
	headLine := func(c entry.Comment, depth int) string {
		indent := strings.Repeat("  ", depth)
		glyph := "·"
		if c.Question {
			if c.Resolved {
				glyph = paint(ansiGreen, "✓")
			} else {
				glyph = paint(ansiRed, "◉")
			}
		}
		head := fmt.Sprintf("%s%s %s  %s", indent, glyph, c.Author, RelTime(now, c.Time))
		if c.Resolved && c.ResolvedBy != "" {
			head += " · resolved by " + c.ResolvedBy
		}
		if c.Edited {
			head += " · edited"
		}
		return head
	}
	bodyLines := func(c entry.Comment, depth int) []string {
		prefix := strings.Repeat("  ", depth) + "  "
		if c.Deleted {
			return []string{prefix + "[deleted]"}
		}
		avail := 0
		if width > 0 {
			avail = max(width-len([]rune(prefix)), 8)
		}
		var out []string
		for line := range strings.SplitSeq(c.Body, "\n") {
			for _, wl := range wrapText(line, avail) {
				out = append(out, prefix+wl)
			}
		}
		return out
	}

	// countDescendants returns how many comments sit below id (all replies,
	// recursively) — shown in a collapsed node's hint line.
	var countDescendants func(id string) int
	countDescendants = func(id string) int {
		n := 0
		for _, ch := range children[id] {
			n += 1 + countDescendants(ch.ID)
		}
		return n
	}

	var threads []CommentThread
	for _, r := range roots {
		var lines []string
		var nodes []CommentNode
		var walk func(c entry.Comment, depth int)
		walk = func(c entry.Comment, depth int) {
			nodeLines := append([]string{headLine(c, depth)}, bodyLines(c, depth)...)
			// A collapsed node keeps its head+body but hides its replies, with a
			// one-line hint. This works at any depth, so a deep sub-thread can be
			// folded from the node it hangs off.
			if collapsed[c.ID] && len(children[c.ID]) > 0 {
				n := countDescendants(c.ID)
				noun := "replies"
				if n == 1 {
					noun = "reply"
				}
				nodeLines = append(nodeLines, fmt.Sprintf("%s  ▸ %d %s", strings.Repeat("  ", depth), n, noun))
			}
			lines = append(lines, nodeLines...)
			nodes = append(nodes, CommentNode{ID: c.ID, Depth: depth, Lines: nodeLines})
			if collapsed[c.ID] {
				return
			}
			for _, child := range children[c.ID] {
				walk(child, depth+1)
			}
		}
		walk(r, 0)
		threads = append(threads, CommentThread{RootID: r.ID, Lines: lines, Nodes: nodes})
	}
	return threads
}

// RenderCommentsCollapsed writes the threaded comments, collapsing any root whose
// id is in collapsed to a single preview line. collapsed==nil expands all.
// width>0 word-wraps comment bodies to that column count (0 leaves them verbatim).
func RenderCommentsCollapsed(w io.Writer, comments []entry.Comment, color bool, collapsed map[string]bool, width int) {
	fmt.Fprintf(w, "Comments (%d)\n", len(comments))
	fmt.Fprintln(w, strings.Repeat("─", 13))
	for _, t := range RenderCommentThreads(comments, color, collapsed, width) {
		for _, ln := range t.Lines {
			fmt.Fprintln(w, ln)
		}
	}
}

// RenderComments writes the full threaded comments (no collapse). Top-level
// comments (and any whose ReplyTo target is absent) render at depth 0; replies
// indent under their parent. width>0 word-wraps comment bodies to that width.
func RenderComments(w io.Writer, comments []entry.Comment, color bool, width int) {
	RenderCommentsCollapsed(w, comments, color, nil, width)
}

// Action renders a one-line confirmation, e.g.
// `added ○ shared a5745cf90565  spec  "Auth flow spec"`.
func Action(w io.Writer, verb string, snap *entry.Snapshot, color bool) {
	fmt.Fprintf(w, "%s %s %s  %s  %q\n",
		verb, Tier(snap.Tier, snap.TierType, color), ShortID(snap.ID), snap.Kind, snap.Title)
}

// Log renders an entry's operation timeline, one line per op.
func Log(w io.Writer, entries []entry.LogEntry) {
	for _, e := range entries {
		ts := e.Time.Format("2006-01-02 15:04")
		line := fmt.Sprintf("%s  %-12s %s", ts, e.Op, e.Author)
		if e.Detail != "" {
			line += "  " + e.Detail
		}
		fmt.Fprintln(w, line)
	}
}

// BodyVersions renders each historical body under an author/time header, so a
// superseded version can be read off and recovered.
func BodyVersions(w io.Writer, versions []entry.BodyVersion) {
	for i, v := range versions {
		fmt.Fprintf(w, "=== version %d — %s @ %s ===\n%s\n\n",
			i+1, v.Author, v.Time.Format("2006-01-02 15:04"), v.Body)
	}
}

// VersionDiff renders the inline line diff between two body versions (1-based;
// from==0 diffs from the empty body, the v1 case). The header carries the
// target version's author/time and a compact change summary; unchanged lines
// print as two-space-indented context, additions as green `+ `, removals as
// red `- `.
func VersionDiff(w io.Writer, versions []entry.BodyVersion, from, to int, color bool) {
	prev := ""
	if from > 0 {
		prev = versions[from-1].Body
	}
	target := versions[to-1]

	head := fmt.Sprintf("v%d", to)
	if from > 0 {
		head = fmt.Sprintf("v%d → v%d", from, to)
	}
	st := textdiff.Stats(prev, target.Body)
	fmt.Fprintf(w, "=== %s — %s @ %s ===  +%d/-%d chars, +%d/-%d lines\n",
		head, target.Author, target.Time.Format("2006-01-02 15:04"),
		st.CharsAdded, st.CharsRemoved, st.LinesAdded, st.LinesRemoved)

	for _, l := range textdiff.Diff(prev, target.Body) {
		switch l.Op {
		case textdiff.Add:
			line := "+ " + l.Text
			if color {
				line = ansiGreen + line + ansiReset
			}
			fmt.Fprintln(w, line)
		case textdiff.Del:
			line := "- " + l.Text
			if color {
				line = ansiRed + line + ansiReset
			}
			fmt.Fprintln(w, line)
		default:
			fmt.Fprintln(w, "  "+l.Text)
		}
	}
}

// DiffChain renders every consecutive version pair as an inline diff — v1 from
// nothing, then v1→v2, v2→v3, … — the glanceable default for `kref diff`.
func DiffChain(w io.Writer, versions []entry.BodyVersion, color bool) {
	for i := range versions {
		VersionDiff(w, versions, i, i+1, color)
		if i < len(versions)-1 {
			fmt.Fprintln(w)
		}
	}
}
