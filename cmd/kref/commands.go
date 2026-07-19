package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/bridge"
	"github.com/trevor-vaughan/kref/internal/content"
	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/hooks"
	"github.com/trevor-vaughan/kref/internal/mcpserver"
	"github.com/trevor-vaughan/kref/internal/outline"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/store"
	"github.com/trevor-vaughan/kref/internal/todoguard"
	"github.com/trevor-vaughan/kref/internal/xdg"
)

func writeJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func parseStatus(s string) (string, error) {
	if slices.Contains(statusValues, s) {
		return s, nil
	}
	return "", fmt.Errorf("invalid status %q (want %s)", s, strings.Join(statusValues, "|"))
}

// parseAuthor splits a "Name <email>" string into its parts, erroring if the
// shape is wrong or either part is empty.
func parseAuthor(s string) (name, email string, err error) {
	open := strings.LastIndex(s, "<")
	closeIdx := strings.LastIndex(s, ">")
	if open < 0 || closeIdx < open || closeIdx != len(strings.TrimSpace(s))-1 {
		return "", "", fmt.Errorf("author must be in the form \"Name <email>\": %q", s)
	}
	name = strings.TrimSpace(s[:open])
	email = strings.TrimSpace(s[open+1 : closeIdx])
	if name == "" || email == "" {
		return "", "", fmt.Errorf("author must be in the form \"Name <email>\": %q", s)
	}
	return name, email, nil
}

func newStatusCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "status <id> <status>",
		Short: "Set an entry's lifecycle status (open|active|accepted|superseded|obsolete)",
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := parseStatus(args[1])
			if err != nil {
				return err
			}
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := s.Resolve(args[0])
			if err != nil {
				return err
			}
			if err := s.SetStatus(id, status); err != nil {
				return err
			}
			snap, err := s.Get(id)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) { render.Action(w, status, snap, color) },
				map[string]string{"status": status, "id": id.String()})
		},
	}
	c.ValidArgsFunction = entryThenEnum(dir, statusValues)
	applyGuide(c, cobra.ExactArgs(2), argGuide{noun: "an entry id and a status", find: "kref list", usage: "kref status <id> <status>", examples: []string{
		"kref status a1b2c3d4 accepted   # open | active | accepted | superseded | obsolete",
	}})
	return c
}

func newSupersedeCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "supersede <old> <new>",
		Short: "Mark <old> superseded by <new> (links them and sets <old>'s status)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			oldID, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}
			newID, err := resolveArg(s, args[1])
			if err != nil {
				return err
			}
			if err := s.Supersede(oldID, newID); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					fmt.Fprintf(w, "superseded %s by %s\n", render.ShortID(oldID), render.ShortID(newID))
				},
				map[string]string{"status": "superseded", "old": oldID.String(), "new": newID.String()})
		},
	}
	c.ValidArgsFunction = entryArgs(dir, 2, sourceAll)
	applyGuide(c, cobra.ExactArgs(2), argGuide{noun: "an old and a new entry id", find: "kref list", usage: "kref supersede <old> <new>", examples: []string{
		"kref supersede a1b2c3d4 e5f6a7b8   # mark <old> superseded by <new>, and link them",
	}})
	return c
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "version",
		Aliases:           []string{"ver"},
		ValidArgsFunction: cobra.NoFileCompletions,
		Short:             "Print the kref version",
		Example:           exampleBlock([]string{"kref version", "kref version --json"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Match `kref --version` by default (plain `kref <version>`) and the
			// rest of the CLI's emit() convention (human by default, JSON under
			// --json), so the same fact has one shape per output mode.
			return emit(cmd,
				func(w io.Writer, _ bool) { fmt.Fprintf(w, "kref %s\n", Version) },
				map[string]string{"version": Version})
		},
	}
}

func newInitCmd(dir *string) *cobra.Command {
	var name, email string
	c := &cobra.Command{
		Use:               "init",
		Short:             "Initialize a kref store and author identity",
		ValidArgsFunction: cobra.NoFileCompletions,
		Example: exampleBlock([]string{
			"kref init                                  # adopt your git user.name / user.email",
			"kref init --name You --email you@example.com",
		}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Already initialized: report who you are rather than re-initializing
			// (which would mint a duplicate identity). The identity is local to
			// this repo and never travels with it.
			if curName, curEmail, ok, iErr := store.Initialized(*dir); iErr == nil && ok {
				if name != "" || email != "" {
					cmd.PrintErrln("note: kref is already initialized; --name/--email are ignored. The author identity is local and set once. To attribute entries to a different author, set KREF_AUTHOR_NAME/KREF_AUTHOR_EMAIL or kref.author.name/email in git config.")
				}
				return emit(cmd,
					func(w io.Writer, _ bool) {
						fmt.Fprintf(w, "kref is already initialized in %s as %s <%s>\n", *dir, curName, curEmail)
					},
					map[string]any{"status": "already-initialized", "dir": *dir, "author": curName, "email": curEmail})
			}
			s, err := store.Init(*dir, name, email)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := bridge.EnsureKrefIgnored(*dir); err != nil {
				return err
			}
			name, email := s.Author()
			cmd.PrintErrln("note: operations are attributed to your git identity but are NOT cryptographically signed (git-bug v0.10.1 limitation).")
			sharedRemote, err := adoptOriginRemote(cmd, s)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					fmt.Fprintf(w, "initialized kref in %s as %s <%s>\n", *dir, name, email)
				},
				map[string]any{
					"status": "initialized", "dir": *dir,
					"author": name, "email": email, "signed": false,
					"shared_remote": sharedRemote,
				})
		},
	}
	c.Flags().StringVar(&name, "name", "", "author name")
	c.Flags().StringVar(&email, "email", "", "author email")
	return c
}

// adoptOriginRemote binds the shared tier to the git "origin" remote when the
// repository already has one, so a freshly-initialized store can sync without a
// separate `kref remote set`. It only records the binding (no URL passed, so no
// git remote is created or modified). It returns the adopted remote name ("" if
// none) and prints a note for each outcome: origin adopted, remotes present but
// no origin, or no remotes at all (sync impossible until one is set).
func adoptOriginRemote(cmd *cobra.Command, s *store.Store) (string, error) {
	remotes, err := s.GitRemotes()
	if err != nil {
		return "", err
	}
	if url, ok := remotes["origin"]; ok {
		if err := s.SetRemote(entry.TierShared, "origin", ""); err != nil {
			return "", err
		}
		cmd.PrintErrf("note: the shared tier will sync via the 'origin' remote (%s) — `kref sync push shared` to publish.\n", url)
		return "origin", nil
	}
	if len(remotes) == 0 {
		cmd.PrintErrln("note: no sync remote is configured — sync is not possible until you set one: `kref remote set <tier> <name> [url]`.")
		return "", nil
	}
	cmd.PrintErrln("note: no 'origin' remote to adopt — the shared tier has no sync remote yet: `kref remote set shared <name>`.")
	return "", nil
}

func newAddCmd(dir *string) *cobra.Command {
	var kind, title, body, tier string
	var contentType string
	var labels []string
	var force bool
	c := &cobra.Command{
		Use:               "new",
		Aliases:           []string{"create"},
		ValidArgsFunction: noPositionalHelp("new takes no arguments — configure the entry with flags like --title, --kind, --body, --tier, --label"),
		Short:             "Create a new entry",
		Long: "Compose a single entry from flags. The body comes from --body or, " +
			"when that is omitted, piped/redirected stdin. To create entries from " +
			"existing markdown files or directories, use `kref ingest` instead.",
		Example: exampleBlock([]string{
			`kref new --title "Auth design" --kind spec`,
			"kref new --body $'# Auth design\\n\\nprose'   # title derived from the H1",
			"kref new --kind spec < design.md            # body piped on stdin",
			"kref new --tier shared --label area:auth --title X",
		}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			tdef, err := s.DeclaredTier(tier)
			if err != nil {
				return err
			}
			t := tdef.Name
			// Body from --body, else piped/redirected stdin (never an interactive
			// terminal — reading it would block on an EOF that never comes). This
			// mirrors `kref update` and matches the agents_md guidance to pipe a
			// body on stdin.
			if !cmd.Flags().Changed("body") && !term.IsTerminal(int(os.Stdin.Fd())) {
				raw, rErr := io.ReadAll(cmd.InOrStdin())
				if rErr != nil {
					return rErr
				}
				body = string(raw)
			}
			if title == "" {
				title = entry.DeriveTitle(body)
			}
			if title == "" {
				return errors.New("provide --title, or a --body with a heading or text to derive one from")
			}
			ct := ""
			if cmd.Flags().Changed("content-type") {
				ct, err = content.Canonical(contentType)
				if err != nil {
					return err
				}
			}
			// A body that trips the secret scanner on a syncable tier is diverted
			// into the quarantine review queue as a draft (approval retiers it to
			// t), not written to t.
			if body != "" {
				fs, held, unscanned, ferr := entryParkFindings(&entry.Snapshot{Tier: string(t), TierType: string(tdef.Type)}, body)
				if ferr != nil {
					return ferr
				}
				if held {
					actor, actorKind := resolveActor(cmd, s)
					parked, perr := s.QuarantineNewEntry(t, kind, title, body, ct, fs, actorKind)
					if perr != nil {
						return perr
					}
					for _, l := range labels {
						if lerr := s.AddLabel(parked.ItemID, l); lerr != nil {
							return lerr
						}
					}
					if force {
						// --force = create+approve in one step (human/CLI only): the
						// draft is promoted to its tier through the normal approve path.
						if aerr := s.ApproveQuarantine(parked.ItemID, "force-approved at write", actor, actorKind); aerr != nil {
							return aerr
						}
						fmt.Fprintf(cmd.OutOrStdout(), "added %s (force-approved)\n", shortStr(parked.ItemID.String(), 12))
						return nil
					}
					printQuarantined(cmd, parked)
					return nil
				}
				if unscanned && s.EffectiveConfig().WarnUnscannedOn() {
					fmt.Fprintln(cmd.ErrOrStderr(), unscannedWarn)
				}
			}
			id, err := s.AddWithContentType(t, kind, title, body, ct)
			if err != nil {
				return err
			}
			for _, l := range labels {
				if err := s.AddLabel(id, l); err != nil {
					return err
				}
			}
			actor, actorKind := resolveActor(cmd, s)
			if err := s.RecordOrigin(id, actor, actorKind, "", "create"); err != nil {
				return err
			}
			snap, err := s.Get(id)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) { render.Action(w, "added", snap, color) },
				map[string]string{"id": id.String()})
		},
	}
	c.Flags().StringVar(&kind, "kind", "document", "entry kind")
	c.Flags().StringVar(&title, "title", "", "entry title")
	c.Flags().StringVar(&body, "body", "", "entry body")
	c.Flags().StringVar(&tier, "tier", "personal", "tier: private|personal|shared, or a custom tier (kref tier list)")
	c.Flags().StringVar(&contentType, "content-type", "", "content type, e.g. application/json (default text/markdown)")
	c.Flags().StringArrayVar(&labels, "label", nil, "label to attach (repeatable)")
	c.Flags().BoolVar(&force, "force", false, "for a flagged body: create the quarantine item and approve it in one step (human/CLI only; leaves an audit trail on the entry)")
	registerEntryFlagCompletions(c, dir)
	return c
}

func newLabelCmd(dir *string) *cobra.Command {
	c := &cobra.Command{Use: "label", Short: "Add or remove entry labels"}
	c.Example = exampleBlock([]string{"kref label add a1b2c3d4 area:auth", "kref label rm a1b2c3d4 area:auth"})
	mk := func(use, short, outVerb string, g argGuide, fn func(*store.Store, entity.Id, string) error) *cobra.Command {
		c := &cobra.Command{
			Use:   use,
			Short: short,
			RunE: func(cmd *cobra.Command, args []string) error {
				s, err := store.Open(*dir)
				if err != nil {
					return err
				}
				defer s.Close()
				id, err := s.Resolve(args[0])
				if err != nil {
					return err
				}
				for _, l := range args[1:] {
					if err := fn(s, id, l); err != nil {
						return err
					}
				}
				snap, err := s.Get(id)
				if err != nil {
					return err
				}
				return emit(cmd,
					func(w io.Writer, _ bool) {
						fmt.Fprintf(w, "%s %s  [%s]\n", outVerb, render.ShortID(id), strings.Join(snap.Labels, ", "))
					},
					map[string]any{"status": outVerb, "id": id.String(), "labels": snap.Labels})
			},
		}
		applyGuide(c, cobra.MinimumNArgs(2), g)
		c.ValidArgsFunction = entryArgs(dir, 1, sourceAll) // id at <id>; labels are free-form
		return c
	}
	c.AddCommand(
		mk("add <id> <label>...", "Add one or more labels to an entry", "labeled",
			argGuide{noun: "an entry id and at least one label", find: "kref list", usage: "kref label add <id> <label>...", examples: []string{
				"kref label add a1b2c3d4 area:auth project:kref",
			}},
			(*store.Store).AddLabel),
		mk("rm <id> <label>...", "Remove one or more labels from an entry", "unlabeled",
			argGuide{noun: "an entry id and at least one label", find: "kref show a1b2c3d4", usage: "kref label rm <id> <label>...", examples: []string{
				"kref label rm a1b2c3d4 area:auth",
			}},
			(*store.Store).RemoveLabel),
	)
	return c
}

func newIngestCmd(dir *string) *cobra.Command {
	var tier string
	var kind string
	var skipMissing bool
	c := &cobra.Command{
		Use:     "ingest <path>...",
		Aliases: []string{"import", "add"},
		Short:   "Ingest markdown files or directories as entries",
		Long: "Ingest reads each markdown file as an entry and writes a `kref-id` trailer " +
			"back into the file, so re-ingesting is idempotent: an unchanged file is a no-op " +
			"and an edited file updates its entry. Directories are scanned recursively.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			tdef, err := s.DeclaredTier(tier)
			if err != nil {
				return err
			}
			t := tdef.Name
			actor, actorKind := resolveActor(cmd, s)
			k := ""
			if cmd.Flags().Changed("kind") {
				k = kind
			}
			results, err := bridge.IngestPaths(s, args, t, k, skipMissing, actor, actorKind)
			if err != nil {
				return err
			}
			if err := emit(cmd,
				func(w io.Writer, color bool) { ingestSummary(w, results, color, s.EffectiveConfig().WarnUnscannedOn()) },
				results); err != nil {
				return err
			}
			n := 0
			for _, r := range results {
				if r.Action == "error" {
					n++
				}
			}
			if n > 0 {
				return fmt.Errorf("%d file(s) failed to ingest", n)
			}
			return nil
		},
	}
	c.Flags().StringVar(&tier, "tier", "personal", "tier: private|personal|shared, or a custom tier (kref tier list)")
	c.Flags().StringVar(&kind, "kind", "document", "entry kind for ingested files")
	c.Flags().BoolVar(&skipMissing, "skip-missing", false, "skip paths that do not exist instead of erroring")
	_ = c.RegisterFlagCompletionFunc("kind", completeKindWithDefault(dir))
	applyGuide(c, cobra.MinimumNArgs(1), argGuide{
		noun:  "at least one path",
		find:  "ls *.md",
		usage: "kref ingest <path>...",
		examples: []string{
			"kref ingest docs/notes.md   # one file (writes a kref-id trailer back into it)",
			"kref ingest docs/           # a whole directory tree",
			"kref ingest .               # everything under the cwd",
		},
	})
	return c
}

func newTrackCmd(dir *string) *cobra.Command {
	var tier, kind string
	c := &cobra.Command{
		Use:   "track <path>",
		Short: "Track a markdown file: ingest it, then keep the entry synced with that file",
		Long: "Track ensures there is an entry for the file (ingesting it if new) and " +
			"marks the entry as kept in sync with that file. A path outside the repo is " +
			"copied under .kref/ (ignored locally) and tracked there; an in-repo file is " +
			"tracked in place.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			tdef, err := s.DeclaredTier(tier)
			if err != nil {
				return err
			}
			t := tdef.Name
			anchor, err := bridge.AnchorForTracking(s, args[0])
			if err != nil {
				return err
			}
			actor, actorKind := resolveActor(cmd, s)
			k := ""
			if cmd.Flags().Changed("kind") {
				k = kind
			}
			res, err := bridge.Ingest(s, anchor, t, k, actor, actorKind)
			if err != nil {
				return err
			}
			if err := s.Track(res.ID, s.RepoRelative(anchor)); err != nil {
				return err
			}
			snap, err := s.Get(res.ID)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					fmt.Fprintf(w, "tracking %s %q <- %s\n", render.ShortID(snap.ID), snap.Title, snap.TrackedPath)
				},
				map[string]string{"status": "tracking", "id": snap.ID.String(), "path": snap.TrackedPath})
		},
	}
	c.Flags().StringVar(&tier, "tier", "personal", "tier: private|personal|shared, or a custom tier (kref tier list)")
	c.Flags().StringVar(&kind, "kind", "document", "entry kind for a newly-ingested file")
	_ = c.RegisterFlagCompletionFunc("kind", completeKindWithDefault(dir))
	applyGuide(c, cobra.ExactArgs(1), argGuide{noun: "a markdown file path", find: "ls *.md", usage: "kref track <path>", examples: []string{
		"kref track docs/notes.md      # track a file already in the repo",
		"kref track ~/scratch/idea.md  # a floater: copied under .kref/ and tracked",
	}})
	return c
}

func newUntrackCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "untrack <id|path>",
		Short: "Stop syncing an entry with its local file (the file on disk is left in place)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}
			if err := s.Untrack(id); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { fmt.Fprintf(w, "untracked %s\n", render.ShortID(id)) },
				map[string]string{"status": "untracked", "id": id.String()})
		},
	}
	c.ValidArgsFunction = entryArgs(dir, 1, sourceAll)
	applyGuide(c, cobra.ExactArgs(1), argGuide{noun: "an entry id or path", find: "kref list", usage: "kref untrack <id|path>", examples: []string{
		"kref untrack a1b2c3d4   # stop syncing; the file stays on disk",
	}})
	return c
}

func newReconcileCmd(dir *string) *cobra.Command {
	var yes, force, dryRun, write bool
	c := &cobra.Command{
		Use:               "reconcile [<id|path>]",
		ValidArgsFunction: entryArgs(dir, 1, sourceAll),
		Short:             "Reconcile a tracked file with its entry (pull by default; --write pushes entry → file)",
		Long: "By default reconcile re-reads tracked markdown files and updates their entries when " +
			"the file changed (idempotent; the default is pull-only — it never writes files), " +
			"self-healing a moved file by re-pointing it. With --write it reverses direction, " +
			"writing each entry's body back out to its tracked file when the file is a safe " +
			"fast-forward; a file that has diverged (holds edits the entry never saw) is shown as a " +
			"unified diff and left untouched unless --force overwrites it. With no argument it " +
			"sweeps every tracked entry after a confirmation. In pull mode a secret in a file fails " +
			"closed unless --force.",
		Args: cobra.MaximumNArgs(1),
		Example: exampleBlock([]string{
			"kref reconcile                       # pull all tracked files (asks to confirm)",
			"kref reconcile docs/note.md          # pull one tracked file",
			"kref reconcile docs/note.md --write  # push the entry back out to its file",
			"kref reconcile --write --dry-run     # preview write-back (and diffs) for all",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			actor, actorKind := resolveActor(cmd, s)

			var targets []*entry.Snapshot
			bulk := len(args) != 1
			if !bulk {
				id, err := resolveReconcileArg(s, args[0])
				if err != nil {
					return err
				}
				snap, err := s.Get(id)
				if err != nil {
					return err
				}
				if !snap.Tracked {
					return fmt.Errorf("%s is not tracked — run `kref track <path>` first", render.ShortID(id))
				}
				targets = []*entry.Snapshot{snap}
			} else {
				all, err := s.List(store.ListFilter{})
				if err != nil {
					return err
				}
				for _, snap := range all {
					if snap.Tracked {
						targets = append(targets, snap)
					}
				}
			}

			if write {
				return reconcileWrite(cmd, s, targets, bulk, dryRun, force, yes, actor, actorKind)
			}

			// Pull mode (default): file → entry.
			if bulk && len(targets) == 0 {
				return emit(cmd,
					func(w io.Writer, _ bool) { fmt.Fprintln(w, "no tracked entries to reconcile") },
					[]bridge.ReconcileResult{})
			}
			if bulk && !yes && !dryRun {
				out := cmd.ErrOrStderr()
				fmt.Fprintf(out, "About to reconcile %d tracked file(s) from disk into their entries.\n", len(targets))
				fmt.Fprint(out, "Type 'yes' to proceed: ")
				line, rErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if rErr != nil && rErr != io.EOF {
					return rErr
				}
				if strings.TrimSpace(line) != "yes" {
					fmt.Fprintln(out, "aborted; nothing reconciled.")
					return nil
				}
			}

			// Build the trailer index lazily — only when a tracked file is missing.
			var index map[string][]string
			for _, snap := range targets {
				if _, statErr := os.Stat(filepath.Join(s.Root(), filepath.FromSlash(snap.TrackedPath))); os.IsNotExist(statErr) {
					index, err = bridge.BuildTrailerIndex(s.Root())
					if err != nil {
						return err
					}
					break
				}
			}

			results := make([]bridge.ReconcileResult, 0, len(targets))
			errCount := 0
			for _, snap := range targets {
				res, rErr := bridge.Reconcile(s, snap, index, dryRun, force, actor, actorKind)
				if rErr != nil {
					return rErr
				}
				if res.Forced {
					fmt.Fprintf(cmd.ErrOrStderr(), "WARNING: forced a secret into %s (tier %s) %q\n",
						render.ShortID(res.ID), snap.Tier, snap.Title)
				}
				if res.Action == "error" {
					errCount++
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", res.Error)
				}
				results = append(results, res)
			}

			if err := emit(cmd,
				func(w io.Writer, _ bool) {
					var n struct{ synced, unchanged, relocated, missing, ambiguous, errored int }
					for _, r := range results {
						switch r.Action {
						case "synced":
							n.synced++
						case "unchanged":
							n.unchanged++
						case "relocated":
							n.relocated++
						case "missing":
							n.missing++
						case "ambiguous":
							n.ambiguous++
						case "error":
							n.errored++
						}
						if r.Action != "unchanged" {
							fmt.Fprintf(w, "%-10s %s  %s\n", r.Action, render.ShortID(r.ID), r.Path)
						}
					}
					fmt.Fprintf(w, "reconciled %d: %d synced, %d unchanged, %d relocated, %d missing, %d ambiguous, %d error\n",
						len(results), n.synced, n.unchanged, n.relocated, n.missing, n.ambiguous, n.errored)
				},
				results); err != nil {
				return err
			}
			if errCount > 0 {
				return fmt.Errorf("%d file(s) failed to reconcile", errCount)
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the bulk confirmation prompt")
	c.Flags().BoolVar(&write, "write", false, "push entries out to their tracked files (entry → file) instead of pulling")
	c.Flags().BoolVar(&force, "force", false, "force past the active guard: pull a secret (default) or overwrite a diverged file (--write)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "report what would change without writing (a drift report; --write also shows diffs)")
	return c
}

// reconcileWrite runs the --write (entry → file) direction of reconcile over the
// resolved targets: it pushes each entry back to its tracked file on a safe
// fast-forward, shows a unified diff and refuses for a diverged file (unless
// force overwrites it), and skips a missing file. A bulk run confirms first
// (unless yes or dryRun) because writing files is destructive. It returns a
// non-nil error when any file diverged-unresolved or errored, so the command
// exits nonzero until the tree is clean.
func reconcileWrite(cmd *cobra.Command, s *store.Store, targets []*entry.Snapshot, bulk, dryRun, force, yes bool, actor, actorKind string) error {
	if bulk && len(targets) == 0 {
		return emit(cmd,
			func(w io.Writer, _ bool) { fmt.Fprintln(w, "no tracked entries to write back") },
			[]bridge.WriteBackResult{})
	}
	if bulk && !yes && !dryRun {
		out := cmd.ErrOrStderr()
		if force {
			fmt.Fprintf(out, "About to write %d entr(y/ies) back to their files; --force will OVERWRITE any diverged file.\n", len(targets))
		} else {
			fmt.Fprintf(out, "About to write %d entr(y/ies) back to their files (safe fast-forwards only; diverged files are shown, not written).\n", len(targets))
		}
		fmt.Fprint(out, "Type 'yes' to proceed: ")
		line, rErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if rErr != nil && rErr != io.EOF {
			return rErr
		}
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(out, "aborted; nothing written.")
			return nil
		}
	}

	results := make([]bridge.WriteBackResult, 0, len(targets))
	blocked := 0
	for _, snap := range targets {
		res, rErr := bridge.WriteBack(s, snap, dryRun, force, actor, actorKind)
		if rErr != nil {
			return rErr
		}
		if res.Action == "forced" {
			fmt.Fprintf(cmd.ErrOrStderr(), "WARNING: forced entry %s (tier %s) over diverged file %s\n",
				render.ShortID(res.ID), snap.Tier, res.Path)
		}
		// WriteBack never yields a per-entry "error" action — an I/O failure is a
		// Go error returned above, which aborts the run. Divergence is the only
		// non-fatal blocker.
		if res.Action == "diverged" {
			blocked++
		}
		results = append(results, res)
	}

	if err := emit(cmd,
		func(w io.Writer, _ bool) {
			var n struct{ written, inSync, missing, diverged, forced int }
			for _, r := range results {
				switch r.Action {
				case "written":
					n.written++
				case "in-sync":
					n.inSync++
				case "missing":
					n.missing++
				case "diverged":
					n.diverged++
				case "forced":
					n.forced++
				}
				if r.Action != "in-sync" {
					fmt.Fprintf(w, "%-10s %s  %s\n", r.Action, render.ShortID(r.ID), r.Path)
				}
				if r.Diff != "" {
					fmt.Fprint(w, r.Diff)
					if r.Action == "diverged" {
						fmt.Fprintf(w, "resolve: `kref reconcile %s` (pull) or `kref reconcile %s --write --force` (push)\n",
							render.ShortID(r.ID), render.ShortID(r.ID))
					}
				}
			}
			fmt.Fprintf(w, "wrote %d: %d written, %d in-sync, %d missing, %d diverged, %d forced\n",
				len(results), n.written, n.inSync, n.missing, n.diverged, n.forced)
		},
		results); err != nil {
		return err
	}
	if blocked > 0 {
		return fmt.Errorf("%d tracked file(s) diverged or errored — resolve before writing back", blocked)
	}
	return nil
}

// numDigits returns the base-10 digit count of n (minimum 1).
func numDigits(n int) int {
	if n < 1 {
		n = 1
	}
	d := 0
	for n > 0 {
		d++
		n /= 10
	}
	return d
}

// renderPagerBody renders the entry body for the pager, wrapping markdown to
// width minus the line-number gutter. The gutter width depends on the rendered
// line count, which depends on the wrap width, so it runs a bounded fixed-point
// (converges in ≤2 passes in practice). Returns the body lines and the total
// gutter width (digits+3 for " │ ").
func renderPagerBody(snap *entry.Snapshot, color bool, width int) ([]string, int) {
	if width <= 0 {
		width = 80
	}
	renderAt := func(cw int) []string {
		if cw < 1 {
			cw = 1
		}
		var b bytes.Buffer
		render.RenderBody(&b, snap.Body, snap.ContentType, color, cw)
		return strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	}
	d := numDigits(len(renderAt(width)))
	var lines []string
	for range 4 {
		lines = renderAt(width - (d + 3))
		nd := numDigits(len(lines))
		if nd == d {
			break
		}
		d = nd
	}
	return lines, d + 3
}

// renderFoldedBody renders snap.Body block-by-block with the given fold applied
// (at wrap width w), then appends the comment block, returning the display lines
// and each surviving heading's body-relative rendered offset. Block-by-block is
// what lets the pager know a heading's *rendered* line — glamour reflows, so the
// offset can't come from the raw source. Mirrors the cockpit's body zone.
func renderFoldedBody(snap *entry.Snapshot, opts render.ShowOptions, folded map[string]bool, w int) foldedBody {
	if w < 1 {
		w = 1
	}
	foldedSrc := outline.Parse(snap.Body).Render(folded)
	foldedLines := strings.Split(foldedSrc, "\n")
	hs := outline.Parse(foldedSrc).Headings()
	var out []string
	var rh []renderedHeading
	for _, span := range headingBlocks(hs, len(foldedLines)) {
		blockLines := append([]string(nil), foldedLines[span.start:span.end]...)
		isSection := span.heading != nil
		if isSection {
			blockLines[0] = injectMarker(blockLines[0], span.heading.Level, folded[span.heading.Path])
		}
		var b bytes.Buffer
		render.RenderBody(&b, strings.Join(blockLines, "\n"), snap.ContentType, opts.Color, w)
		rendered := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
		if isSection {
			// Drop RenderBody's markdown top-margin so the heading is the block's
			// first line and its recorded offset lands on the heading.
			for len(rendered) > 1 && strings.TrimSpace(rendered[0]) == "" {
				rendered = rendered[1:]
			}
			rh = append(rh, renderedHeading{path: span.heading.Path, line: len(out)})
		}
		out = append(out, rendered...)
	}
	if len(snap.Comments) > 0 {
		var cb bytes.Buffer
		render.RenderComments(&cb, snap.Comments, opts.Color, opts.Width)
		clines := strings.Split(strings.TrimRight(cb.String(), "\n"), "\n")
		out = append(out, "")
		rh = append(rh, renderedHeading{path: commentsFoldPath, line: len(out)})
		// Keep the "Comments (N)" header (marked ▾/▸) as the fold affordance; fold
		// the threads beneath it to a hint. Whole-block only — per-thread folding
		// stays cockpit-only.
		marker := "▾ "
		if folded[commentsFoldPath] {
			marker = "▸ "
		}
		out = append(out, marker+clines[0])
		if folded[commentsFoldPath] {
			if hidden := len(clines) - 1; hidden > 0 {
				out = append(out, fmt.Sprintf("▸ %d lines", hidden))
			}
		} else {
			out = append(out, clines[1:]...)
		}
	}
	return foldedBody{lines: out, headings: rh}
}

// foldedPagerBody renders the unfolded fold-aware body and its gutter width via
// the same bounded fixed-point as renderPagerBody (the wrap width depends on the
// gutter, which depends on the line count). The returned width becomes the fixed
// wrap width for all subsequent folds so the layout does not reflow on a fold.
func foldedPagerBody(snap *entry.Snapshot, opts render.ShowOptions) (foldedBody, int) {
	width := opts.Width
	if width <= 0 {
		width = 80
	}
	empty := map[string]bool{}
	d := numDigits(len(renderFoldedBody(snap, opts, empty, width).lines))
	var fb foldedBody
	for range 4 {
		fb = renderFoldedBody(snap, opts, empty, width-(d+3))
		nd := numDigits(len(fb.lines))
		if nd == d {
			break
		}
		d = nd
	}
	return fb, d + 3
}

// showPagerContent composes the pager input for one snapshot: an un-numbered
// header above a numbered body (fancy mode only).
func showPagerContent(snap *entry.Snapshot, opts render.ShowOptions) pagerContent {
	title := render.ShortID(snap.ID) + "  " + snap.Title
	var header []string
	if !opts.NoHeader {
		var hb bytes.Buffer
		render.ShowHeader(&hb, snap, opts.Color, opts.TrackedNote, opts.Favorites)
		header = strings.Split(strings.TrimRight(hb.String(), "\n"), "\n")
		header = append(header, "") // blank line between header and body
	}
	pc := pagerContent{title: title, header: header}
	if opts.Raw {
		pc.body = strings.Split(strings.TrimRight(snap.Body, "\n"), "\n")
		pc.body = appendPagerComments(pc.body, snap, opts)
		return pc
	}
	if content.IsMarkdown(snap.ContentType) {
		// Fold-aware markdown: block-by-block render (comments included) so the
		// pager can fold sections and map the viewport to a heading. foldRender
		// re-renders at the fixed wrap width on each fold.
		fb, gw := foldedPagerBody(snap, opts)
		width := opts.Width
		if width <= 0 {
			width = 80
		}
		wrapW := width - gw
		pc.body = fb.lines
		pc.gutterW = gw
		pc.number = true
		pc.rawBody = snap.Body
		pc.markdown = true
		pc.hasComments = len(snap.Comments) > 0
		pc.foldRender = func(folded map[string]bool) foldedBody {
			return renderFoldedBody(snap, opts, folded, wrapW)
		}
		return pc
	}
	// Non-markdown (e.g. code, JSON): render once, no fold.
	pc.body, pc.gutterW = renderPagerBody(snap, opts.Color, opts.Width)
	pc.number = true
	pc.body = appendPagerComments(pc.body, snap, opts)
	if len(snap.Comments) > 0 {
		pc.gutterW = numDigits(len(pc.body)) + 3 // keep the gutter wide enough after appending
	}
	return pc
}

// appendPagerComments appends the rendered comment block (if any) to body, with a
// separating blank line. Used by the non-fold pager paths; the markdown fold path
// appends comments inside renderFoldedBody so a fold re-render keeps them.
func appendPagerComments(body []string, snap *entry.Snapshot, opts render.ShowOptions) []string {
	if len(snap.Comments) == 0 {
		return body
	}
	var cb bytes.Buffer
	render.RenderComments(&cb, snap.Comments, opts.Color, opts.Width)
	body = append(body, "")
	return append(body, strings.Split(strings.TrimRight(cb.String(), "\n"), "\n")...)
}

// showPaged runs the interactive pager over one entry. refetch (optional)
// backs the pager's r hotkey: it must return a freshly-read snapshot — the
// reason to refresh is that another process (an editing agent, a sync) has
// changed the entry since it was opened.
func showPaged(snap *entry.Snapshot, opts render.ShowOptions, refetch func() (*entry.Snapshot, render.ShowOptions, error), expand func() ([]string, error)) error {
	pc := showPagerContent(snap, opts)
	if refetch != nil {
		pc.reload = func() (pagerContent, error) {
			s2, o2, err := refetch()
			if err != nil {
				return pagerContent{}, err
			}
			return showPagerContent(s2, o2), nil
		}
	}
	pc.expand = expand
	return Page(pc)
}

// showEntryPaged runs the interactive show pager for one entry, wiring the
// r-hotkey refetch (fresh store handle) and the e-hotkey extended-header expand.
// Shared by `kref show` and the list cockpit's open action.
func showEntryPaged(dir *string, snap *entry.Snapshot, opts render.ShowOptions) error {
	id := snap.ID
	refetch := func() (*entry.Snapshot, render.ShowOptions, error) {
		s2, err := store.Open(*dir)
		if err != nil {
			return nil, opts, err
		}
		defer s2.Close()
		snap2, err := s2.Get(id)
		if err != nil {
			return nil, opts, err
		}
		if m, mErr := s2.Merged(id); mErr == nil {
			snap2.Merged = m
		}
		opts2 := opts
		opts2.TrackedNote = ""
		if snap2.Tracked {
			drift2, dErr := bridge.DriftState(s2, snap2)
			if dErr != nil {
				return nil, opts, dErr
			}
			opts2.TrackedNote = snap2.TrackedPath + " [" + drift2 + "]"
		}
		return snap2, opts2, nil
	}
	expand := func() ([]string, error) {
		s2, err := store.Open(*dir)
		if err != nil {
			return nil, err
		}
		defer s2.Close()
		log, err := s2.Log(id)
		if err != nil {
			return nil, err
		}
		links, err := s2.Links(id)
		if err != nil {
			return nil, err
		}
		var hb bytes.Buffer
		render.ExtendedHeader(&hb, snap, time.Now(), log, links, opts.Color, opts.TrackedNote, opts.Favorites)
		hdr := strings.Split(strings.TrimRight(hb.String(), "\n"), "\n")
		hdr = append(hdr, "") // blank line between header and body
		return hdr, nil
	}
	return showPaged(snap, opts, refetch, expand)
}

// openEntry opens a single entry in the interactive viewer the list cockpit
// dispatches to: the todo cockpit for a todo, the show pager otherwise.
func openEntry(cmd *cobra.Command, dir *string, s *store.Store, snap *entry.Snapshot) error {
	if snap.Kind == todoguard.TodoKind {
		return runTodoCockpit(cmd, dir, snap.ID.String(), false, false)
	}
	opts := render.ShowOptions{
		Color:     useColor(cmd),
		Width:     ttyWidth(),
		Favorites: favoritesFor(s.Favorites(), snap.ID),
	}
	if snap.Tracked {
		if drift, err := bridge.DriftState(s, snap); err == nil {
			opts.TrackedNote = snap.TrackedPath + " [" + drift + "]"
		}
	}
	return showEntryPaged(dir, snap, opts)
}

func newShowCmd(dir *string) *cobra.Command {
	var noHeader, headerOnly, noPager bool
	c := &cobra.Command{
		Use:     "show [<id>]",
		Aliases: []string{"cat", "view", "get"},
		Short:   "Show an entry",
		Args:    cobra.MaximumNArgs(1),
		Example: exampleBlock([]string{
			"kref show a1b2c3d4           # view one entry",
			"kref show a1b2c3d4 --header  # just the metadata block, no body, no pager",
			"kref show                    # the most-recently-modified entry",
			"kref show ./docs/note.md     # address it by the file it came from",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			if headerOnly && noHeader {
				return errors.New("give --header or --no-header, not both")
			}
			if headerOnly && plainMode(cmd) {
				return errors.New("--header and --plain are contradictory (metadata-only vs verbatim body)")
			}
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveTargetOrRecent(cmd, s, args)
			if err != nil {
				return err
			}
			snap, err := s.Get(id)
			if err != nil {
				return err
			}
			if m, mErr := s.Merged(id); mErr == nil {
				snap.Merged = m
			}
			var drift string
			if snap.Tracked {
				if drift, err = bridge.DriftState(s, snap); err != nil {
					return err
				}
			}
			favNames := favoritesFor(s.Favorites(), id)
			if jsonMode(cmd) {
				return writeJSON(cmd, struct {
					*entry.Snapshot
					Favorites []string `json:"favorites"`
				}{snap, favNames})
			}
			plain := plainMode(cmd)
			opts := render.ShowOptions{
				Raw:        plain,
				NoHeader:   noHeader || plain,
				HeaderOnly: headerOnly,
				Color:      useColor(cmd),
				Width:      ttyWidth(),
				Favorites:  favNames,
			}
			if snap.Tracked {
				opts.TrackedNote = snap.TrackedPath + " [" + drift + "]"
			}
			// --header is a chrome-free metadata peek: no body, and never paged
			// (the block is short by design).
			if usePager(cmd) && !noPager && !headerOnly {
				return showEntryPaged(dir, snap, opts)
			}
			var buf bytes.Buffer
			render.Show(&buf, snap, opts)
			fmt.Fprint(cmd.OutOrStdout(), buf.String())
			return nil
		},
	}
	c.Flags().BoolVar(&noHeader, "no-header", false, "omit the metadata header block")
	c.Flags().BoolVar(&headerOnly, "header", false, "print only the metadata header block (no body, no pager)")
	c.Flags().BoolVar(&noPager, "no-pager", false, "do not page output even on a terminal")
	c.ValidArgsFunction = entryArgs(dir, 1, sourceAll)
	return c
}

// listColumnsHelpSentinel is the NoOptDefVal for --columns: a bare `--columns`
// (or `--columns=help`) sets this, signalling "print the available columns"
// rather than selecting any. Selecting columns uses the `--columns=a,b,c` form.
// The value is shown verbatim by pflag's help as `--columns[="help"]`, so it
// must be plain, readable text (no control characters) and not a real column.
const listColumnsHelpSentinel = "help"

func newListCmd(dir *string) *cobra.Command {
	var kind, status, tier string
	var labels []string
	var includeDeleted, all, newOnly, check bool
	var wide, archived, noPager bool
	var openQuestions bool
	var columns, sortBy string
	c := &cobra.Command{
		Use:               "list",
		Aliases:           []string{"ls"},
		ValidArgsFunction: cobra.NoFileCompletions,
		Short:             "List entries",
		Example: exampleBlock([]string{
			"kref list                 # all entries",
			"kref list --tier private  # filter by tier",
			"kref list --new           # incoming + unpushed since last sync",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			if columns == listColumnsHelpSentinel {
				if len(args) > 0 {
					return fmt.Errorf("to choose columns use --columns=%s (with '='); bare --columns lists the available columns", strings.Join(args, ","))
				}
				fmt.Fprint(cmd.OutOrStdout(), render.ColumnHelp())
				return nil
			}
			plain := plainMode(cmd)
			jsonOut := jsonMode(cmd)
			columnsSet := cmd.Flags().Changed("columns")
			if columnsSet && wide {
				return errors.New("use one of --columns or --wide, not both")
			}
			if jsonOut && (columnsSet || wide) {
				return errors.New("--columns/--wide are not compatible with --json")
			}
			if newOnly && (plain || columnsSet || wide || cmd.Flags().Changed("sort")) {
				return errors.New("--plain/--columns/--wide/--sort are not compatible with --new")
			}
			var sortSpec *render.SortSpec
			if sortBy != "" {
				var perr error
				if sortSpec, perr = render.ParseSort(sortBy); perr != nil {
					return perr
				}
			}
			if check && plain {
				return errors.New("--check is not compatible with --plain")
			}
			var cols []render.Column
			switch {
			case columnsSet:
				var perr error
				if cols, perr = render.ParseColumns(columns); perr != nil {
					return perr
				}
			case wide:
				cols = render.WideColumns
			default:
				cols = render.DefaultColumns
			}
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			if newOnly {
				incoming, unpushed, err := s.WhatsNew()
				if err != nil {
					return err
				}
				return emit(cmd,
					func(w io.Writer, color bool) { render.WhatsNew(w, incoming, unpushed, color) },
					map[string]any{"incoming": incoming, "unpushed": unpushed})
			}
			var t entry.Tier
			if tier != "" {
				tdef, err := s.TierDef(tier)
				if err != nil {
					return err
				}
				t = tdef.Name
			}
			lf := store.ListFilter{
				Kind: kind, Status: status, Tier: t, Labels: labels,
				IncludeDelete: includeDeleted || all, ArchivedOnly: archived, IncludeArchived: all,
				OpenQuestionsOnly: openQuestions,
			}
			var items []*entry.Snapshot
			if jsonMode(cmd) || check || openQuestions {
				// --json needs full bodies/links/provenance; --check compares
				// against snap.Body for drift. Both use the full DAG read.
				items, err = s.List(lf)
			} else {
				// Table/--plain view: serve the lean metadata from the excerpt
				// cache (O(entries), no DAG recompile).
				var exs []store.Excerpt
				exs, err = s.ListExcerpts(lf)
				if err == nil {
					items = make([]*entry.Snapshot, len(exs))
					for i, e := range exs {
						items[i] = e.ToSnapshot()
					}
				}
			}
			if err != nil {
				return err
			}
			// Merged is a per-entry ref-graph walk; acceptable at kref scale.
			for _, it := range items {
				if m, mErr := s.Merged(it.ID); mErr == nil {
					it.Merged = m
				}
			}
			// Favorited entries pin to the top of every view; the id-set is the
			// values of the merged (user + shared) favorites map.
			favIDs := map[string]bool{}
			for _, id := range s.Favorites() {
				favIDs[id] = true
			}
			// Order items here so --json and --plain come out sorted; the table
			// renderer re-applies the spec (and the same pinning) to its
			// post-collapse rows.
			render.SortSnapshots(items, sortSpec, favIDs)
			var drift map[string]string
			if check {
				drift = make(map[string]string, len(items))
				for _, it := range items {
					if it.Tracked {
						st, dErr := bridge.DriftState(s, it)
						if dErr != nil {
							return dErr
						}
						drift[it.ID.String()] = st
					}
				}
			}
			// Surface the review queue from the command users start with. Skipped
			// for --plain (a machine format) and --json (structured entries only).
			var pending []store.QuarantineItem
			if !plain && !jsonOut {
				if q, qErr := s.QuarantineQueue(); qErr == nil {
					pending = q
				}
			}
			human := func(w io.Writer, color bool) {
				if len(pending) > 0 {
					renderQuarantineBanner(w, pending)
				}
				render.RenderList(w, items, render.ListOptions{
					Columns: cols, Plain: plain, Color: color, ShowAll: all, Sort: sortSpec, Favorites: favIDs,
				})
				if len(drift) > 0 {
					fmt.Fprintln(w, "\ntracked file drift (--check):")
					for _, it := range items {
						if st, ok := drift[it.ID.String()]; ok {
							fmt.Fprintf(w, "  %-9s %s  %s\n", st, render.ShortID(it.ID), it.TrackedPath)
						}
					}
				}
			}
			// On a terminal, the interactive list cockpit: navigate rows, open the
			// selected one in the existing viewer, act inline. --plain/--json/--check
			// keep the static output (a machine format bypasses like a pipe would).
			if usePager(cmd) && !noPager && !plain && !jsonOut && !check {
				acts := listCockpitActions{s: s, filter: lf}
				return runListCockpit(acts,
					render.ListOptions{Columns: cols, Color: useColor(cmd), ShowAll: all, Sort: sortSpec, Favorites: favIDs},
					useColor(cmd), lf,
					func(res listResult) error {
						switch res.action {
						case "review":
							queue, qerr := acts.QuarantineQueue()
							if qerr != nil {
								return qerr
							}
							start := 0
							for i, it := range queue {
								if it.ID == res.id {
									start = i
									break
								}
							}
							rr, rerr := runReviewModel(acts, queue, start, useColor(cmd), ttyWidth())
							if rerr != nil {
								return rerr
							}
							if rr.action == "open" {
								snap, gerr := s.Get(rr.target)
								if gerr != nil {
									return gerr
								}
								return openEntry(cmd, dir, s, snap)
							}
							return nil
						case "open":
							snap, gerr := s.Get(res.id)
							if gerr != nil {
								return gerr
							}
							return openEntry(cmd, dir, s, snap)
						case "edit":
							return editEntry(cmd, s, res.id)
						}
						return nil
					})
			}
			return emit(cmd, human, items)
		},
	}
	c.Flags().BoolVar(&noPager, "no-pager", false, "do not page output even on a terminal")
	c.Flags().BoolVar(&check, "check", false, "check each tracked file for drift (reads files)")
	c.Flags().BoolVar(&openQuestions, "open-questions", false, "only entries with an unresolved question comment")
	c.Flags().StringVar(&kind, "kind", "", "filter by kind")
	c.Flags().StringVar(&status, "status", "", "filter by status")
	c.Flags().StringVar(&tier, "tier", "", "filter by tier (kref tier list shows them)")
	c.Flags().StringArrayVar(&labels, "label", nil, "filter by label (repeatable, AND)")
	c.Flags().BoolVar(&includeDeleted, "include-deleted", false, "include soft-deleted (tombstoned) entries")
	c.Flags().BoolVar(&all, "all", false, "show everything: superseded + tombstoned, uncollapsed")
	c.Flags().BoolVar(&newOnly, "new", false, "show what changed since your last sync (incoming + unpushed)")
	c.Flags().StringVar(&columns, "columns", "", "columns to show, e.g. --columns=id,kind,author (bare --columns lists all)")
	c.Flags().Lookup("columns").NoOptDefVal = listColumnsHelpSentinel
	c.Flags().BoolVarP(&wide, "wide", "w", false, "preset: tier,id,kind,status,author,edited,title")
	c.Flags().BoolVar(&archived, "archived", false, "show only archived entries")
	c.Flags().StringVar(&sortBy, "sort", "edited", "order by a field, e.g. --sort title or --sort tier — dates put newest first; :asc/:desc overrides")
	registerEntryFlagCompletions(c, dir)
	_ = c.RegisterFlagCompletionFunc("status", fixedFlag(statusValues))
	_ = c.RegisterFlagCompletionFunc("columns", completeColumns)
	_ = c.RegisterFlagCompletionFunc("sort", fixedFlag(sortFlagValues()))
	return c
}

func newSearchCmd(dir *string) *cobra.Command {
	var kind, status, tier, sortBy string
	var labels []string
	var noPager bool
	c := &cobra.Command{
		Use:               "search <query>",
		ValidArgsFunction: cobra.NoFileCompletions,
		Short:             "Search entries and count matches per entry",
		Args:              cobra.ExactArgs(1),
		Example: exampleBlock([]string{
			"kref search auth                # case-insensitive title/body substring",
			"kref search auth --tier shared  # composes with the list filters",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			var t entry.Tier
			if tier != "" {
				tdef, err := s.TierDef(tier)
				if err != nil {
					return err
				}
				t = tdef.Name
			}
			results, err := s.Search(store.ListFilter{
				Kind: kind, Status: status, Tier: t, Search: args[0], Labels: labels,
			})
			if err != nil {
				return err
			}
			if err := sortSearchResults(results, sortBy); err != nil {
				return err
			}
			plain := plainMode(cmd)
			human := func(w io.Writer, color bool) {
				hits := make([]render.SearchHit, len(results))
				for i, r := range results {
					hits[i] = render.SearchHit{Snap: r.Snapshot, Matches: r.Matches}
				}
				if plain {
					render.PlainSearchResults(w, hits)
					return
				}
				render.SearchResults(w, hits, color)
			}
			if usePager(cmd) && !noPager {
				var buf bytes.Buffer
				human(&buf, useColor(cmd))
				return Page(pagerContent{
					title: "kref search — " + args[0],
					body:  strings.Split(strings.TrimRight(buf.String(), "\n"), "\n"),
				})
			}
			return emit(cmd, human, results)
		},
	}
	c.Flags().BoolVar(&noPager, "no-pager", false, "do not page output even on a terminal")
	c.Flags().StringVar(&kind, "kind", "", "filter by kind")
	c.Flags().StringVar(&status, "status", "", "filter by status")
	c.Flags().StringVar(&tier, "tier", "", "filter by tier (kref tier list shows them)")
	c.Flags().StringArrayVar(&labels, "label", nil, "filter by label (repeatable, AND)")
	c.Flags().StringVar(&sortBy, "sort", "", "order by a field, e.g. --sort title or --sort updated — dates put newest first; :asc/:desc overrides (default: matches:desc)")
	registerEntryFlagCompletions(c, dir)
	_ = c.RegisterFlagCompletionFunc("status", fixedFlag(statusValues))
	_ = c.RegisterFlagCompletionFunc("sort", fixedFlag(sortFlagValues("matches")))
	return c
}

// sortSearchResults reorders results per a --sort value, which for search
// accepts "matches" (the default order, descending) on top of the shared
// field keys. An empty value keeps store.Search's matches:desc order.
func sortSearchResults(results []store.SearchResult, sortBy string) error {
	if sortBy == "" {
		return nil
	}
	if key, dir, hasDir := strings.Cut(strings.TrimSpace(sortBy), ":"); key == "matches" {
		desc := false
		switch {
		case !hasDir || dir == "asc":
		case dir == "desc":
			desc = true
		default:
			return fmt.Errorf("unknown sort direction %q (want asc or desc)", dir)
		}
		sort.SliceStable(results, func(i, j int) bool {
			if desc {
				return results[j].Matches < results[i].Matches
			}
			return results[i].Matches < results[j].Matches
		})
		return nil
	}
	spec, err := render.ParseSort(sortBy)
	if err != nil {
		return err
	}
	sort.SliceStable(results, func(i, j int) bool { return spec.Less(results[i].Snapshot, results[j].Snapshot) })
	return nil
}

// registerEntryFlagCompletions wires --kind/--label/--tier (all drawn from the
// store, so custom tiers complete like built-ins) on commands that filter or
// set those fields. Each command only registers the flags it actually defines;
// an unknown flag is a no-op error we deliberately ignore.
func registerEntryFlagCompletions(c *cobra.Command, dir *string) {
	_ = c.RegisterFlagCompletionFunc("kind", completeStoreField(dir, func(e store.Excerpt) []string { return []string{e.Kind} }))
	_ = c.RegisterFlagCompletionFunc("label", completeStoreField(dir, func(e store.Excerpt) []string { return e.Labels }))
	_ = c.RegisterFlagCompletionFunc("tier", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return fixedValues(allTierNames(dir), toComplete), cobra.ShellCompDirectiveNoFileComp
	})
}

func newRmCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <id>",
		Aliases: []string{"remove", "delete", "del"},
		Short:   "Soft-delete an entry (tombstone; not safe for secrets)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}
			snap, err := s.Get(id)
			if err != nil {
				return err
			}
			if err := s.Tombstone(id); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) { render.Action(w, "tombstoned", snap, color) },
				map[string]string{"status": "tombstoned", "id": id.String()})
		},
	}
	c.ValidArgsFunction = entryArgs(dir, 1, sourceAll)
	applyGuide(c, cobra.ExactArgs(1), argGuide{noun: "an entry id", find: "kref list", usage: "kref rm <id>", examples: []string{
		"kref rm a1b2c3d4   # soft-delete (tombstone); undo with kref restore",
	}})
	return c
}

func newRestoreCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "restore <id>",
		Short: "Restore a soft-deleted entry",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}
			if err := s.Restore(id); err != nil {
				return err
			}
			snap, err := s.Get(id)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) { render.Action(w, "restored", snap, color) },
				map[string]string{"status": "restored", "id": id.String()})
		},
	}
	c.ValidArgsFunction = entryArgs(dir, 1, sourceDeleted)
	applyGuide(c, cobra.ExactArgs(1), argGuide{noun: "an entry id", find: "kref list --include-deleted", usage: "kref restore <id>", examples: []string{
		"kref restore a1b2c3d4        # bring back a tombstoned entry",
		"kref restore ./docs/note.md  # address it by the file it came from",
	}})
	return c
}

func newArchiveCmd(dir *string) *cobra.Command {
	var obsolete, accepted, yes bool
	c := &cobra.Command{
		Use:               "archive [<id|path>]",
		ValidArgsFunction: entryArgs(dir, 1, sourceAll),
		Short:             "Archive an entry (hide it from the normal list; still listable with --archived)",
		Args:              cobra.MaximumNArgs(1),
		Example: exampleBlock([]string{
			"kref archive a1b2c3d4      # hide one entry",
			"kref archive --obsolete    # archive every obsolete entry (asks to confirm)",
			"kref archive --accepted    # archive every accepted entry (asks to confirm)",
			"kref archive --obsolete -y # ...without the confirmation prompt",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			if obsolete && accepted {
				return errors.New("give --obsolete or --accepted, not both")
			}
			byStatus := obsolete || accepted
			if byStatus && len(args) > 0 {
				return errors.New("give an entry id or a status flag (--obsolete/--accepted), not both")
			}
			if !byStatus && len(args) == 0 {
				return errors.New("give an entry id to archive, or --obsolete/--accepted to archive every entry in that status")
			}
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()

			if byStatus {
				status := "obsolete"
				if accepted {
					status = "accepted"
				}
				return archiveByStatus(cmd, s, status, yes)
			}
			id, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}
			if err := s.Archive(id); err != nil {
				return err
			}
			snap, err := s.Get(id)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) { render.Action(w, "archived", snap, color) },
				map[string]string{"status": "archived", "id": id.String()})
		},
	}
	c.Flags().BoolVar(&obsolete, "obsolete", false, "archive every obsolete entry")
	c.Flags().BoolVar(&accepted, "accepted", false, "archive every accepted entry")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the --obsolete/--accepted confirmation prompt")
	return c
}

// archiveByStatus archives every non-archived entry in the given status,
// confirming first unless yes is set. It proceeds on a y/yes answer and aborts
// otherwise.
func archiveByStatus(cmd *cobra.Command, s *store.Store, status string, yes bool) error {
	// Status filter over the default (non-archived) set, so already-archived
	// entries in this status are not re-archived.
	matches, err := s.List(store.ListFilter{Status: status})
	if err != nil {
		return err
	}
	noun := "entries"
	if len(matches) == 1 {
		noun = "entry"
	}
	if len(matches) == 0 {
		return emit(cmd,
			func(w io.Writer, _ bool) { fmt.Fprintf(w, "no %s entries to archive\n", status) },
			map[string]int{"archived": 0})
	}
	if !yes {
		out := cmd.ErrOrStderr()
		fmt.Fprintf(out, "Archive %d %s %s? Type y to proceed: ", len(matches), status, noun)
		line, rErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if rErr != nil && rErr != io.EOF {
			return rErr
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
		default:
			fmt.Fprintln(out, "aborted; nothing archived.")
			return nil
		}
	}
	for _, snap := range matches {
		if err := s.Archive(snap.ID); err != nil {
			return err
		}
	}
	return emit(cmd,
		func(w io.Writer, _ bool) { fmt.Fprintf(w, "archived %d %s %s\n", len(matches), status, noun) },
		map[string]int{"archived": len(matches)})
}

func newUnarchiveCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "unarchive <id|path>",
		Short: "Unarchive an entry (return it to the normal list)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}
			if err := s.Unarchive(id); err != nil {
				return err
			}
			snap, err := s.Get(id)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) { render.Action(w, "unarchived", snap, color) },
				map[string]string{"status": "unarchived", "id": id.String()})
		},
	}
	c.ValidArgsFunction = entryArgs(dir, 1, sourceArchived)
	applyGuide(c, cobra.ExactArgs(1), argGuide{noun: "an entry id", find: "kref list --archived", usage: "kref unarchive <id>", examples: []string{
		"kref unarchive a1b2c3d4   # return an archived entry to the normal list",
	}})
	return c
}

// resolveTiers turns repeatable --tier flags into tiers: default = every
// resolved tier. Explicit names are accepted when resolved OR merely
// well-formed, so a bundle from a machine with tiers unknown here can still be
// imported by naming them.
func resolveTiers(s *store.Store, flags []string) ([]entry.Tier, error) {
	if len(flags) == 0 {
		return s.TierNames(), nil
	}
	tiers := make([]entry.Tier, 0, len(flags))
	for _, f := range flags {
		if _, err := s.TierDef(f); err != nil {
			switch f {
			case string(entry.TierPrivate), string(entry.TierPersonal), string(entry.TierShared):
			default:
				if vErr := entry.ValidateTierName(f); vErr != nil {
					return nil, err // the TierDef error names the known set
				}
			}
		}
		tiers = append(tiers, entry.Tier(f))
	}
	return tiers, nil
}

func newBundleCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "bundle",
		Short: "Export/import entries as portable git bundles (filter with --tier)",
	}

	var exportTiers []string
	export := &cobra.Command{
		Use:   "export [<file>]",
		Short: "Write a git bundle of entries (default all tiers; - or omitted = stdout)",
		Args:  cobra.MaximumNArgs(1),
		Example: exampleBlock([]string{
			"kref bundle export --tier private backup.bundle      # private only, to a file",
			"kref bundle export --tier private - | age -r AGE_KEY # pipe to an encryptor",
			"kref bundle export everything.bundle                 # all tiers",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			tiers, err := resolveTiers(s, exportTiers)
			if err != nil {
				return err
			}
			path := ""
			if len(args) == 1 && args[0] != "-" {
				path = args[0]
			}
			w := cmd.OutOrStdout()
			var f *os.File
			if path != "" {
				var cErr error
				f, cErr = os.Create(path)
				if cErr != nil {
					return cErr
				}
				w = f
			}
			if err := s.Export(w, tiers); err != nil {
				if f != nil {
					_ = f.Close()
				}
				return err
			}
			if f != nil {
				// Close is the write barrier: a failed flush here must not be
				// reported as a successful export of a backup bundle.
				if err := f.Close(); err != nil {
					return err
				}
				cmd.PrintErrf("exported to %s\n", path)
			}
			return nil
		},
	}
	export.Flags().StringArrayVar(&exportTiers, "tier", nil, "tier to include (repeatable; default: all)")

	var importTiers []string
	imp := &cobra.Command{
		Use:   "import <file>",
		Short: "Read a git bundle into the store (- = stdin; --tier to filter)",
		Args:  cobra.ExactArgs(1),
		Example: exampleBlock([]string{
			"kref bundle import --tier private backup.bundle  # restore private into a fresh clone",
			"age -d private.age | kref bundle import -        # decrypt then import from stdin",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			tiers, err := resolveTiers(s, importTiers)
			if err != nil {
				return err
			}
			path := args[0]
			if path == "-" {
				tmp, tErr := os.CreateTemp(xdg.CacheTempDir(), "kref-import-*.bundle")
				if tErr != nil {
					return tErr
				}
				defer func() { _ = os.Remove(tmp.Name()) }()
				if _, cErr := io.Copy(tmp, cmd.InOrStdin()); cErr != nil {
					_ = tmp.Close()
					return cErr
				}
				_ = tmp.Close()
				path = tmp.Name()
			}
			res, err := s.Import(path, tiers)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { fmt.Fprintf(w, "imported %d ref(s)\n", res.Refs) },
				res)
		},
	}
	imp.Flags().StringArrayVar(&importTiers, "tier", nil, "tier to import (repeatable; default: all)")

	c.AddCommand(export, imp)
	return c
}

func newVaultCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "vault",
		Short: "Back up / restore the private tier to a local, machine-only vault",
	}
	c.AddCommand(&cobra.Command{
		Use:               "backup",
		Short:             "Export the private tier to the local vault ($XDG_DATA_HOME/kref/...)",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Example:           exampleBlock([]string{"kref vault backup   # mirror private to the local vault"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			path, err := s.VaultBackup()
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { fmt.Fprintf(w, "backed up private to %s\n", path) },
				map[string]string{"status": "backed-up", "path": path})
		},
	})
	c.AddCommand(&cobra.Command{
		Use:               "restore",
		Short:             "Restore the private tier from the local vault",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Example:           exampleBlock([]string{"kref vault restore   # bring private back from the local vault"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			res, path, err := s.VaultRestore()
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { fmt.Fprintf(w, "restored %d ref(s) from %s\n", res.Refs, path) },
				map[string]any{"status": "restored", "refs": res.Refs, "path": path})
		},
	})
	return c
}

func newPurgeCmd(dir *string) *cobra.Command {
	var force, gc, push bool
	c := &cobra.Command{
		Use:     "purge <id>",
		Aliases: []string{"destroy"},
		Short:   "Hard-delete an entry: remove its ref (and optionally gc objects)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := s.Resolve(args[0])
			if err != nil {
				return err
			}
			snap, err := s.Get(id)
			if err != nil {
				return err
			}
			if !force {
				confirmed, err := confirmPurge(cmd, snap, gc, push)
				if err != nil {
					return err
				}
				if !confirmed {
					return emit(cmd,
						func(w io.Writer, _ bool) { fmt.Fprintf(w, "aborted %s\n", render.ShortID(id)) },
						map[string]string{"status": "aborted", "id": id.String()})
				}
			} else if gc {
				cmd.PrintErrln("warning: --gc runs repo-wide 'git gc --prune=now' and prunes ALL unreachable objects in this repo")
			}
			if err := s.Purge(id, gc, push); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) { render.Action(w, "purged", snap, color) },
				map[string]any{"status": "purged", "id": id.String(), "gc": gc, "push": push})
		},
	}
	c.Flags().BoolVar(&force, "force", false, "skip the confirmation prompt")
	c.Flags().BoolVar(&gc, "gc", false, "also run repo-wide `git gc --prune=now` to excise objects now (irreversible)")
	c.Flags().BoolVar(&push, "push", false, "also delete the entry on the tier's configured remote")
	c.ValidArgsFunction = entryArgs(dir, 1, sourceAll)
	applyGuide(c, cobra.ExactArgs(1), argGuide{noun: "an entry id", find: "kref list", usage: "kref purge <id>", examples: []string{
		"kref purge a1b2c3d4        # delete the entry's ref",
		"kref purge a1b2c3d4 --gc   # ...and gc objects now",
	}})
	return c
}

// remoteListRun renders every tier's remote configuration; it backs both
// `kref remote list` and the bare `kref remote`.
func remoteListRun(dir *string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		s, err := store.Open(*dir)
		if err != nil {
			return err
		}
		defer s.Close()
		remotes, err := s.Remotes()
		if err != nil {
			return err
		}
		type jsonRemote struct {
			Tier     string `json:"tier"`
			Remote   string `json:"remote"`
			URL      string `json:"url"`
			Syncable bool   `json:"syncable"`
		}
		jr := make([]jsonRemote, len(remotes))
		for i, r := range remotes {
			jr[i] = jsonRemote{Tier: string(r.Tier), Remote: r.Remote, URL: r.URL, Syncable: r.Syncable}
		}
		return emit(cmd,
			func(w io.Writer, color bool) {
				// The tier badge ("● private") is multi-byte and, with color,
				// carries ANSI escapes; align on the rune width of the plain
				// badge, the same trick render's table uses.
				const tierW, nameW = 10, 8
				fmt.Fprintf(w, "%-*s  %-*s  %s\n", tierW, "TIER", nameW, "REMOTE", "URL")
				for _, r := range remotes {
					name, url := r.Remote, r.URL
					switch {
					case !r.Syncable:
						name, url = "—", "(never syncs)"
					case name == "":
						name, url = "—", "(not configured — kref remote set "+string(r.Tier)+" <name> [url])"
					case url == "":
						url = "(no such git remote)"
					}
					plain := render.Tier(string(r.Tier), string(r.Type), false)
					gap := max(tierW-utf8.RuneCountInString(plain), 0)
					namePad := max(nameW-utf8.RuneCountInString(name), 0)
					fmt.Fprintf(w, "%s%s  %s%s  %s\n",
						render.Tier(string(r.Tier), string(r.Type), color), strings.Repeat(" ", gap),
						name, strings.Repeat(" ", namePad), url)
				}
			},
			map[string]any{"remotes": jr})
	}
}

func newRemoteCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:     "remote",
		Aliases: []string{"remotes"},
		Short:   "Manage per-tier sync remotes (bare `kref remote` lists them)",
		Args:    cobra.NoArgs,
		RunE:    remoteListRun(dir),
	}
	c.Example = exampleBlock([]string{"kref remote set shared origin git@github.com:you/team.git"})
	list := &cobra.Command{
		Use:     "list",
		Short:   "List every tier's configured remote",
		Args:    cobra.NoArgs,
		Example: exampleBlock([]string{"kref remote list"}),
		RunE:    remoteListRun(dir),
	}
	c.AddCommand(list)
	get := &cobra.Command{
		Use:               "get <tier>",
		Short:             "Print the configured remote for one tier",
		ValidArgsFunction: nthEnumFn(0, func() []string { return remoteTierNames(dir) }),
		Example:           exampleBlock([]string{"kref remote get shared"}),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			tdef, err := s.DeclaredTier(args[0])
			if err != nil {
				return err
			}
			if tdef.Type == entry.TierPrivate {
				return errors.New("the private tier cannot have a remote")
			}
			t := tdef.Name
			remotes, err := s.Remotes()
			if err != nil {
				return err
			}
			var name, url string
			for _, r := range remotes {
				if r.Tier == t {
					name, url = r.Remote, r.URL
					break
				}
			}
			if name == "" {
				return fmt.Errorf("no remote configured for tier %s (use `kref remote set %s <name> <url>`)", t, t)
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					if url == "" {
						fmt.Fprintf(w, "%s → %s\n", t, name)
						return
					}
					fmt.Fprintf(w, "%s → %s (%s)\n", t, name, url)
				},
				map[string]string{"tier": string(t), "remote": name, "url": url})
		},
	}
	c.AddCommand(get)
	set := &cobra.Command{
		Use:               "set <tier> <name> [url]",
		Short:             "Configure the git remote for a tier (private is not allowed)",
		ValidArgsFunction: nthEnumFn(0, func() []string { return remoteTierNames(dir) }),
		Example:           exampleBlock([]string{"kref remote set shared origin git@github.com:you/team.git"}),
		Args:              cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			tdef, err := s.DeclaredTier(args[0])
			if err != nil {
				return err
			}
			if tdef.Type == entry.TierPrivate {
				return errors.New("the private tier cannot have a remote")
			}
			t := tdef.Name
			url := ""
			if len(args) == 3 {
				url = args[2]
			}
			if err := s.SetRemote(t, args[1], url); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					fmt.Fprintf(w, "remote set: %s → %s\n", t, args[1])
				},
				map[string]string{"status": "remote-set", "tier": string(t), "remote": args[1]})
		},
	}
	c.AddCommand(set)
	return c
}

// tierListRun renders the resolved tier set; it backs both `kref tier list`
// and the bare `kref tier`.
func tierListRun(dir *string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		s, err := store.Open(*dir)
		if err != nil {
			return err
		}
		defer s.Close()
		defs := s.Tiers()
		remotes := map[entry.Tier]string{}
		for _, d := range defs {
			if d.Declared && d.Type != entry.TierPrivate {
				if r, rErr := s.RemoteFor(d.Name); rErr == nil {
					remotes[d.Name] = r
				}
			}
		}
		type jsonTier struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Declared bool   `json:"declared"`
			Remote   string `json:"remote"`
		}
		jt := make([]jsonTier, len(defs))
		for i, d := range defs {
			jt[i] = jsonTier{Name: string(d.Name), Type: string(d.Type), Declared: d.Declared, Remote: remotes[d.Name]}
		}
		return emit(cmd,
			func(w io.Writer, color bool) {
				// Same rune-width alignment trick as remoteListRun: the badge is
				// multi-byte and, with color, carries ANSI escapes.
				const tierW, typeW = 14, 8
				fmt.Fprintf(w, "%-*s  %-*s  %s\n", tierW, "TIER", typeW, "TYPE", "REMOTE")
				for _, d := range defs {
					plain := render.Tier(string(d.Name), string(d.Type), false)
					gap := max(tierW-utf8.RuneCountInString(plain), 0)
					remote := remotes[d.Name]
					switch {
					case d.Type == entry.TierPrivate:
						remote = "— (never syncs)"
					case !d.Declared:
						remote = "(undeclared — kref tier add " + string(d.Name) + " --type personal|shared)"
					case remote == "":
						remote = "—"
					}
					fmt.Fprintf(w, "%s%s  %-*s  %s\n",
						render.Tier(string(d.Name), string(d.Type), color), strings.Repeat(" ", gap),
						typeW, string(d.Type), remote)
				}
			},
			map[string]any{"tiers": jt})
	}
}

func newTierCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "tier",
		Short: "Manage visibility tiers (bare `kref tier` lists them)",
		Args:  cobra.NoArgs,
		RunE:  tierListRun(dir),
	}
	c.Example = exampleBlock([]string{
		"kref tier add research --type personal",
		"kref tier add team-x --type shared --remote teamx --url git@github.com:org/teamx.git",
		"kref tier rm research",
	})
	list := &cobra.Command{
		Use:               "list",
		Short:             "List every tier: type, remote, declared state",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Example:           exampleBlock([]string{"kref tier list"}),
		RunE:              tierListRun(dir),
	}

	var typ, remoteName, remoteURL string
	add := &cobra.Command{
		Use:               "add <name>",
		Short:             "Declare a custom tier (typed personal or shared), optionally wiring its remote",
		ValidArgsFunction: cobra.NoFileCompletions,
		Example: exampleBlock([]string{
			"kref tier add research --type personal",
			"kref tier add team-x --type shared --remote teamx --url git@github.com:org/teamx.git",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			if remoteURL != "" && remoteName == "" {
				return errors.New("--url requires --remote")
			}
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.TierAdd(args[0], entry.Tier(typ), remoteName, remoteURL); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) {
					fmt.Fprintf(w, "tier added: %s (%s)\n", render.Tier(args[0], typ, color), typ)
				},
				map[string]string{"status": "tier-added", "name": args[0], "type": typ, "remote": remoteName})
		},
	}
	add.Flags().StringVar(&typ, "type", "personal", "tier type: personal|shared")
	add.Flags().StringVar(&remoteName, "remote", "", "git remote to sync this tier with")
	add.Flags().StringVar(&remoteURL, "url", "", "create the git remote with this URL if missing (requires --remote)")
	_ = add.RegisterFlagCompletionFunc("type", fixedFlag([]string{"personal", "shared"}))
	applyGuide(add, cobra.ExactArgs(1), argGuide{noun: "a tier name", find: "kref tier list", usage: "kref tier add <name> --type personal|shared", examples: []string{
		"kref tier add research --type personal",
	}})

	var force bool
	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Undeclare a custom tier (refuses while it still holds entries; --force orphans them)",
		ValidArgsFunction: func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return fixedValues(customTierNames(dir), toComplete), cobra.ShellCompDirectiveNoFileComp
		},
		Example: exampleBlock([]string{"kref tier rm research"}),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.TierRemove(args[0], force); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { fmt.Fprintf(w, "tier removed: %s\n", args[0]) },
				map[string]string{"status": "tier-removed", "name": args[0]})
		},
	}
	rm.Flags().BoolVar(&force, "force", false, "remove the declaration even if the tier holds entries (refs stay; the namespace becomes undeclared)")
	applyGuide(rm, cobra.ExactArgs(1), argGuide{noun: "a tier name", find: "kref tier list", usage: "kref tier rm <name>", examples: []string{
		"kref tier rm research",
	}})

	c.AddCommand(list, add, rm)
	return c
}

func runSync(cmd *cobra.Command, dir, tierFlag string, op func(*store.Store, entry.Tier) error, verb string) error {
	s, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	var tiers []entry.Tier
	if tierFlag != "" {
		tdef, err := s.DeclaredTier(tierFlag)
		if err != nil {
			return err
		}
		tiers = []entry.Tier{tdef.Name}
	} else {
		tiers, err = s.SyncableTiers()
		if err != nil {
			return err
		}
	}
	done := []string{}
	for _, t := range tiers {
		if err := op(s, t); err != nil {
			return err
		}
		done = append(done, string(t))
	}
	status := verb
	if len(done) == 0 {
		status = "nothing-to-" + strings.TrimSuffix(verb, "ed") // pushed→push, pulled→pull
	}
	return emit(cmd,
		func(w io.Writer, _ bool) {
			if len(done) == 0 {
				fmt.Fprintf(w, "%s: nothing to do\n", verb)
				return
			}
			fmt.Fprintf(w, "%s: %s\n", verb, strings.Join(done, ", "))
		},
		map[string]any{"status": status, "tiers": done})
}

func newSyncCmd(dir *string) *cobra.Command {
	c := &cobra.Command{Use: "sync", Short: "Sync tiers with their configured remotes"}
	c.Example = exampleBlock([]string{"kref sync push   # push all syncable tiers", "kref sync pull --tier shared"})
	var pushTier, pullTier string
	var force bool
	push := &cobra.Command{
		Use:               "push",
		Short:             "Push tier(s) to their configured remote",
		ValidArgsFunction: cobra.NoFileCompletions,
		Example: exampleBlock([]string{
			"kref sync push                # every tier with a remote (private is skipped)",
			"kref sync push --tier shared",
			"kref sync push --force        # push even without a scanner (UNSCANNED)",
		}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSync(cmd, *dir, pushTier, func(s *store.Store, t entry.Tier) error {
				if !force {
					return s.Push(t)
				}
				unscanned, err := s.PushForce(t)
				if err == nil && unscanned {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: betterleaks not found — tier %s pushed UNSCANNED (--force); a secret in it is now disclosed until rotated.\n"+
							"Install the scanner: `go install github.com/betterleaks/betterleaks@latest` (or set KREF_BETTERLEAKS).\n", t)
				}
				return err
			}, "pushed")
		},
	}
	push.Flags().StringVar(&pushTier, "tier", "", "tier to push (default: all syncable)")
	push.Flags().BoolVar(&force, "force", false, "push even when the secret scanner is unavailable (content leaves UNSCANNED; detected secrets still block)")
	pull := &cobra.Command{
		Use:               "pull",
		Short:             "Pull tier(s) from their configured remote",
		ValidArgsFunction: cobra.NoFileCompletions,
		Example:           exampleBlock([]string{"kref sync pull                # all syncable tiers", "kref sync pull --tier shared"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSync(cmd, *dir, pullTier, func(s *store.Store, t entry.Tier) error { return s.Pull(t) }, "pulled")
		},
	}
	pull.Flags().StringVar(&pullTier, "tier", "", "tier to pull (default: all syncable)")
	c.AddCommand(push, pull)
	return c
}

func newHooksCmd(dir *string) *cobra.Command {
	c := &cobra.Command{Use: "hooks", Short: "Manage git lifecycle hooks (lefthook)"}
	c.Example = exampleBlock([]string{"kref hooks install   # wire kref into git lifecycle events", "kref hooks print"})
	var force bool
	var ingestPaths []string
	install := &cobra.Command{
		Use:               "install",
		Short:             "Write a .lefthook.yml wiring kref sync/ingest to git events",
		ValidArgsFunction: cobra.NoFileCompletions,
		Example: exampleBlock([]string{
			"kref hooks install",
			"kref hooks install --ingest-path docs/   # also scan markdown there on commit",
		}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			generated := hooks.Render(exe, ingestPaths)
			path := filepath.Join(*dir, hooks.InstallPath)
			existing, statErr := os.ReadFile(path)
			if statErr != nil && !os.IsNotExist(statErr) {
				return statErr
			}
			if statErr == nil && !force {
				return fmt.Errorf("%s already exists (use --force to merge kref's hooks into it)", path)
			}
			content, err := hooks.Merge(existing, generated)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				return err
			}
			return writeJSON(cmd, map[string]string{
				"status": "written",
				"path":   path,
				"next":   "run `lefthook install` to activate the hooks",
			})
		},
	}
	install.Flags().BoolVar(&force, "force", false, "merge kref's hooks into an existing .lefthook.yml")
	install.Flags().StringArrayVar(&ingestPaths, "ingest-path", nil, "directory the post-commit hook scans for markdown (repeatable; default: docs/superpowers/plans specs .specify openspec)")
	printCmd := &cobra.Command{
		Use:               "print",
		Short:             "Print the lefthook configuration to stdout",
		ValidArgsFunction: cobra.NoFileCompletions,
		Example:           exampleBlock([]string{"kref hooks print   # render the lefthook config to stdout"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), hooks.Render(exe, ingestPaths))
			return nil
		},
	}
	printCmd.Flags().StringArrayVar(&ingestPaths, "ingest-path", nil, "directory the post-commit hook scans for markdown (repeatable; default: docs/superpowers/plans specs .specify openspec)")
	c.AddCommand(install, printCmd)
	return c
}

// stdinBodyAllowed reports whether update may consume stdin as the body
// source. An interactive terminal is never a body source — reading it would
// block until an EOF that never comes (`kref update <id> --kind todo` used to
// hang exactly this way). Piped/redirected stdin is a body source, except for
// a metadata-only update (reattribute/content-type without title/kind), which
// must not eat the stream.
func stdinBodyAllowed(stdinTTY, reattributing, ctypeSet, titleSet, kindSet bool) bool {
	if stdinTTY {
		return false
	}
	if (reattributing || ctypeSet) && !titleSet && !kindSet {
		return false
	}
	return true
}

func newUpdateCmd(dir *string) *cobra.Command {
	var body, file, title, kind string
	var contentType string
	var resetAuthor, all, yes bool
	var author string
	var noFmt, noLint bool
	var ifVersion int
	var force bool
	c := &cobra.Command{
		Use:     "update <id|path>... | --all",
		Aliases: []string{"set"},
		Short:   "Update an entry's body/title/kind (body from --body, --file, or stdin)",
		RunE: func(cmd *cobra.Command, args []string) error {
			bodyChanged := cmd.Flags().Changed("body")
			fileSet := cmd.Flags().Changed("file")
			titleSet := cmd.Flags().Changed("title")
			kindSet := cmd.Flags().Changed("kind")
			authorSet := cmd.Flags().Changed("author")
			ctypeSet := cmd.Flags().Changed("content-type")
			ifVersionSet := cmd.Flags().Changed("if-version")
			if ctypeSet {
				cc, cerr := content.Canonical(contentType)
				if cerr != nil {
					return cerr
				}
				contentType = cc
			}
			if resetAuthor && authorSet {
				return errors.New("use one of --reset-author or --author, not both")
			}
			reattributing := resetAuthor || authorSet
			if bodyChanged && fileSet {
				return errors.New("use one of --body or --file, not both")
			}
			if titleSet && strings.TrimSpace(title) == "" {
				return errors.New("--title cannot be empty")
			}
			if kindSet && strings.TrimSpace(kind) == "" {
				return errors.New("--kind cannot be empty")
			}

			// Bulk update: multiple ids or --all. Only --kind/--reset-author/
			// --author apply in bulk; per-entry content flags are refused. --all
			// confirms first unless -y.
			if all && len(args) > 0 {
				return errors.New("give entry ids or --all, not both")
			}
			if !all && len(args) == 0 {
				return errors.New("give one or more entry ids, or --all")
			}
			if all || len(args) > 1 {
				if bodyChanged || fileSet || titleSet {
					return errors.New("--body/--file/--title set per-entry content and cannot be applied in bulk; update a single entry for those")
				}
				if !kindSet && !reattributing && !ctypeSet {
					return errors.New("bulk update needs --kind, --content-type, --reset-author, or --author")
				}
				s, err := store.Open(*dir)
				if err != nil {
					return err
				}
				defer s.Close()
				var ids []entity.Id
				if all {
					snaps, lErr := s.List(store.ListFilter{})
					if lErr != nil {
						return lErr
					}
					for _, sn := range snaps {
						ids = append(ids, sn.ID)
					}
				} else {
					for _, a := range args {
						id, rErr := resolveArg(s, a)
						if rErr != nil {
							return rErr
						}
						ids = append(ids, id)
					}
				}
				if len(ids) == 0 {
					return errors.New("no entries to update")
				}
				noun := "entries"
				if len(ids) == 1 {
					noun = "entry"
				}
				if all && !yes {
					out := cmd.ErrOrStderr()
					fmt.Fprintf(out, "Update %d %s? Type y to proceed: ", len(ids), noun)
					line, rErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
					if rErr != nil && rErr != io.EOF {
						return rErr
					}
					switch strings.ToLower(strings.TrimSpace(line)) {
					case "y", "yes":
					default:
						fmt.Fprintln(out, "aborted; nothing updated.")
						return nil
					}
				}
				var rname, remail string
				if reattributing {
					rname, remail = s.Author()
					if authorSet {
						if rname, remail, err = parseAuthor(author); err != nil {
							return err
						}
					}
				}
				// Changing entries to kind:todo requires each body to satisfy the
				// todo grammar; pre-validate all before converting any, so a bulk
				// run never leaves a partial (some converted, some refused) result.
				if kindSet && kind == todoguard.TodoKind {
					var bad []string
					for _, id := range ids {
						rej, gerr := guardKindToTodo(s, id)
						if gerr != nil {
							return gerr
						}
						if rej != nil {
							bad = append(bad, render.ShortID(id))
						}
					}
					if len(bad) > 0 {
						return fmt.Errorf("cannot change to kind:todo — %d entr(y/ies) do not satisfy the todo grammar: %s; fix their bodies first",
							len(bad), strings.Join(bad, ", "))
					}
				}
				for _, id := range ids {
					if kindSet {
						if err := s.SetKind(id, kind); err != nil {
							return err
						}
					}
					if ctypeSet {
						if err := s.SetContentType(id, contentType); err != nil {
							return err
						}
					}
					if reattributing {
						if err := s.Reattribute(id, rname, remail); err != nil {
							return err
						}
					}
				}
				return emit(cmd,
					func(w io.Writer, _ bool) { fmt.Fprintf(w, "updated %d %s\n", len(ids), noun) },
					map[string]int{"updated": len(ids)})
			}

			switch {
			case fileSet:
				raw, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				_, stripped := bridge.SplitMarker(raw)
				body = string(stripped)
				// An empty/whitespace-only file yields no body mutation below
				// (haveBody stays false); it is treated as "no body change",
				// NOT as a request to clear the body.
			case bodyChanged:
				// body already set from the flag
			default:
				if !stdinBodyAllowed(term.IsTerminal(int(os.Stdin.Fd())), reattributing, ctypeSet, titleSet, kindSet) {
					break
				}
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				body = string(raw)
			}
			haveBody := strings.TrimSpace(body) != ""
			if !haveBody && !titleSet && !kindSet && !reattributing && !ctypeSet {
				return errors.New("nothing to update (give --body/--file, --title, --kind, --content-type, --reset-author, --author, or pipe a body on stdin)")
			}

			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}

			// A changed body (from any source) is scanned: a secret on a syncable
			// entry is diverted into the quarantine review queue and the live
			// entry is left untouched, not refused.
			if haveBody && body != "" {
				snap, gErr := s.Get(id)
				if gErr != nil {
					return gErr
				}
				fs, held, unscanned, ferr := entryParkFindings(snap, body)
				if ferr != nil {
					return ferr
				}
				if held {
					actor, actorKind := resolveActor(cmd, s)
					parked, perr := s.QuarantineUpdate(id, body, snap.Version, fs, actorKind)
					if perr != nil {
						return perr
					}
					if force {
						// --force = park+approve in one step (human/CLI only): the
						// held body is replayed onto the live entry via approve.
						if aerr := s.ApproveQuarantine(parked.ItemID, "force-approved at write", actor, actorKind); aerr != nil {
							return aerr
						}
						fmt.Fprintf(cmd.OutOrStdout(), "updated %s (force-approved)\n", shortStr(id.String(), 12))
						return nil
					}
					printQuarantined(cmd, parked)
					return nil
				}
				if unscanned && s.EffectiveConfig().WarnUnscannedOn() {
					fmt.Fprintln(cmd.ErrOrStderr(), unscannedWarn)
				}
			}

			// Apply body/title together (Store.Update no-ops unchanged fields).
			if haveBody || titleSet {
				b := body
				if !haveBody {
					// Title-only: keep the stored body verbatim; do not guard it,
					// so a legacy-malformed body never blocks a title change.
					snap, err := s.Get(id)
					if err != nil {
						return err
					}
					b = snap.Body
				} else {
					// The body is changing: run it through the todo write-boundary
					// guard (a no-op for non-todo kinds). The effective kind is the
					// one --kind sets, else the entry's current kind.
					effKind := kind
					if !kindSet {
						ksnap, kerr := s.Get(id)
						if kerr != nil {
							return kerr
						}
						effKind = ksnap.Kind
					}
					// CAS first (spec §8 step 3): refuse a stale todo write before
					// formatting, preserving the author's raw proposed body.
					if cerr := guardTodoCAS(cmd, s, id, effKind, ifVersion, ifVersionSet, b); cerr != nil {
						return cerr
					}
					var gerr error
					if b, gerr = guardTodoWrite(cmd, id, effKind, b, noFmt, noLint); gerr != nil {
						return gerr
					}
				}
				t := ""
				if titleSet {
					t = title
				}
				if err := s.Update(id, b, t); err != nil {
					return err
				}
			}
			if kindSet {
				if kind == todoguard.TodoKind {
					rej, gerr := guardKindToTodo(s, id)
					if gerr != nil {
						return gerr
					}
					if rej != nil {
						return fmt.Errorf("cannot change %s to kind:todo — its body does not satisfy the todo grammar:\n%s\nfix the body first (e.g. `kref edit %s`), then retry",
							render.ShortID(id), rej.Error(), render.ShortID(id))
					}
				}
				if err := s.SetKind(id, kind); err != nil {
					return err
				}
			}
			if ctypeSet {
				if err := s.SetContentType(id, contentType); err != nil {
					return err
				}
			}
			if reattributing {
				name, email := s.Author()
				if authorSet {
					if name, email, err = parseAuthor(author); err != nil {
						return err
					}
				}
				if err := s.Reattribute(id, name, email); err != nil {
					return err
				}
			}
			snap, err := s.Get(id)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, color bool) { render.Action(w, "updated", snap, color) },
				map[string]string{"status": "updated", "id": id.String()})
		},
	}
	c.Flags().StringVar(&body, "body", "", "new body (override)")
	c.Flags().StringVar(&file, "file", "", "read the new body from a file (the file's content becomes the new body; secret-scanned). An empty file is treated as no body change, not a clear")
	c.Flags().StringVar(&title, "title", "", "also set the title")
	c.Flags().StringVar(&kind, "kind", "", "also set the kind")
	c.Flags().StringVar(&contentType, "content-type", "", "set the entry content type, e.g. text/x-go")
	c.Flags().BoolVar(&resetAuthor, "reset-author", false, "reattribute the entry to your current kref identity")
	c.Flags().StringVar(&author, "author", "", "reattribute the entry to an explicit author, \"Name <email>\"")
	c.Flags().BoolVar(&all, "all", false, "bulk-update every entry (only --kind/--reset-author/--author; confirms unless -y)")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the --all confirmation prompt")
	c.Flags().BoolVar(&noFmt, "no-fmt", false, "skip the todo formatter on this write (todo entries only)")
	c.Flags().BoolVar(&noLint, "no-lint", false, "skip the todo grammar linter on this write — writes even if malformed (todo entries only)")
	c.Flags().IntVar(&ifVersion, "if-version", 0, "guard a todo write: write only if the entry is still at version N (kref todo header / kref log show it); a mismatch is refused as a stale write")
	c.Flags().BoolVar(&force, "force", false, "for a flagged body: park the quarantine item and approve it in one step (human/CLI only; leaves a q-status:approved audit item)")
	_ = c.RegisterFlagCompletionFunc("kind", completeStoreField(dir, func(e store.Excerpt) []string { return []string{e.Kind} }))
	c.ValidArgsFunction = entryArgs(dir, 0, sourceAll) // variadic: ids at every position
	applyGuide(c, cobra.ArbitraryArgs, argGuide{noun: "one or more entry ids or paths (or --all)", find: "kref list", usage: "kref update <id|path>... | --all", examples: []string{
		`kref update a1b2c3d4 --title "New title"`,
		"kref update a1b2c3d4 b5c6d7e8 --kind plan        # bulk: set kind on several",
		"kref list --plain --columns=id | xargs kref update --reset-author   # bulk via a pipe",
	}})
	return c
}

func newResolveCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "resolve <id|path>",
		Short: "Acknowledge an entry's concurrent merge, clearing its ◆ merged flag",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}
			n, err := s.AcknowledgeMerge(id)
			if err != nil {
				return err
			}
			status := "resolved"
			if n == 0 {
				status = "nothing-to-resolve"
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					if n == 0 {
						fmt.Fprintf(w, "nothing to resolve %s\n", render.ShortID(id))
						return
					}
					fmt.Fprintf(w, "resolved %s (%d merge(s) acknowledged)\n", render.ShortID(id), n)
				},
				map[string]any{"status": status, "id": id.String(), "acknowledged": n})
		},
	}
	c.ValidArgsFunction = entryArgs(dir, 1, sourceAll)
	applyGuide(c, cobra.ExactArgs(1), argGuide{noun: "an entry id", find: "kref list", usage: "kref resolve <id>", examples: []string{
		"kref resolve a1b2c3d4   # clear the merged flag after reviewing a concurrent merge",
	}})
	return c
}

func newLinkCmd(dir *string) *cobra.Command {
	c := &cobra.Command{Use: "link", Short: "Create or remove a typed link between two entries"}
	c.Example = exampleBlock([]string{"kref link add a1b2c3d4 e5f6a7b8 --type blocks", "kref link rm a1b2c3d4 e5f6a7b8"})
	var linkType string
	add := &cobra.Command{
		Use:   "add <id|path> <target>",
		Short: "Add a typed link from one entry to another (free-form --type, default relates)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			from, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}
			to, err := resolveArg(s, args[1])
			if err != nil {
				return err
			}
			leak, err := s.LinkWouldLeak(from, to)
			if err != nil {
				return err
			}
			if leak {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: linking a more-public entry to a more-private one; the private id rides along on push\n")
			}
			if err := s.AddLink(from, to.String(), linkType); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					fmt.Fprintf(w, "linked %s --%s--> %s\n", render.ShortID(from), linkType, render.ShortID(to))
				},
				map[string]any{
					"status": "linked", "from": from.String(), "to": to.String(),
					"type": linkType, "cross_tier_warning": leak,
				})
		},
	}
	add.Flags().StringVar(&linkType, "type", "relates", "link type (free-form)")
	add.ValidArgsFunction = entryArgs(dir, 2, sourceAll)
	applyGuide(add, cobra.ExactArgs(2), argGuide{noun: "a source and a target entry", find: "kref list", usage: "kref link add <id|path> <target>", examples: []string{
		"kref link add a1b2c3d4 e5f6a7b8                # default 'relates' link",
		"kref link add a1b2c3d4 e5f6a7b8 --type blocks  # a typed link",
	}})
	rm := &cobra.Command{
		Use:   "rm <id|path> <target>",
		Short: "Remove the link(s) from one entry to another",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			from, err := resolveArg(s, args[0])
			if err != nil {
				return err
			}
			to, err := resolveArg(s, args[1])
			if err != nil {
				return err
			}
			if err := s.RemoveLink(from, to.String()); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					fmt.Fprintf(w, "unlinked %s and %s\n", render.ShortID(from), render.ShortID(to))
				},
				map[string]any{"status": "unlinked", "from": from.String(), "to": to.String()})
		},
	}
	rm.ValidArgsFunction = entryArgs(dir, 2, sourceAll)
	applyGuide(rm, cobra.ExactArgs(2), argGuide{noun: "a source and a target entry", find: "kref show a1b2c3d4", usage: "kref link rm <id|path> <target>", examples: []string{
		"kref link rm a1b2c3d4 e5f6a7b8   # remove the link(s) between them",
	}})
	c.AddCommand(add, rm)
	return c
}

// resolveArg turns a CLI argument into an entry id. A path-like argument (an
// existing file, or one containing a separator or a .md suffix) is resolved via
// its kref-id trailer; everything else is an id prefix.
func resolveArg(s *store.Store, arg string) (entity.Id, error) {
	looksLikePath := strings.ContainsRune(arg, '/') || strings.HasSuffix(arg, ".md")
	if _, err := os.Stat(arg); err == nil {
		looksLikePath = true
	}
	if looksLikePath {
		id, err := bridge.IDFromFile(arg)
		if err != nil {
			return "", err
		}
		return s.Resolve(id)
	}
	return s.Resolve(arg)
}

// resolveReconcileArg resolves a reconcile target. It first tries the normal
// id/trailer resolution; if that fails (e.g. a markdown formatter stripped the
// file's kref-id HTML comment) it falls back to the stored tracked-path mapping,
// so a tracked file stays reconcilable by path after losing its trailer — the
// sweep form already recovers it that way, and the path form should not be a
// dead end. The fallback only matches a *tracked* entry, so an untracked file
// without a trailer still surfaces the original resolution error.
func resolveReconcileArg(s *store.Store, arg string) (entity.Id, error) {
	id, err := resolveArg(s, arg)
	if err == nil {
		return id, nil
	}
	rel := s.RepoRelative(arg)
	all, lErr := s.List(store.ListFilter{})
	if lErr != nil {
		return "", err // surface the original (more actionable) resolution error
	}
	for _, snap := range all {
		if snap.Tracked && snap.TrackedPath == rel {
			return snap.ID, nil
		}
	}
	return "", err
}

// resolveActor returns (actor, actorKind). --actor flag or KREF_ACTOR env marks an
// agent; otherwise the git identity name as a human.
func resolveActor(cmd *cobra.Command, s *store.Store) (string, string) {
	v, _ := cmd.Flags().GetString("actor")
	if v == "" {
		v = os.Getenv("KREF_ACTOR")
	}
	if v != "" {
		return v, "agent"
	}
	name, _ := s.Author()
	return name, "human"
}

func resolveEditor() []string {
	for _, env := range []string{"KREF_EDITOR", "VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return strings.Fields(v)
		}
	}
	return []string{"vi"}
}

func newEditCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit an entry's body in $EDITOR (title re-derives from the H1)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := s.Resolve(args[0])
			if err != nil {
				return err
			}
			return editEntry(cmd, s, id)
		},
	}
	c.ValidArgsFunction = entryArgs(dir, 1, sourceAll)
	applyGuide(c, cobra.ExactArgs(1), argGuide{noun: "an entry id", find: "kref list", usage: "kref edit <id>", examples: []string{
		"kref edit a1b2c3d4   # revise the body in $EDITOR (title re-derives from the H1)",
	}})
	return c
}

// editEntry opens an entry's body in $EDITOR (the reopen-until-clean guard loop
// with an implicit CAS) and writes it back. Shared by `kref edit` and the
// interactive list cockpit's edit action.
func editEntry(cmd *cobra.Command, s *store.Store, id entity.Id) error {
	snap, err := s.Get(id)
	if err != nil {
		return err
	}

	f, err := os.CreateTemp(xdg.CacheTempDir(), "kref-edit-*.md")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	_ = f.Close()

	// Reopen-until-clean loop (spec §8, no-work-loss): a todo body that
	// fails the guard is re-presented in the editor with the violations
	// prepended as an HTML-comment banner; the author fixes and re-saves.
	// A non-todo entry never rejects, so this runs exactly once. If a
	// reopened editor leaves the same rejected body unchanged, abort
	// rather than loop forever — the content is left untouched on disk.
	seed := snap.Body
	baseVersion := snap.Version // the head we opened at, for the implicit CAS below
	var lastRejected string
	haveRejected := false
	for {
		if err := os.WriteFile(tmp, []byte(seed), 0o600); err != nil {
			return err
		}
		ed := resolveEditor()
		ec := exec.Command(ed[0], append(ed[1:], tmp)...)
		ec.Stdin, ec.Stdout, ec.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := ec.Run(); err != nil {
			return fmt.Errorf("editor exited with error: %w", err)
		}
		raw, err := os.ReadFile(tmp)
		if err != nil {
			return err
		}
		body := stripLintBanner(string(raw))
		if strings.TrimSpace(body) == "" {
			return errors.New("aborted: edited body is empty")
		}
		guarded, gerr := todoguard.Guard(snap.Kind, body, todoguard.Options{})
		var rej *todoguard.RejectedError
		if errors.As(gerr, &rej) {
			if haveRejected && rej.Body == lastRejected {
				return fmt.Errorf("edit discarded: todo still has %d lint violation(s) and was not changed:\n%s",
					len(rej.Violations), rej.Error())
			}
			lastRejected = rej.Body
			haveRejected = true
			seed = lintBanner(rej.Violations) + rej.Body
			fmt.Fprintf(cmd.ErrOrStderr(),
				"todo has %d lint violation(s); reopening the editor to fix them\n", len(rej.Violations))
			continue
		}
		if gerr != nil {
			return gerr
		}
		// Implicit CAS (spec §8 step 3): the editor may have been open a
		// while; if a concurrent writer moved a todo off the version we
		// opened, refuse the save and preserve the author's edit rather than
		// clobber the intervening change (a no-op for non-todo kinds).
		head, herr := s.Get(id)
		if herr != nil {
			return herr
		}
		if cerr := todoguard.CheckVersion(snap.Kind, baseVersion, head.Version); cerr != nil {
			path, werr := todoguard.WriteRejected(id.String(), body)
			if werr != nil {
				return fmt.Errorf("%w (could not save recovery file: %w)", cerr, werr)
			}
			return fmt.Errorf("%w\nyour edit was saved to %s — re-run `kref edit %s` (it reloads the current version) and re-apply", cerr, path, render.ShortID(id))
		}
		// Re-derive title from an H1 only; "" leaves the title unchanged.
		if err := s.Update(id, guarded, entry.FirstHeading(guarded)); err != nil {
			return err
		}
		break
	}
	snap, err = s.Get(id)
	if err != nil {
		return err
	}
	return emit(cmd,
		func(w io.Writer, color bool) { render.Action(w, "updated", snap, color) },
		map[string]string{"status": "updated", "id": id.String()})
}

func confirmPurge(cmd *cobra.Command, snap *entry.Snapshot, gc, push bool) (bool, error) {
	out := cmd.ErrOrStderr()
	fmt.Fprintln(out, "About to PURGE (hard-delete, irreversible):")
	fmt.Fprintf(out, "  id:     %s\n", snap.ID)
	fmt.Fprintf(out, "  tier:   %s\n", snap.Tier)
	fmt.Fprintf(out, "  kind:   %s\n", snap.Kind)
	fmt.Fprintf(out, "  status: %s\n", snap.Status)
	fmt.Fprintf(out, "  title:  %s\n", snap.Title)
	fmt.Fprint(out, "\nThis removes the entity's git ref")
	if gc {
		fmt.Fprint(out, " and runs `git gc --prune=now` (repo-wide) to excise its objects")
	}
	fmt.Fprintln(out, ".")
	if push {
		fmt.Fprintln(out, "It will also be DELETED on the tier's configured remote.")
	}
	fmt.Fprintln(out, "It cannot be undone. If this entry held a secret that was already pushed")
	fmt.Fprintln(out, "to a remote, purging does NOT un-leak it — rotate the secret.")
	fmt.Fprint(out, "\nType 'yes' to proceed: ")

	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	return strings.TrimSpace(line) == "yes", nil
}

func newLogCmd(dir *string) *cobra.Command {
	var sincePull bool
	c := &cobra.Command{
		Use:               "log [<id|path>]",
		ValidArgsFunction: entryArgs(dir, 1, sourceAll),
		Aliases:           []string{"audit"},
		Short:             "Show an entry's operation history",
		Args:              cobra.MaximumNArgs(1),
		Example: exampleBlock([]string{
			"kref log a1b2c3d4              # operation history",
			"kref log                       # the most-recently-modified entry",
			"kref log a1b2c3d4 --since-pull # only ops you added after the last pull",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveTargetOrRecent(cmd, s, args)
			if err != nil {
				return err
			}
			if sincePull {
				ops, baseline, err := s.SincePull(id)
				if err != nil {
					return err
				}
				return emit(cmd,
					func(w io.Writer, _ bool) {
						if !baseline {
							fmt.Fprintln(w, "(no pull baseline for this entry — showing full log)")
						}
						render.Log(w, ops)
					},
					ops)
			}
			log, err := s.Log(id)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { render.Log(w, log) },
				log)
		},
	}
	c.Flags().BoolVar(&sincePull, "since-pull", false, "show only ops you added after the last pull")
	return c
}

// parseDiffVersions maps the optional trailing version args onto a from/to
// pair (1-based; from==0 means "from the empty body", the v1 case).
// One number n selects v(n-1)→vn; two numbers select vm→vn.
func parseDiffVersions(args []string, count int) (from, to int, err error) {
	nums := make([]int, len(args))
	for i, a := range args {
		n, convErr := strconv.Atoi(a)
		if convErr != nil || n < 1 {
			return 0, 0, fmt.Errorf("version %q is not a positive number (an entry id must come first: kref diff <id> [<from>] <to>)", a)
		}
		if n > count {
			return 0, 0, fmt.Errorf("version v%d does not exist (the entry has %d version(s))", n, count)
		}
		nums[i] = n
	}
	switch len(nums) {
	case 1:
		return nums[0] - 1, nums[0], nil
	default:
		if nums[0] >= nums[1] {
			return 0, 0, fmt.Errorf("version range must ascend: v%d → v%d", nums[0], nums[1])
		}
		return nums[0], nums[1], nil
	}
}

func newDiffCmd(dir *string) *cobra.Command {
	var noPager, full bool
	c := &cobra.Command{
		Use:               "diff [<id|path>] [[<from>] <to>]",
		ValidArgsFunction: entryArgs(dir, 1, sourceAll),
		Short:             "Show what changed between body versions (inline diff; --full for whole bodies)",
		Args:              cobra.MaximumNArgs(3),
		Example: exampleBlock([]string{
			"kref diff a1b2c3d4       # every version as an inline diff",
			"kref diff a1b2c3d4 3     # what v3 changed (v2 → v3)",
			"kref diff a1b2c3d4 1 4   # v1 → v4 in one diff",
			"kref diff a1b2c3d4 --full # whole body of every version (recover one)",
			"kref diff                # the most-recently-modified entry",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveTargetOrRecent(cmd, s, args[:min(len(args), 1)])
			if err != nil {
				return err
			}
			versions, err := s.BodyVersions(id)
			if err != nil {
				return err
			}
			if jsonMode(cmd) {
				// The JSON contract stays the full version set regardless of
				// selection args — scripts diff however they like.
				return writeJSON(cmd, versions)
			}
			if len(versions) == 0 {
				return errors.New("entry has no body versions")
			}

			var buf bytes.Buffer
			switch {
			case full:
				render.BodyVersions(&buf, versions)
			case len(args) > 1:
				from, to, vErr := parseDiffVersions(args[1:], len(versions))
				if vErr != nil {
					return vErr
				}
				render.VersionDiff(&buf, versions, from, to, useColor(cmd))
			default:
				render.DiffChain(&buf, versions, useColor(cmd))
			}
			if usePager(cmd) && !noPager {
				// Numbered gutter so the pager's <n>g jump has visible targets to
				// aim at when navigating across the concatenated output.
				lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
				return Page(pagerContent{
					title:   render.ShortID(id) + "  body versions",
					body:    lines,
					number:  true,
					gutterW: numDigits(len(lines)) + 3,
				})
			}
			fmt.Fprint(cmd.OutOrStdout(), buf.String())
			return nil
		},
	}
	c.Flags().BoolVar(&noPager, "no-pager", false, "do not page output even on a terminal")
	c.Flags().BoolVar(&full, "full", false, "print every version's whole body instead of diffs")
	return c
}

func newTreeCmd(dir *string) *cobra.Command {
	return &cobra.Command{
		Use:               "tree [<id|path>]",
		ValidArgsFunction: entryArgs(dir, 1, sourceAll),
		Short:             "Show the parent-child tree rooted at an entry",
		Args:              cobra.MaximumNArgs(1),
		Example: exampleBlock([]string{
			"kref tree a1b2c3d4   # parent-child tree rooted here",
			"kref tree            # the most-recently-modified entry",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := resolveTargetOrRecent(cmd, s, args)
			if err != nil {
				return err
			}
			root, err := s.Tree(id)
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { render.Tree(w, root) },
				root)
		},
	}
}

// confirmRetier asks for an interactive "yes" before promoting to a
// shared-typed tier.
func confirmRetier(cmd *cobra.Command, snap *entry.Snapshot, target entry.Tier) (bool, error) {
	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "Promote %s (%q) to shared-typed tier %q? It becomes visible to everyone that tier syncs with.\n", render.ShortID(snap.ID), snap.Title, target)
	fmt.Fprint(out, "Type 'yes' to proceed: ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	return strings.TrimSpace(line) == "yes", nil
}

func runRetier(cmd *cobra.Command, dir, idArg, tierArg string, yes bool) error {
	s, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	targetDef, err := s.DeclaredTier(tierArg)
	if err != nil {
		return err
	}
	target := targetDef.Name
	id, err := resolveArg(s, idArg)
	if err != nil {
		return err
	}
	snap, err := s.Get(id)
	if err != nil {
		return err
	}
	from := snap.Tier
	if from == string(target) {
		return emit(cmd,
			func(w io.Writer, _ bool) { fmt.Fprintf(w, "%s already in %s\n", render.ShortID(id), target) },
			map[string]string{"status": "noop", "id": id.String(), "tier": string(target)})
	}
	out := cmd.ErrOrStderr()
	if targetDef.Type == entry.TierShared {
		dangling, err := s.CrossTierLinks(id, target)
		if err != nil {
			return err
		}
		for _, l := range dangling {
			fmt.Fprintf(out, "warning: links to %s (%q), which stays below shared — teammates won't see it\n", render.ShortID(l.ID), l.Title)
		}
		if !yes {
			confirmed, err := confirmRetier(cmd, snap, target)
			if err != nil {
				return err
			}
			if !confirmed {
				return emit(cmd,
					func(w io.Writer, _ bool) { fmt.Fprintf(w, "aborted %s\n", render.ShortID(id)) },
					map[string]string{"status": "aborted", "id": id.String()})
			}
		}
	}
	if s.TierType(entry.Tier(from)) == entry.TierShared && targetDef.Type != entry.TierShared {
		pushed, err := s.WasPushed(id)
		if err != nil {
			return err
		}
		if pushed {
			fmt.Fprintf(out, "warning: %s was already pushed; demoting only stops FUTURE local sync — rotate if sensitive, it cannot retract what already left\n", render.ShortID(id))
		}
	}
	actor, actorKind := resolveActor(cmd, s)
	if err := s.Retier(id, target, actor, actorKind); err != nil {
		return err
	}
	return emit(cmd,
		func(w io.Writer, _ bool) {
			fmt.Fprintf(w, "retiered %s: %s → %s\n", render.ShortID(id), from, target)
		},
		map[string]string{"status": "retiered", "id": id.String(), "from": from, "to": string(target)})
}

func newRetierCmd(dir *string) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:     "retier <id|path> <tier>",
		Aliases: []string{"mv"},
		Short:   "Move an entry to a visibility tier (kref tier list shows them)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRetier(cmd, *dir, args[0], args[1], yes)
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "skip the promote-to-shared confirmation prompt")
	c.ValidArgsFunction = entryThenEnumFn(dir, func() []string { return declaredTierNames(dir) })
	applyGuide(c, cobra.ExactArgs(2), argGuide{noun: "an entry id and a tier", find: "kref list", usage: "kref retier <id|path> <tier>", examples: []string{
		"kref retier a1b2c3d4 shared   # any declared tier — see kref tier list",
	}})
	return c
}

func newTidyCmd(dir *string) *cobra.Command {
	return &cobra.Command{
		Use:               "tidy",
		Short:             "Review consolidation candidates: duplicates, diverged, superseded",
		ValidArgsFunction: cobra.NoFileCompletions,
		Example:           exampleBlock([]string{"kref tidy   # review duplicates, diverged, and superseded entries"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			report, err := s.Tidy()
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { render.Tidy(w, report) },
				report)
		},
	}
}

func newMCPCmd(dir *string) *cobra.Command {
	var allow []string
	var clientRoots bool
	c := &cobra.Command{
		Use:               "mcp",
		Short:             "Run an MCP server exposing kref tools over stdio",
		ValidArgsFunction: cobra.NoFileCompletions,
		Example:           exampleBlock([]string{"kref mcp   # run the MCP server over stdio (for an agent host)"}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcpserver.Serve(cmd.Context(), *dir, Version, allow, clientRoots)
		},
	}
	c.Flags().StringArrayVar(&allow, "allow", nil, "allow serving any repo under this absolute root via a per-call dir (repeatable); enables global mode")
	c.Flags().BoolVar(&clientRoots, "client-roots", false, "confine each call to the client's advertised MCP roots (mutually exclusive with --allow)")
	return c
}
