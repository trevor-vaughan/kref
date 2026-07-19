package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/store"
)

func newQuarantineCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "quarantine",
		Short: "Review, approve, or reject writes held for secret review",
		Long: "A write that trips the secret scanner on a syncable tier is held in " +
			"the quarantine review queue instead of being applied. Approve applies it " +
			"through the normal write path; reject discards it (content preserved for audit).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			queue, err := s.QuarantineQueue()
			if err != nil {
				return err
			}
			// An empty queue is a common, reassuring case — say so plainly (except
			// under --json, which stays a clean machine array).
			if len(queue) == 0 && !jsonMode(cmd) {
				fmt.Fprintln(cmd.OutOrStdout(), "review queue is clear — nothing awaiting review.")
				return nil
			}
			// On a terminal, open the interactive review viewer at the first item;
			// otherwise print the static queue (or --json).
			if usePager(cmd) && !plainMode(cmd) && !jsonMode(cmd) {
				res, rerr := runReviewModel(listCockpitActions{s: s, filter: store.ListFilter{}}, queue, 0, useColor(cmd), ttyWidth())
				if rerr != nil {
					return rerr
				}
				if res.action == "open" {
					snap, gerr := s.Get(res.target)
					if gerr != nil {
						return gerr
					}
					return openEntry(cmd, dir, s, snap)
				}
				return nil
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { renderQuarantineQueue(w, queue, false) },
				quarantineJSONList(queue))
		},
	}
	c.AddCommand(
		newQuarantineListCmd(dir), newQuarantineShowCmd(dir),
		newQuarantineApproveCmd(dir), newQuarantineRejectCmd(dir),
		newQuarantineRecoverCmd(dir), newQuarantinePurgeCmd(dir),
	)
	return c
}

func newQuarantineShowCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "show <id>",
		Short: "Review a held write — its findings and proposed change",
		Example: exampleBlock([]string{
			"kref quarantine show a1b2c3d4",
		}),
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
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
			detail, err := s.QuarantineDetail(id)
			if err != nil {
				return err
			}
			// On a terminal, the interactive review viewer (approve/reject/open in
			// place); otherwise the static review, or --json.
			if usePager(cmd) && !plainMode(cmd) && !jsonMode(cmd) {
				queue, qerr := s.QuarantineQueue()
				if qerr != nil {
					return qerr
				}
				start := 0
				for i, it := range queue {
					if it.ID == id {
						start = i
						break
					}
				}
				res, rerr := runReviewModel(listCockpitActions{s: s, filter: store.ListFilter{}}, queue, start, useColor(cmd), ttyWidth())
				if rerr != nil {
					return rerr
				}
				if res.action == "open" {
					snap, gerr := s.Get(res.target)
					if gerr != nil {
						return gerr
					}
					return openEntry(cmd, dir, s, snap)
				}
				return nil
			}
			return emit(cmd,
				func(w io.Writer, color bool) { renderQuarantineReview(w, detail, color, ttyWidth()) },
				detail)
		},
	}
	return c
}

func newQuarantineListCmd(dir *string) *cobra.Command {
	var rejected bool
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List writes held for secret review (the review queue)",
		Example: exampleBlock([]string{
			"kref quarantine list",
			"kref quarantine list --rejected   # tombstoned rejections (recover or purge)",
			"kref quarantine list --json",
		}),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			var items []store.QuarantineItem
			if rejected {
				items, err = s.RejectedQuarantine()
			} else {
				items, err = s.QuarantineQueue()
			}
			if err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { renderQuarantineQueue(w, items, rejected) },
				quarantineJSONList(items))
		},
	}
	c.Flags().BoolVar(&rejected, "rejected", false, "list rejected (tombstoned) items — recover or purge them — instead of the pending queue")
	return c
}

func newQuarantinePurgeCmd(dir *string) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "purge [<id>]",
		Short: "Hard-delete rejected writes (excises the held content); irreversible",
		Long: "Permanently remove rejected quarantine items — their ref, their history " +
			"(so a held secret is excised, not just hidden), and their recovery files. " +
			"With an id, purge that one; with no id, purge every rejected item. Only " +
			"rejected items are purgeable; a pending review is never removed.",
		Example: exampleBlock([]string{
			"kref quarantine purge a1b2c3d4   # one rejected item",
			"kref quarantine purge -y         # every rejected item, no prompt",
		}),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()

			if len(args) == 1 {
				id, err := resolveArg(s, args[0])
				if err != nil {
					return err
				}
				if !yes && !confirmQuarantinePurge(cmd, "Purge rejected item "+shortStr(id.String(), 12)+"?") {
					fmt.Fprintln(cmd.ErrOrStderr(), "aborted; nothing purged.")
					return nil
				}
				if err := s.PurgeRejectedQuarantine(id); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "purged %s\n", shortStr(id.String(), 12))
				return nil
			}

			items, err := s.RejectedQuarantine()
			if err != nil {
				return err
			}
			if len(items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no rejected writes to purge.")
				return nil
			}
			if !yes && !confirmQuarantinePurge(cmd, fmt.Sprintf("Purge ALL %d rejected item(s)?", len(items))) {
				fmt.Fprintln(cmd.ErrOrStderr(), "aborted; nothing purged.")
				return nil
			}
			for _, it := range items {
				if err := s.PurgeRejectedQuarantine(it.ID); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "purged %d rejected item(s)\n", len(items))
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

// confirmQuarantinePurge prompts for a yes/no on an irreversible purge.
func confirmQuarantinePurge(cmd *cobra.Command, prompt string) bool {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s This is irreversible.\nType 'yes' to proceed: ", prompt)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return false
	}
	return strings.TrimSpace(line) == "yes"
}

func newQuarantineRecoverCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "recover <id>",
		Short: "Return a rejected write to the pending review queue",
		Example: exampleBlock([]string{
			"kref quarantine recover a1b2c3d4",
		}),
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
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
			if err := s.RecoverQuarantine(id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "recovered %s — back in the review queue\n", shortStr(id.String(), 12))
			return nil
		},
	}
	return c
}

func newQuarantineApproveCmd(dir *string) *cobra.Command {
	var note string
	c := &cobra.Command{
		Use:   "approve <id>",
		Short: "Apply a held write to its live target (or promote a draft)",
		Example: exampleBlock([]string{
			"kref quarantine approve a1b2c3d4",
			"kref quarantine approve a1b2c3d4 -m 'confirmed false positive'",
		}),
		Args: cobra.ExactArgs(1),
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
			actor, actorKind := resolveActor(cmd, s)
			if err := s.ApproveQuarantine(id, note, actor, actorKind); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "approved %s\n", shortStr(id.String(), 12))
			return nil
		},
	}
	c.Flags().StringVarP(&note, "message", "m", "", "note recorded on the resolved review thread")
	return c
}

func newQuarantineRejectCmd(dir *string) *cobra.Command {
	var note string
	c := &cobra.Command{
		Use:   "reject <id>",
		Short: "Discard a held write (content preserved for audit)",
		Example: exampleBlock([]string{
			"kref quarantine reject a1b2c3d4",
			"kref quarantine reject a1b2c3d4 -m 'real secret, rotate it'",
		}),
		Args: cobra.ExactArgs(1),
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
			_, actorKind := resolveActor(cmd, s)
			path, err := s.RejectQuarantine(id, note, actorKind)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rejected %s; content preserved to %s\n", shortStr(id.String(), 12), path)
			return nil
		},
	}
	c.Flags().StringVarP(&note, "message", "m", "", "reason recorded on the resolved review thread")
	return c
}
