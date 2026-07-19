package todo_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/todo"
)

var _ = Describe("Delta", func() {
	It("counts newly-done items as to-review and new non-done items as changed", func() {
		seen := "# T\n\n## Open\n- [ ] a\n- [ ] b\n\n## Done (compact)\n"
		head := "# T\n\n## Open\n- [ ] a\n- [ ] c new\n\n## Done (compact)\n- [x] b\n"
		d := todo.Delta(seen, head)
		Expect(d.ToReview).To(Equal(1))
		Expect(d.NewDone).To(Equal([]string{"b"}))
		Expect(d.Changed).To(Equal(1))
		Expect(d.ChangedItems).To(Equal([]string{"c new"}))
	})

	It("reports zero when nothing changed", func() {
		body := "# T\n\n## Open\n- [ ] a\n- [ ] really\n\n## Done (compact)\n- [x] b\n"
		d := todo.Delta(body, body)
		Expect(d.ToReview).To(Equal(0))
		Expect(d.Changed).To(Equal(0))
	})
})
