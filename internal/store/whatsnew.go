package store

import (
	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// WhatsNew returns the entries that arrived in the last pull (incoming) and the
// entries changed since the last push (unpushed), across syncable tiers.
func (s *Store) WhatsNew() (incoming, unpushed []*entry.Snapshot, err error) {
	tiers, err := s.SyncableTiers()
	if err != nil {
		return nil, nil, err
	}
	incoming = []*entry.Snapshot{}
	unpushed = []*entry.Snapshot{}
	for _, t := range tiers {
		for hex := range s.incomingEntries(t) {
			snap, err := s.Get(entity.Id(hex))
			if err != nil {
				continue // entry may have been purged since the pull
			}
			incoming = append(incoming, snap)
		}
		delta, err := s.pushDelta(t)
		if err != nil {
			return nil, nil, err
		}
		for _, id := range delta {
			snap, err := s.Get(id)
			if err != nil {
				continue
			}
			unpushed = append(unpushed, snap)
		}
	}
	return incoming, unpushed, nil
}

// SincePull returns the entry's operations added after the last pull (the ops
// beyond the recorded post-pull op-count). The bool is false when the entry has
// no pull baseline (it was not part of any recorded pull), in which case the
// full log is returned.
func (s *Store) SincePull(id entity.Id) ([]entry.LogEntry, bool, error) {
	log, err := s.Log(id)
	if err != nil {
		return nil, false, err
	}
	tiers, err := s.SyncableTiers()
	if err != nil {
		return nil, false, err
	}
	for _, t := range tiers {
		if count, ok := s.incomingEntries(t)[id.String()]; ok {
			if count > len(log) {
				count = len(log)
			}
			return log[count:], true, nil
		}
	}
	return log, false, nil
}
