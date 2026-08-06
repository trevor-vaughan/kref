package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/store"
)

var _ = Describe("listSelection", func() {
	openStore := func() *store.Store {
		GinkgoHelper()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(s.Close)
		return s
	}

	It("resolves a tier name to the stored tier", func() {
		sel := listSelection{tier: "private"}
		lf, err := sel.filter(openStore())
		Expect(err).NotTo(HaveOccurred())
		Expect(lf.Tier).To(Equal(entry.TierPrivate))
	})

	It("rejects an unknown tier instead of returning an empty result", func() {
		sel := listSelection{tier: "nope"}
		_, err := sel.filter(openStore())
		Expect(err).To(MatchError(ContainSubstring(`unknown tier "nope"`)))
	})

	It("leaves the tier unset when the flag is empty", func() {
		lf, err := (&listSelection{}).filter(openStore())
		Expect(err).NotTo(HaveOccurred())
		Expect(lf.Tier).To(BeEmpty())
	})

	It("maps the remaining selection flags onto the store filter", func() {
		sel := listSelection{
			kind: "spec", status: "open", labels: []string{"area:auth"},
			archived: true, openQuestions: true,
		}
		lf, err := sel.filter(openStore())
		Expect(err).NotTo(HaveOccurred())
		Expect(lf.Kind).To(Equal("spec"))
		Expect(lf.Status).To(Equal("open"))
		Expect(lf.Labels).To(Equal([]string{"area:auth"}))
		Expect(lf.ArchivedOnly).To(BeTrue())
		Expect(lf.OpenQuestionsOnly).To(BeTrue())
	})

	It("makes --all imply deleted and archived entries", func() {
		lf, err := (&listSelection{all: true}).filter(openStore())
		Expect(err).NotTo(HaveOccurred())
		Expect(lf.IncludeDelete).To(BeTrue())
		Expect(lf.IncludeArchived).To(BeTrue())
	})

	It("includes deleted entries on --include-deleted alone", func() {
		lf, err := (&listSelection{includeDeleted: true}).filter(openStore())
		Expect(err).NotTo(HaveOccurred())
		Expect(lf.IncludeDelete).To(BeTrue())
		Expect(lf.IncludeArchived).To(BeFalse())
	})

	It("parses --sort and reports an unparseable one", func() {
		spec, err := (&listSelection{sortBy: "title"}).sortSpec()
		Expect(err).NotTo(HaveOccurred())
		Expect(spec).NotTo(BeNil())

		_, err = (&listSelection{sortBy: "nonsense"}).sortSpec()
		Expect(err).To(HaveOccurred())
	})

	It("returns no sort spec for an empty --sort", func() {
		spec, err := (&listSelection{}).sortSpec()
		Expect(err).NotTo(HaveOccurred())
		Expect(spec).To(BeNil())
	})
})
