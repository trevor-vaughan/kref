package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/entity/dag"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// resignBackupRefPrefix holds the pre-rewrite tip of every resigned entry, so a
// rewrite can be undone locally. Like kref-pushed/*, these are bookkeeping and
// never pushed: dag.Push only transfers a tier's own namespace.
const resignBackupRefPrefix = "refs/kref-resign-backup/"

func resignBackupRef(t entry.Tier, id entity.Id) string {
	return resignBackupRefPrefix + t.Namespace() + "/" + id.String()
}

// ResignResult reports the outcome for one entry. A non-empty Reason means the
// entry was left alone and every count is zero.
//
// NewTip is where the REWRITE landed, not the entry's final ref: Resign then
// commits an `origin ... resign` provenance operation on top of it. Undo is
// therefore `update-ref <entry ref> <OldTip>`, which discards both.
type ResignResult struct {
	ID     entity.Id  `json:"id"`
	Tier   entry.Tier `json:"tier"`
	Signed int        `json:"signed"` // commits signed by this run
	Reason string     `json:"reason,omitempty"`
	OldTip string     `json:"old_tip,omitempty"` // pre-rewrite tip, also what the backup ref holds
	NewTip string     `json:"new_tip,omitempty"` // where the rewrite landed; empty on a dry run
	// Forced records that --force overrode the already-pushed refusal, which is
	// the one case where the rewrite cannot be published afterwards.
	Forced bool `json:"forced,omitempty"`
}

// ResignProgress is called once per entry as a sweep advances, after that entry
// is finished. done counts entries handled so far, including refusals; total is
// the length of the batch. A nil callback reports nothing.
type ResignProgress func(done, total int, id entity.Id)

// Resign rewrites the commit history of the given entries so their commits
// carry signatures, and reports what it did to each.
//
// This is safe for identity: entry, operation and comment ids all derive from
// the operation-pack BLOB (entity.DeriveId) and never from the commit, and the
// rewrite reuses each tree verbatim — so ids, links, favorites and lamport
// clocks all survive. Only commit hashes change.
//
// What is NOT safe is divergence, though not in the way it looks. Rewriting a
// published entry produces a chain the remote's ref cannot fast-forward to, and
// the damage lands on the REWRITER rather than on their collaborators
// (verified end to end; the e2e suite pins it):
//
//  1. `kref sync push` refuses the rewrite outright — a non-fast-forward update
//     — so the new chain never reaches the remote and no peer sees it.
//  2. Pulling after that is what breaks things: the fetch brings the old chain
//     back, MergeAll joins it to the rewritten one, and the entry then fails to
//     read at all with "multiple leafs in the entity DAG". That is the rewriter's
//     OWN copy, and refs/kref-resign-backup/ is the way back.
//  3. Reaching a collaborator needs a raw `git push --force` around kref. Even
//     then their fetch is refused for the same reason, so their copy stays
//     intact and readable — but their sync for that tier wedges in both
//     directions until someone reconciles the ref.
//
// Hence the pushed-mirror gate. --force overrides it only because every outcome
// above is a refusal rather than silent corruption, and the backup ref makes the
// local mess reversible.
func (s *Store) Resign(ids []entity.Id, dryRun, force bool, progress ResignProgress) ([]ResignResult, error) {
	// Distinguish "you have not turned signing on" from "kref cannot sign here at
	// all". Store.Signing() is false for both, and telling someone to configure a
	// key they already have sends them to fix the one thing that is not broken.
	if s.sigUnavailable != "" {
		return nil, errors.New("cannot resign: " + s.sigUnavailable)
	}
	if !s.Signing() {
		return nil, errors.New(
			"this repository is not configured to sign: set a signing key and turn on " +
				"commit.gpgsign (or kref.sign) before resigning")
	}
	// A composite: each leaf takes the write lock itself, because
	// withWriteLock is not reentrant (see writelock.go).
	//
	// Every error path returns `out` rather than nil. Each entry's rewrite has
	// already moved its ref and written its backup by the time a LATER entry
	// fails, so discarding the accumulated results would leave the operator with
	// mutations on disk and no record of which ones happened.
	out := make([]ResignResult, 0, len(ids))
	for _, id := range ids {
		res, err := s.resignOne(id, dryRun, force)
		if err != nil {
			// resignOne surfaces bare git errors (RefExist, the locked rewrite)
			// that name no entry, so a sweep would report an unattributable
			// failure.
			return out, fmt.Errorf("resign %s: %w", id, err)
		}
		if res.Reason == "" && !dryRun {
			// A re-signature attests NOW to THEN's content; it is not evidence
			// the content was signed when written. Recording it keeps the
			// entry honest about which it is.
			if err := s.RecordOrigin(id, s.author.Name(), "human", "", "resign"); err != nil {
				// The rewrite already landed. Report the entry as done and say
				// so, because the repair is to re-apply the marker — a second
				// resign would rewrite an already-signed chain for nothing.
				out = append(out, res)
				return out, fmt.Errorf(
					"resign %s: rewrote to %s but could not record its provenance, "+
						"so the entry now looks signed-at-write: %w", id, res.NewTip, err)
			}
		}
		out = append(out, res)
		if progress != nil {
			progress(len(out), len(ids), id)
		}
	}
	return out, nil
}

// ResignableIDs returns every entry eligible for a resign sweep: all tiers a
// by-id lookup searches, minus the hidden system tiers, whose held writes are
// the reviewer's to accept or reject rather than to rewrite.
func (s *Store) ResignableIDs() ([]entity.Id, error) {
	var ids []entity.Id
	for _, t := range s.searchTierNames() {
		if entry.IsSystemTier(t) {
			continue
		}
		tierIDs, err := dag.ListLocalIds(entry.Definition(t), s.repo)
		if err != nil {
			return nil, err
		}
		ids = append(ids, tierIDs...)
	}
	return ids, nil
}

func (s *Store) resignOne(id entity.Id, dryRun, force bool) (ResignResult, error) {
	t, e, err := s.locate(id)
	if err != nil {
		return ResignResult{}, err
	}
	res := ResignResult{ID: id, Tier: t}

	if entry.IsSystemTier(t) {
		res.Reason = fmt.Sprintf("held in the %s tier; approve or reject it instead", t)
		return res, nil
	}

	// Signing someone else's work with our key would misrepresent provenance,
	// and a chain where only some commits are signed is worse than none. So an
	// entry is resignable only when every operation in it is ours.
	//
	// OperationAuthors, not Authors: this rewrites and re-signs every commit,
	// including a peer's attestation, whose attester is read back FROM the
	// signature (see signerName). Authors() leaves attesters out on purpose, for
	// the claim derivation in Attest, and that is the narrower question.
	if foreign := foreignAuthors(e.OperationAuthors(), s.author.Email()); len(foreign) > 0 {
		res.Reason = "has operations by " + strings.Join(foreign, ", ") +
			"; signing another author's work would misrepresent it"
		return res, nil
	}

	pushed, err := s.repo.RefExist(pushedRef(t, id))
	if err != nil {
		return res, err
	}
	if pushed && !force {
		res.Reason = "already pushed; the remote refuses a rewritten history, and " +
			"pulling afterwards breaks your own copy of this entry " +
			"(attest to it instead with `kref attest`, or re-run with --force to rewrite anyway)"
		return res, nil
	}
	res.Forced = pushed

	ref := entryRef(t, id)
	err = s.withWriteLock(func() error { return s.rewriteRef(&res, ref, dryRun) })
	return res, err
}

// rewriteRef rebuilds every commit reachable from ref with a signature, then
// moves the ref, keeping the previous tip as a backup. It is the locked leaf of
// a resign.
func (s *Store) rewriteRef(res *ResignResult, ref string, dryRun bool) error {
	t, id := res.Tier, res.ID
	oldTip, err := s.git("rev-parse", ref)
	if err != nil {
		return err
	}
	res.OldTip = oldTip

	// --topo-order --reverse yields parents before children, which the remap
	// below depends on; it also handles the merge commits sync creates.
	listed, err := s.git("rev-list", "--topo-order", "--reverse", ref)
	if err != nil {
		return err
	}
	commits := strings.Fields(listed)

	rewritten := make(map[string]string, len(commits))
	newTip := oldTip
	for _, old := range commits {
		meta, err := s.commitMeta(old)
		if err != nil {
			return err
		}
		// git-bug leaves the commit ident empty (it reads git's author.* config,
		// which is normally unset) and `git commit-tree` refuses that. The
		// authoritative author is the operation pack's, already checked to be
		// ours, so stamping it here fills a blank rather than overwriting a
		// claim.
		if meta.authorEmail == "" {
			meta.authorName, meta.authorEmail = s.author.Name(), s.author.Email()
		}
		if meta.commitEmail == "" {
			meta.commitName, meta.commitEmail = s.author.Name(), s.author.Email()
		}
		res.Signed++
		if dryRun {
			continue
		}
		created, err := s.rebuildCommit(meta, rewritten)
		if err != nil {
			return fmt.Errorf("rebuild %s of %s: %w", old, id, err)
		}
		rewritten[old] = created
		newTip = created
	}
	if dryRun {
		return nil
	}

	// Back up before moving, so a bad outcome is recoverable.
	if _, err := s.git("update-ref", resignBackupRef(t, id), oldTip); err != nil {
		return err
	}
	if _, err := s.git("update-ref", ref, newTip); err != nil {
		return err
	}
	res.NewTip = newTip
	return nil
}

// commitMeta is everything needed to faithfully rebuild a commit.
type commitMeta struct {
	tree        string
	parents     []string
	authorName  string
	authorEmail string
	authorDate  string
	commitName  string
	commitEmail string
	commitDate  string
	message     string
}

// commitMeta reads a commit's metadata via format tokens rather than parsing
// `cat-file commit`, whose signature header spans continuation lines.
func (s *Store) commitMeta(hash string) (commitMeta, error) {
	const format = "%T%n%an%n%ae%n%aI%n%cn%n%ce%n%cI%n%P%n%B"
	raw, err := s.gitRaw(nil, "", "show", "-s", "--format="+format, hash)
	if err != nil {
		return commitMeta{}, err
	}
	// Exactly eight single-line fields precede the raw body, which may be empty
	// or span lines and so must stay unsplit.
	parts := strings.SplitN(raw, "\n", 9)
	if len(parts) < 9 {
		return commitMeta{}, fmt.Errorf("commit %s: got %d metadata fields, want 9", hash, len(parts))
	}
	return commitMeta{
		tree:       parts[0],
		authorName: parts[1], authorEmail: parts[2], authorDate: parts[3],
		commitName: parts[4], commitEmail: parts[5], commitDate: parts[6],
		parents: strings.Fields(parts[7]),
		// `show` terminates its format with a newline; the body itself keeps any
		// internal ones. git-bug writes empty messages, which must stay empty.
		message: strings.TrimSuffix(parts[8], "\n"),
	}, nil
}

// foreignAuthors returns the display forms of any authors that are not mine.
func foreignAuthors(authors []entry.Author, mine string) []string {
	var out []string
	for _, a := range authors {
		if a.Email != mine {
			out = append(out, a.Name+" <"+a.Email+">")
		}
	}
	return out
}

// rebuildCommit writes a new, signed commit with the same tree, identity and
// dates, its parents remapped to the already-rewritten ones.
func (s *Store) rebuildCommit(m commitMeta, rewritten map[string]string) (string, error) {
	args := []string{"commit-tree", "-S", m.tree}
	for _, p := range m.parents {
		mapped, ok := rewritten[p]
		if !ok {
			// --topo-order --reverse guarantees parents come first; a miss
			// means the walk order broke, and silently keeping the old parent
			// would fork the entry into two leaves.
			return "", fmt.Errorf("parent %s was not rewritten first", p)
		}
		args = append(args, "-p", mapped)
	}
	out, err := s.gitRaw([]string{
		"GIT_AUTHOR_NAME=" + m.authorName,
		"GIT_AUTHOR_EMAIL=" + m.authorEmail,
		"GIT_AUTHOR_DATE=" + m.authorDate,
		"GIT_COMMITTER_NAME=" + m.commitName,
		"GIT_COMMITTER_EMAIL=" + m.commitEmail,
		"GIT_COMMITTER_DATE=" + m.commitDate,
	}, m.message, args...)
	return strings.TrimSpace(out), err
}

// git runs a git command in the store's repo and returns its trimmed stdout —
// the right shape for the plumbing that emits a single hash or ref.
func (s *Store) git(args ...string) (string, error) {
	out, err := s.gitRaw(nil, "", args...)
	return strings.TrimSpace(out), err
}

// gitRaw runs a git command in the store's repo with extra environment and
// stdin, returning stdout verbatim. Callers that read multi-field output MUST
// use this rather than git: trimming would swallow the trailing empty fields
// that an empty commit message or a parentless root commit produce, silently
// shifting every field.
func (s *Store) gitRaw(env []string, stdin string, args ...string) (string, error) {
	// Every kref git subprocess runs under the active identity profile, so
	// `commit-tree -S` here signs with the same key the store's own writes and
	// verification use. The profile goes first so a caller-supplied variable
	// still wins on a collision.
	profile := s.signingEnv()
	full := make([]string, 0, len(profile)+len(env))
	full = append(full, profile...)
	full = append(full, env...)
	return runGit(full, stdin, append([]string{"-C", s.dir}, args...)...)
}

// signingEnv is the identity-profile environment, or nil when the repo has no
// signature support (in which case there is no profile to export either).
func (s *Store) signingEnv() []string {
	reader, ok := s.repo.(sigReader)
	if !ok {
		return nil
	}
	return reader.signingEnv()
}

// runGit runs a git command anywhere, returning stdout verbatim. It is separate
// from the Store method because reading a standalone gitconfig file has no repo
// to run in.
func runGit(env []string, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}
