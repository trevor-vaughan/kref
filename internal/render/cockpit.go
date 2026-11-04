package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/todo"
)

// signalGlyph returns the header glyph for a signal under the given theme.
// Inline item bullets are NOT themed (they stay geometric); only these three
// header signals switch to emoji.
func signalGlyph(theme, signal string) string {
	if theme == "emoji" {
		switch signal {
		case "awaiting":
			return "❓"
		case "review":
			return "👀"
		case "changed":
			return "✏️"
		}
	}
	switch signal {
	case "awaiting":
		return "◉"
	case "review":
		return "◆"
	case "changed":
		return "✎"
	}
	return ""
}

// TodoCockpit writes the cockpit header: a signal line (awaiting-you, and
// to-review/changed when computed — a -1 count is suppressed) followed by the
// list of open questions. theme is "geometric" or "emoji".
func TodoCockpit(w io.Writer, c todo.Cockpit, theme string, color bool) {
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + ansiReset
	}
	ver := ""
	if c.Version > 0 {
		// The head version doubles as the CAS token: --if-version / kref_update's
		// if_version. Surface it so a co-editor can declare the base it read.
		ver = fmt.Sprintf(" · v%d", c.Version)
	}
	edited := ""
	if !c.Edited.IsZero() {
		abs := c.Edited.Format("2006-01-02")
		if rel := RelTime(time.Now(), c.Edited); rel != "" {
			edited = fmt.Sprintf(" · edited %s (%s)", rel, abs)
		} else {
			edited = " · edited " + abs
		}
	}
	fmt.Fprintf(w, "%s %s   open %d · done %d%s%s\n",
		paint(ansiRed, signalGlyph(theme, "awaiting")),
		paint(ansiRed, fmt.Sprintf("%d awaiting you", c.Awaiting)),
		c.Open, c.Done, ver, edited)
	if c.ToReview >= 0 {
		fmt.Fprintf(w, "%s %s\n", paint(ansiYellow, signalGlyph(theme, "review")),
			paint(ansiYellow, fmt.Sprintf("%d to review", c.ToReview)))
	}
	if c.Changed >= 0 {
		fmt.Fprintf(w, "%s %s\n", paint(ansiGreen, signalGlyph(theme, "changed")),
			paint(ansiGreen, fmt.Sprintf("%d changed", c.Changed)))
	}
	if c.QuarantinePending > 0 {
		// A repo-wide awareness badge — the actionable review queue lives in the
		// interactive `kref list` cockpit, not here.
		label := fmt.Sprintf("%d awaiting review", c.QuarantinePending)
		if c.QuarantineStale > 0 {
			label = fmt.Sprintf("%d awaiting review (%d stale)", c.QuarantinePending, c.QuarantineStale)
		}
		fmt.Fprintf(w, "%s %s\n", paint(ansiYellow, "⚠"), paint(ansiYellow, label))
	}
	for i, q := range c.Questions {
		fmt.Fprintf(w, "  %d. %s\n", i+1, q)
	}
	fmt.Fprintln(w)
}

// TodoStripFields returns the cockpit's sticky-strip fields, most important
// first, so the viewer can drop from the tail as the terminal narrows.
//
// The alert signals outrank the bookkeeping deliberately. TodoCockpit prints
// them on lines AFTER the counts, so porting that order would shed exactly the
// fields that exist to demand attention. Counts, version and edited-time are
// recoverable from `kref show --json`; an unseen alert is not.
//
// The fields are built here rather than split out of TodoCockpit's output:
// splitting would break the SGR pairs the coloured segments are wrapped in, and
// `kref todo --no-pager` still renders through TodoCockpit unchanged.
func TodoStripFields(c todo.Cockpit, theme string, color bool) []string {
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + ansiReset
	}
	fields := []string{
		paint(ansiRed, fmt.Sprintf("%s %d awaiting you", signalGlyph(theme, "awaiting"), c.Awaiting)),
	}
	if c.ToReview >= 0 {
		fields = append(fields, paint(ansiYellow, fmt.Sprintf("%s %d to review", signalGlyph(theme, "review"), c.ToReview)))
	}
	if c.Changed >= 0 {
		fields = append(fields, paint(ansiGreen, fmt.Sprintf("%s %d changed", signalGlyph(theme, "changed"), c.Changed)))
	}
	if c.QuarantinePending > 0 {
		label := fmt.Sprintf("⚠ %d awaiting review", c.QuarantinePending)
		if c.QuarantineStale > 0 {
			label = fmt.Sprintf("⚠ %d awaiting review (%d stale)", c.QuarantinePending, c.QuarantineStale)
		}
		fields = append(fields, paint(ansiYellow, label))
	}
	fields = append(fields, fmt.Sprintf("open %d · done %d", c.Open, c.Done))
	if c.Version > 0 {
		// The head version doubles as the CAS token: --if-version / kref_update's
		// if_version.
		fields = append(fields, fmt.Sprintf("v%d", c.Version))
	}
	if !c.Edited.IsZero() {
		abs := c.Edited.Format("2006-01-02")
		if rel := RelTime(time.Now(), c.Edited); rel != "" {
			fields = append(fields, fmt.Sprintf("edited %s (%s)", rel, abs))
		} else {
			fields = append(fields, "edited "+abs)
		}
	}
	return fields
}

// TodoPickerItem is one candidate row in the todo picker: the numbers a reader
// needs to answer "which todo needs me" without opening any of them.
type TodoPickerItem struct {
	ID       entity.Id
	Title    string
	Awaiting int
	Open     int
	Edited   time.Time
}

// TodoPickerLines returns the picker's column-aligned row labels — short id,
// title, the awaiting-you signal, the open-item count and the relative edited
// time. The signal is omitted at zero: a "◉0" on every idle todo is noise
// competing with the one row that has something waiting.
//
// It takes and returns slices because alignment needs the whole set, the same
// contract ListLines has. Building the labels here rather than in the picker
// keeps every todo presentation decision in one package.
func TodoPickerLines(items []TodoPickerItem, theme string, now time.Time) []string {
	titleW := 0
	for _, it := range items {
		if n := utf8.RuneCountInString(it.Title); n > titleW {
			titleW = n
		}
	}
	glyph := signalGlyph(theme, "awaiting")
	lines := make([]string, 0, len(items))
	for _, it := range items {
		signal := ""
		if it.Awaiting > 0 {
			signal = fmt.Sprintf("%s%d", glyph, it.Awaiting)
		}
		edited := ""
		if !it.Edited.IsZero() {
			if rel := RelTime(now, it.Edited); rel != "" {
				edited = rel
			} else {
				edited = it.Edited.Format("2006-01-02")
			}
		}
		lines = append(lines, strings.TrimRight(fmt.Sprintf("%s  %s  %s  %s  %s",
			ShortID(it.ID), pad(it.Title, titleW), padLeft(signal, 3),
			padLeft(strconv.Itoa(it.Open), 3), edited), " "))
	}
	return lines
}
