package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Store archive", func() {
	mk := func() *Store {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		return s
	}
	ids := func(snaps []*entry.Snapshot) []string {
		out := make([]string, len(snaps))
		for i, s := range snaps {
			out[i] = s.ID.String()
		}
		return out
	}

	It("hides archived from the default list and surfaces them under the archive filters", func() {
		s := mk()
		keep, err := s.Add(entry.TierShared, "spec", "Keep", "b")
		Expect(err).NotTo(HaveOccurred())
		gone, err := s.Add(entry.TierShared, "spec", "Gone", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Archive(gone)).To(Succeed())

		def, err := s.List(ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(def)).To(ConsistOf(keep.String()))

		only, err := s.List(ListFilter{ArchivedOnly: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(only)).To(ConsistOf(gone.String()))

		all, err := s.List(ListFilter{IncludeArchived: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(all)).To(ConsistOf(keep.String(), gone.String()))
	})

	It("Unarchive returns an entry to the default list", func() {
		s := mk()
		id, err := s.Add(entry.TierShared, "spec", "X", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Archive(id)).To(Succeed())
		Expect(s.Unarchive(id)).To(Succeed())
		def, err := s.List(ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(def)).To(ConsistOf(id.String()))
	})

	It("preserves status across archive", func() {
		s := mk()
		id, err := s.Add(entry.TierShared, "spec", "X", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.SetStatus(id, "obsolete")).To(Succeed())
		Expect(s.Archive(id)).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Archived).To(BeTrue())
		Expect(snap.Status).To(Equal("obsolete"))
	})
})
