package render_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
)

var _ = Describe("Tree rendering", func() {
	It("indents children under their parent", func() {
		var b bytes.Buffer
		render.Tree(&b, &entry.TreeNode{
			ID:    "aaa1",
			Title: "Root",
			Children: []*entry.TreeNode{
				{ID: "bbb2", Title: "Child"},
			},
		})
		out := b.String()
		Expect(out).To(ContainSubstring("Root"))
		Expect(out).To(ContainSubstring("  bbb2"))
		Expect(out).To(ContainSubstring("Child"))
	})
})

var _ = Describe("Tidy rendering", func() {
	It("renders duplicate and superseded sections", func() {
		var b bytes.Buffer
		render.Tidy(&b, entry.TidyReport{
			Duplicates: []entry.DuplicateGroup{{
				NormalizedTitle: "auth design",
				Entries: []entry.TidyEntry{
					{ID: "aaa1", Title: "Auth Design", Tier: "personal", Status: "open"},
					{ID: "bbb2", Title: "auth design", Tier: "personal", Status: "open"},
				},
			}},
			Superseded: []entry.TidyEntry{{ID: "ccc3", Title: "Old", Tier: "personal", Status: "superseded"}},
		})
		out := b.String()
		Expect(out).To(ContainSubstring("Duplicate titles:"))
		Expect(out).To(ContainSubstring("(×2)"))
		Expect(out).To(ContainSubstring("Superseded:"))
		Expect(out).To(ContainSubstring("Old"))
	})
	It("reports a clean store", func() {
		var b bytes.Buffer
		render.Tidy(&b, entry.TidyReport{})
		Expect(b.String()).To(ContainSubstring("nothing to tidy"))
	})
})
