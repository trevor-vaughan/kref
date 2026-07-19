package store

import (
	"fmt"
	"strings"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/entity/dag"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// Retier moves an entry to a new visibility tier, preserving its id. It reads the
// entry's ops from its current tier, rebuilds the entity in the target tier (same
// ops + a retier provenance event), commits, then removes the old ref. Op hashes
// exclude the Lamport clock, so the id is stable; the target tier re-clocks via
// the normal write path. Promoting to shared rescans for secrets, fail-closed.
func (s *Store) Retier(id entity.Id, target entry.Tier, actor, actorKind string) error {
	return s.withWriteLock(func() error {
		if _, err := s.DeclaredTier(string(target)); err != nil {
			return err
		}
		cur, e, err := s.locate(id)
		if err != nil {
			return err
		}
		if cur == target {
			return nil // already in the target tier
		}
		// Promote-to-shared-TYPED secret gate (security; unskippable by callers).
		if s.TierType(target) == entry.TierShared {
			offenders, err := s.scanForPush([]entity.Id{id})
			if err != nil {
				return err
			}
			if len(offenders) > 0 {
				return &RetierBlockedError{Offenders: offenders}
			}
		}
		// Rebuild in the target tier: same ops (id preserved) + a retier event.
		nw := entry.New(target)
		for _, op := range e.Operations() {
			nw.Append(op)
		}
		nw.Append(entry.NewRecordOrigin(s.author, actor, actorKind, "", "retier"))
		if err := nw.Commit(s.repo); err != nil {
			return fmt.Errorf("commit %s in tier %s: %w", id, target, err)
		}
		if err := dag.Remove(entry.Definition(cur), s.repo, id); err != nil {
			return fmt.Errorf("entry %s is now in both %s and %s; rerun retier to finish (remove old: %w)", id, cur, target, err)
		}
		return nil
	})
}

func tierRank(t entry.Tier) int {
	switch t {
	case entry.TierPrivate:
		return 0
	case entry.TierPersonal:
		return 1
	case entry.TierShared:
		return 2
	default:
		return 3
	}
}

// CrossTierLinks returns the entry's outgoing links whose targets sit in a tier
// below target — references teammates won't see after a promotion to target.
func (s *Store) CrossTierLinks(id entity.Id, target entry.Tier) ([]entry.LinkRef, error) {
	snap, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	var dangling []entry.LinkRef
	for _, l := range snap.Links {
		tsnap, err := s.Get(entity.Id(l.To))
		if err != nil {
			continue // target not found (deleted/unknown) — skip
		}
		if tierRank(s.TierType(entry.Tier(tsnap.Tier))) < tierRank(s.TierType(target)) {
			dangling = append(dangling, entry.LinkRef{ID: entity.Id(l.To), Type: l.Type, Title: tsnap.Title})
		}
	}
	return dangling, nil
}

// WasPushed reports whether the entry was already pushed from any pushable tier
// (a 6a kref-pushed mirror ref exists).
func (s *Store) WasPushed(id entity.Id) (bool, error) {
	for _, d := range s.tiers {
		if d.Type == entry.TierPrivate {
			continue
		}
		exist, err := s.repo.RefExist(pushedRef(d.Name, id))
		if err != nil {
			return false, err
		}
		if exist {
			return true, nil
		}
	}
	return false, nil
}

// RetierBlockedError reports that a retier to a shared-typed tier was blocked
// by a secret.
// Its message never contains the secret value.
type RetierBlockedError struct {
	Offenders []pushOffender
}

func (e *RetierBlockedError) Error() string {
	var b strings.Builder
	b.WriteString("retier blocked: the entry carries a secret and cannot move to a shared-typed tier.\n")
	b.WriteString("Offending entries (the secret value is not shown):\n")
	for _, o := range e.Offenders {
		fmt.Fprintf(&b, "  - %s (%q): %s — %s\n", o.ID.String(), o.Title, o.Rule, o.Desc)
	}
	b.WriteString("Rotate the secret, `kref purge <id> --gc`, recreate the entry clean, then retier.")
	return b.String()
}
