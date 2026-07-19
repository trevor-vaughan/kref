package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/todoguard"
)

const (
	qDestPrefix   = "q-dest:"
	qReviewPrefix = "q-review:"
	qStatusPrefix = "q-status:"
)

// reviewCommentID returns the review question-comment id recorded on a
// quarantine item (the q-review:<cid> label), or "" if none.
func reviewCommentID(item *entry.Snapshot) string { return labelValue(item, qReviewPrefix) }

// destTier returns the intended destination tier of a new-entry draft
// (the q-dest:<tier> label), or "" if the item is not a draft.
func destTier(item *entry.Snapshot) string { return labelValue(item, qDestPrefix) }

func labelValue(item *entry.Snapshot, prefix string) string {
	for _, l := range item.Labels {
		if v, ok := strings.CutPrefix(l, prefix); ok {
			return v
		}
	}
	return ""
}

// isHeldOp reports whether the item is a held-op intent-item (vs a new-entry
// draft): a held op carries the reserved quarantine kind, a draft any other.
func isHeldOp(item *entry.Snapshot) bool { return item.Kind == quarantineKind }

// resolveThread resolves the review question-comment on target, posting an
// optional decision note first. A blank review id is tolerated (nothing to
// resolve); a "not found" on resolve is tolerated too — a rejected draft is
// tombstoned in the quarantine tier, which ResolveComment does not search, so a
// decision is never blocked by an unresolvable thread on an already-dead item.
func (s *Store) resolveThread(target entity.Id, reviewCID, note, actorKind string) error {
	if reviewCID == "" {
		return nil
	}
	if strings.TrimSpace(note) != "" {
		if _, err := s.AddComment(target, actorKind, note, false, reviewCID); err != nil {
			return err
		}
	}
	err := s.ResolveComment(target, reviewCID)
	if err != nil && strings.Contains(err.Error(), "not found") {
		return nil
	}
	return err
}

// ApproveQuarantine applies a parked write. For a new-entry draft it retiers the
// draft to its intended tier; for a held op it replays the op onto the live
// target through the normal write path (so it inherits flock, CAS, and DAG
// merge exactly like a fresh write). Either way the review thread is resolved
// with the optional note. Approving a flagged draft destined for a shared-typed
// tier re-runs the promote scan and returns *RetierBlockedError if the finding
// is not allowlisted — an un-allowlisted secret is never promoted to shared.
func (s *Store) ApproveQuarantine(id entity.Id, note, approver, actorKind string) error {
	item, err := s.Get(id)
	if err != nil {
		return err
	}
	if isHeldOp(item) {
		return s.approveHeldOp(item, note, actorKind)
	}
	dest := destTier(item)
	if dest == "" {
		return fmt.Errorf("quarantine item %s is neither a held op nor a draft (no %s label)", id, qDestPrefix)
	}
	reviewCID := reviewCommentID(item)
	if err := s.Retier(id, entry.Tier(dest), approver, actorKind); err != nil {
		return err // includes *RetierBlockedError for an un-allowlisted shared promotion
	}
	// The draft is now the live entry; resolve its review thread, clear q-labels.
	if err := s.resolveThread(id, reviewCID, note, actorKind); err != nil {
		return err
	}
	return s.clearQuarantineLabels(id, item)
}

func (s *Store) clearQuarantineLabels(id entity.Id, item *entry.Snapshot) error {
	for _, l := range item.Labels {
		if strings.HasPrefix(l, qDestPrefix) || strings.HasPrefix(l, qReviewPrefix) {
			if err := s.RemoveLabel(id, l); err != nil {
				return err
			}
		}
	}
	return nil
}

// approveHeldOp replays a held op onto its live target, resolves the review
// thread, then tombstones the item (archive + q-status:approved) as an audit
// record of the decision.
func (s *Store) approveHeldOp(item *entry.Snapshot, note, actorKind string) error {
	var in intent
	if err := json.Unmarshal([]byte(item.Body), &in); err != nil {
		return fmt.Errorf("parse quarantine intent %s: %w", item.ID, err)
	}
	if in.Schema != intentSchema {
		return fmt.Errorf("quarantine intent %s has unsupported schema %d (want %d)", item.ID, in.Schema, intentSchema)
	}
	target := entity.Id(in.TargetID)
	if err := s.replayIntent(target, in); err != nil {
		return err // includes *todoguard.StaleError for a moved todo
	}
	if err := s.resolveThread(target, reviewCommentID(item), note, actorKind); err != nil {
		return err
	}
	if err := s.Archive(item.ID); err != nil {
		return err
	}
	return s.AddLabel(item.ID, qStatusPrefix+"approved")
}

// replayIntent applies a held op through the normal store write path so it
// inherits flock, CAS, and DAG merge exactly like a fresh write.
func (s *Store) replayIntent(target entity.Id, in intent) error {
	switch in.OpKind {
	case "set-body":
		snap, err := s.Get(target)
		if err != nil {
			return err
		}
		if cerr := todoguard.CheckVersion(snap.Kind, in.BaseVersion, snap.Version); cerr != nil {
			return cerr // stale: the target moved under the parked write; re-review
		}
		return s.Update(target, in.Content, "")
	case "add-comment":
		_, err := s.AddComment(target, in.ActorKind, in.Content, in.Question, in.ReplyTo)
		return err
	case "edit-comment":
		return s.EditComment(target, in.CommentTarget, in.Content)
	case "resolve":
		if strings.TrimSpace(in.Content) != "" {
			if _, err := s.AddComment(target, in.ActorKind, in.Content, false, in.CommentTarget); err != nil {
				return err
			}
		}
		return s.ResolveComment(target, in.CommentTarget)
	default:
		return fmt.Errorf("unknown quarantine op kind %q", in.OpKind)
	}
}

// PurgeRejectedQuarantine hard-deletes a rejected item — removing its ref,
// pruning the objects (excising the held write, which may be a real secret), and
// deleting its recovery file(s). Only a rejected item (q-status:rejected) can be
// purged here, so a pending review is never lost. Irreversible.
func (s *Store) PurgeRejectedQuarantine(id entity.Id) error {
	item, err := s.Get(id)
	if err != nil {
		return err
	}
	if labelValue(item, qStatusPrefix) != "rejected" {
		return fmt.Errorf("quarantine item %s is not rejected — only rejected items can be purged here", id)
	}
	if err := s.Purge(id, true, false); err != nil { // gc=prune (excise), push=false (non-syncable)
		return err
	}
	return todoguard.RemoveRejected(id.String())
}

// RejectQuarantine discards a parked write without touching the live target: it
// preserves the proposed content to the recovery dir (so the author's work is
// never lost), resolves the review thread as rejected, and tombstones the item
// (archive + q-status:rejected). It returns the recovery file path. The item is
// preserved for audit, not purged.
func (s *Store) RejectQuarantine(id entity.Id, note, actorKind string) (string, error) {
	item, err := s.Get(id)
	if err != nil {
		return "", err
	}
	content, reviewTarget := item.Body, id
	if isHeldOp(item) {
		var in intent
		if err := json.Unmarshal([]byte(item.Body), &in); err != nil {
			return "", fmt.Errorf("parse quarantine intent %s: %w", id, err)
		}
		content = in.Content
		reviewTarget = entity.Id(in.TargetID)
	}
	path, err := todoguard.WriteRejected(id.String(), content)
	if err != nil {
		return "", err
	}
	if err := s.resolveThread(reviewTarget, reviewCommentID(item), note, actorKind); err != nil {
		return "", err
	}
	if err := s.Archive(id); err != nil {
		return "", err
	}
	if err := s.AddLabel(id, qStatusPrefix+"rejected"); err != nil {
		return "", err
	}
	return path, nil
}
