package store

import (
	"fmt"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// locate finds an entry by id across every namespace a by-id lookup must search
// (user tiers + hidden system tiers like quarantine). Lock-free: callers that
// mutate hold the write lock themselves. It is the single place that names the
// by-id search set, so a new by-id method reaches quarantine by default. Returns
// the holding tier, the entry, or a tier-agnostic "entry <id> not found".
func (s *Store) locate(id entity.Id) (entry.Tier, *entry.Entry, error) {
	for _, t := range s.searchTierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return "", nil, fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		return t, e, nil
	}
	return "", nil, fmt.Errorf("entry %s not found", id)
}

// mutate locates id, applies apply, and commits — the write-locked wrapper for
// the uniform "read → append op → commit" by-id mutations.
func (s *Store) mutate(id entity.Id, apply func(*entry.Entry) error) error {
	return s.withWriteLock(func() error {
		_, e, err := s.locate(id)
		if err != nil {
			return err
		}
		if err := apply(e); err != nil {
			return err
		}
		return e.Commit(s.repo)
	})
}
