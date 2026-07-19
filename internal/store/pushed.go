package store

import (
	"fmt"
	"strings"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/entity/dag"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

// pushedRefPrefix namespaces the local mirror refs recording what has been
// pushed. They are bookkeeping only: dag.Push transfers a tier's own namespace
// (kref-<tier>/*), never kref-pushed/*, so these never leave the machine.
const pushedRefPrefix = "refs/kref-pushed/"

func entryRef(t entry.Tier, id entity.Id) string {
	return "refs/" + t.Namespace() + "/" + id.String()
}

func pushedRef(t entry.Tier, id entity.Id) string {
	return pushedRefPrefix + t.Namespace() + "/" + id.String()
}

// pushDelta returns the ids in tier t whose current tip differs from their last
// pushed tip (new or changed entries) — exactly the content about to leave.
func (s *Store) pushDelta(t entry.Tier) ([]entity.Id, error) {
	ids, err := dag.ListLocalIds(entry.Definition(t), s.repo)
	if err != nil {
		return nil, err
	}
	var delta []entity.Id
	for _, id := range ids {
		cur, err := s.repo.ResolveRef(entryRef(t, id))
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", id, err)
		}
		mirror := pushedRef(t, id)
		exist, err := s.repo.RefExist(mirror)
		if err != nil {
			return nil, err
		}
		if exist {
			prev, err := s.repo.ResolveRef(mirror)
			if err != nil {
				return nil, err
			}
			if prev == cur {
				continue
			}
		}
		delta = append(delta, id)
	}
	return delta, nil
}

// pushOffender is one entry whose history carries a secret that would be pushed.
type pushOffender struct {
	ID    entity.Id
	Title string
	Rule  string
	Desc  string
}

// scanForPush runs betterleaks over every body version AND every comment body of
// each given entry (the DAG ships full history, so a secret edited away — or in a
// deleted comment — still leaves). It returns the offending entries; it never
// returns or logs the secret value.
func (s *Store) scanForPush(ids []entity.Id) ([]pushOffender, error) {
	var offenders []pushOffender
	for _, id := range ids {
		versions, err := s.BodyVersions(id)
		if err != nil {
			return nil, err
		}
		var buf []byte
		for _, v := range versions {
			buf = append(buf, v.Body...)
			buf = append(buf, '\n')
		}
		// Comment bodies push too, and are not covered by BodyVersions — scan the
		// full comment op-history so a secret in a deleted or edited-away comment
		// is still caught before it leaves the machine.
		comments, err := s.commentBodies(id)
		if err != nil {
			return nil, err
		}
		for _, cb := range comments {
			buf = append(buf, cb...)
			buf = append(buf, '\n')
		}
		if len(buf) == 0 {
			continue
		}
		findings, err := scan.Scan(buf)
		if err != nil {
			return nil, err
		}
		if len(findings) > 0 {
			title := ""
			if snap, err := s.Get(id); err == nil {
				title = snap.Title
			}
			offenders = append(offenders, pushOffender{
				ID: id, Title: title, Rule: findings[0].RuleID, Desc: findings[0].Description,
			})
		}
	}
	return offenders, nil
}

// PushBlockedError reports secrets found in the delta about to be pushed. Its
// message is a remediation runbook; it never contains the secret value.
type PushBlockedError struct {
	Tier      entry.Tier
	Remote    string
	Offenders []pushOffender
}

func (e *PushBlockedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "push blocked: a secret would be pushed to %q (tier %s).\n", e.Remote, e.Tier)
	b.WriteString("Offending entries (the secret value is not shown):\n")
	for _, o := range e.Offenders {
		fmt.Fprintf(&b, "  - %s (%q): %s — %s\n", o.ID.String(), o.Title, o.Rule, o.Desc)
	}
	b.WriteString("History is immutable, so editing the body is not enough. For each entry:\n")
	b.WriteString("  1. Rotate the exposed secret now — assume it is already compromised.\n")
	b.WriteString("  2. kref purge <id> --gc     # hard-delete the entry and its history locally\n")
	b.WriteString("  3. recreate the entry with a clean body\n")
	fmt.Fprintf(&b, "  4. kref sync push --tier %s\n", e.Tier)
	b.WriteString("Nothing left your machine — the push was aborted before contacting the remote.")
	return b.String()
}

// recordPushed mirrors each just-pushed entry's tip to its kref-pushed ref so the
// next push scans only the new delta. Only the pushed (delta) ids need updating;
// unchanged entries already have a matching mirror.
func (s *Store) recordPushed(t entry.Tier, ids []entity.Id) error {
	for _, id := range ids {
		cur, err := s.repo.ResolveRef(entryRef(t, id))
		if err != nil {
			return fmt.Errorf("resolve %s: %w", id, err)
		}
		if err := s.repo.UpdateRef(pushedRef(t, id), cur); err != nil {
			return fmt.Errorf("record pushed-state for %s: %w", id, err)
		}
	}
	return nil
}
