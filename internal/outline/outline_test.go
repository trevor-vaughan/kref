package outline_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/outline"
)

var _ = Describe("Parse and Headings", func() {
	body := "# Doc\n\nintro\n\n## Open\n\n- [ ] a\n\n### Priority\n\n- [ ] b\n\n## Done\n\n- [x] c\n"

	It("returns every heading in order with level, line, text, and a nested path", func() {
		o := outline.Parse(body)
		hs := o.Headings()
		Expect(hs).To(HaveLen(4))
		Expect(hs[0]).To(Equal(outline.Heading{Path: "Doc", Level: 1, Line: 0, Text: "Doc"}))
		Expect(hs[1]).To(Equal(outline.Heading{Path: "Doc\x1fOpen", Level: 2, Line: 4, Text: "Open"}))
		Expect(hs[2]).To(Equal(outline.Heading{Path: "Doc\x1fOpen\x1fPriority", Level: 3, Line: 8, Text: "Priority"}))
		Expect(hs[3]).To(Equal(outline.Heading{Path: "Doc\x1fDone", Level: 2, Line: 12, Text: "Done"}))
	})

	It("finds no headings in heading-less content", func() {
		Expect(outline.Parse("just prose\n\nmore prose\n").Headings()).To(BeEmpty())
	})
})

var _ = Describe("Render", func() {
	body := "# Doc\n\nintro\n\n## Open\n\n- [ ] a\n\n### Priority\n\n- [ ] b\n\n## Done\n\n- [x] c\n"

	It("replaces a folded section's content with a '▸ N lines' hint", func() {
		o := outline.Parse(body)
		out := o.Render(map[string]bool{"Doc\x1fDone": true})
		Expect(out).To(ContainSubstring("## Done\n▸ 3 lines")) // blank, "- [x] c", trailing "" = 3 hidden lines
		Expect(out).NotTo(ContainSubstring("- [x] c"))
		Expect(out).To(ContainSubstring("- [ ] a")) // Open untouched
	})

	It("folding a ## also hides its ### (nesting by level)", func() {
		o := outline.Parse(body)
		out := o.Render(map[string]bool{"Doc\x1fOpen": true})
		Expect(out).NotTo(ContainSubstring("### Priority"))
		Expect(out).NotTo(ContainSubstring("- [ ] b"))
		Expect(out).To(ContainSubstring("## Done")) // sibling ## survives
	})

	It("returns the body unchanged when nothing is folded", func() {
		Expect(outline.Parse(body).Render(nil)).To(Equal(strings.TrimRight(body, "\n")))
	})
})

var _ = Describe("HeadingAt and AllPaths", func() {
	body := "# Doc\n\nintro\n\n## Open\n\n- [ ] a\n\n### Priority\n\n- [ ] b\n\n## Done\n\n- [x] c\n"

	It("returns the innermost heading whose span covers a line", func() {
		o := outline.Parse(body)
		h, ok := o.HeadingAt(9) // "- [ ] b", inside ### Priority
		Expect(ok).To(BeTrue())
		Expect(h.Text).To(Equal("Priority"))
		h2, ok2 := o.HeadingAt(2) // "intro", inside # Doc only
		Expect(ok2).To(BeTrue())
		Expect(h2.Text).To(Equal("Doc"))
	})

	It("returns false before the first heading", func() {
		_, ok := outline.Parse("intro\n# Later\n").HeadingAt(0)
		Expect(ok).To(BeFalse())
	})

	It("AllPaths lists every heading path (for collapse-all)", func() {
		Expect(outline.Parse(body).AllPaths()).To(ConsistOf(
			"Doc", "Doc\x1fOpen", "Doc\x1fOpen\x1fPriority", "Doc\x1fDone"))
	})
})
