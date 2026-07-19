package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Excerpt/DAG list parity", func() {
	It("ListExcerpts equals toExcerpt over store.List for a filter matrix", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		mkData(s)

		filters := []ListFilter{
			{},
			{Kind: "spec"},
			{Status: "open"},
			{Tier: entry.TierShared},
			{Labels: []string{"x"}},
			{IncludeArchived: true},
			{ArchivedOnly: true},
			{IncludeDelete: true},
		}
		for _, f := range filters {
			snaps, err := s.List(f)
			Expect(err).NotTo(HaveOccurred())
			want := make([]Excerpt, len(snaps))
			for i, sn := range snaps {
				want[i] = toExcerpt(sn)
			}
			got, err := s.ListExcerpts(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(ConsistOf(toIfaceExcerpts(want)...), "filter %+v", f)
		}
	})
})

func mkData(s *Store) {
	a, _ := s.Add(entry.TierShared, "spec", "Alpha", "b")
	_, _ = s.Add(entry.TierPersonal, "note", "Beta", "b")
	c, _ := s.Add(entry.TierShared, "spec", "Gamma", "b")
	_ = s.AddLabel(a, "x")
	_ = s.SetStatus(c, "active")
	g, _ := s.Add(entry.TierShared, "spec", "Gone", "b")
	_ = s.Archive(g)
}

func toIfaceExcerpts(es []Excerpt) []any {
	out := make([]any, len(es))
	for i, e := range es {
		out[i] = e
	}
	return out
}
