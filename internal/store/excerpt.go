package store

import (
	"time"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// Excerpt is the lean, cacheable projection of a Snapshot: every field the
// `list` table/--plain view and tab-completion need, plus the entry's typed
// links (small; feeds show's expanded header and incoming-link lookups). It
// still omits the heavy Body/Provenance. Source is pre-derived from provenance
// so the cache can serve the ColSource column without carrying the whole
// provenance log.
type Excerpt struct {
	ID             entity.Id
	Kind           string
	Title          string
	Status         string
	Tier           string
	TierType       string
	ContentType    string
	Labels         []string
	Links          []entry.Link
	Deleted        bool
	Archived       bool
	Tracked        bool
	TrackedPath    string
	Source         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	EditedAt       time.Time
	CreatedBy      string
	CreatedByEmail string
}

// deriveSource mirrors the ColSource renderer: the last non-empty provenance
// SourcePath wins.
func deriveSource(prov []entry.OriginEvent) string {
	src := ""
	for _, o := range prov {
		if o.SourcePath != "" {
			src = o.SourcePath
		}
	}
	return src
}

func toExcerpt(s *entry.Snapshot) Excerpt {
	return Excerpt{
		ID: s.ID, Kind: s.Kind, Title: s.Title, Status: s.Status,
		Tier: s.Tier, TierType: s.TierType, ContentType: s.ContentType,
		Labels: s.Labels, Links: s.Links, Deleted: s.Deleted, Archived: s.Archived,
		Tracked: s.Tracked, TrackedPath: s.TrackedPath,
		Source:    deriveSource(s.Provenance),
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, EditedAt: s.EditedAt,
		CreatedBy: s.CreatedBy, CreatedByEmail: s.CreatedByEmail,
	}
}

// sourceProvenance reconstructs the minimal provenance the ColSource renderer
// reads. Other provenance detail is intentionally not cached (belongs to show).
func sourceProvenance(src string) []entry.OriginEvent {
	if src == "" {
		return nil
	}
	return []entry.OriginEvent{{SourcePath: src}}
}

// ToSnapshot exposes the excerpt->snapshot projection for the list renderer.
func (e Excerpt) ToSnapshot() *entry.Snapshot { return e.toSnapshot() }

func (e Excerpt) toSnapshot() *entry.Snapshot {
	return &entry.Snapshot{
		ID: e.ID, Kind: e.Kind, Title: e.Title, Status: e.Status,
		Tier: e.Tier, TierType: e.TierType, ContentType: e.ContentType,
		Labels: e.Labels, Links: e.Links, Deleted: e.Deleted, Archived: e.Archived,
		Tracked: e.Tracked, TrackedPath: e.TrackedPath,
		Provenance: sourceProvenance(e.Source),
		CreatedAt:  e.CreatedAt, UpdatedAt: e.UpdatedAt, EditedAt: e.EditedAt,
		CreatedBy: e.CreatedBy, CreatedByEmail: e.CreatedByEmail,
	}
}
