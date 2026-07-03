package store

import (
	"fmt"
	"sort"

	"github.com/git-bug/git-bug/entity"

	"github.com/riotbox/kref/internal/entry"
)

// AddLink appends a typed link from an entry to another, searching all tiers.
func (s *Store) AddLink(id entity.Id, to, linkType string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewAddLink(s.author, to, linkType))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// RemoveLink removes every link to `to` on an entry, searching all tiers.
func (s *Store) RemoveLink(id entity.Id, to string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewRemoveLink(s.author, to))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// LinkWouldLeak reports whether a link from->to stores a more-private id on a
// more-public (syncable) source — true when the target sits in a tier below the
// source. The caller warns and proceeds (consistent with retier's warn-not-
// refuse philosophy); it does not block the link.
func (s *Store) LinkWouldLeak(from, to entity.Id) (bool, error) {
	fromSnap, err := s.Get(from)
	if err != nil {
		return false, err
	}
	toSnap, err := s.Get(to)
	if err != nil {
		return false, err
	}
	return tierRank(s.TierType(entry.Tier(toSnap.Tier))) < tierRank(s.TierType(entry.Tier(fromSnap.Tier))), nil
}

// Supersede records that newID supersedes oldID: a "supersedes" link from the
// new entry to the old one, and the old entry marked superseded. This is the
// named consolidation capability (the CLI and, later, the MCP adapter call it),
// so the directional convention lives here, not in the adapters.
func (s *Store) Supersede(oldID, newID entity.Id) error {
	if oldID == newID {
		return fmt.Errorf("cannot supersede an entry with itself")
	}
	if err := s.AddLink(newID, oldID.String(), "supersedes"); err != nil {
		return err
	}
	return s.SetStatus(oldID, "superseded")
}

// Links returns an entry's outgoing and incoming typed edges. Outgoing edges
// come from the entry's own snapshot; incoming edges are found by scanning every
// live entry for links targeting id. Titles are resolved where the other end is
// a live (non-deleted) entry.
func (s *Store) Links(id entity.Id) (entry.LinkView, error) {
	all, err := s.List(ListFilter{})
	if err != nil {
		return entry.LinkView{}, err
	}
	byID := make(map[string]*entry.Snapshot, len(all))
	var self *entry.Snapshot
	for _, snap := range all {
		byID[snap.ID.String()] = snap
		if snap.ID == id {
			self = snap
		}
	}
	if self == nil {
		if self, err = s.Get(id); err != nil {
			return entry.LinkView{}, err
		}
	}
	// Initialize as empty (non-nil) slices so the JSON shape is [] not null,
	// consistent with the empty-slice convention established in slice 4.
	view := entry.LinkView{Outgoing: []entry.LinkRef{}, Incoming: []entry.LinkRef{}}
	for _, l := range self.Links {
		ref := entry.LinkRef{ID: entity.Id(l.To), Type: l.Type}
		if snap, ok := byID[l.To]; ok {
			ref.Title = snap.Title
		}
		view.Outgoing = append(view.Outgoing, ref)
	}
	for _, snap := range all {
		if snap.ID == id {
			continue
		}
		for _, l := range snap.Links {
			if l.To == id.String() {
				view.Incoming = append(view.Incoming, entry.LinkRef{ID: snap.ID, Type: l.Type, Title: snap.Title})
			}
		}
	}
	return view, nil
}

// Tidy compiles the read-only consolidation review: live duplicate-title groups,
// diverged (concurrently-merged) entries, and superseded chains. Duplicate
// detection is over live (non-superseded, non-deleted) entries so a superseded
// predecessor is not reported as a duplicate of its successor. It performs no
// mutations. The report's slices are non-nil so they serialize as [] not null.
func (s *Store) Tidy() (entry.TidyReport, error) {
	all, err := s.List(ListFilter{})
	if err != nil {
		return entry.TidyReport{}, err
	}
	report := entry.TidyReport{
		Duplicates: []entry.DuplicateGroup{},
		Diverged:   []entry.TidyEntry{},
		Superseded: []entry.TidyEntry{},
	}
	groups := map[string][]entry.TidyEntry{}
	var order []string
	for _, snap := range all {
		te := entry.TidyEntry{ID: snap.ID, Title: snap.Title, Tier: snap.Tier, Status: snap.Status}
		if snap.Status == "superseded" {
			report.Superseded = append(report.Superseded, te)
			continue
		}
		key := entry.NormalizeTitle(snap.Title)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], te)
	}
	for _, key := range order {
		if g := groups[key]; len(g) > 1 {
			report.Duplicates = append(report.Duplicates, entry.DuplicateGroup{NormalizedTitle: key, Entries: g})
		}
	}
	for _, snap := range all {
		if snap.Status == "superseded" {
			continue
		}
		merged, err := s.Merged(snap.ID)
		if err != nil {
			return entry.TidyReport{}, err
		}
		if merged {
			report.Diverged = append(report.Diverged, entry.TidyEntry{
				ID: snap.ID, Title: snap.Title, Tier: snap.Tier, Status: snap.Status,
			})
		}
	}
	return report, nil
}

// Tree builds the parent-child descendant tree rooted at id. A "parent-child"
// link from X to Y means Y is X's parent, so the tree descends into entries that
// name a node as their parent. Cycles are guarded by a visited set. Children are
// non-nil (empty slice) so leaves serialize as [] rather than null.
func (s *Store) Tree(id entity.Id) (*entry.TreeNode, error) {
	all, err := s.List(ListFilter{})
	if err != nil {
		return nil, err
	}
	title := map[string]string{}
	children := map[string][]string{} // parent id -> child ids
	found := false
	for _, snap := range all {
		title[snap.ID.String()] = snap.Title
		if snap.ID == id {
			found = true
		}
		for _, l := range snap.Links {
			if l.Type == "parent-child" {
				children[l.To] = append(children[l.To], snap.ID.String())
			}
		}
	}
	if !found {
		snap, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		title[id.String()] = snap.Title
	}
	visited := map[string]bool{}
	var build func(idStr string) *entry.TreeNode
	build = func(idStr string) *entry.TreeNode {
		node := &entry.TreeNode{ID: entity.Id(idStr), Title: title[idStr], Children: []*entry.TreeNode{}}
		if visited[idStr] {
			return node // cycle guard: stop descending
		}
		visited[idStr] = true
		kids := children[idStr]
		sort.Strings(kids)
		for _, c := range kids {
			node.Children = append(node.Children, build(c))
		}
		return node
	}
	return build(id.String()), nil
}
