package todo

import "strings"

// Section is one foldable/navigable section in the todo outline. The todo
// grammar (see Parse) treats only "## " lines as section boundaries; the "#"
// title and "###" sub-headings are content within a section, so Sections lists
// the "## " headings only — the same granularity CollapseDone/CollapseSections
// fold at.
type Section struct {
	Heading string // the exact "## " heading line, e.g. "## Done (compact)"
	Index   int    // position in the returned slice
}

// Sections returns the todo body's "## " section headings, in document order.
func Sections(body string) []Section {
	var out []Section
	for ln := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(ln, "## ") {
			out = append(out, Section{Heading: strings.TrimRight(ln, " "), Index: len(out)})
		}
	}
	return out
}
