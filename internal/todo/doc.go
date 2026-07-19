// Package todo models the kref todo document: a line-oriented, conservative
// view of the single kind:todo entry body. It recognizes level-2 (## ) sections
// and top-level checkbox items; every other line is preserved verbatim so
// formatting is loss-free.
package todo

import "strings"

// State is a checkbox state.
type State int

const (
	// StateOpen is "- [ ]".
	StateOpen State = iota
	// StateDone is "- [x]".
	StateDone
)

type nodeKind int

const (
	nodeRaw nodeKind = iota
	nodeItem
)

// node is one unit inside a section: a checkbox item block (the checkbox line
// plus its indented notes) or a run of opaque lines (prose, blanks, ### heads).
type node struct {
	kind  nodeKind
	state State    // meaningful only when kind == nodeItem
	lines []string // verbatim, newline-free
}

// section is a level-2 block, or the preamble (heading == "").
type section struct {
	heading string // full heading line, e.g. "## Open"; "" for the preamble
	nodes   []node
}

// Document is the parsed todo body.
type Document struct {
	sections []section
}

// String renders the document back to a body. For an unchanged document it is
// an exact round-trip of the parsed input (Split/Join symmetry on "\n").
func (d *Document) String() string {
	var lines []string
	for _, s := range d.sections {
		if s.heading != "" {
			lines = append(lines, s.heading)
		}
		for _, n := range s.nodes {
			lines = append(lines, n.lines...)
		}
	}
	return strings.Join(lines, "\n")
}

// CountItems returns how many checkbox items in the document have the given
// state. Exported for tests and for the future cockpit's signal counts.
func CountItems(d *Document, st State) int {
	n := 0
	for _, s := range d.sections {
		for _, nd := range s.nodes {
			if nd.kind == nodeItem && nd.state == st {
				n++
			}
		}
	}
	return n
}
