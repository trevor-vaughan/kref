package store

import (
	"errors"
	"fmt"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// AttestResult reports the outcome for one entry. A non-empty Reason means the
// entry was left alone and nothing was written.
type AttestResult struct {
	ID       entity.Id   `json:"id"`
	Tier     entry.Tier  `json:"tier"`
	Attested bool        `json:"attested"`
	Claim    entry.Claim `json:"claim,omitempty"`
	Reason   string      `json:"reason,omitempty"`
	NewTip   string      `json:"new_tip,omitempty"`
}

// Attest appends a signed attestation to each entry, vouching for the history
// beneath it.
//
// This is the answer for history that can no longer be rewritten. Resign
// rebuilds every commit including the root, and git-bug requires exactly one
// root per entity, so a rewritten chain can never be reconciled with a published
// one. An attestation instead FAST-FORWARDS: it keeps the entry id, publishes
// through the ordinary push, and needs no new ref namespace — therefore no new
// Purge obligation.
//
// Unlike Resign there is no foreign-author refusal and no --force. Vouching for
// a peer's history is the case this exists to serve; it is recorded honestly as
// ClaimReceived rather than refused, and there is no refusal for a force flag to
// override.
func (s *Store) Attest(ids []entity.Id) ([]AttestResult, error) {
	// Distinguish "you have not turned signing on" from "kref cannot sign here at
	// all". Store.Signing() is false for both, and telling someone to configure a
	// key they already have sends them to fix the one thing that is not broken.
	if s.sigUnavailable != "" {
		return nil, errors.New("cannot attest: " + s.sigUnavailable)
	}
	if !s.Signing() {
		return nil, errors.New(
			"this repository is not configured to sign: set a signing key and turn on " +
				"commit.gpgsign (or kref.sign) before attesting")
	}
	// Every error path returns `out`, not nil: an earlier entry's attestation has
	// already landed by the time a later one fails, and discarding the results
	// would leave mutations on disk with no record of which happened.
	out := make([]AttestResult, 0, len(ids))
	for _, id := range ids {
		res, err := s.attestOne(id)
		if err != nil {
			return out, fmt.Errorf("attest %s: %w", id, err)
		}
		out = append(out, res)
	}
	return out, nil
}

func (s *Store) attestOne(id entity.Id) (AttestResult, error) {
	t, _, err := s.locate(id)
	if err != nil {
		return AttestResult{}, err
	}
	res := AttestResult{ID: id, Tier: t}

	// GATE ORDER IS THE ENFORCEMENT. 062c9d6 exists because a later gate cannot
	// undo an earlier one's omission. Do not reorder without a spec that fails.
	//
	// The gates read lock-free and s.mutate takes the write lock, so a
	// concurrent kref process can append to this entry in between — the same
	// shape resignOne has. The claim is therefore derived INSIDE the lock
	// below, where it is decided against the entry the write actually commits.
	// The signature gates keep the window: another process could turn a
	// not-good chain good between gate 2 and the append, in which case this
	// writes a redundant attestation. That is wasteful, not unsafe — it vouches
	// for history the attester's own key already covers.

	// 1. A held write is the reviewer's to accept or reject, not to vouch for.
	if entry.IsSystemTier(t) {
		res.Reason = fmt.Sprintf("held in the %s tier; approve or reject it instead", t)
		return res, nil
	}

	state, _, _, err := s.refSigState(entryRef(t, id))
	if err != nil {
		return res, err
	}

	// 2. Nothing to vouch for. Attesting anyway would add a commit and a claim
	//    that change no verdict.
	if state == entry.SigGood {
		res.Reason = "already has a good signature throughout; nothing to attest"
		return res, nil
	}

	// 3. A tampered commit. chainState refuses to absorb bad, so an attestation
	//    here would change nothing — while reading, to a human, as an endorsement
	//    of altered content.
	if state == entry.SigBad {
		res.Reason = "has a bad signature in its history; " +
			"attesting would vouch for altered content"
		return res, nil
	}

	// mutate takes the write lock, re-locates, and commits. withWriteLock is NOT
	// reentrant (see writelock.go), so this must not be wrapped in another one.
	//
	// The claim is DERIVED here rather than above so it is decided against the
	// same entry the append lands on: a peer's operation arriving between the
	// gates and the lock must read `received`, not `authored`.
	var claim entry.Claim
	if err := s.mutate(id, func(e *entry.Entry) error {
		claim = entry.ClaimAuthored
		if foreign := foreignAuthors(e.Authors(), s.author.Email()); len(foreign) > 0 {
			claim = entry.ClaimReceived
		}
		e.Append(entry.NewAttest(s.author, claim))
		return nil
	}); err != nil {
		return res, err
	}
	res.Claim = claim

	tip, err := s.git("rev-parse", entryRef(t, id))
	if err != nil {
		return res, err
	}
	res.Attested, res.NewTip = true, tip
	return res, nil
}
