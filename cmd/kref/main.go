package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/riotbox/kref/internal/store"
)

// Version is set at build time via -ldflags "-X main.Version=<tag>".
var Version = "dev"

func newRootCmd() *cobra.Command {
	cobra.EnableCommandSorting = false
	var dir string
	root := &cobra.Command{
		Use:           "kref",
		Short:         "Repo-resident knowledge base over git objects",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("kref {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	// Cobra completes only canonical subcommand names; this appends their aliases
	// so `kref imp<TAB>` also offers `import`. Cobra still calls root's
	// ValidArgsFunction after its subcommand-name pass because root has no ValidArgs.
	root.ValidArgsFunction = completeCommandAliases
	root.PersistentFlags().StringVar(&dir, "dir", ".", "repository directory (default: the enclosing repo, discovered git-style; selects the ref store; path arguments stay relative to the current directory)")
	root.PersistentFlags().Bool("json", false, "machine-readable JSON output")
	root.PersistentFlags().Bool("plain", false, "chrome-free line-oriented output: TSV for list/search, the verbatim stored body for show")
	root.PersistentFlags().String("actor", "", "attribute actions to an agent (else the git identity, as human)")
	// --plain and --json are the two machine contracts; asking for both is a
	// contradiction. No subcommand defines PersistentPreRunE, so the root hook
	// is never shadowed.
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if jsonMode(cmd) && plainMode(cmd) {
			return fmt.Errorf("--plain and --json are mutually exclusive")
		}
		return nil
	}
	// After any successful op-creating command, nudge (at most daily) when
	// syncable entries exist but no remote does — losing the repo would lose
	// the work. Reads, --json runs, and errors stay quiet.
	root.PersistentPostRun = func(cmd *cobra.Command, _ []string) { maybeWarnNoRemote(cmd, dir) }
	root.AddGroup(
		&cobra.Group{ID: "core", Title: "Core Commands:"},
		&cobra.Group{ID: "lifecycle", Title: "Lifecycle Commands:"},
		&cobra.Group{ID: "sync", Title: "Sync Commands:"},
		&cobra.Group{ID: "setup", Title: "Setup Commands:"},
		&cobra.Group{ID: "additional", Title: "Additional Commands:"},
	)

	addTo := func(group string, c *cobra.Command) {
		c.GroupID = group
		root.AddCommand(c)
	}
	addTo("core", newAddCmd(&dir))
	addTo("core", newUpdateCmd(&dir))
	addTo("core", newEditCmd(&dir))
	addTo("core", newIngestCmd(&dir))
	addTo("core", newTrackCmd(&dir))
	addTo("core", newReconcileCmd(&dir))
	addTo("core", newListCmd(&dir))
	addTo("core", newSearchCmd(&dir))
	addTo("core", newShowCmd(&dir))
	addTo("core", newLogCmd(&dir))
	addTo("core", newDiffCmd(&dir))
	addTo("core", newLinksCmd(&dir))
	addTo("core", newLinkCmd(&dir))
	addTo("core", newTreeCmd(&dir))
	addTo("core", newTidyCmd(&dir))
	addTo("core", newLabelCmd(&dir))
	addTo("core", newFavCmd(&dir))
	addTo("lifecycle", newRmCmd(&dir))
	addTo("lifecycle", newRestoreCmd(&dir))
	addTo("lifecycle", newArchiveCmd(&dir))
	addTo("lifecycle", newUnarchiveCmd(&dir))
	addTo("lifecycle", newUntrackCmd(&dir))
	addTo("lifecycle", newPurgeCmd(&dir))
	addTo("lifecycle", newStatusCmd(&dir))
	addTo("lifecycle", newSupersedeCmd(&dir))
	addTo("lifecycle", newResolveCmd(&dir))
	addTo("lifecycle", newRetierCmd(&dir))
	addTo("sync", newRemoteCmd(&dir))
	addTo("sync", newTierCmd(&dir))
	addTo("sync", newSyncCmd(&dir))
	addTo("sync", newBundleCmd(&dir))
	addTo("sync", newVaultCmd(&dir))
	addTo("setup", newInitCmd(&dir))
	addTo("setup", newConfigCmd(&dir))
	addTo("setup", newHooksCmd(&dir))
	addTo("setup", newMCPCmd(&dir))
	addTo("setup", newAgentsMDCmd())
	addTo("additional", newVersionCmd())
	addTo("additional", newCompletionCmd())

	listAliasesInUsage(root)

	// Capture cobra's stock help renderer BEFORE overriding it, so the concise
	// branch of renderHelp reproduces today's grouped/aliased output exactly.
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(c *cobra.Command, _ []string) {
		renderHelp(c, helpAuto, defaultHelp)
	})
	root.SetHelpCommand(newHelpCmd(root, defaultHelp))
	return root
}

// aliasedName renders a command's left-hand help column: "name (a, b)" when it
// has aliases, else just "name".
func aliasedName(c *cobra.Command) string {
	if len(c.Aliases) == 0 {
		return c.Name()
	}
	return c.Name() + " (" + strings.Join(c.Aliases, ", ") + ")"
}

// listAliasesInUsage rewrites cobra's default usage template so the "Available
// Commands" list shows each command's aliases inline. Without this, aliases are
// only visible from each subcommand's own --help, which makes them feel like
// undiscoverable magic. The replacement is verified by a cli_test spec, so a
// future cobra template change that breaks the substring fails loudly.
func listAliasesInUsage(root *cobra.Command) {
	width := 11 // cobra's default minimum name padding
	for _, c := range root.Commands() {
		if !c.IsAvailableCommand() && c.Name() != "help" {
			continue
		}
		if n := len(aliasedName(c)); n > width {
			width = n
		}
	}
	cobra.AddTemplateFunc("aliasedName", aliasedName)
	tmpl := strings.ReplaceAll(
		root.UsageTemplate(),
		"{{rpad .Name .NamePadding }} {{.Short}}",
		fmt.Sprintf("{{rpad (aliasedName .) %d }} {{.Short}}", width),
	)
	root.SetUsageTemplate(tmpl)
}

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		// Persistent root flags share their *pflag.Flag with every subcommand,
		// so this reflects a --json given on whichever subcommand ran.
		jsonMode, _ := root.PersistentFlags().GetBool("json")
		fmt.Fprintln(os.Stderr, formatCLIError(err, jsonMode))
		os.Exit(1)
	}
}

// formatCLIError renders a top-level command error for stderr. Under --json it
// returns a single-line {"error": "..."} envelope so a script driving kref with
// --json parses one format for both success and failure; otherwise it returns
// the conventional "error: <msg>" line.
func formatCLIError(err error, jsonMode bool) string {
	if jsonMode {
		if b, mErr := json.Marshal(map[string]string{"error": err.Error()}); mErr == nil {
			return string(b)
		}
	}
	return "error: " + err.Error()
}

// mutatingCommands are the depth-1 commands that append operations to entries;
// only they can leave new work without an off-machine copy.
var mutatingCommands = map[string]bool{
	"new": true, "ingest": true, "track": true, "update": true, "edit": true,
	"status": true, "supersede": true, "link": true, "label": true,
	"retier": true, "rm": true,
	"restore": true, "archive": true, "unarchive": true, "resolve": true,
	"reconcile": true,
}

// maybeWarnNoRemote prints the periodic no-remote data-loss warning to stderr
// after a mutating command, at most once per 24h (tracked in local git config).
// It never fails the command: any error on this path is silently dropped.
func maybeWarnNoRemote(cmd *cobra.Command, dir string) {
	if jsonMode(cmd) {
		return
	}
	c := cmd
	for c.Parent() != nil && c.Parent().Parent() != nil {
		c = c.Parent()
	}
	if !mutatingCommands[c.Name()] {
		return
	}
	s, err := store.Open(dir)
	if err != nil {
		return
	}
	defer s.Close()
	now := time.Now()
	due, err := s.WarnNoRemoteDue(now, 24*time.Hour)
	if err != nil || !due {
		return
	}
	fmt.Fprintln(cmd.ErrOrStderr(),
		"warning: no sync remote is configured — this repository holds the only copy of your entries.")
	fmt.Fprintln(cmd.ErrOrStderr(),
		"Set one with `kref remote set <tier> <name> [url]` (see `kref remote`).")
	_ = s.MarkNoRemoteWarned(now)
}
