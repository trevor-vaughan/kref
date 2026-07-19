package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/store"
	"github.com/trevor-vaughan/kref/internal/todo"
	"github.com/trevor-vaughan/kref/internal/watermark"
)

func newTodoCmd(dir *string) *cobra.Command {
	var full, noPager bool
	c := &cobra.Command{
		Use:     "todo [id]",
		Short:   "Show the todo cockpit (or: show / fmt / lint)",
		Example: exampleBlock([]string{"kref todo", "kref todo show a1b2c3d4", "kref todo fmt", "kref todo lint"}),
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			return runTodoCockpit(cmd, dir, arg, full, noPager)
		},
	}
	c.Flags().BoolVar(&full, "full", false, "expand the Done section instead of collapsing it")
	c.Flags().BoolVar(&noPager, "no-pager", false, "do not launch the interactive cockpit; print the static view")
	// The id targets live under `show`; the parent completes only its subcommand
	// names, so `kref todo <TAB>` lists show/fmt/lint rather than mixing verbs
	// and ids. `kref todo <id>` still runs (handled above), it just isn't
	// tab-suggested here.
	c.ValidArgsFunction = cobra.NoFileCompletions
	c.AddCommand(newTodoShowCmd(dir), newTodoFmtCmd(dir), newTodoLintCmd(dir))
	return c
}

// runTodoCockpit renders the cockpit for the resolved todo (the sole/default
// entry, or the one named by arg). Shared by `kref todo` and `kref todo show`.
// noPager forces the static render even on an interactive terminal.
func runTodoCockpit(cmd *cobra.Command, dir *string, arg string, full bool, noPager bool) error {
	s, err := store.Open(*dir)
	if err != nil {
		return err
	}
	defer s.Close()
	snap, err := resolveTodoDefault(s, arg)
	if err != nil {
		return err
	}
	if plainMode(cmd) {
		fmt.Fprint(cmd.OutOrStdout(), snap.Body)
		return nil
	}
	body := snap.Body
	if !full {
		body, err = todo.CollapseDone(body)
		if err != nil {
			return err
		}
	}
	body = todo.AnnotateHeadingCounts(body)
	c := todo.Summarize(snap.Body, snap.Comments)
	c.Version = snap.Version
	c.Edited = snap.EditedAt
	if q, qerr := s.QuarantineQueue(); qerr == nil {
		now := time.Now()
		c.QuarantinePending = len(q)
		c.QuarantineStale = 0
		for _, it := range q {
			if store.QuarantineStale(it, now) {
				c.QuarantineStale++
			}
		}
	}
	_, email := s.Author()
	key := watermark.Key(s.Root(), snap.ID.String(), email)
	if seen, ok, gerr := watermark.Get(key); gerr == nil && ok {
		d := todo.Delta(seen, snap.Body)
		c.ToReview, c.Changed = d.ToReview, d.Changed
	}
	theme := s.EffectiveConfig().GlyphTheme()
	color := useColor(cmd)

	// On an interactive terminal (and not --plain / --no-pager) launch the
	// interactive cockpit. The watermark is advanced here in the TUI branch and
	// does NOT run again in the static path below (early return). This ensures
	// exactly one advance per human view.
	if usePager(cmd) && !noPager {
		// The interactive cockpit renders the questions in the discussion zone, so
		// the header shows only the awaiting-you count, not the numbered list (the
		// static path below keeps the list — it has no discussion zone).
		hc := c
		hc.Questions = nil
		var hb bytes.Buffer
		render.TodoCockpit(&hb, hc, theme, color)
		header := strings.Split(strings.TrimRight(hb.String(), "\n"), "\n")
		if _, kind := resolveActor(cmd, s); kind == "human" {
			if serr := watermark.Set(key, snap.Body); serr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not save todo watermark: %v\n", serr)
			}
		}
		title := "todo · " + render.ShortID(snap.ID)
		id := snap.ID
		reload := func() ([]string, []entry.Comment, error) {
			fresh, ferr := s.Get(id)
			if ferr != nil {
				return nil, nil, ferr
			}
			cc := todo.Summarize(fresh.Body, fresh.Comments)
			cc.Version = fresh.Version
			cc.Edited = fresh.EditedAt
			if q, qerr := s.QuarantineQueue(); qerr == nil {
				now := time.Now()
				cc.QuarantinePending = len(q)
				cc.QuarantineStale = 0
				for _, it := range q {
					if store.QuarantineStale(it, now) {
						cc.QuarantineStale++
					}
				}
			}
			if seen, ok, gerr := watermark.Get(key); gerr == nil && ok {
				d := todo.Delta(seen, fresh.Body)
				cc.ToReview, cc.Changed = d.ToReview, d.Changed
			}
			cc.Questions = nil // count only in the header; the zone shows the questions
			var hb2 bytes.Buffer
			render.TodoCockpit(&hb2, cc, theme, color)
			return strings.Split(strings.TrimRight(hb2.String(), "\n"), "\n"), fresh.Comments, nil
		}
		_, actorKind := resolveActor(cmd, s)
		in := cockpitInputFor(title, header, snap.Body, color, ttyWidth(), full, snap.Comments)
		in.writer = s
		in.entryID = snap.ID
		in.actorKind = actorKind
		in.reload = reload
		return RunCockpit(in)
	}

	w := cmd.OutOrStdout()
	render.TodoCockpit(w, c, theme, color)
	render.RenderBody(w, body, snap.ContentType, color, ttyWidth())
	// Advance the seen-watermark only on a human view; an agent view
	// (--actor / KREF_ACTOR) reads the delta without consuming it.
	if _, kind := resolveActor(cmd, s); kind == "human" {
		if serr := watermark.Set(key, snap.Body); serr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not save todo watermark: %v\n", serr)
		}
	}
	return nil
}

func newTodoShowCmd(dir *string) *cobra.Command {
	var full, noPager bool
	c := &cobra.Command{
		Use:     "show [id]",
		Short:   "Show the todo cockpit for a todo (the sole/default one, or by id)",
		Example: exampleBlock([]string{"kref todo show", "kref todo show a1b2c3d4"}),
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			return runTodoCockpit(cmd, dir, arg, full, noPager)
		},
	}
	c.Flags().BoolVar(&full, "full", false, "expand the Done section instead of collapsing it")
	c.Flags().BoolVar(&noPager, "no-pager", false, "do not launch the interactive cockpit; print the static view")
	c.ValidArgsFunction = entryArgs(dir, 1, sourceTodo)
	return c
}

// resolveTodoDefault is resolveTodo plus the config todo.default fallback: with
// no argument and several kind:todo entries, the configured default is used
// before erroring.
func resolveTodoDefault(s *store.Store, arg string) (*entry.Snapshot, error) {
	if arg == "" {
		if def := s.EffectiveConfig().DefaultTodo(); def != "" {
			arg = def
		}
	}
	return resolveTodo(s, arg)
}

// resolveTodo finds the target kind:todo entry: an explicit id/favorite when
// given, otherwise the sole kind:todo entry (error on none or several).
func resolveTodo(s *store.Store, arg string) (*entry.Snapshot, error) {
	if arg != "" {
		id, err := s.Resolve(arg)
		if err != nil {
			return nil, err
		}
		return s.Get(id)
	}
	snaps, err := s.List(store.ListFilter{Kind: "todo"})
	if err != nil {
		return nil, err
	}
	switch len(snaps) {
	case 0:
		return nil, errors.New("no kind:todo entry found; pass an id")
	case 1:
		return snaps[0], nil
	default:
		return nil, fmt.Errorf("%d kind:todo entries found; pass an id", len(snaps))
	}
}

func newTodoFmtCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:     "fmt [id]",
		Short:   "Auto-arrange the todo: move done items to Done, normalize",
		Example: exampleBlock([]string{"kref todo fmt", "kref todo fmt a1b2c3d4"}),
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			snap, err := resolveTodo(s, arg)
			if err != nil {
				return err
			}
			out, err := todo.Format(snap.Body)
			if err != nil {
				return err
			}
			verb := "formatted"
			if out == snap.Body {
				verb = "unchanged"
			} else if err := s.Update(snap.ID, out, snap.Title); err != nil {
				return err
			}
			snap, err = s.Get(snap.ID)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) { render.Action(w, verb, snap, color) },
				map[string]string{"status": verb, "id": snap.ID.String()})
		},
	}
	c.ValidArgsFunction = entryArgs(dir, 1, sourceTodo)
	return c
}

func newTodoLintCmd(dir *string) *cobra.Command {
	var fix bool
	c := &cobra.Command{
		Use:     "lint [id]",
		Short:   "Check the todo grammar; --fix applies the auto-fixes first",
		Example: exampleBlock([]string{"kref todo lint", "kref todo lint --fix"}),
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()

			// Explicit target: unchanged single-entry behavior.
			if len(args) == 1 {
				snap, err := resolveTodo(s, args[0])
				if err != nil {
					return err
				}
				body, err := lintFixBody(s, snap, fix)
				if err != nil {
					return err
				}
				vs := todo.Lint(body)
				if err := emit(cmd,
					func(w io.Writer, color bool) { render.TodoLint(w, vs, color) },
					vs); err != nil {
					return err
				}
				if len(vs) > 0 {
					return fmt.Errorf("%d lint violation(s)", len(vs))
				}
				return nil
			}

			// No argument: lint every todo entry; pass cleanly when there are
			// none so a lefthook pre-commit gate never blocks a todo-less repo.
			todos, err := s.List(store.ListFilter{Kind: "todo"})
			if err != nil {
				return err
			}
			report := map[string][]todo.Violation{}
			total := 0
			for _, snap := range todos {
				body, err := lintFixBody(s, snap, fix)
				if err != nil {
					return err
				}
				vs := todo.Lint(body)
				report[snap.ID.String()] = vs
				total += len(vs)
			}
			if err := emit(cmd,
				func(w io.Writer, color bool) {
					if len(todos) == 0 {
						fmt.Fprintln(w, "todo: ok (no todo entries)")
						return
					}
					for _, snap := range todos {
						fmt.Fprintf(w, "%s  %s\n", render.ShortID(snap.ID), snap.Title)
						render.TodoLint(w, report[snap.ID.String()], color)
					}
				},
				report); err != nil {
				return err
			}
			if total > 0 {
				return fmt.Errorf("%d lint violation(s) across %d todo(s)", total, len(todos))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&fix, "fix", false, "apply auto-fixes (formatter) before linting")
	c.ValidArgsFunction = entryArgs(dir, 1, sourceTodo)
	return c
}

// lintFixBody returns the body to lint for a todo. With fix set it applies the
// formatter (auto rules) and writes the result back before returning it; without
// fix it returns the stored body untouched.
func lintFixBody(s *store.Store, snap *entry.Snapshot, fix bool) (string, error) {
	body := snap.Body
	if !fix {
		return body, nil
	}
	out, err := todo.Format(body)
	if err != nil {
		return "", err
	}
	if out != body {
		if err := s.Update(snap.ID, out, snap.Title); err != nil {
			return "", err
		}
		body = out
	}
	return body, nil
}
