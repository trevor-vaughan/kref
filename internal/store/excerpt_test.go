package store

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Excerpt conversions", func() {
	It("projects a snapshot to an excerpt and derives Source from the last provenance path", func() {
		snap := &entry.Snapshot{
			ID: "abc", Kind: "spec", Title: "T", Status: "open",
			Tier: "shared", TierType: "shared", ContentType: "text/markdown",
			Labels: []string{"x"}, Archived: true, Tracked: true, TrackedPath: "p.md",
			Body: "SHOULD NOT APPEAR",
			Provenance: []entry.OriginEvent{
				{SourcePath: "first.md", Time: time.Unix(1, 0)},
				{SourcePath: "second.md", Time: time.Unix(2, 0)},
			},
			CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(20, 0), EditedAt: time.Unix(30, 0),
			CreatedBy: "A", CreatedByEmail: "a@e",
		}
		e := toExcerpt(snap)
		Expect(e.ID.String()).To(Equal("abc"))
		Expect(e.Source).To(Equal("second.md"))
		Expect(e.Archived).To(BeTrue())
		Expect(e.TrackedPath).To(Equal("p.md"))
	})

	It("round-trips through toSnapshot for every list-relevant field including Source", func() {
		e := Excerpt{
			ID: "abc", Kind: "spec", Title: "T", Status: "open",
			Tier: "shared", TierType: "shared", ContentType: "text/markdown",
			Labels: []string{"x"}, Archived: true, Tracked: true, TrackedPath: "p.md",
			Source:    "second.md",
			CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(20, 0), EditedAt: time.Unix(30, 0),
			CreatedBy: "A", CreatedByEmail: "a@e",
		}
		s := e.toSnapshot()
		Expect(s.ID.String()).To(Equal("abc"))
		Expect(s.Title).To(Equal("T"))
		Expect(s.TrackedPath).To(Equal("p.md"))
		Expect(s.Body).To(BeEmpty())
		Expect(s.Provenance).To(HaveLen(1))
		Expect(s.Provenance[0].SourcePath).To(Equal("second.md"))
	})

	It("carries Links through toExcerpt and toSnapshot", func() {
		snap := &entry.Snapshot{
			ID: "abc", Kind: "spec", Title: "T", Status: "open",
			Tier: "shared", TierType: "shared",
			Links: []entry.Link{{To: "def", Type: "relates"}},
		}
		e := toExcerpt(snap)
		Expect(e.Links).To(Equal([]entry.Link{{To: "def", Type: "relates"}}))
		Expect(e.toSnapshot().Links).To(Equal([]entry.Link{{To: "def", Type: "relates"}}))
	})
})
