package render_test

import (
	"bytes"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
)

// --plain promises "chrome-free line-oriented output". Comments belong in it —
// dropping them was once diagnosed as a bug — but the threaded presentation
// (count header, rule, glyphs, relative times, indentation) is decoration that
// has no place in output meant to be piped.
var _ = Describe("Show under Raw (--plain) with comments", func() {
	commented := func(cs ...entry.Comment) *entry.Snapshot {
		return &entry.Snapshot{Tier: "private", Status: "open", Title: "T", Body: "body text", Comments: cs}
	}
	plain := func(snap *entry.Snapshot) string {
		var b bytes.Buffer
		render.Show(&b, snap, render.ShowOptions{Raw: true, NoHeader: true})
		return b.String()
	}

	It("keeps the verbatim body and each comment as an author-prefixed line", func() {
		out := plain(commented(
			entry.Comment{ID: "c1", Author: "alice", Body: "a remark"},
			entry.Comment{ID: "c2", Author: "bob", Body: "another"},
		))
		Expect(out).To(ContainSubstring("body text"))
		Expect(out).To(ContainSubstring("alice: a remark"))
		Expect(out).To(ContainSubstring("bob: another"))
	})

	It("drops the count header, the rule, and the status glyphs", func() {
		out := plain(commented(
			entry.Comment{ID: "c1", Author: "alice", Body: "a remark", Question: true},
			entry.Comment{ID: "c2", Author: "bob", Body: "done", Question: true, Resolved: true},
		))
		Expect(out).NotTo(ContainSubstring("Comments ("))
		Expect(out).NotTo(ContainSubstring("─"))
		for _, glyph := range []string{"·", "◉", "✓"} {
			Expect(out).NotTo(ContainSubstring(glyph))
		}
	})

	It("marks an open question and a resolved one in words", func() {
		out := plain(commented(
			entry.Comment{ID: "c1", Author: "alice", Body: "is this right?", Question: true},
			entry.Comment{ID: "c2", Author: "bob", Body: "settled", Question: true, Resolved: true},
		))
		Expect(out).To(ContainSubstring("alice: is this right? [open]"))
		Expect(out).To(ContainSubstring("bob: settled [resolved]"))
	})

	It("shows a tombstoned comment as deleted rather than printing its body", func() {
		out := plain(commented(
			entry.Comment{ID: "c1", Author: "alice", Body: "was here", Deleted: true},
		))
		Expect(out).To(ContainSubstring("alice: [deleted]"))
		Expect(out).NotTo(ContainSubstring("was here"))
	})

	It("keeps a multi-line comment body verbatim under its author line", func() {
		out := plain(commented(
			entry.Comment{ID: "c1", Author: "alice", Body: "first\n    indented second"},
		))
		Expect(out).To(ContainSubstring("alice: first\n    indented second"))
	})

	It("orders a reply immediately after the comment it answers", func() {
		out := plain(commented(
			entry.Comment{ID: "c1", Author: "alice", Body: "question", Question: true},
			entry.Comment{ID: "c2", Author: "bob", Body: "unrelated"},
			entry.Comment{ID: "c3", Author: "carol", Body: "the reply", ReplyTo: "c1"},
		))
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		var idx []int
		for i, ln := range lines {
			if strings.HasPrefix(ln, "alice:") || strings.HasPrefix(ln, "carol:") {
				idx = append(idx, i)
			}
		}
		Expect(idx).To(HaveLen(2))
		Expect(idx[1]).To(Equal(idx[0]+1), "the reply must follow its parent")
	})

	It("still renders the threaded block with its chrome when not raw", func() {
		var b bytes.Buffer
		render.Show(&b, commented(entry.Comment{ID: "c1", Author: "alice", Body: "a remark"}),
			render.ShowOptions{NoHeader: true})
		Expect(b.String()).To(ContainSubstring("Comments (1)"))
		Expect(b.String()).To(ContainSubstring("a remark"))
	})
})

// An agent's comment is committed under the repo's git identity, so without the
// actor it reads as if the human wrote it.
var _ = Describe("comment attribution", func() {
	agent := entry.Comment{ID: "c1", Author: "Repo Owner", AuthorKind: "agent", Actor: "claude-opus-5 via claude-code/2.1.220", Body: "from an agent"}
	human := entry.Comment{ID: "c2", Author: "Repo Owner", AuthorKind: "human", Body: "from a person"}

	It("shows the agent, not the git identity, in the threaded view", func() {
		var b bytes.Buffer
		render.RenderComments(&b, []entry.Comment{agent, human}, false, 0)
		Expect(b.String()).To(ContainSubstring("claude-opus-5"))
		Expect(b.String()).To(ContainSubstring("Repo Owner"), "the human comment still shows the identity")
	})

	It("shows the agent under --plain too", func() {
		var b bytes.Buffer
		render.RenderCommentsPlain(&b, []entry.Comment{agent, human})
		Expect(b.String()).To(ContainSubstring("claude-opus-5: from an agent"))
		Expect(b.String()).To(ContainSubstring("Repo Owner: from a person"))
	})
})

// A comment header repeats its author on every reply, so it carries the model
// alone; the client that delivered the write stays in provenance and JSON.
var _ = Describe("composed actor in comment headers", func() {
	agent := entry.Comment{ID: "c1", Author: "Repo Owner", AuthorKind: "agent",
		Actor: "claude-opus-5 via claude-code/2.1.220", Body: "from an agent"}

	It("shows the model without the client in the threaded view", func() {
		var b bytes.Buffer
		render.RenderComments(&b, []entry.Comment{agent}, false, 0)
		Expect(b.String()).To(ContainSubstring("claude-opus-5"))
		Expect(b.String()).NotTo(ContainSubstring("claude-code/2.1.220"))
	})

	It("shows the model without the client under --plain", func() {
		var b bytes.Buffer
		render.RenderCommentsPlain(&b, []entry.Comment{agent})
		Expect(b.String()).To(ContainSubstring("claude-opus-5: from an agent"))
		Expect(b.String()).NotTo(ContainSubstring("claude-code"))
	})

	It("keeps the full composed actor in the provenance header", func() {
		var b bytes.Buffer
		render.ShowHeader(&b, &entry.Snapshot{
			Tier: "private", Status: "open", Title: "T",
			Provenance: []entry.OriginEvent{{
				Actor: "claude-opus-5 via claude-code/2.1.220", ActorKind: "agent", Trigger: "create",
			}},
		}, false, "", nil)
		Expect(b.String()).To(ContainSubstring("claude-opus-5 via claude-code/2.1.220"))
	})
})
