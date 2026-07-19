package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// Tree renders a parent-child tree with two-space indentation per depth level.
func Tree(w io.Writer, root *entry.TreeNode) {
	var walk func(n *entry.TreeNode, depth int)
	walk = func(n *entry.TreeNode, depth int) {
		fmt.Fprintf(w, "%s%s  %s\n", strings.Repeat("  ", depth), ShortID(n.ID), n.Title)
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
}

// Tidy renders the consolidation review surface.
func Tidy(w io.Writer, report entry.TidyReport) {
	if len(report.Duplicates) == 0 && len(report.Diverged) == 0 && len(report.Superseded) == 0 {
		fmt.Fprintln(w, "nothing to tidy")
		return
	}
	if len(report.Duplicates) > 0 {
		fmt.Fprintln(w, "Duplicate titles:")
		for _, g := range report.Duplicates {
			fmt.Fprintf(w, "  %q (×%d)\n", g.NormalizedTitle, len(g.Entries))
			for _, e := range g.Entries {
				fmt.Fprintf(w, "    %s  %s  %s\n", ShortID(e.ID), e.Tier, e.Title)
			}
		}
	}
	if len(report.Diverged) > 0 {
		fmt.Fprintln(w, "Diverged (concurrent edits — see `kref log`/`kref diff`):")
		for _, e := range report.Diverged {
			fmt.Fprintf(w, "  %s  %s  %s\n", ShortID(e.ID), e.Tier, e.Title)
		}
	}
	if len(report.Superseded) > 0 {
		fmt.Fprintln(w, "Superseded:")
		for _, e := range report.Superseded {
			fmt.Fprintf(w, "  %s  %s  %s\n", ShortID(e.ID), e.Tier, e.Title)
		}
	}
}
