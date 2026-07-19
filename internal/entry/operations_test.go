package entry_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity/dag"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("SetBody version count", func() {
	It("counts set-body ops into Snapshot.Version (the head vN)", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		Expect(e.Compile().Version).To(BeZero()) // no body yet

		e.Append(entry.NewSetBody(author, "first"))
		Expect(e.Compile().Version).To(Equal(1))

		e.Append(entry.NewSetBody(author, "second"))
		Expect(e.Compile().Version).To(Equal(2))
	})

	It("does not count non-body ops", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewSetBody(author, "b"))
		e.Append(entry.NewSetTitle(author, "New"))
		e.Append(entry.NewSetStatus(author, "accepted"))
		Expect(e.Compile().Version).To(Equal(1))
	})
})

var _ = Describe("SetTitle", func() {
	It("updates the snapshot title on apply", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "Old"))
		e.Append(entry.NewSetTitle(author, "New"))
		snap := e.Compile()
		Expect(snap.Title).To(Equal("New"))
	})
})

var _ = Describe("Restore", func() {
	It("un-deletes a tombstoned entry on apply", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "Doc"))
		e.Append(entry.NewTombstone(author))
		e.Append(entry.NewRestore(author))
		snap := e.Compile()
		Expect(snap.Deleted).To(BeFalse())
		Expect(snap.Status).To(Equal("open"))
	})

	It("is a no-op on a live entry and preserves its status", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "Doc"))
		e.Append(entry.NewSetStatus(author, "accepted"))
		e.Append(entry.NewRestore(author)) // entry was never tombstoned
		snap := e.Compile()
		Expect(snap.Deleted).To(BeFalse())
		Expect(snap.Status).To(Equal("accepted"))
	})
})

var _ = Describe("Entry mutations", func() {
	It("applies status, body, links, and tombstone", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "Auth design"))
		e.Append(entry.NewSetBody(author, "# Auth\nbody text"))
		e.Append(entry.NewSetStatus(author, "accepted"))
		e.Append(entry.NewAddLink(author, "kref-abc", "relates"))
		e.Append(entry.NewRemoveLink(author, "kref-abc"))
		e.Append(entry.NewSetBody(author, "updated"))
		Expect(e.Commit(repo)).To(Succeed())

		snap := mustRead(repo, e.Id()).Compile()
		Expect(snap.Status).To(Equal("accepted"))
		Expect(snap.Body).To(Equal("updated"))
		Expect(snap.Links).To(BeEmpty())

		e.Append(entry.NewTombstone(author))
		Expect(e.Commit(repo)).To(Succeed())
		Expect(mustRead(repo, e.Id()).Compile().Deleted).To(BeTrue())
	})
})

var _ = Describe("DeriveTitle", func() {
	It("uses the first H1 heading", func() {
		Expect(entry.DeriveTitle("# Auth flow\n\nbody")).To(Equal("Auth flow"))
	})
	It("falls back to the first non-empty line when there is no H1", func() {
		Expect(entry.DeriveTitle("\n\njust a line\nmore")).To(Equal("just a line"))
	})
	It("returns empty for empty content", func() {
		Expect(entry.DeriveTitle("   \n\n")).To(Equal(""))
	})
})

var _ = Describe("FirstHeading", func() {
	It("returns the H1 text", func() {
		Expect(entry.FirstHeading("# Title\n\nx")).To(Equal("Title"))
	})
	It("returns empty when there is no H1", func() {
		Expect(entry.FirstHeading("no heading here")).To(Equal(""))
	})
	It("ignores H2+ headings (H1 only)", func() {
		Expect(entry.FirstHeading("## Subhead\n\nx")).To(Equal(""))
	})
})

var _ = Describe("RecordOrigin", func() {
	It("appends an origin event (append-only, in order)", func() {
		author := newAuthor(newTestRepo())
		s := &entry.Snapshot{}
		entry.NewRecordOrigin(author, "alice", "human", "", "create").Apply(s)
		entry.NewRecordOrigin(author, "claude", "agent", "docs/n.md", "ingest").Apply(s)
		Expect(s.Provenance).To(HaveLen(2))
		Expect(s.Provenance[0].Trigger).To(Equal("create"))
		Expect(s.Provenance[1].Actor).To(Equal("claude"))
		Expect(s.Provenance[1].ActorKind).To(Equal("agent"))
		Expect(s.Provenance[1].SourcePath).To(Equal("docs/n.md"))
	})
	It("validates a non-empty trigger", func() {
		author := newAuthor(newTestRepo())
		Expect(entry.NewRecordOrigin(author, "a", "human", "", "").Validate()).To(HaveOccurred())
		Expect(entry.NewRecordOrigin(author, "a", "human", "", "create").Validate()).To(Succeed())
	})
})

var _ = Describe("SetKind", func() {
	It("applies the new kind and rejects empty", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "document", "T"))
		e.Append(entry.NewSetKind(author, "spec"))
		Expect(e.Compile().Kind).To(Equal("spec"))
		Expect(entry.NewSetKind(author, "").Validate()).To(MatchError(ContainSubstring("kind required")))
	})
})

var _ = Describe("AckMerge", func() {
	It("folds acknowledged hashes (deduped) and rejects an empty set", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewAckMerge(author, []string{"aaa", "bbb"}))
		e.Append(entry.NewAckMerge(author, []string{"bbb", "ccc"}))
		Expect(e.Compile().AckedMerges).To(ConsistOf("aaa", "bbb", "ccc"))
		Expect(entry.NewAckMerge(author, nil).Validate()).To(MatchError(ContainSubstring("acked merge set required")))
	})
})

var _ = Describe("Reattribute", func() {
	It("overwrites the snapshot author with its payload, not the op author", func() {
		repo := newTestRepo()
		author := newAuthor(repo)
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "Title"))
		e.Append(entry.NewReattribute(author, "New Owner", "owner@example.com"))
		snap := e.Compile()
		Expect(snap.CreatedBy).To(Equal("New Owner"))
		Expect(snap.CreatedByEmail).To(Equal("owner@example.com"))
	})

	It("rejects an empty name or email", func() {
		author := newAuthor(newTestRepo())
		Expect(entry.NewReattribute(author, "", "e@x.com").Validate()).To(HaveOccurred())
		Expect(entry.NewReattribute(author, "Name", "").Validate()).To(HaveOccurred())
	})
})

var _ = Describe("Label operations", func() {
	It("AddLabel adds, dedups, and keeps the set sorted", func() {
		author := newAuthor(newTestRepo())
		s := &entry.Snapshot{}
		entry.NewAddLabel(author, "spec").Apply(s)
		entry.NewAddLabel(author, "area:auth").Apply(s)
		entry.NewAddLabel(author, "spec").Apply(s) // dup
		Expect(s.Labels).To(Equal([]string{"area:auth", "spec"}))
	})
	It("RemoveLabel removes a single label", func() {
		author := newAuthor(newTestRepo())
		s := &entry.Snapshot{Labels: []string{"area:auth", "spec"}}
		entry.NewRemoveLabel(author, "spec").Apply(s)
		Expect(s.Labels).To(Equal([]string{"area:auth"}))
	})
	It("AddLabel validates a non-empty label", func() {
		author := newAuthor(newTestRepo())
		Expect(entry.NewAddLabel(author, "").Validate()).To(HaveOccurred())
		Expect(entry.NewAddLabel(author, "ok").Validate()).To(Succeed())
	})
})

var _ = Describe("Archive and Unarchive", func() {
	It("archives without changing status, and unarchive reverses it", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewSetStatus(author, "obsolete"))
		e.Append(entry.NewArchive(author))
		s := e.Compile()
		Expect(s.Archived).To(BeTrue())
		Expect(s.Status).To(Equal("obsolete")) // archive must not clobber status

		e.Append(entry.NewUnarchive(author))
		s = e.Compile()
		Expect(s.Archived).To(BeFalse())
		Expect(s.Status).To(Equal("obsolete"))
	})
})

var _ = Describe("content type", func() {
	It("defaults to text/markdown when Create carries no content type", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "Doc"))
		snap := e.Compile()
		Expect(snap.ContentType).To(Equal("text/markdown"))
	})

	It("honors a content type set on the Create op", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		c := entry.NewCreate(author, "document", "Cfg")
		c.ContentType = "application/json"
		e.Append(c)
		Expect(e.Compile().ContentType).To(Equal("application/json"))
	})

	It("SetContentType replaces the content type", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "document", "Cfg"))
		e.Append(entry.NewSetContentType(author, "text/x-go"))
		Expect(e.Compile().ContentType).To(Equal("text/x-go"))
	})

	It("SetContentType rejects an unsupported type", func() {
		author := newAuthor(newTestRepo())
		op := entry.NewSetContentType(author, "image/png")
		Expect(op.Validate()).To(HaveOccurred())
	})
})

var _ = Describe("AddComment", func() {
	It("appends a top-level comment", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		op := entry.NewAddComment(author, "human", "first note", false, "")
		e.Append(op)
		snap := e.Compile()
		Expect(snap.Comments).To(HaveLen(1))
		c := snap.Comments[0]
		Expect(c.ID).To(Equal(op.Id().String()))
		Expect(c.Body).To(Equal("first note"))
		Expect(c.AuthorKind).To(Equal("human"))
		Expect(c.Question).To(BeFalse())
		Expect(c.ReplyTo).To(BeEmpty())
		Expect(c.Time).To(Equal(op.Time()))
	})

	It("marks a question and records reply-to", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		q := entry.NewAddComment(author, "human", "why?", true, "")
		e.Append(q)
		r := entry.NewAddComment(author, "agent", "because", false, q.Id().String())
		e.Append(r)
		snap := e.Compile()
		Expect(snap.Comments).To(HaveLen(2))
		Expect(snap.Comments[0].Question).To(BeTrue())
		Expect(snap.Comments[1].ReplyTo).To(Equal(q.Id().String()))
		Expect(snap.Comments[1].AuthorKind).To(Equal("agent"))
	})

	It("rejects an empty body", func() {
		author := newAuthor(newTestRepo())
		Expect(entry.NewAddComment(author, "human", "", false, "").Validate()).To(HaveOccurred())
	})

	It("does not change body version", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewSetBody(author, "content"))
		v := e.Compile().Version
		e.Append(entry.NewAddComment(author, "human", "a note", false, ""))
		Expect(e.Compile().Version).To(Equal(v))
	})
})

var _ = Describe("ResolveComment", func() {
	It("resolves a question comment and records who and when", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		q := entry.NewAddComment(author, "human", "why?", true, "")
		e.Append(q)
		r := entry.NewResolveComment(author, q.Id().String())
		e.Append(r)
		snap := e.Compile()
		Expect(snap.Comments).To(HaveLen(1))
		Expect(snap.Comments[0].Resolved).To(BeTrue())
		Expect(snap.Comments[0].ResolvedBy).To(Equal(author.Name()))
		Expect(snap.Comments[0].ResolvedAt).To(Equal(r.Time()))
	})

	It("is a no-op for an unknown target", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		q := entry.NewAddComment(author, "human", "why?", true, "")
		e.Append(q)
		e.Append(entry.NewResolveComment(author, "deadbeef"))
		snap := e.Compile()
		Expect(snap.Comments[0].Resolved).To(BeFalse())
	})

	It("does not resolve a non-question comment", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		c := entry.NewAddComment(author, "human", "plain note", false, "")
		e.Append(c)
		e.Append(entry.NewResolveComment(author, c.Id().String()))
		snap := e.Compile()
		Expect(snap.Comments[0].Resolved).To(BeFalse())
	})

	It("is idempotent: double-resolve keeps the first ResolvedAt", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		q := entry.NewAddComment(author, "human", "why?", true, "")
		e.Append(q)
		r1 := entry.NewResolveComment(author, q.Id().String())
		e.Append(r1)
		firstAt := e.Compile().Comments[0].ResolvedAt
		r2 := entry.NewResolveComment(author, q.Id().String())
		e.Append(r2)
		snap := e.Compile()
		Expect(snap.Comments[0].ResolvedAt).To(Equal(firstAt))
	})

	It("rejects an empty target", func() {
		author := newAuthor(newTestRepo())
		Expect(entry.NewResolveComment(author, "").Validate()).To(HaveOccurred())
	})
})

var _ = Describe("EditComment and DeleteComment", func() {
	It("edits a comment's body and marks it edited", func() {
		author := newAuthor(newTestRepo())
		s := &entry.Snapshot{}
		add := entry.NewAddComment(author, "human", "orig", false, "")
		add.Apply(s)
		id := add.Id().String()

		entry.NewEditComment(author, id, "revised").Apply(s)

		Expect(s.Comments).To(HaveLen(1))
		Expect(s.Comments[0].Body).To(Equal("revised"))
		Expect(s.Comments[0].Edited).To(BeTrue())
		Expect(s.Comments[0].EditedAt).NotTo(BeZero())
	})

	It("ignores an edit to an unknown or deleted comment", func() {
		author := newAuthor(newTestRepo())
		s := &entry.Snapshot{}
		add := entry.NewAddComment(author, "human", "orig", false, "")
		add.Apply(s)
		id := add.Id().String()

		entry.NewEditComment(author, "deadbeef", "x").Apply(s) // unknown: no-op
		Expect(s.Comments[0].Body).To(Equal("orig"))

		entry.NewDeleteComment(author, id).Apply(s)
		entry.NewEditComment(author, id, "y").Apply(s) // deleted: no-op
		Expect(s.Comments[0].Deleted).To(BeTrue())
		Expect(s.Comments[0].Body).To(Equal("orig")) // body untouched by the ignored edit
	})

	It("deletes a comment stickily, keeping it in the list", func() {
		author := newAuthor(newTestRepo())
		s := &entry.Snapshot{}
		add := entry.NewAddComment(author, "human", "orig", false, "")
		add.Apply(s)
		id := add.Id().String()

		entry.NewDeleteComment(author, id).Apply(s)
		Expect(s.Comments).To(HaveLen(1))
		Expect(s.Comments[0].Deleted).To(BeTrue())
		Expect(s.Comments[0].DeletedBy).To(Equal(author.Name()))
		Expect(s.Comments[0].DeletedAt).NotTo(BeZero())
	})
})

var _ = Describe("EditedAt derivation", func() {
	It("SetBody sets EditedAt and UpdatedAt to the body op time", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		s := &entry.Snapshot{}
		(&entry.SetBody{OpBase: dag.NewOpBase(entry.SetBodyOp, author, 1000), Body: "x"}).Apply(s)

		Expect(s.EditedAt.Unix()).To(Equal(int64(1000)))
		Expect(s.UpdatedAt.Unix()).To(Equal(int64(1000)))
	})

	It("a metadata-only op bumps UpdatedAt but leaves EditedAt at the last body edit", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		s := &entry.Snapshot{}
		(&entry.SetBody{OpBase: dag.NewOpBase(entry.SetBodyOp, author, 1000), Body: "x"}).Apply(s)
		(&entry.AddLabel{OpBase: dag.NewOpBase(entry.AddLabelOp, author, 2000), Label: "area:x"}).Apply(s)

		Expect(s.EditedAt.Unix()).To(Equal(int64(1000)))
		Expect(s.UpdatedAt.Unix()).To(Equal(int64(2000)))
		Expect(s.EditedAt.Before(s.UpdatedAt)).To(BeTrue())
	})

	It("falls back to CreatedAt when the entry has no body op", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "No body"))
		snap := e.Compile()

		Expect(snap.EditedAt).To(Equal(snap.CreatedAt))
		Expect(snap.EditedAt.IsZero()).To(BeFalse())
	})
})
