package todo

import (
	"strconv"
	"strings"
)

// headingLevel returns the ATX heading level (2 or 3) for a "## "/"### " line,
// or 0 for anything else (including the level-1 title and non-headings).
func headingLevel(ln string) int {
	n := 0
	for n < len(ln) && ln[n] == '#' {
		n++
	}
	if (n == 2 || n == 3) && n < len(ln) && ln[n] == ' ' {
		return n
	}
	return 0
}

// AnnotateHeadingCounts returns body with " (N)" appended to each level-2 or
// level-3 heading whose subtree holds N>0 open ("- [ ]") checkbox items. A
// heading's subtree runs until the next heading with level <= its own, so a
// parent count includes its children. Display-only: callers must not persist
// the result. Byte-identical to body when no heading has open items.
func AnnotateHeadingCounts(body string) string {
	lines := strings.Split(body, "\n")
	for i, ln := range lines {
		lvl := headingLevel(ln)
		if lvl == 0 {
			continue
		}
		count := 0
		for j := i + 1; j < len(lines); j++ {
			if hl := headingLevel(lines[j]); hl != 0 && hl <= lvl {
				break
			}
			if st, ok := checkboxState(lines[j]); ok && st == StateOpen {
				count++
			}
		}
		if count > 0 {
			lines[i] = ln + " (" + strconv.Itoa(count) + ")"
		}
	}
	return strings.Join(lines, "\n")
}
