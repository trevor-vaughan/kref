package render

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// ExtendedHeader renders the show header expanded with the entry's history and
// links: the base rows plus Created, Edited, Editors, the last ten body
// versions, and outgoing/incoming links. now anchors the relative Edited time.
func ExtendedHeader(w io.Writer, snap *entry.Snapshot, now time.Time, log []entry.LogEntry, links entry.LinkView, color bool, trackedNote string, favorites []string) {
	rows := baseHeaderRows(snap, color, trackedNote, favorites)
	rows = append(rows, extHeaderRows(now, log, links)...)
	writeHeaderRows(w, rows)
}

const maxVersionRows = 10

func extHeaderRows(now time.Time, log []entry.LogEntry, links entry.LinkView) []hdrRow {
	rc := utf8.RuneCountInString
	var rows []hdrRow
	add := func(label, value string) { rows = append(rows, hdrRow{label, value, rc(value)}) }

	// Created — the create op.
	for _, e := range log {
		if e.Op == "create" {
			add("Created", fmt.Sprintf("%s by %s", e.Time.Format("2006-01-02"), e.Author))
			break
		}
	}

	// set-body ops, newest first.
	var edits []entry.LogEntry
	for _, e := range log {
		if e.Op == "set-body" && e.Version > 0 {
			edits = append(edits, e)
		}
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].Time.After(edits[j].Time) })

	// Edited — latest set-body: relative (abs) · vN.
	if len(edits) > 0 {
		last := edits[0]
		abs := last.Time.Format("2006-01-02")
		v := ""
		if rel := RelTime(now, last.Time); rel != "" {
			v = fmt.Sprintf("%s (%s) · v%d", rel, abs, last.Version)
		} else {
			v = fmt.Sprintf("%s · v%d", abs, last.Version)
		}
		add("Edited", v)
	}

	// Editors — distinct op authors with counts, most active first.
	counts := map[string]int{}
	var order []string
	for _, e := range log {
		if _, seen := counts[e.Author]; !seen {
			order = append(order, e.Author)
		}
		counts[e.Author]++
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	if len(order) > 0 {
		parts := make([]string, len(order))
		for i, a := range order {
			parts[i] = fmt.Sprintf("%s (%d)", a, counts[a])
		}
		add("Editors", strings.Join(parts, ", "))
	}

	// Versions — up to ten, newest first: vN  date time  author  change summary.
	// The op log's Detail already carries the per-version diff stat prefixed with
	// "vN  "; strip that prefix so the version number is not repeated.
	for i, e := range edits {
		if i >= maxVersionRows {
			break
		}
		label := ""
		if i == 0 {
			label = "Versions"
		}
		row := fmt.Sprintf("v%d  %s  %s", e.Version, e.Time.Format("2006-01-02 15:04"), e.Author)
		if summary := strings.TrimPrefix(e.Detail, fmt.Sprintf("v%d  ", e.Version)); summary != "" {
			row += "  " + summary
		}
		add(label, row)
	}

	// Links — outgoing then incoming; a "no links" line when both are empty.
	if len(links.Outgoing) == 0 && len(links.Incoming) == 0 {
		add("Links", "no links")
	} else {
		first := true
		emit := func(dir string, refs []entry.LinkRef) {
			for _, l := range refs {
				label := ""
				if first {
					label = "Links"
					first = false
				}
				add(label, fmt.Sprintf("%-4s %-12s %s  %s", dir, l.Type, ShortID(l.ID), l.Title))
			}
		}
		emit("out:", links.Outgoing)
		emit("in:", links.Incoming)
	}
	return rows
}
