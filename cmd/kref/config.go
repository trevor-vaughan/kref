package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/config"
	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

func newConfigCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Show or manage kref configuration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			eff := s.EffectiveConfig()
			return emit(cmd,
				func(w io.Writer, _ bool) {
					fmt.Fprintf(w, "version: %d\n", eff.Version)
					fmt.Fprintf(w, "warn_unscanned: %t\n", eff.WarnUnscannedOn())
					if len(eff.Favorites) > 0 {
						fmt.Fprintln(w, "favorites:")
						names := make([]string, 0, len(eff.Favorites))
						for n := range eff.Favorites {
							names = append(names, n)
						}
						sort.Strings(names)
						for _, n := range names {
							fmt.Fprintf(w, "  %s -> %s\n", n, eff.Favorites[n])
						}
					}
				},
				eff)
		},
	}
	c.AddCommand(&cobra.Command{
		Use:     "migrate",
		Short:   "Migrate the project config entry to the current schema (the user file auto-migrates on load)",
		Example: exampleBlock([]string{"kref config migrate"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			msg, err := s.MigrateConfig()
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { fmt.Fprintln(w, msg) },
				map[string]string{"status": msg})
		},
	})

	var initShared, initForce bool
	var initTier string
	initCmd := &cobra.Command{
		Use:     "init",
		Short:   "Write the user config template, or create the shared project config entry with --shared",
		Example: exampleBlock([]string{"kref config init", "kref config init --shared", "kref config init --shared --tier team"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if initShared {
				s, err := store.Open(*dir)
				if err != nil {
					return err
				}
				defer s.Close()
				if _, _, ok := s.ProjectConfigEntry(); ok {
					return fmt.Errorf("a project config entry already exists")
				}
				tierName, err := chooseSharedTier(s, initTier)
				if err != nil {
					return err
				}
				body, err := config.MarshalEntry(config.InitialUserConfig())
				if err != nil {
					return err
				}
				id, err := s.Add(tierName, "config", "kref.conf", string(body))
				if err != nil {
					return err
				}
				actor, actorKind := resolveActor(cmd, s)
				if err := s.RecordOrigin(id, actor, actorKind, "", "create"); err != nil {
					return err
				}
				return emit(cmd,
					func(w io.Writer, _ bool) {
						fmt.Fprintf(w, "created project config entry %s in tier %s\n", render.ShortID(id), tierName)
					},
					map[string]string{"status": "created", "id": id.String(), "tier": string(tierName)})
			}
			path, err := config.UserPath(os.Getenv)
			if err != nil {
				return err
			}
			if _, statErr := os.Stat(path); statErr == nil && !initForce {
				return fmt.Errorf("%s already exists (use --force to overwrite; the old file is backed up to .bck)", path)
			}
			if err := config.WriteFile(path, config.InitialUserConfig()); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { fmt.Fprintf(w, "wrote %s\n", path) },
				map[string]string{"status": "wrote", "path": path})
		},
	}
	initCmd.Flags().BoolVar(&initShared, "shared", false, "create the shared project config entry instead of the user file")
	initCmd.Flags().BoolVar(&initForce, "force", false, "overwrite an existing user config file (backed up to .bck)")
	initCmd.Flags().StringVar(&initTier, "tier", "", "tier for the shared config entry (default: the sole shared tier)")
	c.AddCommand(initCmd)

	c.AddCommand(&cobra.Command{
		Use:     "check",
		Short:   "Validate the effective config and report version, warnings, and scanner status",
		Example: exampleBlock([]string{"kref config check", "kref config check --json"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			eff := s.EffectiveConfig()
			validErr := config.Validate(eff)
			_, scanErr := scan.Scan([]byte("x"))
			betterleaksOK := !errors.Is(scanErr, scan.ErrMissing)
			warnings := s.ConfigWarnings()
			report := map[string]any{
				"valid":               validErr == nil,
				"version":             eff.Version,
				"current_version":     config.CurrentVersion,
				"warnings":            warnings,
				"betterleaks_present": betterleaksOK,
			}
			if validErr != nil {
				report["error"] = validErr.Error()
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					if validErr != nil {
						fmt.Fprintf(w, "INVALID: %v\n", validErr)
					} else {
						fmt.Fprintln(w, "config valid")
					}
					fmt.Fprintf(w, "schema version: %d (current %d)\n", eff.Version, config.CurrentVersion)
					fmt.Fprintf(w, "betterleaks: %s\n", map[bool]string{true: "present", false: "MISSING"}[betterleaksOK])
					for _, warn := range warnings {
						fmt.Fprintf(w, "warning: %s\n", warn)
					}
				},
				report)
		},
	})

	c.AddCommand(&cobra.Command{
		Use:     "edit",
		Short:   "Edit the user config in $EDITOR, validating before save (visudo-style)",
		Example: exampleBlock([]string{"kref config edit"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := config.UserPath(os.Getenv)
			if err != nil {
				return err
			}
			seed, rerr := os.ReadFile(path)
			if os.IsNotExist(rerr) {
				seed, err = config.Template(config.InitialUserConfig())
				if err != nil {
					return err
				}
			} else if rerr != nil {
				return rerr
			}
			tmp, err := os.CreateTemp("", "kref-config-*.yaml")
			if err != nil {
				return err
			}
			tmpPath := tmp.Name()
			defer func() { _ = os.Remove(tmpPath) }()
			if _, err := tmp.Write(seed); err != nil {
				_ = tmp.Close()
				return err
			}
			if err := tmp.Close(); err != nil {
				return err
			}

			editor := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
			for {
				ed := exec.Command("sh", "-c", editor+" \""+tmpPath+"\"")
				ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
				if err := ed.Run(); err != nil {
					return fmt.Errorf("editor exited: %w", err)
				}
				edited, err := os.ReadFile(tmpPath)
				if err != nil {
					return err
				}
				parsed, perr := config.Parse(edited)
				if perr == nil {
					perr = config.Validate(parsed)
				}
				if perr == nil {
					if err := config.WriteBytes(path, edited); err != nil {
						return err
					}
					return emit(cmd,
						func(w io.Writer, _ bool) { fmt.Fprintf(w, "saved %s\n", path) },
						map[string]string{"status": "saved", "path": path})
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "config invalid: %v\n", perr)
				fmt.Fprint(cmd.ErrOrStderr(), "[e]dit again or [d]iscard? ")
				var ans string
				_, _ = fmt.Fscanln(cmd.InOrStdin(), &ans)
				if ans == "d" || ans == "discard" {
					return fmt.Errorf("edit discarded; %s unchanged", path)
				}
			}
		},
	})
	return c
}

// firstNonEmpty returns the first non-empty string, or "" when all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// chooseSharedTier picks the tier for a shared config entry. With want set, it
// must name a declared shared tier; otherwise the sole declared shared tier is
// used, erroring when zero or many exist.
func chooseSharedTier(s *store.Store, want string) (entry.Tier, error) {
	var shared []entry.Tier
	for _, d := range s.Tiers() {
		if d.Type == entry.TierShared && d.Declared {
			shared = append(shared, d.Name)
		}
	}
	names := make([]string, len(shared))
	for i, t := range shared {
		names[i] = string(t)
	}
	sort.Strings(names)
	if want != "" {
		for _, t := range shared {
			if string(t) == want {
				return t, nil
			}
		}
		return "", fmt.Errorf("tier %q is not a declared shared tier (shared tiers: %s)", want, strings.Join(names, ", "))
	}
	switch len(shared) {
	case 0:
		return "", fmt.Errorf("no declared shared tier to hold the project config entry")
	case 1:
		return shared[0], nil
	default:
		return "", fmt.Errorf("multiple shared tiers (%s); pass --tier", strings.Join(names, ", "))
	}
}
