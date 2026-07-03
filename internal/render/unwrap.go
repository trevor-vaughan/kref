package render

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// UnwrapMarkdown joins soft-wrapped continuation lines inside paragraphs and
// tight list items (goldmark TextBlocks) so glamour can reflow them to the
// display width — glamour renders soft breaks inside list items as literal
// newlines, which keeps LLM-authored 78-column source unreadably choppy.
// Hard breaks (trailing two spaces or a backslash) are preserved, and every
// line outside a paragraph — fences, indented code, tables, headings, HTML,
// blanks — passes through verbatim. CRLF input comes out LF. Parsing uses the
// GFM extensions to match glamour's block grammar (a table must not be
// mistaken for a joinable paragraph).
func UnwrapMarkdown(src string) string {
	if src == "" {
		return src
	}
	source := []byte(src)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := md.Parser().Parse(text.NewReader(source))

	// lineStarts[i] is the byte offset where source line i begins.
	lineStarts := []int{0}
	for i, b := range source {
		if b == '\n' && i+1 < len(source) {
			lineStarts = append(lineStarts, i+1)
		}
	}
	lineOf := func(off int) int { // index of the line containing offset off
		lo, hi := 0, len(lineStarts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if lineStarts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo
	}
	lineEnd := func(i int) int { // offset just past line i's content (no \n, no \r)
		end := len(source)
		if i+1 < len(lineStarts) {
			end = lineStarts[i+1] - 1
		} else if source[end-1] == '\n' {
			end--
		}
		if end > lineStarts[i] && source[end-1] == '\r' {
			end--
		}
		return end
	}
	hardBreak := func(i int) bool {
		end, start := lineEnd(i), lineStarts[i]
		if end > start && source[end-1] == '\\' {
			return true
		}
		return end-start >= 2 && source[end-1] == ' ' && source[end-2] == ' '
	}

	// Multi-line paragraph blocks: first source line -> that block's segments.
	joinStart := map[int][]text.Segment{}
	consumed := map[int]bool{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if k := n.Kind(); k != ast.KindParagraph && k != ast.KindTextBlock {
			return ast.WalkContinue, nil
		}
		lines := n.Lines()
		if lines.Len() < 2 {
			return ast.WalkContinue, nil
		}
		segs := make([]text.Segment, lines.Len())
		for i := 0; i < lines.Len(); i++ {
			segs[i] = lines.At(i)
		}
		joinStart[lineOf(segs[0].Start)] = segs
		for _, s := range segs[1:] {
			consumed[lineOf(s.Start)] = true
		}
		return ast.WalkContinue, nil
	})

	var out strings.Builder
	out.Grow(len(source))
	for i := 0; i < len(lineStarts); i++ {
		if consumed[i] {
			continue
		}
		segs, ok := joinStart[i]
		if !ok {
			out.Write(source[lineStarts[i]:lineEnd(i)])
			out.WriteByte('\n')
			continue
		}
		// Split the block into runs at hard-break boundaries; each run joins
		// into one output line.
		var runs [][]text.Segment
		cur := []text.Segment{segs[0]}
		for _, seg := range segs[1:] {
			if hardBreak(lineOf(cur[len(cur)-1].Start)) {
				runs = append(runs, cur)
				cur = []text.Segment{seg}
			} else {
				cur = append(cur, seg)
			}
		}
		runs = append(runs, cur)
		for _, run := range runs {
			first := lineOf(run[0].Start)
			// The run's first line keeps its structural prefix (list marker,
			// "> ", indent); continuation prefixes are dropped by the join.
			out.Write(source[lineStarts[first]:run[0].Start])
			for j, seg := range run {
				ln := lineOf(seg.Start)
				raw := string(source[seg.Start:lineEnd(ln)])
				if j < len(run)-1 {
					raw = strings.TrimRight(raw, " \t") // soft junction: collapse to one space
				}
				if j > 0 {
					out.WriteByte(' ')
				}
				out.WriteString(raw)
			}
			out.WriteByte('\n')
		}
	}
	res := out.String()
	if !strings.HasSuffix(src, "\n") {
		res = strings.TrimSuffix(res, "\n")
	}
	return res
}
