package todo_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/todo"
)

func rules(vs []todo.Violation) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Rule
	}
	return out
}

var _ = Describe("Lint", func() {
	It("passes a well-formed document", func() {
		body := "# T\n\n## Open\n- [ ] a\n\n## Done (compact)\nx\n"
		Expect(todo.Lint(body)).To(BeEmpty())
	})

	It("flags a missing required section", func() {
		body := "# T\n\n## Open\n- [ ] a\n"
		Expect(rules(todo.Lint(body))).To(ContainElement("missing-section"))
	})

	It("flags a bad checkbox state", func() {
		body := "# T\n\n## Open\n- [X] shouty\n\n## Done (compact)\n"
		Expect(rules(todo.Lint(body))).To(ContainElement("checkbox-state"))
	})

	It("flags a [?] item as an invalid checkbox state", func() {
		body := "# T\n\n## Open\n- [?] anything?\n\n## Done (compact)\n"
		Expect(rules(todo.Lint(body))).To(ContainElement("checkbox-state"))
	})

	It("flags the retired ## Questions for Trevor heading as unknown", func() {
		body := "# T\n\n## Open\n\n## Questions for Trevor\n\n## Done (compact)\n"
		Expect(rules(todo.Lint(body))).To(ContainElement("unknown-heading"))
	})

	It("flags an unknown ## heading (typo guard)", func() {
		body := "# T\n\n## Opne\n\n## Open\n\n## Done (compact)\n"
		Expect(rules(todo.Lint(body))).To(ContainElement("unknown-heading"))
	})

	It("flags zero or multiple H1 titles", func() {
		body := "# One\n# Two\n\n## Open\n\n## Done (compact)\n"
		Expect(rules(todo.Lint(body))).To(ContainElement("h1"))
	})

	It("does not flag free-named ### subsections", func() {
		body := "# T\n\n## Open\n### Whatever Name\n- [ ] a\n\n## Done (compact)\n"
		Expect(todo.Lint(body)).To(BeEmpty())
	})
})
