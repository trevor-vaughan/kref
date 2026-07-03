package hooks

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Render", func() {
	It("invokes kref by absolute path and wires the spec-dir post-commit capture", func() {
		out := Render("/abs/bin/kref", nil)
		Expect(out).To(ContainSubstring("run: /abs/bin/kref sync pull"))
		Expect(out).To(ContainSubstring("run: /abs/bin/kref sync push"))
		Expect(out).To(ContainSubstring("post-commit:"))
		Expect(out).To(ContainSubstring("ingest --skip-missing {files}"))
		Expect(out).To(ContainSubstring("docs/superpowers/plans"))
		Expect(out).To(ContainSubstring("openspec"))
		Expect(out).To(ContainSubstring(`glob: "*.md"`))
		Expect(out).NotTo(ContainSubstring("run: kref ")) // never bare kref
	})
})

var _ = Describe("Merge banner", func() {
	It("omits the managed banner when merging into an existing file", func() {
		existing := []byte("pre-commit:\n  commands:\n    mine:\n      run: make lint\n")
		out, err := Merge(existing, Render("/usr/bin/kref", DefaultIngestPaths))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).NotTo(ContainSubstring("Managed by"))
		Expect(string(out)).To(ContainSubstring("kref-sync-push"))
	})

	It("keeps the banner for a fresh install", func() {
		out, err := Merge(nil, Render("/usr/bin/kref", DefaultIngestPaths))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).To(ContainSubstring("Managed by"))
	})
})

var _ = Describe("Merge", func() {
	It("preserves foreign hooks and replaces only kref-* commands", func() {
		existing := `pre-commit:
  commands:
    lint:
      run: npm run lint
post-merge:
  commands:
    kref-sync-pull:
      run: /old/kref sync pull
`
		merged, err := Merge([]byte(existing), Render("/abs/bin/kref", nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(merged)).To(ContainSubstring("npm run lint"))            // foreign kept
		Expect(string(merged)).To(ContainSubstring("/abs/bin/kref sync pull")) // kref refreshed
		Expect(string(merged)).NotTo(ContainSubstring("/old/kref"))            // stale kref gone
		Expect(string(merged)).To(ContainSubstring("post-commit:"))            // new kref hook added
	})

	It("returns the generated config verbatim when there is no existing file", func() {
		merged, err := Merge(nil, Render("/abs/bin/kref", nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(merged)).To(Equal(Render("/abs/bin/kref", nil)))
	})
})
