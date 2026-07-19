package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/store"
	"github.com/trevor-vaughan/kref/internal/todo"
	"github.com/trevor-vaughan/kref/internal/todoguard"
)

// guardKindToTodo reports whether an entry's current body would satisfy the todo
// grammar if its kind were changed to todo. It returns a non-nil *RejectedError
// (carrying the violations) when the body does not lint — so callers can refuse
// the kind change and a non-conforming entry can't become an un-lintable todo —
// or a real error on a store failure. A body that lints (possibly after
// auto-format) yields (nil, nil).
func guardKindToTodo(s *store.Store, id entity.Id) (*todoguard.RejectedError, error) {
	snap, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	_, gerr := todoguard.Guard(todoguard.TodoKind, snap.Body, todoguard.Options{})
	var rej *todoguard.RejectedError
	if errors.As(gerr, &rej) {
		return rej, nil
	}
	return nil, gerr
}

// guardTodoWrite runs the todo write-boundary guard for a non-interactive CLI
// write and returns the body to write (formatted, for a todo). --no-fmt/--no-lint
// warn loudly when they disable a stage on a todo (mirroring the sync push
// --force secret escape). On a lint rejection it preserves the rejected body to
// a recovery file and returns an error naming it, so a fail-closed write never
// drops the author's content.
func guardTodoWrite(cmd *cobra.Command, id entity.Id, kind, body string, noFmt, noLint bool) (string, error) {
	if kind == todoguard.TodoKind {
		if noFmt {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: --no-fmt: writing todo %s without formatting\n", render.ShortID(id))
		}
		if noLint {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: --no-lint: writing todo %s without grammar validation\n", render.ShortID(id))
		}
	}
	out, err := todoguard.Guard(kind, body, todoguard.Options{NoFmt: noFmt, NoLint: noLint})
	var rej *todoguard.RejectedError
	if errors.As(err, &rej) {
		path, werr := todoguard.WriteRejected(id.String(), rej.Body)
		if werr != nil {
			return "", fmt.Errorf("%w (could not save recovery file: %w)", err, werr)
		}
		return "", fmt.Errorf("%w\nyour rejected body was saved to %s — fix it and retry, or pass --no-lint to override", err, path)
	}
	return out, err
}

// guardTodoCAS runs the CLI update CAS stage (spec §8 step 3) for a body write.
// For a non-todo kind it is a no-op. For a todo: with --if-version given it
// refuses the write when the entry's head version has moved off the declared
// base (preserving the proposed body to a recovery file so nothing is lost);
// with --if-version omitted it writes but prints a loud "unguarded todo write"
// warning, mirroring the deliberate-override tone of --no-lint.
func guardTodoCAS(cmd *cobra.Command, s *store.Store, id entity.Id, kind string, ifVersion int, ifVersionSet bool, body string) error {
	if kind != todoguard.TodoKind {
		return nil
	}
	if !ifVersionSet {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: unguarded todo write to %s (no --if-version); a concurrent edit could be clobbered\n", render.ShortID(id))
		return nil
	}
	head, err := s.Get(id)
	if err != nil {
		return err
	}
	cerr := todoguard.CheckVersion(kind, ifVersion, head.Version)
	if cerr == nil {
		return nil
	}
	path, werr := todoguard.WriteRejected(id.String(), body)
	if werr != nil {
		return fmt.Errorf("%w (could not save recovery file: %w)", cerr, werr)
	}
	return fmt.Errorf("%w\nyour rejected body was saved to %s — re-read the entry and re-apply", cerr, path)
}

// lintBannerLead marks the HTML-comment block kref edit prepends to a rejected
// todo body so the author sees the violations in their editor. It leads with
// REJECTED / NOT applied because the editor's alt-screen buries the stderr
// notice — this comment is the only in-context signal that the save failed, so
// it must read as an alert, not advice. It is an HTML comment (invisible
// markdown) rather than a #-prefixed line, which would collide with the todo's
// H1. stripLintBanner removes it on the next read.
const lintBannerLead = "<!-- ⚠  REJECTED — your last save was NOT applied. Fix the kref todo lint issue(s) below, then save again (this comment auto-deletes on save):"

// lintBanner renders the reject banner (trailing blank line included) for the
// given violations.
func lintBanner(vs []todo.Violation) string {
	var b strings.Builder
	b.WriteString(lintBannerLead + "\n")
	for _, v := range vs {
		if v.Line > 0 {
			fmt.Fprintf(&b, "  line %d: %s: %s\n", v.Line, v.Rule, v.Msg)
		} else {
			fmt.Fprintf(&b, "  %s: %s\n", v.Rule, v.Msg)
		}
	}
	b.WriteString("-->\n\n")
	return b.String()
}

// stripLintBanner removes a leading lintBanner block if present, returning the
// bare body. A body the author never had a banner on is returned unchanged.
func stripLintBanner(s string) string {
	if !strings.HasPrefix(s, lintBannerLead) {
		return s
	}
	_, after, ok := strings.Cut(s, "\n-->")
	if !ok {
		return s
	}
	rest := after
	rest = strings.TrimPrefix(rest, "\n") // end the "-->" line
	rest = strings.TrimPrefix(rest, "\n") // the blank spacer line
	return rest
}
