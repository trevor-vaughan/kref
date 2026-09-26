package entry_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entities/identity"

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

// Attribution drives the foreign-author guard in `kref resign`, so both halves
// of Authors' contract matter: repeats collapse, and the ORDER is first-seen —
// the reader takes the first name as whoever started the entry.
var _ = Describe("Entry.Authors", func() {
	It("returns each distinct author once, in first-seen order", func() {
		repo := newTestRepo()
		mine := newAuthor(repo)
		theirs, err := identity.NewIdentity(repo, "Someone Else", "else@example.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(theirs.Commit(repo)).To(Succeed())

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(mine, "spec", "T"))
		e.Append(entry.NewSetBody(theirs, "b"))
		e.Append(entry.NewAddLabel(mine, "x")) // repeat: must not appear twice
		e.Append(entry.NewSetTitle(theirs, "T2"))

		Expect(e.Authors()).To(Equal([]entry.Author{
			{Name: "Tester", Email: "tester@example.com"},
			{Name: "Someone Else", Email: "else@example.com"},
		}))
	})

	// Distinctness is on the (name, email) pair, not the email: two identities
	// sharing an address are still two claims about who wrote the thing.
	It("keeps two authors apart when they share an email", func() {
		repo := newTestRepo()
		one, err := identity.NewIdentity(repo, "Ada", "shared@example.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(one.Commit(repo)).To(Succeed())
		two, err := identity.NewIdentity(repo, "Grace", "shared@example.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(two.Commit(repo)).To(Succeed())

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(one, "spec", "T"))
		e.Append(entry.NewSetBody(two, "b"))

		Expect(e.Authors()).To(HaveLen(2))
	})

	It("returns an empty slice rather than nil for an entry with no operations", func() {
		Expect(entry.New(entry.TierShared).Authors()).To(BeEmpty())
	})

	// Attesting is not authoring. `kref attest` derives its claim from whether
	// Authors() holds anyone but you, so a peer's bare attestation counted here
	// would make your own later re-attestation of your own work say "received".
	It("does not count an attestation as authorship", func() {
		repo := newTestRepo()
		mine := newAuthor(repo)
		peer, err := identity.NewIdentity(repo, "Peer", "peer@example.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(peer.Commit(repo)).To(Succeed())

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(mine, "spec", "T"))
		e.Append(entry.NewSetBody(mine, "b"))
		e.Append(entry.NewAttest(peer, entry.ClaimAuthored))

		Expect(e.Authors()).To(Equal([]entry.Author{
			{Name: "Tester", Email: "tester@example.com"},
		}))
	})
})

// OperationAuthors answers the question Authors() deliberately stopped
// answering: who wrote ANY operation here, attestations included. `kref resign`
// rewrites and re-signs every commit it touches, so it needs the wider list --
// re-signing a peer's attestation under our key would misrepresent it exactly
// the way re-signing their edit would.
var _ = Describe("Entry.OperationAuthors", func() {
	It("counts an attester that Authors() leaves out", func() {
		repo := newTestRepo()
		mine := newAuthor(repo)
		peer, err := identity.NewIdentity(repo, "Peer", "peer@example.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(peer.Commit(repo)).To(Succeed())

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(mine, "spec", "T"))
		e.Append(entry.NewAttest(peer, entry.ClaimAuthored))

		Expect(e.Authors()).To(HaveLen(1))
		Expect(e.OperationAuthors()).To(Equal([]entry.Author{
			{Name: "Tester", Email: "tester@example.com"},
			{Name: "Peer", Email: "peer@example.com"},
		}))
	})

	It("returns an empty slice rather than nil for an entry with no operations", func() {
		Expect(entry.New(entry.TierShared).OperationAuthors()).To(BeEmpty())
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

	It("shows an attestation in the log with its claim", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "Title"))
		e.Append(entry.NewAttest(author, entry.ClaimReceived))

		log := e.Log()
		Expect(log).To(HaveLen(2))
		Expect(log[1].Op).To(Equal("attest"))
		Expect(log[1].Detail).To(Equal("received"))
	})
})

var _ = Describe("Entry.CommentBodies", func() {
	It("returns every AddComment and EditComment body in order, skipping deletes", func() {
		author := newAuthor(newTestRepo())
		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "T"))
		e.Append(entry.NewSetBody(author, "body"))
		e.Append(entry.NewAddComment(author, "", "human", "first comment", false, ""))
		e.Append(entry.NewEditComment(author, "target", "edited comment"))
		e.Append(entry.NewDeleteComment(author, "target")) // no body — not collected
		Expect(e.CommentBodies()).To(Equal([]string{"first comment", "edited comment"}))
	})
})
