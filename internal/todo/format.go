package todo

import "strings"

// Format applies the mechanical auto-fix rules and returns the new body:
//   - every done ([x]) item is moved into "## Done (compact)";
//   - any non-done item found inside Done is moved back to "## Open";
//   - each checkbox line has its trailing whitespace trimmed.
//
// It is deterministic and idempotent: Format(Format(x)) == Format(x). If the
// required sections (## Open and ## Done (compact)) are not both present the
// body is returned unchanged, so a broken document is never partially
// rearranged (the linter reports the missing section instead).
func Format(body string) (string, error) {
	d := Parse(body)
	openIdx := d.sectionIndex("## Open")
	doneIdx := d.sectionIndex("## Done (compact)")
	if openIdx < 0 || doneIdx < 0 {
		return body, nil
	}
	var done []node
	for i := range d.sections {
		if i == doneIdx {
			continue
		}
		done = append(done, d.sections[i].extractItems(func(st State) bool { return st == StateDone })...)
	}
	strays := d.sections[doneIdx].extractItems(func(st State) bool { return st != StateDone })
	d.sections[doneIdx].nodes = append(d.sections[doneIdx].nodes, done...)
	d.sections[openIdx].nodes = append(d.sections[openIdx].nodes, strays...)
	d.trimCheckboxLines()
	return d.String(), nil
}

func (d *Document) sectionIndex(heading string) int {
	for i := range d.sections {
		if d.sections[i].heading == heading {
			return i
		}
	}
	return -1
}

// extractItems removes and returns item nodes whose state matches; other nodes
// keep their position and order.
func (s *section) extractItems(match func(State) bool) []node {
	var kept, taken []node
	for _, n := range s.nodes {
		if n.kind == nodeItem && match(n.state) {
			taken = append(taken, n)
		} else {
			kept = append(kept, n)
		}
	}
	s.nodes = kept
	return taken
}

func (d *Document) trimCheckboxLines() {
	for si := range d.sections {
		for ni := range d.sections[si].nodes {
			n := &d.sections[si].nodes[ni]
			if n.kind == nodeItem && len(n.lines) > 0 {
				n.lines[0] = strings.TrimRight(n.lines[0], " \t")
			}
		}
	}
}
