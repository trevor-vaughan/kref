// Package textpatch applies unified diffs to a text body — the fine-grained
// alternative to full-body replacement behind the MCP kref_patch tool (agents
// send diffs; humans use `kref edit`). Application is LENIENT the way
// LLM-diff appliers must be: hunk line numbers are treated as hints only, and
// each hunk is located by its context/removal lines, which must match the
// current body. It is strict where safety demands it: a hunk whose context is
// missing (stale diff) or ambiguous (identical sections, no usable hint)
// fails loudly, and application is all-or-nothing — any failing hunk aborts
// the whole patch.
package textpatch

import (
	"errors"
	"fmt"
	"strings"
)

// hunkLine is one line of a hunk body: op is ' ' (context), '-' (removal),
// or '+' (addition); text is the line without its op prefix.
type hunkLine struct {
	op   byte
	text string
}

// hunk is one @@ section. hint is the 1-based old-file start line from the
// hunk header, 0 when absent/unparseable — a tiebreaker, never a locator.
type hunk struct {
	hint  int
	lines []hunkLine
}

// headerPrefixes are per-file diff furniture: they never appear inside a hunk
// body, so seeing one closes the current hunk. (A removed body line that
// itself began with "-- " would render as "--- " and be misread here; that
// ambiguity is inherent to the unified-diff text format.)
var headerPrefixes = []string{"diff ", "index ", "--- ", "+++ "}

func isHeader(line string) bool {
	for _, p := range headerPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// parseHint extracts the 1-based old-file start line from a hunk header like
// "@@ -12,5 +14,6 @@", returning 0 when there is none ("@@ @@" and friends).
func parseHint(header string) int {
	i := strings.IndexByte(header, '-')
	if i < 0 {
		return 0
	}
	n, seen := 0, false
	for j := i + 1; j < len(header) && header[j] >= '0' && header[j] <= '9'; j++ {
		n, seen = n*10+int(header[j]-'0'), true
	}
	if !seen {
		return 0
	}
	return n
}

// parse reads unified-diff text into hunks. Everything before the first @@
// (git preamble, file headers, prose) is ignored; inside a hunk, a bare blank
// line is treated as blank context (trailing-space stripping is rampant in
// transit) and the "\ No newline at end of file" marker is skipped. A line
// with no recognized prefix is kept as context — the common LLM slip of
// dropping the leading space — which at worst turns into a loud
// context-not-found error at apply time, never a silent misread.
func parse(diff string) ([]hunk, error) {
	var hunks []hunk
	inHunk := false
	dlines := strings.Split(diff, "\n")
	// The diff text's own trailing newline yields a final empty element; it is
	// framing, not a blank context line, so drop it. Interior blank lines
	// (genuine blank context with the space stripped) still parse below.
	if n := len(dlines); n > 0 && dlines[n-1] == "" {
		dlines = dlines[:n-1]
	}
	for _, line := range dlines {
		switch {
		case strings.HasPrefix(line, "@@"):
			hunks = append(hunks, hunk{hint: parseHint(line)})
			inHunk = true
		case !inHunk:
			// preamble/furniture before the first hunk — ignore
		case isHeader(line):
			inHunk = false // a new file section; its furniture is not hunk content
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" — metadata, not content
		case line == "":
			cur := &hunks[len(hunks)-1]
			cur.lines = append(cur.lines, hunkLine{op: ' ', text: ""})
		case line[0] == '+' || line[0] == '-' || line[0] == ' ':
			cur := &hunks[len(hunks)-1]
			cur.lines = append(cur.lines, hunkLine{op: line[0], text: line[1:]})
		default:
			cur := &hunks[len(hunks)-1]
			cur.lines = append(cur.lines, hunkLine{op: ' ', text: line})
		}
	}
	if len(hunks) == 0 {
		return nil, errors.New("no unified-diff hunks found (expected @@ sections)")
	}
	return hunks, nil
}

// old and new return the hunk's before-image (context + removals) and
// after-image (context + additions), in order.
func (h hunk) old() []string {
	var out []string
	for _, l := range h.lines {
		if l.op == ' ' || l.op == '-' {
			out = append(out, l.text)
		}
	}
	return out
}

func (h hunk) new() []string {
	var out []string
	for _, l := range h.lines {
		if l.op == ' ' || l.op == '+' {
			out = append(out, l.text)
		}
	}
	return out
}

// findAll returns every index in lines where seq matches under eq.
func findAll(lines, seq []string, eq func(a, b string) bool) []int {
	var out []int
	for i := 0; i+len(seq) <= len(lines); i++ {
		match := true
		for j := range seq {
			if !eq(lines[i+j], seq[j]) {
				match = false
				break
			}
		}
		if match {
			out = append(out, i)
		}
	}
	return out
}

func exactEq(a, b string) bool { return a == b }

// trimEq compares ignoring trailing whitespace — the drift editors and
// copy/paste most often introduce. Leading whitespace stays significant
// (indentation is content).
func trimEq(a, b string) bool {
	return strings.TrimRight(a, " \t") == strings.TrimRight(b, " \t")
}

// locate picks the position for a hunk's before-image. Preference order:
// exact matches over trailing-whitespace-lenient ones; positions at/after
// `from` (hunks apply in order) over earlier ones; and among several
// survivors, the one nearest the header's line hint. Ambiguity without a
// hint is an error — guessing between identical sections is exactly the
// silent mis-edit this applier exists to refuse.
func locate(lines, seq []string, from, hint, n int) (int, error) {
	cands := findAll(lines, seq, exactEq)
	if len(cands) == 0 {
		cands = findAll(lines, seq, trimEq)
	}
	if len(cands) == 0 {
		return 0, fmt.Errorf("hunk %d: context not found in the current body (the entry may have changed — re-read it)", n)
	}
	pool := make([]int, 0, len(cands))
	for _, c := range cands {
		if c >= from {
			pool = append(pool, c)
		}
	}
	if len(pool) == 0 {
		pool = cands
	}
	if len(pool) == 1 {
		return pool[0], nil
	}
	if hint <= 0 {
		return 0, fmt.Errorf("hunk %d: context matches %d locations and the hunk header gives no line hint — add surrounding context lines", n, len(pool))
	}
	best, bestDist := pool[0], -1
	for _, c := range pool {
		d := c - (hint - 1)
		if d < 0 {
			d = -d
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = c, d
		}
	}
	return best, nil
}

// Apply applies unified-diff text to body. Hunks apply in order, each
// searched from just after the previous application (which naturally walks
// repeated similar sections). Any failure returns an error naming the hunk;
// nothing partial is ever returned.
func Apply(body, diff string) (string, error) {
	hunks, err := parse(diff)
	if err != nil {
		return "", err
	}
	lines := strings.Split(body, "\n")
	from := 0
	for i, h := range hunks {
		oldSeq, newSeq := h.old(), h.new()
		if len(oldSeq) == 0 {
			return "", fmt.Errorf("hunk %d: no context or removal lines — nothing to locate it by", i+1)
		}
		pos, err := locate(lines, oldSeq, from, h.hint, i+1)
		if err != nil {
			return "", err
		}
		next := make([]string, 0, len(lines)-len(oldSeq)+len(newSeq))
		next = append(next, lines[:pos]...)
		next = append(next, newSeq...)
		next = append(next, lines[pos+len(oldSeq):]...)
		lines = next
		from = pos + len(newSeq)
	}
	return strings.Join(lines, "\n"), nil
}
