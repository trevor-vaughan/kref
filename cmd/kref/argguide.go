package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/git-bug/git-bug/entity"
	"github.com/spf13/cobra"

	"github.com/riotbox/kref/internal/render"
	"github.com/riotbox/kref/internal/store"
)

// argGuide is the data a command supplies to coach a user who invoked it
// without the entry reference(s) it needs. The same examples drive both the
// actionable error (guidedArgs) and the --help Example block (exampleBlock), so
// the two never drift.
type argGuide struct {
	noun     string   // what is missing, e.g. "an entry id"
	find     string   // discovery command, e.g. "kref list"
	usage    string   // canonical form, e.g. "kref purge <id>"
	examples []string // worked invocations, each "<cmd>  # what it does"
}

// guidedArgs wraps an arg-count validator with argGuide coaching. On the wrong
// count it returns the actionable error; otherwise it defers to inner so every
// other validation path is unchanged. SilenceUsage stays true at the root, so
// this error is the entire output of a mistaken invocation.
func guidedArgs(inner cobra.PositionalArgs, g argGuide) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := inner(cmd, args); err != nil {
			return g.render(cmd)
		}
		return nil
	}
}

// render builds the multi-line actionable error. formatCLIError prepends
// "error: ", so the first line reads "error: kref purge needs an entry id."
func (g argGuide) render(cmd *cobra.Command) error {
	path := cmd.CommandPath()
	var b strings.Builder
	fmt.Fprintf(&b, "%s needs %s.\n", path, g.noun)
	fmt.Fprintf(&b, "  find one:  %s\n", g.find)
	fmt.Fprintf(&b, "  then:      %s\n", g.usage)
	for i, ex := range g.examples {
		lead := "  example:   "
		if i > 0 {
			lead = "             "
		}
		fmt.Fprintf(&b, "%s%s\n", lead, ex)
	}
	fmt.Fprintf(&b, "  details:   %s --help", path)
	return errors.New(b.String())
}

// exampleBlock renders examples for cobra's Example field (2-space indented,
// shown under "Examples:" in --help).
func exampleBlock(examples []string) string {
	lines := make([]string, len(examples))
	for i, e := range examples {
		lines[i] = "  " + e
	}
	return strings.Join(lines, "\n")
}

// applyGuide wires a command's Args validator and Example block from one guide.
func applyGuide(c *cobra.Command, inner cobra.PositionalArgs, g argGuide) {
	c.Args = guidedArgs(inner, g)
	c.Example = exampleBlock(g.examples)
}

// resolveTargetOrRecent resolves args[0] to an id, or — when no arg is given —
// falls back to the most-recently-modified entry, announcing the choice on
// stderr (suppressed under --json so piped stdout stays clean). An empty store
// yields a friendly "create one first" error.
func resolveTargetOrRecent(cmd *cobra.Command, s *store.Store, args []string) (entity.Id, error) {
	if len(args) == 1 {
		return resolveArg(s, args[0])
	}
	snap, err := s.MostRecent()
	if err != nil {
		if errors.Is(err, store.ErrNoEntries) {
			return "", fmt.Errorf("no entries yet — create one with `kref new ...`")
		}
		return "", err
	}
	if !jsonMode(cmd) {
		fmt.Fprintf(cmd.ErrOrStderr(), "(no id given — showing most recent: %s %q)\n",
			render.ShortID(snap.ID), snap.Title)
	}
	return snap.ID, nil
}
