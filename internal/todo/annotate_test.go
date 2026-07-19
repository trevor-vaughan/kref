package todo_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/todo"
)

var _ = Describe("AnnotateHeadingCounts", func() {
	It("annotates ## and ### headings with their subtree open-item counts", func() {
		body := "# T\n\n## Open\n### Priority\n- [ ] a\n- [ ] b\n### Later\n- [ ] c\n\n## Done (compact)\n- [x] d\n"
		out := todo.AnnotateHeadingCounts(body)
		// Parent includes children: Open subtree has 3 open items.
		Expect(out).To(ContainSubstring("## Open (3)"))
		Expect(out).To(ContainSubstring("### Priority (2)"))
		Expect(out).To(ContainSubstring("### Later (1)"))
	})

	It("skips headings whose subtree has no open items", func() {
		body := "# T\n\n## Done (compact)\n- [x] d\n"
		out := todo.AnnotateHeadingCounts(body)
		Expect(out).To(ContainSubstring("## Done (compact)\n")) // no " (N)"
		Expect(out).NotTo(ContainSubstring("Done (compact) ("))
	})

	It("does not count indented (non-top-level) checkboxes", func() {
		body := "## Open\n- [ ] a\n  - [ ] nested note\n"
		Expect(todo.AnnotateHeadingCounts(body)).To(ContainSubstring("## Open (1)"))
	})

	It("does not annotate the level-1 title", func() {
		body := "# T\n- [ ] a\n"
		Expect(todo.AnnotateHeadingCounts(body)).To(ContainSubstring("# T\n"))
		Expect(todo.AnnotateHeadingCounts(body)).NotTo(ContainSubstring("# T ("))
	})

	It("is byte-identical when no heading has open items", func() {
		body := "prose only\nno headings here\n"
		Expect(todo.AnnotateHeadingCounts(body)).To(Equal(body))
	})
})
