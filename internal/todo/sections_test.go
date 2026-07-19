package todo_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/todo"
)

var _ = Describe("Sections", func() {
	It("returns the ## section headings in order, excluding the # title and ### subheadings", func() {
		body := "# Title\n\n## Open\n\n- [ ] a\n\n### Priority\n\n- [ ] b\n\n## Done\n\n- [x] c\n"
		secs := todo.Sections(body)
		Expect(secs).To(HaveLen(2))
		Expect(secs[0].Heading).To(Equal("## Open"))
		Expect(secs[0].Index).To(Equal(0))
		Expect(secs[1].Heading).To(Equal("## Done"))
		Expect(secs[1].Index).To(Equal(1))
	})

	It("returns nothing for a body with no ## headings", func() {
		Expect(todo.Sections("# just a title\nprose\nno sections\n")).To(BeEmpty())
	})
})
