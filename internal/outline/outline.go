// Package outline parses a markdown body into its ATX-heading tree and renders it
// with folds applied. It is pure (no TUI deps) and is the single fold model shared
// by kref's pager and todo cockpit.
package outline

import (
	"fmt"
	"strings"
)

// Heading is one ATX heading with its position and a stable nesting identity.
type Heading struct {
	Path  string // ancestor heading texts + this text, joined by \x1f — dedups duplicate titles
	Level int    // 1..6
	Line  int    // 0-based index of the heading line within the body
	Text  string // heading text, leading #s and surrounding space stripped
}

// Outline is the heading structure of a markdown body.
type Outline struct {
	lines    []string
	headings []Heading
}

// headingLevel returns the ATX level (1..6) of a line, or 0 if it is not a heading.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

// Parse splits body into lines and records its ATX headings with nesting paths.
func Parse(body string) *Outline {
	lines := strings.Split(body, "\n")
	o := &Outline{lines: lines}
	var stack []Heading // ancestors by increasing level
	for i, ln := range lines {
		lvl := headingLevel(ln)
		if lvl == 0 {
			continue
		}
		text := strings.TrimSpace(ln[lvl:])
		for len(stack) > 0 && stack[len(stack)-1].Level >= lvl {
			stack = stack[:len(stack)-1]
		}
		parts := make([]string, 0, len(stack)+1)
		for _, a := range stack {
			parts = append(parts, a.Text)
		}
		parts = append(parts, text)
		h := Heading{Path: strings.Join(parts, "\x1f"), Level: lvl, Line: i, Text: text}
		o.headings = append(o.headings, h)
		stack = append(stack, h)
	}
	return o
}

// Headings returns the headings in document order.
func (o *Outline) Headings() []Heading {
	out := make([]Heading, len(o.headings))
	copy(out, o.headings)
	return out
}

// spanEnd returns the exclusive line index where heading i's section ends: the
// next heading of level <= headings[i].Level, or len(lines).
func (o *Outline) spanEnd(i int) int {
	lvl := o.headings[i].Level
	for j := i + 1; j < len(o.headings); j++ {
		if o.headings[j].Level <= lvl {
			return o.headings[j].Line
		}
	}
	return len(o.lines)
}

// Render returns the body with each folded heading's content (the lines after the
// heading up to spanEnd) replaced by a single "▸ N lines" hint. Outer folds win:
// a line hidden by an ancestor fold is not emitted, so an inner folded heading
// inside a folded ancestor never appears.
func (o *Outline) Render(folded map[string]bool) string {
	// Map heading line -> heading index for quick lookup while walking lines.
	byLine := make(map[int]int, len(o.headings))
	for i, h := range o.headings {
		byLine[h.Line] = i
	}
	var out []string
	skipUntil := -1 // exclusive line index we are fast-forwarding to (inside a fold)
	for ln := range o.lines {
		if ln < skipUntil {
			continue
		}
		out = append(out, o.lines[ln])
		if i, ok := byLine[ln]; ok && folded[o.headings[i].Path] {
			end := o.spanEnd(i)
			hidden := end - (ln + 1)
			if hidden > 0 {
				out = append(out, fmt.Sprintf("▸ %d lines", hidden))
				skipUntil = end
			}
		}
	}
	// Split keeps a trailing "" for a body ending in "\n"; drop the phantom
	// trailing blank so Render(nil) is the body without its trailing newline.
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// HeadingAt returns the innermost heading whose section (heading line through
// spanEnd) contains body line n. Used by the cursorless pager to fold "the
// section I'm scrolled into". Returns false when n precedes the first heading.
func (o *Outline) HeadingAt(n int) (Heading, bool) {
	best := -1
	for i := range o.headings {
		if o.headings[i].Line <= n && n < o.spanEnd(i) {
			best = i // later matches are deeper (higher Line), so keep the last
		}
	}
	if best < 0 {
		return Heading{}, false
	}
	return o.headings[best], true
}

// AllPaths returns every heading path, for collapse-all.
func (o *Outline) AllPaths() []string {
	out := make([]string, len(o.headings))
	for i, h := range o.headings {
		out[i] = h.Path
	}
	return out
}
