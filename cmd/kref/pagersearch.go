package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/trevor-vaughan/kref/internal/tui"
)

// pagerSearch is the shared incremental-search state for the pager and cockpit: a
// query-input line, the current match list (content-line offsets), and n/N
// cycling. The host routes /,n,N,enter,esc,backspace,runes to it; matching is
// injected (the pager searches its gutter-free lines, the cockpit its ScrollView
// content) and scroll-to goes through the given ScrollView.
type pagerSearch struct {
	active  bool // capturing the query (input line shown)
	query   string
	matches []int // content-line offsets of the committed query
	idx     int   // current match
}

// start begins capturing a fresh query.
func (p *pagerSearch) start() { p.active, p.query = true, "" }

// searching reports whether the query input line is active.
func (p *pagerSearch) searching() bool { return p.active }

// input handles a key while the query line is active: enter commits (computes
// matches via matcher and scrolls to the first), esc cancels, backspace/runes
// edit. Returns true when the key was consumed by the search input.
func (p *pagerSearch) input(msg tea.KeyMsg, matcher func(string) []int, sv *tui.ScrollView) bool {
	if !p.active {
		return false
	}
	switch msg.String() {
	case "enter":
		p.active = false
		p.matches = matcher(p.query)
		p.idx = 0
		p.jump(sv)
	case "esc":
		p.active, p.query = false, ""
	case "backspace":
		if p.query != "" {
			p.query = p.query[:len(p.query)-1]
		}
	default:
		if len(msg.Runes) > 0 {
			p.query += string(msg.Runes)
		}
	}
	return true
}

// cycle moves to the next (dir=1) / previous (dir=-1) match and scrolls to it.
func (p *pagerSearch) cycle(dir int, sv *tui.ScrollView) {
	if len(p.matches) == 0 {
		return
	}
	p.idx = (p.idx + dir + len(p.matches)) % len(p.matches)
	p.jump(sv)
}

// jump scrolls to the current match. Hosts expand their folds when a query is
// committed (see the search-input handling) so a fold never hides a hit.
func (p *pagerSearch) jump(sv *tui.ScrollView) {
	if len(p.matches) == 0 {
		return
	}
	sv.SetYOffset(p.matches[p.idx])
}

// refresh recomputes matches for the current query against fresh content (after a
// reload or re-render), clamping the index.
func (p *pagerSearch) refresh(matcher func(string) []int) {
	p.matches = matcher(p.query)
	if p.idx >= len(p.matches) {
		p.idx = 0
	}
}

// footer returns the search indicator: the live "/query" prompt while capturing,
// the "match i/N" count when there are results, or "" otherwise.
func (p *pagerSearch) footer() string {
	if p.active {
		return "/" + p.query
	}
	if len(p.matches) > 0 {
		return fmt.Sprintf("match %d/%d", p.idx+1, len(p.matches))
	}
	return ""
}
