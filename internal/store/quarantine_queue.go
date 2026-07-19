package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

// QuarantineStaleAfter is how long a write may sit in the review queue before it
// is flagged stale — long enough that a same-session write is never "stale",
// short enough that a forgotten one is surfaced. A constant (no config) to match
// the no-remote nag's fixed cadence.
const QuarantineStaleAfter = 7 * 24 * time.Hour

// quarantineWarnKey is the local-config marker for the last time the "writes
// await review" reminder fired, throttled like the no-remote nag.
const quarantineWarnKey = "kref.warn.quarantine"

// QuarantineStale reports whether a pending item has awaited review at least
// QuarantineStaleAfter (from its CreatedAt to now).
func QuarantineStale(item QuarantineItem, now time.Time) bool {
	return now.Sub(item.CreatedAt) >= QuarantineStaleAfter
}

// WarnQuarantineDue reports whether the periodic "writes await review" reminder
// should fire: at least one pending item is stale (older than QuarantineStaleAfter)
// AND the last quarantine warning is at least interval old. Fresh-only and empty
// queues stay quiet.
func (s *Store) WarnQuarantineDue(now time.Time, interval time.Duration) (bool, error) {
	q, err := s.QuarantineQueue()
	if err != nil {
		return false, err
	}
	stale := false
	for _, it := range q {
		if QuarantineStale(it, now) {
			stale = true
			break
		}
	}
	if !stale {
		return false, nil
	}
	return s.warnDue(quarantineWarnKey, now, interval)
}

// MarkQuarantineWarned records now as the last time the quarantine reminder fired.
func (s *Store) MarkQuarantineWarned(now time.Time) error {
	return s.markWarned(quarantineWarnKey, now)
}

// QuarantineItem summarises one pending item in the review queue.
type QuarantineItem struct {
	ID          entity.Id
	HeldOp      bool           // true = a held operation; false = a new-entry draft
	OpKind      string         // held ops: set-body|add-comment|edit-comment|resolve
	Target      entity.Id      // held ops: the live entry the write targets
	TargetTitle string         // held ops: title of the target, if resolvable
	DestTier    string         // drafts: intended destination tier
	Kind        string         // drafts: entry kind
	Title       string         // drafts: entry title
	Findings    []scan.Finding // findings that parked the write (held ops carry them)
	CreatedAt   time.Time
}

// QuarantineQueue returns the pending review queue: every non-archived item in
// the quarantine tier (approve/reject either promote an item out or archive it),
// oldest first. It reads the DAG directly — the excerpt cache deliberately
// excludes the quarantine tier so a held secret never enters it.
func (s *Store) QuarantineQueue() ([]QuarantineItem, error) {
	snaps, err := s.List(ListFilter{Tier: entry.TierQuarantine})
	if err != nil {
		return nil, err
	}
	items := make([]QuarantineItem, 0, len(snaps))
	for _, snap := range snaps {
		items = append(items, s.classifyQueueItem(snap))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

// QuarantineDetail is the full review view of one quarantine item: its queue
// classification plus the proposed content (and, for a set-body op, the target's
// current body so a reviewer can diff) and the comment id an edit/resolve acts on.
type QuarantineDetail struct {
	Item            QuarantineItem
	ProposedContent string // the held body / comment text, or a draft's body
	CurrentBody     string // set-body: the target's current body (for a diff); else ""
	CommentTarget   string // edit-comment / resolve: the comment id acted on
}

// RejectedQuarantine lists the tombstoned rejected items (archived, labelled
// q-status:rejected), newest first — the audit/recovery view. Reject preserves
// items rather than purging, so a decision can be reconsidered until an explicit
// purge.
func (s *Store) RejectedQuarantine() ([]QuarantineItem, error) {
	snaps, err := s.List(ListFilter{
		Tier:         entry.TierQuarantine,
		ArchivedOnly: true,
		Labels:       []string{qStatusPrefix + "rejected"},
	})
	if err != nil {
		return nil, err
	}
	items := make([]QuarantineItem, 0, len(snaps))
	for _, snap := range snaps {
		items = append(items, s.classifyQueueItem(snap))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

// RecoverQuarantine returns a rejected item to the pending review queue:
// unarchive it and drop the q-status:rejected label so it can be reconsidered
// (approved or rejected afresh). The held write itself is untouched.
func (s *Store) RecoverQuarantine(id entity.Id) error {
	if err := s.Unarchive(id); err != nil {
		return err
	}
	return s.RemoveLabel(id, qStatusPrefix+"rejected")
}

// QuarantineDetail assembles the review view for one quarantine item id.
func (s *Store) QuarantineDetail(id entity.Id) (QuarantineDetail, error) {
	snap, err := s.Get(id)
	if err != nil {
		return QuarantineDetail{}, err
	}
	d := QuarantineDetail{Item: s.classifyQueueItem(snap)}
	if !d.Item.HeldOp {
		d.ProposedContent = snap.Body // a draft's body is the new entry's content
		return d, nil
	}
	var in intent
	if err := json.Unmarshal([]byte(snap.Body), &in); err != nil {
		return QuarantineDetail{}, fmt.Errorf("parse quarantine intent %s: %w", id, err)
	}
	d.ProposedContent = in.Content
	d.CommentTarget = in.CommentTarget
	if in.OpKind == "set-body" {
		if tsnap, terr := s.Get(d.Item.Target); terr == nil {
			d.CurrentBody = tsnap.Body
		}
	}
	return d, nil
}

// classifyQueueItem turns a quarantine-tier snapshot into a queue summary. A
// held op carries its intent JSON (op-kind, target, findings); a draft carries
// its kind/title and a q-dest label.
func (s *Store) classifyQueueItem(snap *entry.Snapshot) QuarantineItem {
	it := QuarantineItem{ID: snap.ID, CreatedAt: snap.CreatedAt}
	if isHeldOp(snap) {
		it.HeldOp = true
		var in intent
		if json.Unmarshal([]byte(snap.Body), &in) == nil {
			it.OpKind = in.OpKind
			it.Target = entity.Id(in.TargetID)
			it.Findings = in.Findings
			if tsnap, terr := s.Get(it.Target); terr == nil {
				it.TargetTitle = tsnap.Title
			}
		}
		return it
	}
	it.Kind = snap.Kind
	it.Title = snap.Title
	it.DestTier = destTier(snap)
	return it
}
