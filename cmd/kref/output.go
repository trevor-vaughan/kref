package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/trevor-vaughan/kref/internal/bridge"
	"github.com/trevor-vaughan/kref/internal/render"
)

// jsonMode reports whether the inherited persistent --json flag is set.
func jsonMode(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// plainMode reports whether the inherited persistent --plain flag is set:
// chrome-free line-oriented output (TSV lists, verbatim show body). Plain is a
// machine contract, so it also suppresses color and the pager.
func plainMode(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("plain")
	return v
}

// useColor enables ANSI color only for human output to an interactive
// terminal, and never when NO_COLOR is set. KREF_COLOR=1 forces color on and
// KREF_COLOR=0 forces it off, overriding both NO_COLOR and terminal detection
// (recording environments like VHS set NO_COLOR in the session and pipes are
// not terminals — the override is the escape hatch for both). --json always
// wins: machine output is never colored. Any other KREF_COLOR value falls
// through to auto-detection, which keys off the real stdout fd; under
// `go test` (and pipes) it is not a terminal, so output is plain and
// deterministic.
func useColor(cmd *cobra.Command) bool {
	if jsonMode(cmd) {
		return false
	}
	if plainMode(cmd) {
		return false
	}
	switch os.Getenv("KREF_COLOR") {
	case "1":
		return true
	case "0":
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// usePager reports whether show should route through the interactive pager:
// human output to a real terminal. The caller additionally honors --no-pager.
func usePager(cmd *cobra.Command) bool {
	if jsonMode(cmd) {
		return false
	}
	if plainMode(cmd) {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// ttyWidth returns stdout's terminal width, 80 as a fallback when the size is
// unavailable, or 0 when stdout is not a terminal (so rendering stays unwrapped
// for pipes).
func ttyWidth() int {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return 0
	}
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// ingestSummary renders per-file ingest results and a tallied footer. It lives
// here, not in render, so the render package stays free of a bridge import.
func ingestSummary(w io.Writer, results []bridge.IngestResult, color, warnUnscanned bool) {
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Action]++
		if r.Action == "error" {
			fmt.Fprintf(w, "  %-11s %s: %s\n", "error", r.Path, r.Error)
			continue
		}
		line := fmt.Sprintf("  %-11s %s %s  %s", r.Action, render.Tier(r.Tier, r.TierType, color), render.ShortID(r.ID), r.Path)
		if r.ContentType != "" && r.ContentType != "text/markdown" {
			line += "  (" + r.ContentType + ")"
		}
		if r.Quarantined {
			line += "  (quarantined → private)"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "\n%d created, %d updated, %d unchanged, %d quarantined, %d failed\n",
		counts["created"], counts["updated"], counts["unchanged"], counts["quarantined"], counts["error"])
	unscanned := 0
	for _, r := range results {
		if r.Unscanned {
			unscanned++
		}
	}
	// The UNSCANNED warning is advisory and can be silenced via the config key
	// warn_unscanned: false. This gates ONLY the warning — the sync-push
	// boundary still fail-closes on a missing scanner regardless.
	if unscanned > 0 && warnUnscanned {
		fmt.Fprintf(w, "\nwarning: betterleaks not found — %d file(s) stored UNSCANNED; secrets cannot be quarantined.\n", unscanned)
		fmt.Fprintln(w, "Install it: `go install github.com/betterleaks/betterleaks@latest` (or set KREF_BETTERLEAKS).")
	}
	if n := counts["quarantined"]; n > 0 {
		fmt.Fprintf(w, "\n%d file(s) matched secret patterns and were quarantined to the private tier (unpushable).\n", n)
		fmt.Fprintln(w, "Review one with `kref show <id>`. If it's a false positive, move it: `kref retier <id> shared`.")
		fmt.Fprintln(w, "If it's a real secret, rotate it and `kref purge <id>` — do not retier it to a syncable tier.")
	}
}

// emit routes a command's output: frozen JSON under --json, otherwise the
// human closure with a resolved color flag.
func emit(cmd *cobra.Command, human func(w io.Writer, color bool), jsonValue any) error {
	if jsonMode(cmd) {
		return writeJSON(cmd, jsonValue)
	}
	human(cmd.OutOrStdout(), useColor(cmd))
	return nil
}
