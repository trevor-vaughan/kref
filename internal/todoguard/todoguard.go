// Package todoguard is the single write-boundary policy for kind:todo entries.
// Every path that writes a todo body — the CLI update/edit commands and the MCP
// update/patch/remember tools — routes its proposed body through Guard, so the
// format-then-lint rule (spec b17c81d9b3e5 §8) is defined and tested once rather
// than duplicated across CLI and MCP. The CAS (stale-write) stage lands here in
// Plan 3b; this package is where it slots in.
package todoguard

import (
	"fmt"
	"strings"

	"github.com/trevor-vaughan/kref/internal/todo"
)

// TodoKind is the reserved entry kind that opts an entry into the todo grammar,
// the on-save formatter, the linter, and the cockpit.
const TodoKind = "todo"

// Options selects which guard stages run. The zero value runs every stage.
type Options struct {
	NoFmt  bool // skip the formatter (spec §8 step 1 opt-out)
	NoLint bool // skip the linter (spec §8 step 2 opt-out; a deliberate override)
}

// RejectedError reports that the linter found hard violations the formatter
// could not fix, so the write must be refused (fail-closed). It carries the
// violations for display and Body — the post-format bytes that were rejected —
// so callers can preserve the author's work for recovery (spec §8, no-work-loss).
type RejectedError struct {
	Violations []todo.Violation
	Body       string
}

func (e *RejectedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "todo body rejected: %d lint violation(s)", len(e.Violations))
	for _, v := range e.Violations {
		if v.Line > 0 {
			fmt.Fprintf(&b, "\n  line %d: %s: %s", v.Line, v.Rule, v.Msg)
		} else {
			fmt.Fprintf(&b, "\n  %s: %s", v.Rule, v.Msg)
		}
	}
	return b.String()
}

// StaleError reports a compare-and-swap failure: a todo write was based on a
// version other than the entry's current head (spec b17c81d9b3e5 §8 step 3).
// Base is the version the writer declared it read; Head is the entry's current
// body-version count (the vN kref log shows). Nothing is lost — the writer
// re-reads head and re-applies — but the write is refused rather than silently
// clobbering the intervening change that dropped v26/v27 and v33 on the todo.
type StaleError struct {
	Base int
	Head int
}

func (e *StaleError) Error() string {
	return fmt.Sprintf(
		"stale todo write: based on v%d but the entry is now at v%d; re-read it (kref show / kref_get) and re-apply your change",
		e.Base, e.Head)
}

// CheckVersion is the CAS stage of the todo write boundary. For a todo it
// returns a *StaleError when the base version the writer read differs from the
// current head; for any other kind it is a no-op. Callers invoke it only when
// they have a base to check — an omitted CLI --if-version skips CAS entirely
// (with a loud "unguarded" warning at the call site), while the MCP tools
// require the base and so always reach here.
func CheckVersion(kind string, base, head int) error {
	if kind != TodoKind {
		return nil
	}
	if base != head {
		return &StaleError{Base: base, Head: head}
	}
	return nil
}

// Guard applies the todo write-boundary policy to a proposed body for an entry
// of the given kind and returns the body that should be written.
//
// For any kind other than TodoKind it is a no-op: body is returned unchanged
// with a nil error. For a todo it formats the body (unless NoFmt), then lints
// the result (unless NoLint); if hard violations survive it returns a
// *RejectedError carrying those violations and the formatted body, and no write
// should occur. With NoLint the (formatted) body is returned even if it would
// not lint clean. With both NoFmt and NoLint it is equivalent to no guard.
func Guard(kind, body string, opts Options) (string, error) {
	if kind != TodoKind {
		return body, nil
	}
	out := body
	if !opts.NoFmt {
		formatted, err := todo.Format(body)
		if err != nil {
			return body, err
		}
		out = formatted
	}
	if !opts.NoLint {
		if vs := todo.Lint(out); len(vs) > 0 {
			return out, &RejectedError{Violations: vs, Body: out}
		}
	}
	return out, nil
}
