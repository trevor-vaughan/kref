package render_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/riotbox/kref/internal/entry"
	"github.com/riotbox/kref/internal/render"
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

var _ = Describe("Links rendering", func() {
	It("prints outgoing and incoming sections", func() {
		var b bytes.Buffer
		render.Links(&b, entry.LinkView{
			Outgoing: []entry.LinkRef{{ID: "bbb2", Type: "relates", Title: "B"}},
			Incoming: []entry.LinkRef{{ID: "ccc3", Type: "parent-child", Title: "C"}},
		})
		out := b.String()
		Expect(out).To(ContainSubstring("Outgoing:"))
		Expect(out).To(ContainSubstring("relates"))
		Expect(out).To(ContainSubstring("Incoming:"))
		Expect(out).To(ContainSubstring("parent-child"))
	})
	It("reports when there are no links", func() {
		var b bytes.Buffer
		render.Links(&b, entry.LinkView{})
		Expect(b.String()).To(ContainSubstring("no links"))
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
