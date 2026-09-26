package textdiff

import (
	"fmt"
	"strings"

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

	It("keeps a one-line edit small in a body far past the LCS guard", func() {
		// kref's own plan entries run 1000-1600 lines, so two versions of one
		// are well past lcsGuard's cell budget. Before the head/tail trim that
		// degraded to delete-all/add-all and `kref diff` reported a two-line
		// edit as a whole-body rewrite.
		body := func(mid string) string {
			var b strings.Builder
			for i := range 1500 {
				if i == 700 {
					b.WriteString(mid + "\n")
					continue
				}
				fmt.Fprintf(&b, "line %d\n", i)
			}
			return b.String()
		}

		lines := Diff(body("before"), body("after"))
		Expect(lines).To(HaveLen(1501)) // 1499 same + one del + one add
		Expect(lines[700]).To(Equal(Line{Del, "before"}))
		Expect(lines[701]).To(Equal(Line{Add, "after"}))
		Expect(Stats(body("before"), body("after"))).To(Equal(DiffStats{
			LinesAdded: 1, LinesRemoved: 1, CharsAdded: 5, CharsRemoved: 6,
		}))
	})

	It("still degrades to delete-all/add-all when nothing lines up", func() {
		// The guard is the backstop for genuinely unrelated bodies: no common
		// head, no common tail, nothing to trim.
		var a, b strings.Builder
		for i := range 1500 {
			fmt.Fprintf(&a, "a %d\n", i)
			fmt.Fprintf(&b, "b %d\n", i)
		}
		s := Stats(a.String(), b.String())
		Expect(s.LinesRemoved).To(Equal(1500))
		Expect(s.LinesAdded).To(Equal(1500))
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
