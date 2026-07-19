package todo_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/todo"
)

var _ = Describe("Summarize", func() {
	const body = "# T\n\n## Open\n- [ ] a\n- [ ] b\n\n## Done (compact)\n- [x] shipped one\n- [x] shipped two\n"

	It("counts open/done from the body and questions from open question-comments", func() {
		comments := []entry.Comment{
			{ID: "1", Body: "should we X?", Question: true},
			{ID: "2", Body: "answered", Question: true, Resolved: true}, // resolved: excluded
			{ID: "3", Body: "gone", Question: true, Deleted: true},      // deleted: excluded
			{ID: "4", Body: "just a note", Question: false},             // not a question: excluded
		}
		c := todo.Summarize(body, comments)
		Expect(c.Open).To(Equal(2))
		Expect(c.Done).To(Equal(2))
		Expect(c.Awaiting).To(Equal(1))
		Expect(c.Questions).To(Equal([]string{"should we X?"}))
		Expect(c.ToReview).To(Equal(-1))
		Expect(c.Changed).To(Equal(-1))
	})

	It("uses only the first line of a multi-line question body", func() {
		comments := []entry.Comment{{ID: "1", Body: "first line?\nmore detail", Question: true}}
		c := todo.Summarize("# T\n\n## Open\n\n## Done (compact)\n", comments)
		Expect(c.Awaiting).To(Equal(1))
		Expect(c.Questions).To(Equal([]string{"first line?"}))
	})

	It("has zero awaiting with no question-comments", func() {
		c := todo.Summarize(body, nil)
		Expect(c.Awaiting).To(Equal(0))
		Expect(c.Questions).To(BeEmpty())
	})
})

var _ = Describe("CollapseDone", func() {
	It("replaces the Done section content with a one-line summary, keeping the heading", func() {
		body := "# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n- [x] one\n- [x] two\nprose\n"
		out, err := todo.CollapseDone(body)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("## Done (compact)"))
		Expect(out).NotTo(ContainSubstring("- [x] one"))
		Expect(out).To(ContainSubstring("2 done"))
		Expect(out).To(ContainSubstring("## Open\n- [ ] a"))
	})

	It("returns the body unchanged when there is no Done section", func() {
		body := "# T\n\n## Open\n- [ ] a\n"
		out, err := todo.CollapseDone(body)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(body))
	})
})

var _ = Describe("CollapseSections", func() {
	body := "## Open (compact)\n\n- [ ] a\n\n## Done (compact)\n\n- [x] c\n- [x] d\n"

	It("folds only the named sections, leaving others intact", func() {
		out := todo.CollapseSections(body, map[string]bool{"## Open (compact)": true})
		Expect(out).To(ContainSubstring("## Open (compact)"))
		Expect(out).NotTo(ContainSubstring("- [ ] a")) // Open folded to a summary
		Expect(out).To(ContainSubstring("- [x] c"))    // Done left intact
	})

	It("is a no-op for an unknown heading", func() {
		out := todo.CollapseSections(body, map[string]bool{"## Nope": true})
		Expect(out).To(ContainSubstring("- [ ] a"))
		Expect(out).To(ContainSubstring("- [x] c"))
	})

	It("is a no-op for an empty collapse set", func() {
		Expect(todo.CollapseSections(body, nil)).To(Equal(body))
	})

	It("CollapseDone equals folding just the Done section", func() {
		done, err := todo.CollapseDone(body)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(Equal(todo.CollapseSections(body, map[string]bool{"## Done (compact)": true})))
	})
})
