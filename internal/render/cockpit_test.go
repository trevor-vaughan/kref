package render_test

import (
	"bytes"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/todo"
)

var _ = Describe("TodoCockpit", func() {
	base := todo.Cockpit{Open: 3, Done: 5, Awaiting: 1, Questions: []string{"do X?"}, ToReview: -1, Changed: -1}

	It("geometric theme shows the awaiting-you signal and its question, hiding not-computed signals", func() {
		var b bytes.Buffer
		render.TodoCockpit(&b, base, "geometric", false)
		out := b.String()
		Expect(out).To(ContainSubstring("◉"))
		Expect(out).To(ContainSubstring("1 awaiting you"))
		Expect(out).To(ContainSubstring("do X?"))
		Expect(out).NotTo(ContainSubstring("to review"))
		Expect(out).NotTo(ContainSubstring("changed"))
	})

	It("shows the current version token when set (the CAS token)", func() {
		var b bytes.Buffer
		c := base
		c.Version = 7
		render.TodoCockpit(&b, c, "geometric", false)
		Expect(b.String()).To(ContainSubstring("v7"))
	})

	It("emoji theme swaps the header glyph", func() {
		var b bytes.Buffer
		render.TodoCockpit(&b, base, "emoji", false)
		Expect(b.String()).To(ContainSubstring("❓"))
	})

	It("shows computed to-review and changed signals when present", func() {
		var b bytes.Buffer
		c := base
		c.ToReview, c.Changed = 2, 4
		render.TodoCockpit(&b, c, "geometric", false)
		out := b.String()
		Expect(out).To(ContainSubstring("2 to review"))
		Expect(out).To(ContainSubstring("4 changed"))
	})

	It("numbers the awaiting-you questions", func() {
		var b bytes.Buffer
		c := base
		c.Questions = []string{"first?", "second?"}
		render.TodoCockpit(&b, c, "geometric", false)
		out := b.String()
		Expect(out).To(ContainSubstring("1. first?"))
		Expect(out).To(ContainSubstring("2. second?"))
		Expect(out).NotTo(ContainSubstring("▸"))
	})

	It("shows edited with relative and absolute time, and abs-only when stale", func() {
		var b bytes.Buffer
		c := base
		c.Edited = time.Now().Add(-2 * time.Hour)
		render.TodoCockpit(&b, c, "geometric", false)
		Expect(b.String()).To(MatchRegexp(`edited 2h ago \(\d{4}-\d{2}-\d{2}\)`))

		var b2 bytes.Buffer
		c.Edited = time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
		render.TodoCockpit(&b2, c, "geometric", false)
		Expect(b2.String()).To(ContainSubstring("edited 2020-01-02"))
		Expect(b2.String()).NotTo(ContainSubstring("2020-01-02)")) // no doubled "(abs)"
	})

	It("suppresses the edited field when Edited is zero", func() {
		var b bytes.Buffer
		render.TodoCockpit(&b, base, "geometric", false) // base.Edited is zero
		Expect(b.String()).NotTo(ContainSubstring("edited"))
	})

	It("shows the quarantine review badge when writes are pending, and hides it at zero", func() {
		var b bytes.Buffer
		c := base
		c.QuarantinePending = 2
		render.TodoCockpit(&b, c, "geometric", false)
		Expect(b.String()).To(ContainSubstring("⚠"))
		Expect(b.String()).To(ContainSubstring("2 awaiting review"))

		var b0 bytes.Buffer
		render.TodoCockpit(&b0, base, "geometric", false) // base.QuarantinePending is zero
		Expect(b0.String()).NotTo(ContainSubstring("awaiting review"))
	})

	It("shows a stale count in the review badge when any are stale", func() {
		var b bytes.Buffer
		c := todo.Cockpit{QuarantinePending: 3, QuarantineStale: 2}
		render.TodoCockpit(&b, c, "geometric", false)
		Expect(b.String()).To(ContainSubstring("3 awaiting review (2 stale)"))
	})

	It("omits the stale count when none are stale", func() {
		var b bytes.Buffer
		c := todo.Cockpit{QuarantinePending: 3, QuarantineStale: 0}
		render.TodoCockpit(&b, c, "geometric", false)
		Expect(b.String()).To(ContainSubstring("3 awaiting review"))
		Expect(b.String()).NotTo(ContainSubstring("stale"))
	})
})

var _ = Describe("TodoStripFields", func() {
	base := todo.Cockpit{Open: 12, Done: 3, Awaiting: 0, ToReview: -1, Changed: -1, Version: 83}

	It("puts awaiting-you first and the counts after it", func() {
		f := render.TodoStripFields(base, "geometric", false)
		Expect(f[0]).To(ContainSubstring("0 awaiting you"))
		Expect(f).To(ContainElement("open 12 · done 3"))
		Expect(f).To(ContainElement("v83"))
	})

	It("ranks every alert above the counts, so a narrow strip keeps them", func() {
		c := base
		c.ToReview, c.Changed, c.QuarantinePending = 2, 3, 4
		f := render.TodoStripFields(c, "geometric", false)
		idx := func(sub string) int {
			for i, s := range f {
				if strings.Contains(s, sub) {
					return i
				}
			}
			return -1
		}
		Expect(idx("2 to review")).To(BeNumerically("<", idx("open 12")))
		Expect(idx("3 changed")).To(BeNumerically("<", idx("open 12")))
		Expect(idx("4 awaiting review")).To(BeNumerically("<", idx("open 12")))
		Expect(idx("open 12")).To(BeNumerically("<", idx("v83")))
	})

	It("marks a stale quarantine queue", func() {
		c := base
		c.QuarantinePending, c.QuarantineStale = 4, 1
		Expect(render.TodoStripFields(c, "geometric", false)).To(ContainElement("⚠ 4 awaiting review (1 stale)"))
	})

	It("omits not-computed signals, the zero version and a zero edited time", func() {
		f := render.TodoStripFields(base, "geometric", false)
		joined := strings.Join(f, "|")
		Expect(joined).NotTo(ContainSubstring("to review"))
		Expect(joined).NotTo(ContainSubstring("changed"))
		Expect(joined).NotTo(ContainSubstring("awaiting review"))
		Expect(joined).NotTo(ContainSubstring("edited"))

		c := base
		c.Version = 0
		Expect(strings.Join(render.TodoStripFields(c, "geometric", false), "|")).NotTo(ContainSubstring("v0"))
	})

	It("emits an edited field when the timestamp is set", func() {
		c := base
		c.Edited = time.Now().Add(-49 * time.Minute)
		Expect(strings.Join(render.TodoStripFields(c, "geometric", false), "|")).To(ContainSubstring("edited "))
	})

	It("never joins two fields into one, so the viewer can drop them separately", func() {
		c := base
		c.ToReview = 2
		for _, f := range render.TodoStripFields(c, "geometric", false) {
			// "open 12 · done 3" is the one intentional compound; nothing else
			// may carry the separator the viewer uses between fields.
			if !strings.HasPrefix(f, "open ") {
				Expect(f).NotTo(ContainSubstring(" · "))
			}
		}
	})

	It("honors the glyph theme", func() {
		emoji := render.TodoStripFields(base, "emoji", false)
		geo := render.TodoStripFields(base, "geometric", false)
		Expect(emoji[0]).NotTo(Equal(geo[0]))
	})

	// The strip must not be able to change `kref todo --no-pager`.
	It("leaves TodoCockpit's output untouched", func() {
		var b bytes.Buffer
		render.TodoCockpit(&b, base, "geometric", false)
		render.TodoStripFields(base, "geometric", false)
		var b2 bytes.Buffer
		render.TodoCockpit(&b2, base, "geometric", false)
		Expect(b2.String()).To(Equal(b.String()))
	})
})
