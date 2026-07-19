package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/entryguard"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
	"github.com/trevor-vaughan/kref/internal/textdiff"
)

// unscannedWarn is printed when a body is written without a scanner available;
// the push boundary still refuses an unscanned syncable tier, so nothing leaves.
const unscannedWarn = "warning: betterleaks not found — body stored UNSCANNED; the push boundary still refuses without a scanner"

// entryParkFindings runs the entry-body secret guard for a CLI write. held is
// true when the body tripped the scanner on a syncable tier and must be
// quarantined (its findings are returned); unscanned is true when the scanner
// was unavailable (the caller writes normally but should warn). Non-refusal
// errors propagate.
func entryParkFindings(snap *entry.Snapshot, body string) (findings []scan.Finding, held, unscanned bool, err error) {
	unscanned, err = entryguard.Check(snap, body, false)
	var refused *entryguard.RefusedError
	if errors.As(err, &refused) {
		return refused.Findings, true, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	return nil, false, unscanned, nil
}

// parkComment handles a flagged comment write: it parks the intent via park,
// and when force is set (human/CLI only) immediately approves it — create+approve
// in one step — printing appliedMsg. Otherwise it reports the parked item. It
// always handles the write, so the caller returns its result directly.
func parkComment(cmd *cobra.Command, s *store.Store, force bool, findings []scan.Finding, appliedMsg string, park func([]scan.Finding) (store.Parked, error)) error {
	parked, err := park(findings)
	if err != nil {
		return err
	}
	if force {
		actor, actorKind := resolveActor(cmd, s)
		if aerr := s.ApproveQuarantine(parked.ItemID, "force-approved at write", actor, actorKind); aerr != nil {
			return aerr
		}
		fmt.Fprintln(cmd.OutOrStdout(), appliedMsg)
		return nil
	}
	printQuarantined(cmd, parked)
	return nil
}

// quarantineLine renders one review-queue item as a scannable line: its short
// id, what it is (held op on a target, or a new-entry draft to a tier), and the
// rule that flagged it when known.
func quarantineLine(it store.QuarantineItem, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s  ", render.ShortID(it.ID))
	if it.HeldOp {
		fmt.Fprintf(&b, "held %s → %s", it.OpKind, render.ShortID(it.Target))
		if it.TargetTitle != "" {
			fmt.Fprintf(&b, " %q", it.TargetTitle)
		}
	} else {
		fmt.Fprintf(&b, "new %s → %s", it.Kind, it.DestTier)
		if it.Title != "" {
			fmt.Fprintf(&b, " %q", it.Title)
		}
	}
	if len(it.Findings) > 0 {
		fmt.Fprintf(&b, "   %s", it.Findings[0].RuleID)
	}
	if rel := render.RelTime(now, it.CreatedAt); rel != "" {
		fmt.Fprintf(&b, "   held %s", rel)
	}
	if store.QuarantineStale(it, now) {
		fmt.Fprint(&b, " — STALE")
	}
	return b.String()
}

// renderQuarantineQueue writes the human view of the review queue, or (rejected)
// the tombstoned-rejected list with its recover/purge affordances.
func renderQuarantineQueue(w io.Writer, items []store.QuarantineItem, rejected bool) {
	if len(items) == 0 {
		if rejected {
			fmt.Fprintln(w, "no rejected writes.")
		} else {
			fmt.Fprintln(w, "no writes awaiting review.")
		}
		return
	}
	if rejected {
		fmt.Fprintf(w, "%d rejected write(s):\n", len(items))
	} else {
		fmt.Fprintf(w, "%d write(s) awaiting review:\n", len(items))
	}
	now := time.Now()
	for _, it := range items {
		fmt.Fprintln(w, quarantineLine(it, now))
	}
	if rejected {
		fmt.Fprintln(w, "recover: kref quarantine recover <id>   ·   purge: kref quarantine purge <id>")
	} else {
		fmt.Fprintln(w, "review: kref quarantine show <id>   ·   decide: kref quarantine approve|reject <id>")
		fmt.Fprintln(w, "(or press enter on a review row in `kref list` to do it interactively)")
	}
}

// renderQuarantineBanner writes the compact review-queue notice prepended to
// `kref list` so a held write is visible from the command users start with.
func renderQuarantineBanner(w io.Writer, items []store.QuarantineItem) {
	now := time.Now()
	stale := 0
	for _, it := range items {
		if store.QuarantineStale(it, now) {
			stale++
		}
	}
	if stale > 0 {
		fmt.Fprintf(w, "⚠ %d write(s) awaiting secret review, %d stale (kref quarantine list):\n", len(items), stale)
	} else {
		fmt.Fprintf(w, "⚠ %d write(s) awaiting secret review (kref quarantine list):\n", len(items))
	}
	for _, it := range items {
		fmt.Fprintln(w, quarantineLine(it, now))
	}
	fmt.Fprintln(w, strings.Repeat("─", 50))
}

// quarantineJSON is the --json shape of one review-queue item.
type quarantineJSON struct {
	ID       string `json:"id"`
	HeldOp   bool   `json:"held_op"`
	OpKind   string `json:"op_kind,omitempty"`
	Target   string `json:"target,omitempty"`
	Title    string `json:"title,omitempty"`
	DestTier string `json:"dest_tier,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Rule     string `json:"rule,omitempty"`
}

func quarantineJSONList(items []store.QuarantineItem) []quarantineJSON {
	out := make([]quarantineJSON, 0, len(items))
	for _, it := range items {
		j := quarantineJSON{ID: it.ID.String(), HeldOp: it.HeldOp, OpKind: it.OpKind, DestTier: it.DestTier, Kind: it.Kind}
		if it.HeldOp {
			j.Target = it.Target.String()
			j.Title = it.TargetTitle
		} else {
			j.Title = it.Title
		}
		if len(it.Findings) > 0 {
			j.Rule = it.Findings[0].RuleID
		}
		out = append(out, j)
	}
	return out
}

// renderQuarantineReview writes the review view of a quarantine item: what write
// is held, the flagging findings, and the proposed change — a current→proposed
// diff for a set-body op, else the proposed content wrapped to width. Reuses
// textdiff (the diff engine) and render.RenderBody (prose wrapping) so the review
// looks like the rest of kref.
func renderQuarantineReview(w io.Writer, d store.QuarantineDetail, color bool, width int) {
	it := d.Item
	if it.HeldOp {
		fmt.Fprintf(w, "held %s → %s", it.OpKind, render.ShortID(it.Target))
		if it.TargetTitle != "" {
			fmt.Fprintf(w, " %q", it.TargetTitle)
		}
		fmt.Fprintln(w)
	} else {
		fmt.Fprintf(w, "new %s → %s", it.Kind, it.DestTier)
		if it.Title != "" {
			fmt.Fprintf(w, " %q", it.Title)
		}
		fmt.Fprintln(w)
	}
	for _, f := range it.Findings {
		fmt.Fprintf(w, "  finding: %s (line %d)\n", f.RuleID, f.StartLine)
	}
	now := time.Now()
	if rel := render.RelTime(now, it.CreatedAt); rel != "" {
		line := "held " + rel
		if store.QuarantineStale(it, now) {
			line += " — STALE"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "─ proposed change %s\n", strings.Repeat("─", max(width-18, 3)))
	if it.HeldOp && it.OpKind == "set-body" {
		for _, l := range textdiff.Diff(d.CurrentBody, d.ProposedContent) {
			switch l.Op {
			case textdiff.Add:
				fmt.Fprintln(w, paintDiff(color, "\x1b[32m", "+ "+l.Text))
			case textdiff.Del:
				fmt.Fprintln(w, paintDiff(color, "\x1b[31m", "- "+l.Text))
			default:
				fmt.Fprintln(w, "  "+l.Text)
			}
		}
		return
	}
	render.RenderBody(w, d.ProposedContent, "text/markdown", color, width)
}

func paintDiff(color bool, code, s string) string {
	if !color {
		return s
	}
	return code + s + "\x1b[0m"
}

// printQuarantined reports a parked write on stdout.
func printQuarantined(cmd *cobra.Command, p store.Parked) {
	fmt.Fprintf(cmd.OutOrStdout(),
		"quarantined as %s (%d findings) — held for human review, not applied and not lost. "+
			"A human approves it with `kref quarantine` (see `kref quarantine show %s`).\n",
		p.ItemID, len(p.Findings), p.ItemID)
}
