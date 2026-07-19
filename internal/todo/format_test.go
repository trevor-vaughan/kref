package todo_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/todo"
)

var _ = Describe("Format", func() {
	It("moves a done item from Open into Done, verbatim with its notes", func() {
		in := "# T\n\n## Open\n- [ ] keep me\n- [x] finished\n  - a note\n\n## Done (compact)\nold\n"
		out, err := todo.Format(in)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("## Open\n- [ ] keep me\n"))
		Expect(out).To(ContainSubstring("- [x] finished\n  - a note"))
		Expect(indexOf(out, "- [x] finished")).To(BeNumerically(">", indexOf(out, "## Done (compact)")))
	})

	It("moves a stray non-done item out of Done into Open", func() {
		in := "# T\n\n## Open\n\n## Done (compact)\n- [ ] misfiled\n"
		out, err := todo.Format(in)
		Expect(err).NotTo(HaveOccurred())
		Expect(indexOf(out, "- [ ] misfiled")).To(BeNumerically("<", indexOf(out, "## Done (compact)")))
	})

	It("is idempotent", func() {
		in := "# T\n\n## Open\n- [x] a\n- [ ] b\n\n## Done (compact)\n"
		once, _ := todo.Format(in)
		twice, _ := todo.Format(once)
		Expect(twice).To(Equal(once))
	})

	It("returns the body unchanged when a required section is missing", func() {
		in := "# T\n\n## Open\n- [x] a\n"
		out, err := todo.Format(in)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(in))
	})
})

var _ = Describe("Format golden (realistic messy body)", func() {
	const messy = "# TODO List\n\n" +
		"> a preamble note\n\n" +
		"## Open\n\n" +
		"### Priority\n" +
		"- [ ] open with a\n  multi-line note\n" +
		"- [x] done in Open should move\n" +
		"### UX\n" +
		"- [?] should we do X?\n\n" +
		"## Future / low priority\n" +
		"- [x] also done, also moves\n\n" +
		"## Done (compact)\n" +
		"already shipped\n"

	It("moves both done items and then stabilizes", func() {
		once, err := todo.Format(messy)
		Expect(err).NotTo(HaveOccurred())
		Expect(todo.CountItems(todo.Parse(once), todo.StateDone)).To(Equal(2))
		Expect(indexOf(once, "- [x] done in Open should move")).
			To(BeNumerically(">", indexOf(once, "## Done (compact)")))
		Expect(indexOf(once, "- [x] also done, also moves")).
			To(BeNumerically(">", indexOf(once, "## Done (compact)")))
		Expect(once).To(ContainSubstring("### Priority\n- [ ] open with a\n  multi-line note\n"))
		Expect(once).To(ContainSubstring("### UX\n- [?] should we do X?"))
		twice, _ := todo.Format(once)
		Expect(twice).To(Equal(once))
	})
})

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
