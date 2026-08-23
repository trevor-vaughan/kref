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
	"strconv"
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

// padLeft right-aligns s in w columns (rune-counted), for numeric cells.
func padLeft(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return spaces(w-n) + s
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
	snap    *entry.Snapshot
	count   int
	matches int
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

	// ColMatches is an INTERNAL column: the per-entry match count for
	// `kref search`. It is deliberately absent from AllColumns, which keeps it
	// out of --columns, that flag's completion, and ColumnHelp — `kref list`
	// has no query, so the column would only ever render 0 there.
	ColMatches Column = "matches"
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

// SearchColumns is the `kref search` layout, shared by the static table and the
// interactive search cockpit so both render the same columns in the same order.
var SearchColumns = []Column{ColMatches, ColTier, ColID, ColKind, ColTitle}

var columnHeaders = map[Column]string{
	ColTier: "TIER", ColID: "ID", ColFullID: "ID", ColKind: "KIND", ColStatus: "STATUS",
	ColTitle: "TITLE", ColAuthor: "AUTHOR", ColEmail: "EMAIL", ColCreated: "CREATED",
	ColUpdated: "UPDATED", ColEdited: "EDITED", ColLabels: "LABELS", ColTracked: "TRACKED", ColPath: "PATH",
	ColSource: "SOURCE", ColMatches: "MATCHES",
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
// Validity is membership in AllColumns — the same set validColumns() reports —
// so what is accepted and what is advertised cannot drift, and internal columns
// (ColMatches) stay unreachable from --columns.
func ParseColumns(s string) ([]Column, error) {
	parts := strings.Split(s, ",")
	cols := make([]Column, 0, len(parts))
	for _, p := range parts {
		c := Column(strings.TrimSpace(p))
		if !slices.Contains(AllColumns, c) {
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
	// Matches maps entry id → query match count, for the ColMatches column.
	// Nil for every view without a query.
	Matches map[string]int
	// PreserveOrder keeps items in the order the caller supplied them, skipping
	// the row sort entirely. `kref search` ranks by match count before it gets
	// here, and a nil Sort is NOT enough to protect that: sortListRows falls
	// through to the default tier→kind→title order, which every other view
	// depends on. "No sort spec" and "already ordered" are different requests.
	PreserveOrder bool
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
	case ColMatches:
		return strconv.Itoa(r.matches)
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
	renderTable(w, items, cols, opts)
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
// A non-nil opts.Sort replaces the default tier→kind→title order; it applies to
// the post-collapse rows (each group is placed by its representative). Favorited
// ids (opts.Favorites) pin their rows to the top ahead of either ordering.
//
// It takes the whole ListOptions rather than six loose parameters: it has one
// caller, and the option set it needs keeps growing.
func renderTable(w io.Writer, items []*entry.Snapshot, cols []Column, opts ListOptions) {
	rows := listRows(items, opts.ShowAll, opts.Matches)
	if len(rows) == 0 {
		fmt.Fprintln(w, "no entries")
		return
	}
	if !opts.PreserveOrder {
		sortListRows(rows, opts.Sort, opts.Favorites)
	}
	widths := columnWidths(rows, cols)
	fmt.Fprintln(w, strings.Join(headerCells(cols, widths), "  "))
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(rowCells(r, cols, widths, opts.Color), "  "))
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
	rows := listRows(items, opts.ShowAll, opts.Matches)
	if !opts.PreserveOrder {
		sortListRows(rows, opts.Sort, opts.Favorites)
	}
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
		switch {
		case c == ColTier:
			wdt := widths[i]
			if last {
				wdt = 0
			}
			cells[i] = tierCell(r.snap.Tier, r.snap.TierType, wdt, color)
		case c == ColMatches:
			// Numeric: right-aligned regardless of position, matching the %*d
			// the search table has always used.
			cells[i] = padLeft(tableCell(c, r), widths[i])
		case last:
			cells[i] = tableCell(c, r)
		default:
			cells[i] = pad(tableCell(c, r), widths[i])
		}
	}
	return cells
}

// listRows applies the clean-view transforms. With showAll, every item is its
// own row. Otherwise superseded entries drop out and entries sharing a
// normalized title collapse to one row (representative = most recently updated,
// tie-broken by id) carrying the group count. matches (nil outside search)
// annotates each row with its query match count.
func listRows(items []*entry.Snapshot, showAll bool, matches map[string]int) []listRow {
	var rows []listRow
	if showAll {
		rows = make([]listRow, 0, len(items))
		for _, it := range items {
			rows = append(rows, listRow{snap: it, count: 1})
		}
	} else {
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
		rows = make([]listRow, 0, len(order))
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
	}
	// Indexing a nil map yields 0, so the non-search callers need no branch.
	for i := range rows {
		rows[i].matches = matches[rows[i].snap.ID.String()]
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
//
// The table itself comes from ListLines, so the static output and the
// interactive search cockpit are the same table by construction. Three options
// carry the search semantics: ShowAll keeps every hit (no collapsing, no
// superseded drop), PreserveOrder leaves the incoming relevance ranking alone,
// and a nil Favorites keeps pinning from outranking it.
func SearchResults(w io.Writer, hits []SearchHit, color bool) {
	if len(hits) == 0 {
		fmt.Fprintln(w, "no matches")
		return
	}
	items := make([]*entry.Snapshot, len(hits))
	matches := make(map[string]int, len(hits))
	total := 0
	for i, h := range hits {
		items[i] = h.Snap
		matches[h.Snap.ID.String()] = h.Matches
		total += h.Matches
	}
	header, lines, _ := ListLines(items, ListOptions{
		Columns:       SearchColumns,
		Color:         color,
		ShowAll:       true,
		PreserveOrder: true,
		Matches:       matches,
	})
	fmt.Fprintln(w, header)
	for _, ln := range lines {
		fmt.Fprintln(w, ln)
	}

	fmt.Fprintf(w, "\n%s\n", SearchTally(len(hits), total))
}

// SearchTally is the count line that closes a search result table — "3 entries,
// 7 matches", singularized. Shared by the static table and the cockpit's exit
// echo so the two cannot word the same fact differently.
func SearchTally(entries, matches int) string {
	entriesNoun, matchesNoun := "entries", "matches"
	if entries == 1 {
		entriesNoun = "entry"
	}
	if matches == 1 {
		matchesNoun = "match"
	}
	return fmt.Sprintf("%d %s, %d %s", entries, entriesNoun, matches, matchesNoun)
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
	// Links are the entry's typed edges, resolved by the command layer (render
	// keeps no store dependency, exactly as TrackedNote arrives preformatted).
	// The header shows up to maxBaseLinkRows of them.
	Links entry.LinkView
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

// maxBaseLinkRows caps the links shown in the base header. An entry with a long
// edge list would otherwise push its body off the screen on every `kref show`;
// the overflow line points at the expanded header, which lists them all.
const maxBaseLinkRows = 10

// linkRow formats one edge for a header: direction, type, short id, title. The
// base and extended headers share it so the two never drift apart.
func linkRow(dir string, l entry.LinkRef) string {
	return fmt.Sprintf("%-4s %-12s %s  %s", dir, l.Type, ShortID(l.ID), l.Title)
}

// baseLinkRows renders an entry's edges for the base header, capped. Returns
// nothing at all when there are no links: an absent row reads better than a
// "no links" line on every entry that has none, and matches how Labels,
// Favorites and Tracked already behave.
func baseLinkRows(links entry.LinkView) []hdrRow {
	total := len(links.Outgoing) + len(links.Incoming)
	if total == 0 {
		return nil
	}
	rc := utf8.RuneCountInString
	var rows []hdrRow
	add := func(label, value string) { rows = append(rows, hdrRow{label, value, rc(value)}) }

	emit := func(dir string, refs []entry.LinkRef) {
		for _, l := range refs {
			if len(rows) >= maxBaseLinkRows {
				return
			}
			label := ""
			if len(rows) == 0 {
				label = "Links"
			}
			add(label, linkRow(dir, l))
		}
	}
	emit("out:", links.Outgoing)
	emit("in:", links.Incoming)

	// Bare count, deliberately: the expanded header that will carry the full list
	// does not exist yet, and naming a key that does not do this would be worse
	// than saying nothing. The view-options work that restores expand owns
	// pointing at it.
	if n := total - len(rows); n > 0 {
		add("", fmt.Sprintf("… +%d more", n))
	}
	return rows
}

// baseHeaderRows builds the standard metadata rows (ID … Tracked), plus the
// entry's links when it has any.
func baseHeaderRows(snap *entry.Snapshot, color bool, trackedNote string, favorites []string, links entry.LinkView) []hdrRow {
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
	return append(rows, baseLinkRows(links)...)
}

// StripFields returns the interactive viewer's sticky-strip fields for one
// entry, most important first, so the viewer can drop from the tail as the
// terminal narrows. These are NOT the header block: baseHeaderRows builds
// label-padded rows for a vertical layout, and its 64-character ID row would
// consume a horizontal strip on its own.
//
// The short id is absent deliberately — the viewer title already carries it
// (cmd/kref/commands.go), and elision cuts the title's tail, so an id at its
// head survives every width.
func StripFields(snap *entry.Snapshot, links entry.LinkView, color bool) []string {
	fields := []string{Tier(snap.Tier, snap.TierType, color) + " / " + snap.Status}
	if snap.Version > 0 {
		fields = append(fields, fmt.Sprintf("v%d", snap.Version))
	}
	if n := len(links.Outgoing) + len(links.Incoming); n > 0 {
		fields = append(fields, fmt.Sprintf("%d links", n))
	}
	open := 0
	for _, c := range snap.Comments {
		// The same predicate store.hasOpenQuestion uses for
		// `kref list --open-questions`.
		if c.Question && !c.Resolved {
			open++
		}
	}
	if open > 0 {
		fields = append(fields, fmt.Sprintf("%d open", open))
	}
	return fields
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

// ShowHeader writes the metadata block for one entry. It takes the same options
// as Show — the fields it reads are exactly a subset — rather than a growing
// list of positional arguments.
func ShowHeader(w io.Writer, snap *entry.Snapshot, opts ShowOptions) {
	writeHeaderRows(w, baseHeaderRows(snap, opts.Color, opts.TrackedNote, opts.Favorites, opts.Links))
}

// Show renders the full detail view of one entry per opts. The full id is
// intentional: Show is the canonical reference surface.
func Show(w io.Writer, snap *entry.Snapshot, opts ShowOptions) {
	if !opts.NoHeader {
		ShowHeader(w, snap, opts)
		if opts.HeaderOnly {
			return
		}
		fmt.Fprintln(w)
	}
	if opts.Raw {
		fmt.Fprintln(w, snap.Body)
		if len(snap.Comments) > 0 {
			fmt.Fprintln(w)
			RenderCommentsPlain(w, snap.Comments)
		}
		return
	}
	RenderBody(w, snap.Body, snap.ContentType, opts.Color, opts.Width)
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

// commentAuthor names who wrote a comment. Every comment is committed under the
// repo's git identity, so an agent's would read as the human's; when the op
// recorded an agent name, that is the meaningful author to show.
func CommentAuthor(c entry.Comment) string {
	if c.Actor != "" {
		// Just the model: the client half is in provenance and JSON, and
		// repeating it on every comment in a thread buries the body.
		return entry.ActorModel(c.Actor)
	}
	return c.Author
}

// threadTree splits comments into top-level roots and a parent→replies map. A
// comment whose ReplyTo names something absent (a parent on another entry, or a
// prefix that was never resolved) is treated as a root, so no comment is ever
// dropped for having a dangling parent.
func threadTree(comments []entry.Comment) (roots []entry.Comment, children map[string][]entry.Comment) {
	present := make(map[string]bool, len(comments))
	for _, c := range comments {
		present[c.ID] = true
	}
	children = make(map[string][]entry.Comment)
	for _, c := range comments {
		if c.ReplyTo != "" && present[c.ReplyTo] {
			children[c.ReplyTo] = append(children[c.ReplyTo], c)
		} else {
			roots = append(roots, c)
		}
	}
	return roots, children
}

func RenderCommentThreads(comments []entry.Comment, color bool, collapsed map[string]bool, width int) []CommentThread {
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + ansiReset
	}

	roots, children := threadTree(comments)

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
		head := fmt.Sprintf("%s%s %s  %s", indent, glyph, CommentAuthor(c), RelTime(now, c.Time))
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
		var rendered []string
		if color && avail > 0 {
			// Render the comment body as markdown (bold/italic/lists/code/links),
			// like the entry body, then indent each line under the comment head.
			var b strings.Builder
			RenderBody(&b, c.Body, "text/markdown", true, avail)
			rendered = strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
			for len(rendered) > 1 && strings.TrimSpace(rendered[0]) == "" {
				rendered = rendered[1:] // drop glamour's top-margin blank line
			}
		} else {
			// Colour off (or no wrap width): raw wrapped text, like `show --plain`.
			for line := range strings.SplitSeq(c.Body, "\n") {
				rendered = append(rendered, wrapText(line, avail)...)
			}
		}
		out := make([]string, len(rendered))
		for i, ln := range rendered {
			out[i] = prefix + ln
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

// RenderCommentsPlain writes the comments for --plain: one "<author>: <body>"
// entry each, bodies verbatim and unwrapped, replies in thread order but not
// indented. Comments belong in --plain, but the threaded presentation — count
// header, rule, glyphs, relative times — is decoration, and stripping
// decoration is what --plain is for. A question's state is spelled out in words
// so it survives where a glyph would not.
func RenderCommentsPlain(w io.Writer, comments []entry.Comment) {
	roots, children := threadTree(comments)
	var walk func(c entry.Comment)
	walk = func(c entry.Comment) {
		body, marker := c.Body, ""
		switch {
		case c.Deleted:
			body = "[deleted]" // never print a tombstoned body
		case c.Question && c.Resolved:
			marker = " [resolved]"
		case c.Question:
			marker = " [open]"
		}
		// The marker rides on the author line so a multi-line body stays
		// byte-for-byte intact below it.
		first, rest, multi := strings.Cut(body, "\n")
		line := CommentAuthor(c) + ": " + first + marker
		if multi {
			line += "\n" + rest
		}
		fmt.Fprintln(w, line)
		for _, child := range children[c.ID] {
			walk(child)
		}
	}
	for _, r := range roots {
		walk(r)
	}
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
