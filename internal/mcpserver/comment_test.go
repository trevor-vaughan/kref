package mcpserver_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/store"
)

// kref_comment takes a caller-supplied comment id. The underlying ops match
// their target by exact id and no-op otherwise, so an unresolved prefix used to
// report success while changing nothing. Every action must resolve the target
// (as the CLI does) and fail loudly when it cannot.
var _ = Describe("kref_comment target resolution", func() {
	// seed builds a repo holding one entry with a plain comment and an open
	// question, returning the dir, the entry id, and both comment ids.
	seed := func() (dir string, id entity.Id, plain, question string) {
		dir = gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err = s.Add(entry.TierPersonal, "doc", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		plain, err = s.AddComment(id, "", "human", "original body", false, "")
		Expect(err).NotTo(HaveOccurred())
		question, err = s.AddComment(id, "", "human", "is this right?", true, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		return dir, id, plain, question
	}

	snapOf := func(dir string, id entity.Id) *entry.Snapshot {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = s.Close() }()
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		return snap
	}

	Describe("edit", func() {
		It("applies to the comment named by a 12-char id prefix", func() {
			dir, id, plain, _ := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "edit", "target": plain[:12], "body": "rewritten",
			})
			Expect(res.IsError).To(BeFalse(), text(res))
			c := snapOf(dir, id).FindComment(plain)
			Expect(c).NotTo(BeNil())
			Expect(c.Body).To(Equal("rewritten"))
			Expect(c.Edited).To(BeTrue())
		})

		It("errors instead of reporting success for a target that matches nothing", func() {
			dir, id, _, _ := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "edit", "target": "deadbeefdead", "body": "rewritten",
			})
			Expect(res.IsError).To(BeTrue())
			Expect(text(res)).To(ContainSubstring("no comment matches"))
		})
	})

	Describe("delete", func() {
		It("tombstones the comment named by an id prefix", func() {
			dir, id, plain, _ := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "delete", "target": plain[:12],
			})
			Expect(res.IsError).To(BeFalse(), text(res))
			Expect(snapOf(dir, id).FindComment(plain).Deleted).To(BeTrue())
		})

		It("errors for a target that matches nothing", func() {
			dir, id, _, _ := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "delete", "target": "deadbeefdead",
			})
			Expect(res.IsError).To(BeTrue())
			Expect(text(res)).To(ContainSubstring("no comment matches"))
		})
	})

	Describe("resolve", func() {
		It("resolves the question named by an id prefix", func() {
			dir, id, _, question := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "resolve", "target": question[:12],
			})
			Expect(res.IsError).To(BeFalse(), text(res))
			Expect(snapOf(dir, id).FindComment(question).Resolved).To(BeTrue())
		})

		It("errors when the target is not a question", func() {
			dir, id, plain, _ := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "resolve", "target": plain[:12],
			})
			Expect(res.IsError).To(BeTrue())
			Expect(text(res)).To(ContainSubstring("not a question"))
		})

		It("attaches a closing note to the resolved question, not to a prefix", func() {
			dir, id, _, question := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "resolve", "target": question[:12], "note": "yes, fixed",
			})
			Expect(res.IsError).To(BeFalse(), text(res))
			snap := snapOf(dir, id)
			var note *entry.Comment
			for i := range snap.Comments {
				if snap.Comments[i].Body == "yes, fixed" {
					note = &snap.Comments[i]
				}
			}
			Expect(note).NotTo(BeNil())
			Expect(note.ReplyTo).To(Equal(question))
		})
	})

	Describe("unresolve", func() {
		It("reopens the resolved question named by an id prefix", func() {
			dir, id, _, question := seed()
			s, err := store.Open(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.ResolveComment(id, question)).To(Succeed())
			Expect(s.Close()).To(Succeed())

			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "unresolve", "target": question[:12],
			})
			Expect(res.IsError).To(BeFalse(), text(res))
			Expect(snapOf(dir, id).FindComment(question).Resolved).To(BeFalse())
		})

		It("errors when the target is not a resolved question", func() {
			dir, id, _, question := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "unresolve", "target": question[:12],
			})
			Expect(res.IsError).To(BeTrue())
			Expect(text(res)).To(ContainSubstring("not a resolved question"))
		})
	})

	Describe("add with reply_to", func() {
		It("stores the parent's full id so the thread nests", func() {
			dir, id, _, question := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "add", "body": "a reply", "reply_to": question[:12],
			})
			Expect(res.IsError).To(BeFalse(), text(res))
			snap := snapOf(dir, id)
			var reply *entry.Comment
			for i := range snap.Comments {
				if snap.Comments[i].Body == "a reply" {
					reply = &snap.Comments[i]
				}
			}
			Expect(reply).NotTo(BeNil())
			Expect(reply.ReplyTo).To(Equal(question), "reply_to must hold the full parent id, not the prefix")
		})

		It("errors for a reply_to that matches nothing", func() {
			dir, id, _, _ := seed()
			res := call(dir, "kref_comment", map[string]any{"model": "test-model",
				"id": id.String(), "action": "add", "body": "a reply", "reply_to": "deadbeefdead",
			})
			Expect(res.IsError).To(BeTrue())
			Expect(text(res)).To(ContainSubstring("no comment matches"))
		})
	})
})
