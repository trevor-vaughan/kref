package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

// quarantineKind is the entry kind of a held-op intent-item.
const quarantineKind = "quarantine"

// intentSchema versions the intent-item JSON body so a later reader can migrate.
const intentSchema = 2

// intent is the JSON body of a held-op quarantine item: enough for sub-project B
// to replay the intended write faithfully through the normal store write path.
type intent struct {
	Schema        int            `json:"schema"`
	OpKind        string         `json:"op_kind"` // "set-body" | "add-comment" | "edit-comment" | "resolve"
	TargetID      string         `json:"target_id"`
	Content       string         `json:"content"`
	Question      bool           `json:"question,omitempty"`
	ReplyTo       string         `json:"reply_to,omitempty"`
	CommentTarget string         `json:"comment_target,omitempty"` // edit-comment / resolve: comment id acted on
	BaseVersion   int            `json:"base_version,omitempty"`   // set-body CAS base captured at park time
	Findings      []scan.Finding `json:"findings"`
	ActorKind     string         `json:"actor_kind"`
}

// Parked is the result of parking a flagged write: the quarantine item's id and
// the findings that diverted it (rule + line, never the secret value).
type Parked struct {
	ItemID   entity.Id
	Findings []scan.Finding
}

// QuarantineNewEntry parks a brand-new entry as a draft in the quarantine tier,
// labelled with its intended destination and carrying its review thread.
// Approval (sub-project B) retiers the draft to destTier — the id and thread
// travel with it.
func (s *Store) QuarantineNewEntry(destTier entry.Tier, kind, title, body, contentType string, findings []scan.Finding, actorKind string) (Parked, error) {
	id, err := s.AddWithContentType(entry.TierQuarantine, kind, title, body, contentType)
	if err != nil {
		return Parked{}, err
	}
	if err := s.AddLabel(id, "q-dest:"+string(destTier)); err != nil {
		return Parked{}, err
	}
	cid, err := s.openReview(id, id, findings, actorKind)
	if err != nil {
		return Parked{}, err
	}
	if err := s.AddLabel(id, "q-review:"+cid); err != nil {
		return Parked{}, err
	}
	return Parked{ItemID: id, Findings: findings}, nil
}

// QuarantineUpdate parks a held body replacement for an existing entry as an
// intent-item. The live target is untouched; its review thread is opened.
// baseVersion is the target's head version the writer based the change on; it is
// captured now so approval can reject a stale replay (compare-and-swap).
func (s *Store) QuarantineUpdate(target entity.Id, newBody string, baseVersion int, findings []scan.Finding, actorKind string) (Parked, error) {
	return s.parkOp(target, intent{
		Schema: intentSchema, OpKind: "set-body", TargetID: target.String(),
		Content: newBody, BaseVersion: baseVersion, Findings: findings, ActorKind: actorKind,
	})
}

// QuarantineComment parks a held comment on an existing entry as an intent-item.
func (s *Store) QuarantineComment(target entity.Id, body string, question bool, replyTo string, findings []scan.Finding, actorKind string) (Parked, error) {
	return s.parkOp(target, intent{
		Schema: intentSchema, OpKind: "add-comment", TargetID: target.String(),
		Content: body, Question: question, ReplyTo: replyTo, Findings: findings, ActorKind: actorKind,
	})
}

// QuarantineEditComment parks a held edit of an existing comment as an
// intent-item; approval replays the edit onto commentTarget.
func (s *Store) QuarantineEditComment(target entity.Id, commentTarget, newBody string, findings []scan.Finding, actorKind string) (Parked, error) {
	return s.parkOp(target, intent{
		Schema: intentSchema, OpKind: "edit-comment", TargetID: target.String(),
		Content: newBody, CommentTarget: commentTarget, Findings: findings, ActorKind: actorKind,
	})
}

// QuarantineResolveNote parks a held resolve-with-note as an intent-item;
// approval posts the note and resolves commentTarget.
func (s *Store) QuarantineResolveNote(target entity.Id, commentTarget, note string, findings []scan.Finding, actorKind string) (Parked, error) {
	return s.parkOp(target, intent{
		Schema: intentSchema, OpKind: "resolve", TargetID: target.String(),
		Content: note, CommentTarget: commentTarget, Findings: findings, ActorKind: actorKind,
	})
}

func (s *Store) parkOp(target entity.Id, in intent) (Parked, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return Parked{}, fmt.Errorf("marshal quarantine intent: %w", err)
	}
	title := fmt.Sprintf("held %s for %s", in.OpKind, in.TargetID)
	id, err := s.AddWithContentType(entry.TierQuarantine, quarantineKind, title, string(payload), "application/json")
	if err != nil {
		return Parked{}, err
	}
	cid, err := s.openReview(target, id, in.Findings, in.ActorKind)
	if err != nil {
		return Parked{}, err
	}
	if err := s.AddLabel(id, "q-review:"+cid); err != nil {
		return Parked{}, err
	}
	return Parked{ItemID: id, Findings: in.Findings}, nil
}

// findingsText renders the review question-comment body: it names the findings
// (rule and line, never the secret value) so a human can decide.
func findingsText(findings []scan.Finding) string {
	b := &strings.Builder{}
	b.WriteString("A write to this entry was quarantined: it tripped the secret scanner and needs human review before it can be applied.\n")
	for _, f := range findings {
		fmt.Fprintf(b, "  line %d: %s: %s\n", f.StartLine, f.RuleID, f.Description)
	}
	return b.String()
}

// openReview posts the in-context review question-comment on reviewTarget (the
// live entry for a held op, or the draft itself for a new entry), naming the
// findings and the quarantine item id. It returns the new comment's id so the
// caller can record it as a q-review label for approval/rejection to resolve.
func (s *Store) openReview(reviewTarget, itemID entity.Id, findings []scan.Finding, actorKind string) (string, error) {
	body := findingsText(findings) + fmt.Sprintf("\nreview: kref quarantine show %s", itemID)
	return s.AddComment(reviewTarget, actorKind, body, true, "")
}
