package render

import (
	"fmt"
	"io"
	"time"

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
