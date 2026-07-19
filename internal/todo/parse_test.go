package todo_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/todo"
)

var _ = DescribeTable("Parse+String round-trips a body exactly",
	func(body string) {
		Expect(todo.Parse(body).String()).To(Equal(body))
	},
	Entry("empty", ""),
	Entry("title + open + done sections",
		"# TODO\n\n## Open\n- [ ] a task\n\n## Done (compact)\nshipped\n"),
	Entry("no trailing newline",
		"# TODO\n\n## Open\n- [ ] a task"),
	Entry("item with indented notes",
		"# T\n\n## Open\n- [ ] a task\n  - a note\n  more note\n\n## Done (compact)\n"),
)

var _ = Describe("Parse item recognition", func() {
	It("classifies open and done items and preserves a stray [?] as raw", func() {
		body := "# T\n\n## Open\n- [ ] open one\n- [?] a question?\n\n## Done (compact)\n- [x] done one\n  - a note\n"
		d := todo.Parse(body)
		Expect(d.String()).To(Equal(body)) // round-trip: the [?] line survives verbatim
		Expect(todo.CountItems(d, todo.StateOpen)).To(Equal(1))
		Expect(todo.CountItems(d, todo.StateDone)).To(Equal(1))
		// [?] is no longer a recognized item state — it is not counted as open work.
	})

	It("keeps an indented note (incl. an interior blank line) with its item", func() {
		body := "# T\n\n## Open\n- [ ] task\n  first note\n\n  second note\n- [ ] next\n"
		d := todo.Parse(body)
		Expect(d.String()).To(Equal(body))
		Expect(todo.CountItems(d, todo.StateOpen)).To(Equal(2))
	})
})
