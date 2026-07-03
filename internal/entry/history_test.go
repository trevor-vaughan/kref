package entry_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Entry.Log", func() {
	It("maps each operation to a typed log entry in order", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "Title"))
		e.Append(entry.NewSetBody(author, "v1 body"))
		e.Append(entry.NewSetStatus(author, "accepted"))
		log := e.Log()
		Expect(log).To(HaveLen(3))
		Expect(log[0].Op).To(Equal("create"))
		Expect(log[1].Op).To(Equal("set-body"))
		Expect(log[2].Op).To(Equal("set-status"))
		Expect(log[2].Detail).To(Equal("accepted"))
	})

	It("maps every op type to its Op string", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewSetBody(author, "b"))
		e.Append(entry.NewSetTitle(author, "T2"))
		e.Append(entry.NewSetKind(author, "memory"))
		e.Append(entry.NewSetStatus(author, "accepted"))
		e.Append(entry.NewAddLabel(author, "x"))
		e.Append(entry.NewRemoveLabel(author, "x"))
		e.Append(entry.NewAddLink(author, "other", "relates"))
		e.Append(entry.NewRemoveLink(author, "other"))
		e.Append(entry.NewTombstone(author))
		e.Append(entry.NewRestore(author))
		e.Append(entry.NewRecordOrigin(author, "alice", "human", "", "create"))
		e.Append(entry.NewAckMerge(author, []string{"h"}))
		ops := []string{}
		for _, le := range e.Log() {
			ops = append(ops, le.Op)
		}
		Expect(ops).To(Equal([]string{
			"create", "set-body", "set-title", "set-kind", "set-status",
			"add-label", "remove-label", "add-link", "remove-link",
			"tombstone", "restore", "origin", "ack-merge",
		}))
	})
})

var _ = Describe("Entry.Log body versions and change stats", func() {
	It("numbers set-body ops and reports compact added/removed stats", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewSetBody(author, "alpha\nbeta\n"))
		e.Append(entry.NewSetBody(author, "alpha\ngamma!\ndelta\n"))
		log := e.Log()

		// v1: the whole body is new — "alpha" (5) + "beta" (4).
		Expect(log[1].Version).To(Equal(1))
		Expect(log[1].Detail).To(Equal("v1  +9/-0 chars, +2/-0 lines"))
		// v2: "beta" (4 chars) replaced by "gamma!" (6) and "delta" (5) added.
		Expect(log[2].Version).To(Equal(2))
		Expect(log[2].Detail).To(Equal("v2  +11/-4 chars, +2/-1 lines"))
	})

	It("leaves non-body ops unversioned", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewSetStatus(author, "accepted"))
		log := e.Log()
		Expect(log[0].Version).To(BeZero())
		Expect(log[1].Version).To(BeZero())
	})
})

var _ = Describe("Entry.BodyVersions", func() {
	It("returns each SetBody body in order", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewSetBody(author, "first"))
		e.Append(entry.NewSetBody(author, "second"))
		vs := e.BodyVersions()
		Expect(vs).To(HaveLen(2))
		Expect(vs[0].Body).To(Equal("first"))
		Expect(vs[1].Body).To(Equal("second"))
	})
})

var _ = Describe("Log set-content-type", func() {
	It("records a set-content-type entry", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewSetContentType(author, "application/json"))
		log := e.Log()
		last := log[len(log)-1]
		Expect(last.Op).To(Equal("set-content-type"))
		Expect(last.Detail).To(Equal("application/json"))
	})
})
