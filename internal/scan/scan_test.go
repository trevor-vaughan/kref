package scan

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolveBetterleaks", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
	})

	writeExe := func(name string) string {
		p := filepath.Join(dir, name)
		Expect(os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
		return p
	}

	It("prefers KREF_BETTERLEAKS over everything else", func() {
		writeExe("betterleaks") // sibling exists but env wins
		exe := filepath.Join(dir, "kref")
		Expect(resolveBetterleaks("/custom/betterleaks", exe)).To(Equal("/custom/betterleaks"))
	})

	It("falls back to a betterleaks next to the kref binary", func() {
		sib := writeExe("betterleaks")
		exe := filepath.Join(dir, "kref")
		Expect(resolveBetterleaks("", exe)).To(Equal(sib))
	})

	It("ignores a non-executable sibling and falls through to PATH", func() {
		Expect(os.WriteFile(filepath.Join(dir, "betterleaks"), []byte("x"), 0o644)).To(Succeed())
		exe := filepath.Join(dir, "kref")
		Expect(resolveBetterleaks("", exe)).To(Equal("betterleaks"))
	})

	It("falls through to PATH when no sibling exists", func() {
		exe := filepath.Join(dir, "kref")
		Expect(resolveBetterleaks("", exe)).To(Equal("betterleaks"))
	})

	It("falls through to PATH when the executable path is unknown", func() {
		Expect(resolveBetterleaks("", "")).To(Equal("betterleaks"))
	})
})

var _ = Describe("Scan", func() {
	// A private-key block is a structural rule match, so betterleaks flags it
	// deterministically. (A synthetic AWS key like AKIALALEMEL33243OLIA is not
	// used here: betterleaks' token-efficiency filter drops fabricated keys that
	// gitleaks would have flagged.)
	It("flags a leaked private key", func() {
		leaky := "-----BEGIN PRIVATE KEY-----\n" +
			"MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKY0000NOTAREALKEY0000FAKEFAKE00\n" +
			"-----END PRIVATE KEY-----\n"
		findings, err := Scan([]byte(leaky))
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).NotTo(BeEmpty())
	})

	It("passes clean content", func() {
		findings, err := Scan([]byte("# Design\nNo secrets here, just prose.\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(BeEmpty())
	})
})

var _ = Describe("missing scanner", func() {
	It("returns ErrMissing when the configured binary does not exist", func() {
		GinkgoT().Setenv("KREF_BETTERLEAKS", "/nonexistent/betterleaks-missing")
		_, err := Scan([]byte("content"))
		Expect(err).To(MatchError(ErrMissing))
		Expect(err.Error()).To(ContainSubstring("go install github.com/betterleaks/betterleaks@latest"))
	})

	It("returns ErrMissing when nothing resolves on PATH", func() {
		GinkgoT().Setenv("KREF_BETTERLEAKS", "")
		GinkgoT().Setenv("PATH", "/nonexistent-empty-dir")
		_, err := Scan([]byte("content"))
		Expect(err).To(MatchError(ErrMissing))
	})
})
