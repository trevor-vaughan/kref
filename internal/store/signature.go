package store

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// sigReader is satisfied by *signingRepo. It exists because signature support
// depends on a go-git handle the plain git-bug repo does not expose.
type sigReader interface {
	chainState(ref string) (entry.SigState, entry.SigReason, *entry.Attestation, error)
	commitAttests(hash repository.Hash) (*entry.Attestation, error)
	signing() bool
	signingEnv() []string
}

// The whole design hangs off this assertion succeeding: a *signingRepo that
// drifts out of sigReader would not fail to build, it would silently make every
// entry read as unsigned and every resign refuse. Pin it at compile time.
var _ sigReader = (*signingRepo)(nil)

// Signing reports whether this store signs the commits it writes.
func (s *Store) Signing() bool {
	reader, ok := s.repo.(sigReader)
	return ok && reader.signing()
}

// SigState returns the signature verdict for an entry, searching every tier. It
// discards the reason: callers that need to tell a reader what to DO read it off
// the snapshot or excerpt, where resolution puts both halves.
func (s *Store) SigState(id entity.Id) (entry.SigState, error) {
	t, _, err := s.locate(id)
	if err != nil {
		return "", err
	}
	st, _, _, err := s.refSigState(entryRef(t, id))
	return st, err
}

// resolveSigState fills a snapshot's signature verdict from the live repo.
//
// It is separate from compileSnapshot because the verdict is not derived from
// the entry's commits: the same ref tip verifies differently once the
// allowed-signers file changes, a key expires, or a different identity profile
// is selected. Resolving on demand is what keeps `kref list` and `kref show`
// from disagreeing after a signing-config fix.
func (s *Store) resolveSigState(t entry.Tier, snap *entry.Snapshot) error {
	sig, why, att, err := s.refSigState(entryRef(t, snap.ID))
	if err != nil {
		return fmt.Errorf("signature state for %s: %w", snap.ID, err)
	}
	snap.SigState, snap.SigReason = sig, why
	if att != nil {
		snap.AttestedBy, snap.AttestedAt, snap.AttestedClaim = att.By, att.At, att.Claim
	}
	return nil
}

// resolveExcerptSigStates fills the live verdict on cached excerpts, in place.
// The excerpts themselves come from the ref-keyed cache; the verdict never does.
func (s *Store) resolveExcerptSigStates(exs []Excerpt) error {
	for i := range exs {
		sig, why, _, err := s.refSigState(entryRef(entry.Tier(exs[i].Tier), exs[i].ID))
		if err != nil {
			return fmt.Errorf("signature state for %s: %w", exs[i].ID, err)
		}
		exs[i].SigState, exs[i].SigReason = sig, why
	}
	return nil
}

// refSigState reports the signature verdict for a whole entry. It returns
// entry.SigUnsigned when the repo has no signature support, which is the honest
// answer: kref could not have written a signature either.
func (s *Store) refSigState(ref string) (entry.SigState, entry.SigReason, *entry.Attestation, error) {
	reader, ok := s.repo.(sigReader)
	if !ok {
		return entry.SigUnsigned, entry.SigReasonNone, nil, nil
	}
	return reader.chainState(ref)
}

// chainState reports one verdict for every commit reachable from ref, worst
// first: bad > untrusted > unsigned > good.
//
// The verdict describes the ENTRY, so it has to describe all of it. Reading only
// the tip meant a single ordinary edit made history written before the signing
// key report `good` — and drop out of `list --unsigned`, where it was the only
// thing that would ever have found it again. An entry is attested when every
// operation in it is, or not at all.
//
// The ordering puts untrusted above unsigned because they call for different
// actions: an unverifiable signature says "get the signer's key", a gap says
// "resign". Being told to resign someone else's operations is the worse mistake.
//
// Cost is one subprocess per entry, the same as the tip-only check it replaces:
// only the commits that actually carry a signature are verified, and they are
// verified together.
//
// PRESENCE is read from each commit object rather than from git's %G?, because
// %G? reports a signed commit as "N" — identical to unsigned — whenever
// verification cannot run at all (most often a missing
// gpg.ssh.allowedSignersFile). Reading the object is also free through the
// go-git handle, so a chain that never signed costs no subprocess at all.
//
// That presence check sees the `gpgsig` header only: go-git recognises
// `gpgsig-sha256` but deliberately does not expose it (plumbing/object/
// commit_scanner.go). Safe solely because a repo writing ONLY that header is a
// SHA-256 repo, which go-git refuses to open; a SHA-1 repo carrying the sha256
// compat header writes BOTH, and git ignores a lone gpgsig-sha256 there exactly
// as this does. signature_test.go pins that refusal, so this stops being safe
// loudly.
func (r *signingRepo) chainState(ref string) (entry.SigState, entry.SigReason, *entry.Attestation, error) {
	hashes, err := r.ListCommits(ref)
	if err != nil {
		return "", entry.SigReasonNone, nil, err
	}
	if len(hashes) == 0 {
		return "", entry.SigReasonNone, nil, fmt.Errorf("no commits reachable from %s", ref)
	}
	signed := make([]repository.Hash, 0, len(hashes))
	gap := false
	for _, h := range hashes {
		_, sig, err := r.readCommitOnce(h)
		if err != nil {
			return "", entry.SigReasonNone, nil, fmt.Errorf("read commit %s: %w", h, err)
		}
		if sig == "" {
			gap = true
			continue
		}
		signed = append(signed, h)
	}
	if len(signed) == 0 {
		// Nothing carries a signature, so there is nothing to verify — and no
		// commit that could vouch for the rest either.
		state, why := foldVerdicts(nil, nil, gap)
		return state, why, nil, nil
	}
	verdicts, err := r.verifyAll(signed)
	if err != nil {
		return "", entry.SigReasonNone, nil, err
	}

	worst, why := foldVerdicts(hashes, verdicts, gap)
	if worst == entry.SigGood {
		// The overwhelmingly common path. Returning here is what keeps
		// attestation free for stores that never needed it.
		return worst, why, nil, nil
	}
	return r.foldWithAttestation(hashes, verdicts, worst, why)
}

// foldVerdicts reduces a set of commits to one verdict, worst first. gap says at
// least one commit in the set carries no signature at all.
//
// Among equally-bad states the more alarming reason wins: being told a key was
// REVOKED matters more than being told another one is merely unimported.
func foldVerdicts(
	hashes []repository.Hash,
	verdicts map[repository.Hash]sigVerdict,
	gap bool,
) (entry.SigState, entry.SigReason) {
	worst := entry.SigGood
	if gap {
		worst = entry.SigUnsigned
	}
	why := entry.SigReasonNone
	for _, h := range hashes {
		v, ok := verdicts[h]
		if !ok {
			continue
		}
		switch {
		case sigRank[v.state] > sigRank[worst]:
			worst, why = v.state, v.reason
		case v.state == worst && reasonRank[v.reason] > reasonRank[why]:
			why = v.reason
		}
	}
	return worst, why
}

// foldWithAttestation re-folds the chain when an attestation vouches for part of
// it. Coverage is git ancestry: the attesting commit is signed, and a commit
// hash already commits to everything beneath it. There is deliberately no digest
// — a second Merkle structure over the same data is only something that can
// disagree with the first.
//
// It absorbs `untrusted` as well as `unsigned`. Excluding it would leave the
// motivating cases unfixed: a rotated or expired key produces a signature that
// is PRESENT and no longer vouched for, not an absent one.
//
// The returned attestation is the one that actually VOUCHED — nil when nothing
// was absorbed, so a caller cannot report an attestation that did no work.
func (r *signingRepo) foldWithAttestation(
	hashes []repository.Hash,
	verdicts map[repository.Hash]sigVerdict,
	worst entry.SigState,
	why entry.SigReason,
) (entry.SigState, entry.SigReason, *entry.Attestation, error) {
	// Walk from the end of ListCommits' output backwards. That is a good
	// heuristic for "newest first" but NOT a topological guarantee: the walk is
	// a LIFO depth-first pre-order and reversing a pre-order does not
	// topologically sort a DAG. On the diamond a sync merge produces, a branch
	// commit can precede its own parent.
	//
	// Correctness does not rest on the order. Whichever attestation is found,
	// coverage is recomputed by a real ancestry walk below, so a "wrong" pick
	// can only cover LESS than another would — never more — and everything left
	// uncovered still folds through `rest`. The consequence is that two
	// attestations on opposite sides of a merge do not combine: one of them is
	// honoured and the other contributes nothing.
	var attester repository.Hash
	var att *entry.Attestation
	for _, h := range slices.Backward(hashes) {
		// Only a commit whose OWN signature is good can vouch for anything.
		if verdicts[h].state != entry.SigGood {
			continue
		}
		found, err := r.commitAttests(h)
		if err != nil {
			return "", entry.SigReasonNone, nil, err
		}
		if found != nil {
			attester, att = h, found
			break
		}
	}
	if att == nil {
		return worst, why, nil, nil
	}

	covered, err := r.ancestry(attester)
	if err != nil {
		return "", entry.SigReasonNone, nil, err
	}

	// A tampered commit survives any attestation. Checked before the re-fold so
	// no ordering accident can let it through.
	for h := range covered {
		if verdicts[h].state == entry.SigBad {
			return entry.SigBad, entry.SigReasonNone, nil, nil
		}
	}

	who, err := r.signerName(attester)
	if err != nil {
		return "", entry.SigReasonNone, nil, err
	}
	att.By = who

	rest := make([]repository.Hash, 0, len(hashes))
	gap := false
	for _, h := range hashes {
		if _, ok := covered[h]; ok {
			continue
		}
		if _, signed := verdicts[h]; !signed {
			gap = true
			continue
		}
		rest = append(rest, h)
	}
	state, reason := foldVerdicts(rest, verdicts, gap)
	return state, reason, att, nil
}

// ancestry returns the attesting commit and every commit beneath it.
//
// It walks parents itself rather than calling ListCommits, which resolves a REF
// name and cannot be handed a hash.
func (r *signingRepo) ancestry(h repository.Hash) (map[repository.Hash]struct{}, error) {
	set := make(map[repository.Hash]struct{})
	stack := []repository.Hash{h}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := set[cur]; seen {
			continue
		}
		set[cur] = struct{}{}
		commit, err := r.ReadCommit(cur)
		if err != nil {
			return nil, fmt.Errorf("ancestry of %s: %w", h, err)
		}
		stack = append(stack, commit.Parents...)
	}
	return set, nil
}

// reasonRank orders the untrusted reasons by how much they demand of a reader:
// a revoked key is a warning, the rest are setup steps.
var reasonRank = map[entry.SigReason]int{
	entry.SigReasonNone:           0,
	entry.SigReasonUnverifiable:   1,
	entry.SigReasonKeyUnavailable: 2,
	entry.SigReasonKeyUntrusted:   3,
	entry.SigReasonKeyExpired:     4,
	entry.SigReasonKeyRevoked:     5,
}

// sigRank orders the states by how much they demand of a reader, so folding a
// chain is a max rather than a pile of special cases.
var sigRank = map[entry.SigState]int{
	entry.SigGood:      0,
	entry.SigUnsigned:  1,
	entry.SigUntrusted: 2,
	entry.SigBad:       3,
}

// verifyAll asks git for a verdict on commits already known to be signed, in
// one call. `git show` accepts many revisions and emits one line each, so the
// whole chain costs a single subprocess.
//
// The hash is echoed back with %H rather than zipping by position: git is free
// to collapse a duplicate revision, and a silent off-by-one here would report
// one commit's verdict against another.
func (r *signingRepo) verifyAll(hashes []repository.Hash) (map[repository.Hash]sigVerdict, error) {
	args := []string{"-C", r.dir, "show", "-s", "--format=%H %G?"}
	for _, h := range hashes {
		args = append(args, h.String())
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), r.env...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("verify %d commit(s): %w: %s",
			len(hashes), err, strings.TrimSpace(stderr.String()))
	}
	verdicts := make(map[repository.Hash]sigVerdict, len(hashes))
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		h, verdict, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		st, why := verdictState(verdict)
		verdicts[repository.Hash(h)] = sigVerdict{state: st, reason: why}
	}
	for _, h := range hashes {
		if _, ok := verdicts[h]; !ok {
			return nil, fmt.Errorf("verify commit %s: git reported no verdict", h)
		}
	}
	return verdicts, nil
}

// sigVerdict is one commit's answer: the state, plus why it is not vouched for.
type sigVerdict struct {
	state  entry.SigState
	reason entry.SigReason
}

// verdictState maps git's one-character %G? report onto a state and, when the
// signature cannot be vouched for, the reason — because the five ways that
// happens ask for five different things from the reader.
//
// The letters are not symmetric across formats, which is why the reasons are
// phrased around the key rather than around a config file (measured against git
// 2.52):
//
//	ssh: vouched G | not in allowed_signers U | no allowedSignersFile N | revoked B
//	gpg: vouched G | no ownertrust           U | key not in keyring    E | revoked R
//
// So ssh revocation is already loud (it lands in SigBad) and E is openpgp's
// alone, while U means the same thing in both: the key is there, nobody has
// vouched for it.
func verdictState(verdict string) (entry.SigState, entry.SigReason) {
	switch verdict {
	case "G":
		return entry.SigGood, entry.SigReasonNone
	case "B":
		return entry.SigBad, entry.SigReasonNone
	case "U":
		return entry.SigUntrusted, entry.SigReasonKeyUntrusted
	case "E":
		return entry.SigUntrusted, entry.SigReasonKeyUnavailable
	case "X", "Y":
		return entry.SigUntrusted, entry.SigReasonKeyExpired
	case "R":
		return entry.SigUntrusted, entry.SigReasonKeyRevoked
	default:
		// "N" on a commit we KNOW carries a signature means verification could
		// not run, not that there is nothing to check.
		return entry.SigUntrusted, entry.SigReasonUnverifiable
	}
}
