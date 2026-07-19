package store

import (
	"sort"
	"sync"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/entity/dag"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// excerptCache owns the per-tier lean-read caches. It holds a back-reference to
// the Store for DAG reads and enrichment; all mutation of loaded state is under
// mu. Reads never rebuild; only ensureFresh/rebuild write.
type excerptCache struct {
	s      *Store
	mu     sync.Mutex
	loaded map[entry.Tier]*diskCache
}

func newExcerptCache(s *Store) *excerptCache {
	return &excerptCache{s: s, loaded: map[entry.Tier]*diskCache{}}
}

// ensureFresh returns an up-to-date cache for a tier, refreshing only the
// entries whose ref OID changed since the last snapshot. Missing/corrupt cache
// triggers a full rebuild. This is the foreground read path (may write).
func (c *excerptCache) ensureFresh(t entry.Tier) (*diskCache, error) {
	c.mu.Lock()
	dc := c.loaded[t]
	c.mu.Unlock()

	if dc == nil {
		loaded, err := loadDiskCache(c.s.repo.LocalStorage(), t)
		if err != nil {
			return c.rebuild(t) // missing/corrupt/version mismatch
		}
		dc = loaded
	}

	cur, err := buildRefMap(c.s.repo, t)
	if err != nil {
		return nil, err
	}
	changed, removed := refDiff(dc.Refs, cur)
	if len(changed) == 0 && len(removed) == 0 {
		c.mu.Lock()
		c.loaded[t] = dc
		c.mu.Unlock()
		return dc, nil
	}

	for _, id := range removed {
		delete(dc.Excerpts, id)
	}
	for _, id := range changed {
		e, err := entry.Read(c.s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				delete(dc.Excerpts, id)
				continue
			}
			return nil, err
		}
		snap := c.s.compileSnapshot(t, e)
		dc.Excerpts[snap.ID] = toExcerpt(snap)
	}
	dc.Refs = cur
	if err := saveDiskCache(c.s.repo.LocalStorage(), t, dc); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.loaded[t] = dc
	c.mu.Unlock()
	return dc, nil
}

// matches applies the same predicate semantics as store.List, minus Search
// (body text is not cached; Search callers use the DAG path).
func matches(e Excerpt, f ListFilter) bool {
	if e.Deleted && !f.IncludeDelete {
		return false
	}
	if f.ArchivedOnly && !e.Archived {
		return false
	}
	if e.Archived && !f.ArchivedOnly && !f.IncludeArchived {
		return false
	}
	if f.Kind != "" && e.Kind != f.Kind {
		return false
	}
	if f.Status != "" && e.Status != f.Status {
		return false
	}
	if len(f.Labels) > 0 {
		have := make(map[string]bool, len(e.Labels))
		for _, l := range e.Labels {
			have[l] = true
		}
		for _, want := range f.Labels {
			if !have[want] {
				return false
			}
		}
	}
	return true
}

// listExcerpts ensures every relevant tier is fresh, then returns the filtered
// excerpts. Tier filter narrows which tiers are read. Results are ordered by ID
// so the emission order is deterministic: dc.Excerpts is a map, and the list
// renderer's stable sort would otherwise leak Go's randomized map iteration into
// tie-broken rows.
func (c *excerptCache) listExcerpts(f ListFilter) ([]Excerpt, error) {
	var out []Excerpt
	for _, t := range c.s.TierNames() {
		if f.Tier != "" && f.Tier != t {
			continue
		}
		dc, err := c.ensureFresh(t)
		if err != nil {
			return nil, err
		}
		for _, e := range dc.Excerpts {
			if matches(e, f) {
				out = append(out, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, nil
}

// readCached returns the tier's excerpts only if the on-disk/in-memory cache is
// present AND its ref map matches the live refs. It never rebuilds or refreshes.
// ok=false means the caller should fall back to the DAG. This is the completion
// path: reads are lock-free and never slower than the current DAG behavior.
func (c *excerptCache) readCached(t entry.Tier) (dc *diskCache, ok bool, err error) {
	c.mu.Lock()
	dc = c.loaded[t]
	c.mu.Unlock()
	if dc == nil {
		loaded, lerr := loadDiskCache(c.s.repo.LocalStorage(), t)
		if lerr != nil {
			return nil, false, nil //nolint:nilerr // missing/corrupt -> fall back, no error
		}
		dc = loaded
	}
	cur, err := buildRefMap(c.s.repo, t)
	if err != nil {
		return nil, false, nil //nolint:nilerr // ref listing failed -> fall back
	}
	changed, removed := refDiff(dc.Refs, cur)
	if len(changed) > 0 || len(removed) > 0 {
		return nil, false, nil // stale -> fall back
	}
	c.mu.Lock()
	c.loaded[t] = dc
	c.mu.Unlock()
	return dc, true, nil
}

// refreshAll brings every tier's excerpt cache up to date, guarding each tier
// with its build lock so concurrent refreshers don't stampede.
func (c *excerptCache) refreshAll() error {
	for _, t := range c.s.TierNames() {
		ok, err := acquireBuildLock(c.s.repo.LocalStorage(), t)
		if err != nil {
			return err
		}
		if !ok {
			continue // another refresh is handling this tier
		}
		_, ferr := c.ensureFresh(t)
		_ = releaseBuildLock(c.s.repo.LocalStorage(), t)
		if ferr != nil {
			return ferr
		}
	}
	return nil
}

// rebuild does a full DAG read of a tier, builds excerpts + ref map, persists
// atomically, and caches in memory. Used on first build and on corruption.
func (c *excerptCache) rebuild(t entry.Tier) (*diskCache, error) {
	exs := map[entity.Id]Excerpt{}
	for streamed := range dag.ReadAll(entry.Definition(t), entry.WrapForRead(), c.s.repo, resolvers(c.s.repo)) {
		if streamed.Err != nil {
			return nil, streamed.Err
		}
		snap := c.s.compileSnapshot(t, streamed.Entity)
		exs[snap.ID] = toExcerpt(snap)
	}
	refs, err := buildRefMap(c.s.repo, t)
	if err != nil {
		return nil, err
	}
	dc := &diskCache{Version: excerptCacheVersion, Excerpts: exs, Refs: refs}
	if err := saveDiskCache(c.s.repo.LocalStorage(), t, dc); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.loaded[t] = dc
	c.mu.Unlock()
	return dc, nil
}
