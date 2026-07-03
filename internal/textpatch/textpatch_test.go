package textpatch

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Apply (lenient unified diff)", func() {
	body := "# Title\n\nline one\nline two\nline three"

	It("applies a hunk located by context, ignoring wrong line numbers", func() {
		diff := "@@ -99,3 +99,3 @@\n line one\n-line two\n+line 2\n line three\n"
		out, err := Apply(body, diff)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("# Title\n\nline one\nline 2\nline three"))
	})

	It("tolerates file headers, git preamble, and the no-newline marker", func() {
		diff := "diff --git a/doc.md b/doc.md\nindex 123..456 100644\n--- a/doc.md\n+++ b/doc.md\n" +
			"@@ -3,3 +3,3 @@\n line one\n-line two\n+line 2\n line three\n\\ No newline at end of file\n"
		out, err := Apply(body, diff)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("line 2"))
	})

	It("applies multiple hunks in order, each after the previous", func() {
		twoSection := "## A\nval: 1\n\n## B\nval: 1"
		diff := "@@ -2,1 +2,1 @@\n-val: 1\n+val: 2\n" +
			"@@ -5,1 +5,1 @@\n-val: 1\n+val: 3\n"
		out, err := Apply(twoSection, diff)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("## A\nval: 2\n\n## B\nval: 3"))
	})

	It("uses the line hint to pick between identical candidate sites", func() {
		twoSection := "## A\nval: 1\n\n## B\nval: 1"
		diff := "@@ -5,1 +5,1 @@\n-val: 1\n+val: 9\n"
		out, err := Apply(twoSection, diff)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("## A\nval: 1\n\n## B\nval: 9"), "hint points at section B")
	})

	It("errors on ambiguous context with no usable hint", func() {
		twoSection := "## A\nval: 1\n\n## B\nval: 1"
		diff := "@@ @@\n-val: 1\n+val: 9\n"
		_, err := Apply(twoSection, diff)
		Expect(err).To(MatchError(ContainSubstring("hunk 1")))
		Expect(err).To(MatchError(ContainSubstring("2 locations")))
	})

	It("errors loudly when a hunk's context is not in the body (stale diff)", func() {
		diff := "@@ -3,3 +3,3 @@\n line one\n-line 2.0\n+line 2.1\n line three\n"
		_, err := Apply(body, diff)
		Expect(err).To(MatchError(ContainSubstring("hunk 1: context not found")))
	})

	It("is all-or-nothing: a later failing hunk aborts the whole patch", func() {
		diff := "@@ -3,1 +3,1 @@\n-line one\n+line ONE\n" +
			"@@ -9,1 +9,1 @@\n-absent\n+x\n"
		_, err := Apply(body, diff)
		Expect(err).To(MatchError(ContainSubstring("hunk 2")))
	})

	It("matches context despite trailing whitespace drift", func() {
		spaced := "line one  \nline two\t\nline three"
		diff := "@@ -1,3 +1,3 @@\n line one\n-line two\n+line 2\n line three\n"
		out, err := Apply(spaced, diff)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("line 2"))
	})

	It("treats a bare blank line inside a hunk as blank context", func() {
		diff := "@@ -1,4 +1,4 @@\n # Title\n\n line one\n-line two\n+line 2\n"
		out, err := Apply(body, diff)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("line 2"))
	})

	It("supports pure deletion and pure addition around context", func() {
		diff := "@@ -3,3 +3,3 @@\n line one\n-line two\n line three\n+line four\n"
		out, err := Apply(body, diff)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("# Title\n\nline one\nline three\nline four"))
	})

	It("rejects a hunk that has nothing to locate it (additions only)", func() {
		diff := "@@ -1,0 +1,1 @@\n+brand new line\n"
		_, err := Apply(body, diff)
		Expect(err).To(MatchError(ContainSubstring("hunk 1")))
		Expect(err).To(MatchError(ContainSubstring("no context")))
	})

	It("rejects input with no hunks at all", func() {
		_, err := Apply(body, "just some prose\n")
		Expect(err).To(MatchError(ContainSubstring("no unified-diff hunks")))
	})
})
