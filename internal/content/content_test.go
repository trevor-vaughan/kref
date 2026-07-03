package content_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/content"
)

var _ = Describe("content type registry", func() {
	It("canonicalizes a known type and strips parameters/case", func() {
		ct, err := content.Canonical("Text/Markdown; charset=utf-8")
		Expect(err).NotTo(HaveOccurred())
		Expect(ct).To(Equal("text/markdown"))
	})

	It("rejects an unsupported or binary type", func() {
		_, err := content.Canonical("image/png")
		Expect(err).To(HaveOccurred())
		_, err = content.Canonical("application/octet-stream")
		Expect(err).To(HaveOccurred())
	})

	It("detects content type from a file extension", func() {
		ct, err := content.Detect("cfg.json", []byte(`{"a":1}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(ct).To(Equal("application/json"))
	})

	It("falls back to text/plain for unknown text extensions", func() {
		ct, err := content.Detect("notes.log", []byte("hello"))
		Expect(err).NotTo(HaveOccurred())
		Expect(ct).To(Equal("text/plain"))
	})

	It("rejects binary content (invalid UTF-8 or NUL bytes)", func() {
		_, err := content.Detect("x.md", []byte{0xff, 0xfe, 0x00})
		Expect(err).To(MatchError(content.ErrBinary))
		Expect(content.EnsureText([]byte("ok\n"))).To(Succeed())
		Expect(content.EnsureText([]byte{0x00})).To(MatchError(content.ErrBinary))
	})

	It("maps code types to a chroma lexer and markdown to none", func() {
		Expect(content.Lexer("text/x-go")).To(Equal("go"))
		Expect(content.Lexer("text/markdown")).To(Equal(""))
		Expect(content.IsMarkdown("text/markdown")).To(BeTrue())
		Expect(content.IsMarkdown("application/json")).To(BeFalse())
	})

	It("keeps the extension map consistent with the registry", func() {
		for _, ct := range content.RegisteredTypes() {
			got, err := content.Canonical(ct)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(ct), "registered type %q must canonicalize to itself", ct)
		}
		for ext, ct := range content.ExtensionTypes() {
			_, err := content.Canonical(ct)
			Expect(err).NotTo(HaveOccurred(), "extension %q maps to unregistered type %q", ext, ct)
		}
	})
})
