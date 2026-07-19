package todo

import "strings"

// Parse builds a Document from a todo body. It never fails; unrecognized lines
// are preserved verbatim as raw nodes so formatting is loss-free.
func Parse(body string) *Document {
	lines := strings.Split(body, "\n")
	d := &Document{}
	cur := section{heading: ""}
	var raw []string
	flush := func() {
		if len(raw) > 0 {
			cur.nodes = append(cur.nodes, node{kind: nodeRaw, lines: raw})
			raw = nil
		}
	}
	i := 0
	for i < len(lines) {
		ln := lines[i]
		if strings.HasPrefix(ln, "## ") {
			flush()
			d.sections = append(d.sections, cur)
			cur = section{heading: ln}
			i++
			continue
		}
		if st, ok := checkboxState(ln); ok {
			flush()
			n := itemBlockLen(lines, i)
			block := make([]string, n)
			copy(block, lines[i:i+n])
			cur.nodes = append(cur.nodes, node{kind: nodeItem, state: st, lines: block})
			i += n
			continue
		}
		raw = append(raw, ln)
		i++
	}
	flush()
	d.sections = append(d.sections, cur)
	return d
}

// checkboxState reports the state of a top-level checkbox line ("- [ ] ",
// "- [x] "). Indented lines are never top-level items.
func checkboxState(ln string) (State, bool) {
	if len(ln) < 5 || !strings.HasPrefix(ln, "- [") || ln[4] != ']' {
		return 0, false
	}
	switch ln[3] {
	case ' ':
		return StateOpen, true
	case 'x':
		return StateDone, true
	}
	return 0, false
}

// itemBlockLen returns how many lines belong to the item that starts at
// lines[start]: the checkbox line, its indented notes, and any blank line that
// is followed by more indented notes. Trailing blank lines are excluded so a
// blank separator between items stays as a raw node.
func itemBlockLen(lines []string, start int) int {
	n := 1
	pendingBlanks := 0
	for i := start + 1; i < len(lines); i++ {
		switch {
		case lines[i] == "":
			pendingBlanks++
		case isIndented(lines[i]):
			n += pendingBlanks + 1
			pendingBlanks = 0
		default:
			return n
		}
	}
	return n
}

func isIndented(ln string) bool {
	return len(ln) > 0 && (ln[0] == ' ' || ln[0] == '\t')
}
