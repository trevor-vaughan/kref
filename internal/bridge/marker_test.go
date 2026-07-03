package bridge

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("kref-id marker", func() {
	const id = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // DevSkim: ignore DS173237

	It("returns empty id and the original body when absent", func() {
		gotID, body := SplitMarker([]byte("# Title\nprose\n"))
		Expect(gotID).To(BeEmpty())
		Expect(string(body)).To(Equal("# Title\nprose\n"))
	})

	It("extracts the id and strips the trailer", func() {
		raw := []byte("# Title\nprose\n\n<!-- kref-id: " + id + " -->\n")
		gotID, body := SplitMarker(raw)
		Expect(gotID).To(Equal(id))
		Expect(string(body)).To(Equal("# Title\nprose\n"))
	})

	It("ignores a marker whose id is not 64 hex chars", func() {
		gotID, _ := SplitMarker([]byte("x\n\n<!-- kref-id: short -->\n"))
		Expect(gotID).To(BeEmpty())
	})

	It("appends a well-formed trailer", func() {
		out := withMarker([]byte("# Title\nprose\n"), id)
		gotID, body := SplitMarker(out)
		Expect(gotID).To(Equal(id))
		Expect(string(body)).To(Equal("# Title\nprose\n"))
	})
})
