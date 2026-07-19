package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// completionShell describes one shell's completion generation, help text, and
// install target. filename is the basename written under the install directory;
// an empty filename means the shell has no standard auto-loaded directory and so
// gets no --install flag (powershell).
type completionShell struct {
	name        string
	long        string
	filename    string
	gen         func(root *cobra.Command, w io.Writer, noDesc bool) error
	installNote func(dir string) string
}

// completionInstallPath returns the file path to install sh's completion script.
// With dirOverride set, the script goes in that directory (leading ~ expanded)
// under sh.filename; relative overrides stay relative. Otherwise it resolves the
// shell's standard XDG-based directory via getenv. getenv is injected so tests
// need no real environment.
func completionInstallPath(sh completionShell, dirOverride string, getenv func(string) string) (string, error) {
	if sh.filename == "" {
		return "", fmt.Errorf("%s has no standard completion directory; print the script and add it to your profile", sh.name)
	}
	if dirOverride != "" {
		return filepath.Join(expandTilde(dirOverride, getenv), sh.filename), nil
	}
	dir, err := completionStdDir(sh.name, getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sh.filename), nil
}

// completionStdDir returns the standard completion directory for shell, honoring
// XDG base-directory variables and falling back to their documented defaults.
func completionStdDir(shell string, getenv func(string) string) (string, error) {
	switch shell {
	case "bash":
		base, err := xdgDir(getenv, "XDG_DATA_HOME", ".local/share")
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "bash-completion", "completions"), nil
	case "zsh":
		base, err := xdgDir(getenv, "XDG_DATA_HOME", ".local/share")
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "zsh", "site-functions"), nil
	case "fish":
		base, err := xdgDir(getenv, "XDG_CONFIG_HOME", ".config")
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "fish", "completions"), nil
	default:
		return "", fmt.Errorf("no standard completion directory for shell %q", shell)
	}
}

// xdgDir returns $<envVar> when set, else $HOME/<fallback>. It errors when
// neither is available so we never write to a bare relative path.
func xdgDir(getenv func(string) string, envVar, fallback string) (string, error) {
	if v := getenv(envVar); v != "" {
		return v, nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("cannot resolve completion directory: neither %s nor HOME is set", envVar)
	}
	return filepath.Join(home, fallback), nil
}

// expandTilde expands a leading ~ to $HOME. Other paths are returned unchanged.
func expandTilde(path string, getenv func(string) string) string {
	if path == "~" {
		return getenv("HOME")
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(getenv("HOME"), path[2:])
	}
	return path
}

// completionShells lists the shells kref generates completions for. bash, zsh,
// and fish have a standard auto-loaded directory (filename set) and so support
// --install; powershell is print-only.
var completionShells = []completionShell{
	{
		name:     "bash",
		filename: "kref",
		long: `Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(kref completion bash)

To load completions for every new session, install the script:

	kref completion bash --install

You will need to start a new shell for this setup to take effect.`,
		gen: func(root *cobra.Command, w io.Writer, noDesc bool) error {
			return root.GenBashCompletionV2(w, !noDesc)
		},
	},
	{
		name:     "zsh",
		filename: "_kref",
		long: `Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(kref completion zsh)

To load completions for every new session, install the script:

	kref completion zsh --install

The install directory must be in your fpath; --install prints the line to add if
it is not. You will need to start a new shell for this setup to take effect.`,
		gen: func(root *cobra.Command, w io.Writer, noDesc bool) error {
			if noDesc {
				return root.GenZshCompletionNoDesc(w)
			}
			return root.GenZshCompletion(w)
		},
		installNote: func(dir string) string {
			return fmt.Sprintf("Ensure this directory is in your fpath, then run: compinit\n"+
				"  fpath=(%s $fpath)   # add to ~/.zshrc if missing", dir)
		},
	},
	{
		name:     "fish",
		filename: "kref.fish",
		long: `Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	kref completion fish | source

To load completions for every new session, install the script:

	kref completion fish --install

You will need to start a new shell for this setup to take effect.`,
		gen: func(root *cobra.Command, w io.Writer, noDesc bool) error {
			return root.GenFishCompletion(w, !noDesc)
		},
	},
	{
		name:     "powershell",
		filename: "", // no standard auto-loaded directory; print-only
		long: `Generate the autocompletion script for powershell.

To load completions in your current shell session:

	kref completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your powershell profile.`,
		gen: func(root *cobra.Command, w io.Writer, noDesc bool) error {
			if noDesc {
				return root.GenPowerShellCompletion(w)
			}
			return root.GenPowerShellCompletionWithDesc(w)
		},
	},
}

// newCompletionCmd builds a custom replacement for cobra's auto-generated
// completion command (disabled via CompletionOptions.DisableDefaultCmd). The
// replacement adds --install on the bash/zsh/fish sub-commands.
func newCompletionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "completion",
		Short: "Generate the autocompletion script for the specified shell",
		Long: `Generate the autocompletion script for kref for the specified shell.
See each sub-command's help for details on how to use the generated script.

Pass --install to the bash, zsh, or fish sub-command to write the script to that
shell's standard completion directory instead of printing it to stdout.`,
		Example: exampleBlock([]string{
			"kref completion zsh                       # print the script to stdout",
			"kref completion zsh --install             # write it to the standard dir",
			"kref completion bash --install --dir DIR  # write it to a custom dir",
		}),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	for _, sh := range completionShells {
		c.AddCommand(newCompletionShellCmd(sh))
	}
	return c
}

// newCompletionShellCmd builds one shell sub-command. Shells with a standard
// completion directory (sh.filename != "") gain --install and --dir; otherwise
// the script is always printed to stdout.
func newCompletionShellCmd(sh completionShell) *cobra.Command {
	var noDesc, install bool
	var dir string
	c := &cobra.Command{
		Use:               sh.name,
		Short:             "Generate the autocompletion script for " + sh.name,
		Long:              sh.long,
		Example:           exampleBlock([]string{"kref completion " + sh.name}),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if install {
				return installCompletion(cmd, sh, dir, noDesc)
			}
			if dir != "" {
				return errors.New("--dir requires --install")
			}
			return sh.gen(cmd.Root(), cmd.OutOrStdout(), noDesc)
		},
	}
	c.Flags().BoolVar(&noDesc, "no-descriptions", false, "disable completion descriptions")
	if sh.filename != "" {
		c.Flags().BoolVar(&install, "install", false, "write the script to "+sh.name+"'s standard completion directory")
		c.Flags().StringVar(&dir, "dir", "", "install directory override (requires --install)")
	}
	return c
}

// installCompletion writes sh's completion script to its install path (honoring
// a --dir override) and reports where it went. The parent directory is created
// if missing; an existing file is overwritten, so re-running is idempotent.
func installCompletion(cmd *cobra.Command, sh completionShell, dirOverride string, noDesc bool) error {
	path, err := completionInstallPath(sh, dirOverride, os.Getenv)
	if err != nil {
		return err
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := sh.gen(cmd.Root(), f, noDesc); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return emit(cmd,
		func(w io.Writer, _ bool) {
			fmt.Fprintf(w, "Installed completion to %s\n", path)
			if sh.installNote != nil {
				fmt.Fprintln(w, sh.installNote(filepath.Dir(path)))
			}
		},
		map[string]any{"shell": sh.name, "path": path, "installed": true})
}
