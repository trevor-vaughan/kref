package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/trevor-vaughan/kref/internal/commentguard"
	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

// resolveCommentID matches a hex prefix against a single entry's comments.
// It returns an unambiguous full ID or an error.
func resolveCommentID(snap *entry.Snapshot, prefix string) (string, error) {
	var matches []string
	for _, c := range snap.Comments {
		if strings.HasPrefix(c.ID, prefix) {
			matches = append(matches, c.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no comment matches %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("comment prefix %q is ambiguous (%d matches)", prefix, len(matches))
	}
}

// findComment returns a pointer into snap.Comments for the given full id, or
// nil when not found.
func findComment(snap *entry.Snapshot, id string) *entry.Comment {
	for i := range snap.Comments {
		if snap.Comments[i].ID == id {
			return &snap.Comments[i]
		}
	}
	return nil
}

// shortStr returns up to n runes from s — guards against ids shorter than the
// requested prefix length (shouldn't happen in practice, but be safe).
func shortStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// completeCommentIDs returns tab-completion candidates for --reply-to and
// --resolve: the first positional (if present) is resolved to an entry, and
// each comment's id + first line of body is offered as id\tdescription.
func completeCommentIDs(dir *string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		s, err := store.Open(*dir)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer s.Close()
		id, err := s.Resolve(args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		snap, err := s.Get(id)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, c := range snap.Comments {
			if !strings.HasPrefix(c.ID, toComplete) {
				continue
			}
			desc := strings.SplitN(c.Body, "\n", 2)[0]
			out = append(out, c.ID+"\t"+desc)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func newCommentCmd(dir *string) *cobra.Command {
	var message, replyTo, resolve string
	var question bool
	var edit, del string
	var yes, force bool

	c := &cobra.Command{
		Use:   "comment <entry>",
		Short: "Add or resolve a comment on an entry",
		Long: "Add a comment to an entry, optionally marking it as a question. " +
			"Use --resolve <prefix> to close an open question by its comment id prefix.",
		Example: exampleBlock([]string{
			"kref comment a1b2c3d4 -m 'looks good'",
			"kref comment a1b2c3d4 -m 'is this right?' --question",
			"kref comment a1b2c3d4 --resolve abc123",
			"kref comment a1b2c3d4 --resolve abc123 -m 'yes, fixed'",
			"kref comment a1b2c3d4 --reply-to abc123 -m 'agreed'",
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			if countSet(question, resolve != "", edit != "", del != "") > 1 {
				return errors.New("--question, --resolve, --edit, and --delete are mutually exclusive")
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
			snap, err := s.Get(id)
			if err != nil {
				return err
			}

			_, actorKind := resolveActor(cmd, s)

			if edit != "" {
				target, err := resolveCommentID(snap, edit)
				if err != nil {
					return err
				}
				body := message
				if body == "" && !cmd.Flags().Changed("message") && !term.IsTerminal(int(os.Stdin.Fd())) {
					raw, rErr := io.ReadAll(cmd.InOrStdin())
					if rErr != nil {
						return rErr
					}
					body = strings.TrimRight(string(raw), "\n")
				}
				if strings.TrimSpace(body) == "" {
					return errors.New("edit body is empty (use -m or pipe stdin)")
				}
				unscanned, cErr := commentguard.Check(snap, body, false)
				var refused *commentguard.RefusedError
				if errors.As(cErr, &refused) {
					return parkComment(cmd, s, force, refused.Findings,
						fmt.Sprintf("edited %s (force-approved)", shortStr(target, 12)),
						func(f []scan.Finding) (store.Parked, error) {
							return s.QuarantineEditComment(id, target, body, f, actorKind)
						})
				}
				if cErr != nil {
					return cErr
				}
				if unscanned {
					fmt.Fprintln(cmd.ErrOrStderr(), "warning: betterleaks not found — comment stored UNSCANNED")
				}
				if eErr := s.EditComment(id, target, body); eErr != nil {
					return eErr
				}
				fmt.Fprintf(cmd.OutOrStdout(), "edited %s\n", shortStr(target, 12))
				return nil
			}

			if del != "" {
				target, err := resolveCommentID(snap, del)
				if err != nil {
					return err
				}
				if !yes {
					out := cmd.ErrOrStderr()
					fmt.Fprintf(out, "About to delete comment %s.\nType 'yes' to proceed: ", shortStr(target, 12))
					line, rErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
					if rErr != nil && rErr != io.EOF {
						return rErr
					}
					if strings.TrimSpace(line) != "yes" {
						fmt.Fprintln(out, "aborted; nothing deleted.")
						return nil
					}
				}
				if dErr := s.DeleteComment(id, target); dErr != nil {
					return dErr
				}
				fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", shortStr(target, 12))
				return nil
			}

			if resolve != "" {
				// Resolve path: close a question comment.
				target, err := resolveCommentID(snap, resolve)
				if err != nil {
					return err
				}
				tc := findComment(snap, target)
				if tc == nil || !tc.Question {
					return fmt.Errorf("comment %s is not a question", resolve)
				}

				// Optional closing note from -m / piped stdin only (no editor).
				note := message
				if note == "" && !cmd.Flags().Changed("message") && !term.IsTerminal(int(os.Stdin.Fd())) {
					raw, rErr := io.ReadAll(cmd.InOrStdin())
					if rErr != nil {
						return rErr
					}
					note = strings.TrimRight(string(raw), "\n")
				}

				if strings.TrimSpace(note) != "" {
					unscanned, cErr := commentguard.Check(snap, note, false)
					var refused *commentguard.RefusedError
					if errors.As(cErr, &refused) {
						// The note is flagged: park the resolve-with-note; the
						// question stays open until a human approves (or --force).
						return parkComment(cmd, s, force, refused.Findings,
							fmt.Sprintf("resolved %s (force-approved)", shortStr(target, 12)),
							func(f []scan.Finding) (store.Parked, error) {
								return s.QuarantineResolveNote(id, target, note, f, actorKind)
							})
					}
					if cErr != nil {
						return cErr
					}
					if unscanned {
						fmt.Fprintln(cmd.ErrOrStderr(), "warning: betterleaks not found — comment stored UNSCANNED")
					}
					if _, aErr := s.AddComment(id, actorKind, note, false, target); aErr != nil {
						return aErr
					}
				}
				if err := s.ResolveComment(id, target); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "resolved %s\n", shortStr(target, 12))
				return nil
			}

			// Add path: read body, optionally resolve --reply-to prefix.
			parent := ""
			if cmd.Flags().Changed("reply-to") {
				parent, err = resolveCommentID(snap, replyTo)
				if err != nil {
					return err
				}
			}

			body := message
			if body == "" && !cmd.Flags().Changed("message") && !term.IsTerminal(int(os.Stdin.Fd())) {
				raw, rErr := io.ReadAll(cmd.InOrStdin())
				if rErr != nil {
					return rErr
				}
				body = strings.TrimRight(string(raw), "\n")
			}

			if strings.TrimSpace(body) == "" {
				return errors.New("comment body is empty")
			}

			// A secret-bearing comment on a syncable entry is diverted into the
			// quarantine review queue (posted only on human approval), not refused.
			// --force is the human's direct-write escape (approve-at-write); it
			// skips the scan, so it never parks.
			unscanned, cErr := commentguard.Check(snap, body, false)
			var refused *commentguard.RefusedError
			if errors.As(cErr, &refused) {
				return parkComment(cmd, s, force, refused.Findings, "commented (force-approved)",
					func(f []scan.Finding) (store.Parked, error) {
						return s.QuarantineComment(id, body, question, parent, f, actorKind)
					})
			}
			if cErr != nil {
				return cErr
			}
			if unscanned {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: betterleaks not found — comment stored UNSCANNED")
			}

			cid, err := s.AddComment(id, actorKind, body, question, parent)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "commented %s\n", shortStr(cid, 12))
			return nil
		},
	}

	applyGuide(c, cobra.ExactArgs(1), argGuide{
		noun:  "an entry id",
		find:  "kref list",
		usage: "kref comment <entry> [-m body] [--question] [--reply-to prefix] [--resolve prefix]",
		examples: []string{
			"kref comment a1b2c3d4 -m 'looks good'",
			"kref comment a1b2c3d4 -m 'is this right?' -q",
			"kref comment a1b2c3d4 --resolve abc123",
		},
	})

	c.Flags().StringVarP(&message, "message", "m", "", "comment body")
	c.Flags().BoolVarP(&question, "question", "q", false, "mark the comment as a question")
	c.Flags().StringVar(&replyTo, "reply-to", "", "parent comment id (prefix)")
	c.Flags().StringVar(&resolve, "resolve", "", "resolve a question by comment id (prefix)")
	c.Flags().StringVar(&edit, "edit", "", "edit a comment body by id (prefix)")
	c.Flags().StringVar(&del, "delete", "", "delete a comment by id (prefix)")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the delete confirmation")
	c.Flags().BoolVar(&force, "force", false, "store a comment body a secret scan flagged (override a false positive)")

	c.ValidArgsFunction = entryArgs(dir, 1, sourceAll)
	_ = c.RegisterFlagCompletionFunc("reply-to", completeCommentIDs(dir))
	_ = c.RegisterFlagCompletionFunc("resolve", completeCommentIDs(dir))
	_ = c.RegisterFlagCompletionFunc("edit", completeCommentIDs(dir))
	_ = c.RegisterFlagCompletionFunc("delete", completeCommentIDs(dir))

	return c
}

// countSet returns how many of the given booleans are true.
func countSet(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}
