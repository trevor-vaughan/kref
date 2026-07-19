package render_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/todo"
)

var _ = Describe("TodoLint", func() {
	It("prints a clean line when there are no violations", func() {
		var b bytes.Buffer
		render.TodoLint(&b, nil, false)
		Expect(b.String()).To(ContainSubstring("ok"))
	})

	It("prints one line per violation with rule and location", func() {
		var b bytes.Buffer
		render.TodoLint(&b, []todo.Violation{
			{Line: 14, Rule: "unknown-heading", Msg: `unknown section heading "## Opne"`},
			{Line: 0, Rule: "missing-section", Msg: `missing required section "## Done (compact)"`},
		}, false)
		out := b.String()
		Expect(out).To(ContainSubstring("line 14"))
		Expect(out).To(ContainSubstring("unknown-heading"))
		Expect(out).To(ContainSubstring("missing required section"))
	})
})
