package textdiff

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Diff", func() {
	It("marks identical inputs as all-same", func() {
		lines := Diff("a\nb\n", "a\nb\n")
		Expect(lines).To(Equal([]Line{{Same, "a"}, {Same, "b"}}))
	})

	It("marks a pure addition", func() {
		lines := Diff("a\nc\n", "a\nb\nc\n")
		Expect(lines).To(Equal([]Line{{Same, "a"}, {Add, "b"}, {Same, "c"}}))
	})

	It("marks a pure removal", func() {
		lines := Diff("a\nb\nc\n", "a\nc\n")
		Expect(lines).To(Equal([]Line{{Same, "a"}, {Del, "b"}, {Same, "c"}}))
	})

	It("marks a modification as remove-then-add", func() {
		lines := Diff("a\nold\nc\n", "a\nnew\nc\n")
		Expect(lines).To(Equal([]Line{{Same, "a"}, {Del, "old"}, {Add, "new"}, {Same, "c"}}))
	})

	It("treats an empty previous body as all-added (the v1 case)", func() {
		lines := Diff("", "a\nb\n")
		Expect(lines).To(Equal([]Line{{Add, "a"}, {Add, "b"}}))
	})

	It("treats an empty new body as all-removed", func() {
		lines := Diff("a\n", "")
		Expect(lines).To(Equal([]Line{{Del, "a"}}))
	})

	It("does not invent a trailing empty line for newline-terminated input", func() {
		Expect(Diff("a\n", "a\n")).To(HaveLen(1))
		Expect(Diff("a", "a")).To(HaveLen(1)) // unterminated final line still counts once
	})
})

var _ = Describe("Stats", func() {
	It("is zero for identical inputs", func() {
		Expect(Stats("x\n", "x\n")).To(Equal(DiffStats{}))
	})

	It("counts added and removed lines and their characters", func() {
		// "old" (3 chars) replaced by "newer" (5 chars); "plus" (4 chars) added.
		s := Stats("a\nold\n", "a\nnewer\nplus\n")
		Expect(s).To(Equal(DiffStats{
			LinesAdded:   2,
			LinesRemoved: 1,
			CharsAdded:   9, // "newer" + "plus"
			CharsRemoved: 3, // "old"
		}))
	})

	It("counts the whole body as added from an empty previous version", func() {
		s := Stats("", "ab\ncd\n")
		Expect(s).To(Equal(DiffStats{LinesAdded: 2, CharsAdded: 4}))
	})
})
