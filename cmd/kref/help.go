package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// helpMode selects how renderHelp renders. helpAuto resolves by the command's
// output writer; helpLong and helpShort are the explicit --long / --short
// overrides carried by the help command.
type helpMode int

const (
	helpAuto helpMode = iota
	helpLong
	helpShort
)

// fullByDefault reports whether help written to w should render the full
// recursive tree. It keys off the command's own writer rather than os.Stdout so
// the Ginkgo specs — which capture output into a *bytes.Buffer via cmd.SetOut —
// are deterministic: only a real file whose fd is not a terminal (a pipe or
// redirect, e.g. an agent capturing stdout) yields full. A terminal, or any
// non-file writer such as a test buffer, yields concise.
func fullByDefault(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return !term.IsTerminal(int(f.Fd()))
}

// renderHelp is the single path both the -h/--help flag (via SetHelpFunc) and
// the custom help command funnel through. defaultHelp is cobra's stock help
// renderer, captured before SetHelpFunc replaces it, so the concise branch
// reproduces today's grouped/aliased output exactly.
func renderHelp(cmd *cobra.Command, mode helpMode, defaultHelp func(*cobra.Command, []string)) {
	full := mode == helpLong || (mode == helpAuto && fullByDefault(cmd.OutOrStdout()))
	if !full {
		defaultHelp(cmd, nil)
		return
	}
	renderFull(cmd.OutOrStdout(), cmd)
}

// renderFull writes the agent-oriented expanded help for target and everything
// under it. For the root it lists commands under their group titles; for a
// subcommand it expands that command's own subtree. The global preamble is
// always shown because the persistent flags and output contract apply tree-wide.
func renderFull(w io.Writer, target *cobra.Command) {
	root := target.Root()
	writeHelpPreamble(w, root)
	if target == root {
		writeGroupedCommands(w, root)
		return
	}
	writeCommandFull(w, target, 0)
}

// writeHelpPreamble renders the cross-cutting facts an agent cannot infer per
// command: the global persistent flags (rendered from the live flag set, so
// they cannot drift) and the JSON output / exit-code contract.
func writeHelpPreamble(w io.Writer, root *cobra.Command) {
	fmt.Fprintf(w, "%s — %s\n\n", root.Name(), root.Short)
	fmt.Fprintln(w, "GLOBAL FLAGS")
	fmt.Fprint(w, root.PersistentFlags().FlagUsages())
	fmt.Fprintln(w)
	fmt.Fprintln(w, "OUTPUT CONTRACT")
	fmt.Fprintln(w, "  Human-readable text by default; pass --json for machine-readable objects.")
	fmt.Fprintln(w, "  Under --json, errors are emitted as a single line: {\"error\": \"<message>\"}.")
	fmt.Fprintln(w, "  Exit status is 0 on success and 1 on any error.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "ENVIRONMENT")
	fmt.Fprintln(w, "  KREF_COLOR=1 forces ANSI color on (KREF_COLOR=0 forces it off), overriding")
	fmt.Fprintln(w, "  both NO_COLOR and terminal detection. Otherwise color is used only on an")
	fmt.Fprintln(w, "  interactive terminal, and never when NO_COLOR is set or under --json.")
	fmt.Fprintln(w)
}

// writeGroupedCommands lists the root's commands under their group titles, in
// registration order (cobra.EnableCommandSorting is false), recursing into each.
func writeGroupedCommands(w io.Writer, root *cobra.Command) {
	for _, g := range root.Groups() {
		fmt.Fprintf(w, "%s\n", g.Title)
		for _, c := range root.Commands() {
			if c.GroupID != g.ID || !c.IsAvailableCommand() {
				continue
			}
			writeCommandFull(w, c, 1)
		}
		fmt.Fprintln(w)
	}
}

// writeCommandFull renders one command — heading, long description, local
// flags, examples — at the given indent depth, then recurses into its available
// subcommands.
func writeCommandFull(w io.Writer, c *cobra.Command, depth int) {
	ind := strings.Repeat("  ", depth)
	body := ind + "  "
	fmt.Fprintf(w, "%s%s\n", ind, commandHeading(c))
	if c.Long != "" {
		fmt.Fprint(w, indentBlock(c.Long, body))
	}
	if usage := strings.TrimRight(c.LocalFlags().FlagUsages(), "\n"); strings.TrimSpace(usage) != "" {
		fmt.Fprintf(w, "%sFlags:\n", body)
		fmt.Fprint(w, indentBlock(usage, body+"  "))
	}
	if c.Example != "" {
		fmt.Fprintf(w, "%sExamples:\n", body)
		fmt.Fprint(w, indentBlock(c.Example, body+"  "))
	}
	fmt.Fprintln(w)
	for _, sub := range c.Commands() {
		if sub.IsAvailableCommand() {
			writeCommandFull(w, sub, depth+1)
		}
	}
}

// commandHeading renders a command's heading line: "name (alias, …)  —  Short".
func commandHeading(c *cobra.Command) string {
	name := aliasedName(c)
	if c.Short == "" {
		return name
	}
	return name + "  —  " + c.Short
}

// indentBlock prefixes every non-empty line of s with indent and guarantees a
// trailing newline.
func indentBlock(s, indent string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = indent + ln
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// newHelpCmd builds a custom replacement for cobra's auto-generated help
// command, adding --long / --short overrides while preserving the "additional"
// group placement and subcommand-name completion.
func newHelpCmd(root *cobra.Command, defaultHelp func(*cobra.Command, []string)) *cobra.Command {
	var long, short bool
	c := &cobra.Command{
		Use:     "help [command]",
		Short:   "Help about any command",
		GroupID: "additional",
		Long: "Help prints usage for kref or a specific command.\n\n" +
			"Depth adapts to the output: a terminal gets the concise grouped command " +
			"list, while a pipe or redirect (what an agent sees) gets the full recursive " +
			"tree with every command, flag, example, and the global output contract. " +
			"Force either depth with --long or --short.",
		ValidArgsFunction: func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return helpTargetCompletions(c.Root(), args, toComplete)
		},
		RunE: func(c *cobra.Command, args []string) error {
			if long && short {
				return errors.New("--long and --short cannot be combined")
			}
			target, _, err := c.Root().Find(args)
			if err != nil || target == nil {
				return fmt.Errorf("unknown help topic %q", strings.Join(args, " "))
			}
			mode := helpAuto
			switch {
			case long:
				mode = helpLong
			case short:
				mode = helpShort
			}
			renderHelp(target, mode, defaultHelp)
			return nil
		},
	}
	c.Flags().BoolVarP(&long, "long", "l", false, "show the full recursive command tree, flags, and examples")
	c.Flags().BoolVarP(&short, "short", "s", false, "show the concise grouped command list")
	return c
}

// helpTargetCompletions completes the command-name argument to `kref help`,
// offering the available subcommands of whatever parent the prior args resolve
// to (the root when there are none). Preserves `kref help <TAB>`.
func helpTargetCompletions(root *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	target, _, err := root.Find(args)
	if err != nil || target == nil {
		target = root
	}
	var names []string
	for _, c := range target.Commands() {
		if c.IsAvailableCommand() {
			names = append(names, c.Name())
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
