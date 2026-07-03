// Package textdiff computes a dependency-free line diff between two text
// bodies. It backs `kref log`'s per-version change stats and `kref diff`'s
// inline diff rendering. Entry bodies are notes-sized, so a plain LCS dynamic
// program is ample; a size guard keeps pathological inputs linear.
package textdiff

// Op classifies one line of a diff.
type Op int

const (
	Same Op = iota
	Add
	Del
)

// Line is one line of diff output: the operation and the line text
// (without its trailing newline).
type Line struct {
	Op   Op
	Text string
}

// DiffStats summarizes a diff: whole removed lines count toward
// CharsRemoved, whole added lines toward CharsAdded (a modified line is one
// removal plus one addition — both sides are counted, which is the honest
// reading of "changed").
type DiffStats struct {
	LinesAdded   int `json:"lines_added"`
	LinesRemoved int `json:"lines_removed"`
	CharsAdded   int `json:"chars_added"`
	CharsRemoved int `json:"chars_removed"`
}

// lcsGuard bounds the DP table size. Beyond it the diff degrades to
// delete-all/add-all, which keeps worst-case memory and time linear; entry
// bodies never get near it in practice.
const lcsGuard = 20_000

// splitLines splits into lines without inventing a trailing empty line for
// newline-terminated input. An empty string has no lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	if s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// Diff returns the line-by-line difference from a to b, in order: unchanged
// lines as Same, lines only in a as Del, lines only in b as Add.
func Diff(a, b string) []Line {
	al, bl := splitLines(a), splitLines(b)
	if len(al)*len(bl) > lcsGuard*lcsGuard/400 { // ~1e6 cells ≈ 4MB of ints
		out := make([]Line, 0, len(al)+len(bl))
		for _, l := range al {
			out = append(out, Line{Del, l})
		}
		for _, l := range bl {
			out = append(out, Line{Add, l})
		}
		return out
	}

	// Standard LCS table: lcs[i][j] = LCS length of al[i:] and bl[j:].
	lcs := make([][]int, len(al)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(bl)+1)
	}
	for i := len(al) - 1; i >= 0; i-- {
		for j := len(bl) - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	out := make([]Line, 0, len(al)+len(bl))
	i, j := 0, 0
	for i < len(al) && j < len(bl) {
		switch {
		case al[i] == bl[j]:
			out = append(out, Line{Same, al[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, Line{Del, al[i]})
			i++
		default:
			out = append(out, Line{Add, bl[j]})
			j++
		}
	}
	for ; i < len(al); i++ {
		out = append(out, Line{Del, al[i]})
	}
	for ; j < len(bl); j++ {
		out = append(out, Line{Add, bl[j]})
	}
	return out
}

// Stats reduces Diff(a, b) to added/removed line and character counts.
func Stats(a, b string) DiffStats {
	var s DiffStats
	for _, l := range Diff(a, b) {
		switch l.Op {
		case Add:
			s.LinesAdded++
			s.CharsAdded += len(l.Text)
		case Del:
			s.LinesRemoved++
			s.CharsRemoved += len(l.Text)
		}
	}
	return s
}
