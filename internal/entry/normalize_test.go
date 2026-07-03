package entry_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("NormalizeTitle", func() {
	It("lowercases, trims, and collapses internal whitespace", func() {
		Expect(entry.NormalizeTitle("  Auth   Design  ")).To(Equal("auth design"))
	})
	It("collapses tabs and newlines to single spaces", func() {
		Expect(entry.NormalizeTitle("Auth\tDesign\nflow")).To(Equal("auth design flow"))
	})
	It("keeps punctuation significant", func() {
		Expect(entry.NormalizeTitle("Auth: Design")).To(Equal("auth: design"))
	})
	It("maps whitespace-only to empty", func() {
		Expect(entry.NormalizeTitle("   ")).To(Equal(""))
	})
})
