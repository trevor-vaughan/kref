package main

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/todo"
)

var _ = Describe("kref edit lint banner", func() {
	vs := []todo.Violation{
		{Line: 3, Rule: "unknown-heading", Msg: `unknown section heading "## Opne"`},
		{Line: 0, Rule: "missing-heading", Msg: "no ## Done section"},
	}

	It("leads with an unmistakable REJECTED signal so a kicked-back editor is legible", func() {
		banner := lintBanner(vs)
		// The first line the editor shows must make clear the save was refused,
		// not merely offer advice — this is the salience fix for "no idea what's
		// going on" when kref reopens the editor.
		lead, _, _ := strings.Cut(banner, "\n")
		Expect(lead).To(ContainSubstring("REJECTED"))
		Expect(lead).To(ContainSubstring("NOT"))
	})

	It("names every violation (line, rule, message) so the reason is exact", func() {
		banner := lintBanner(vs)
		Expect(banner).To(ContainSubstring(`line 3: unknown-heading: unknown section heading "## Opne"`))
		Expect(banner).To(ContainSubstring("missing-heading: no ## Done section"))
	})

	It("round-trips: stripLintBanner removes the banner so it never leaks into the saved todo", func() {
		body := "# T\n\n## Open\n\n- [ ] a\n\n## Done (compact)\n"
		seeded := lintBanner(vs) + body
		Expect(stripLintBanner(seeded)).To(Equal(body))
	})

	It("leaves a body that never had a banner untouched", func() {
		body := "# T\n\n## Open\n"
		Expect(stripLintBanner(body)).To(Equal(body))
	})
})
