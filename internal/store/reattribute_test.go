package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Store.Reattribute", func() {
	It("changes the entry's displayed author", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierPersonal, "spec", "Title", "body")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.Reattribute(id, "New Owner", "owner@example.com")).To(Succeed())

		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.CreatedBy).To(Equal("New Owner"))
		Expect(snap.CreatedByEmail).To(Equal("owner@example.com"))
	})

	It("errors on a missing entry", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.Reattribute("deadbeef", "N", "e@x.com")).NotTo(Succeed())
	})
})
