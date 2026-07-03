package store

import (
	"fmt"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Store init adoption", func() {
	It("adopts an existing git repo", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(id.String()).NotTo(BeEmpty())
	})

	It("errors with guidance when the dir is not a git repo", func() {
		dir := GinkgoT().TempDir() // NOT a git repo
		_, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).To(MatchError(ContainSubstring("not a git repository")))
		Expect(err).To(MatchError(ContainSubstring("git init")))
	})
})

var _ = Describe("Store author", func() {
	It("records the explicit author on created entries", func() {
		dir := gitRepo()
		s, err := Init(dir, "Alice", "alice@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())
		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.CreatedBy).To(Equal("Alice"))
		Expect(got.CreatedByEmail).To(Equal("alice@example.com"))
	})
})

var _ = Describe("Store persistence", func() {
	It("resolves the author identity after close and reopen", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierShared, "spec", "Persisted", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		reopened, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })

		got, err := reopened.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Persisted"))
	})
})

var _ = Describe("Store add/get/list", func() {
	It("stores entries across tiers and filters by kind", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Auth design", "# Auth\nbody")
		Expect(err).NotTo(HaveOccurred())

		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Auth design"))
		Expect(got.Tier).To(Equal("shared"))

		_, err = s.Add(entry.TierPrivate, "memory", "secret note", "ssh key rotation cadence")
		Expect(err).NotTo(HaveOccurred())

		all, err := s.List(ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(2))

		specs, err := s.List(ListFilter{Kind: "spec"})
		Expect(err).NotTo(HaveOccurred())
		Expect(specs).To(HaveLen(1))
		Expect(specs[0].Title).To(Equal("Auth design"))
	})
})

var _ = Describe("Store purge", func() {
	It("hard-deletes an entry so it can no longer be read or listed", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Doomed", "body")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.Purge(id, false, false)).To(Succeed())

		_, err = s.Get(id)
		Expect(err).To(HaveOccurred())

		all, err := s.List(ListFilter{IncludeDelete: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(BeEmpty())
	})

	It("runs git gc when requested without error", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierPrivate, "memory", "Secret", "ghp_012345678901234567890123456789abcdef")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Purge(id, true, false)).To(Succeed())
		_, err = s.Get(id)
		Expect(err).To(HaveOccurred())
	})

	It("errors on an unknown id", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.Purge("kref-nonexistent", false, false)).To(HaveOccurred())
	})
})

var _ = Describe("Store id resolution", func() {
	It("resolves an unambiguous hex prefix to the full id", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "Prefixed", "b")
		Expect(err).NotTo(HaveOccurred())

		got, err := s.Resolve(id.String()[:8])
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(id))
	})

	It("returns a tier-agnostic not-found for an unknown prefix", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Resolve("deadbeef")
		Expect(err).To(MatchError(ContainSubstring("not found")))
		Expect(err.Error()).NotTo(ContainSubstring("tier"))
	})

	It("rejects an empty prefix", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Resolve("")
		Expect(err).To(HaveOccurred())
	})

	It("reports an ambiguous prefix when more than one id matches", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		// Add entries until two ids share a first hex char — guaranteed within
		// 17 adds (16 possible first hex chars, pigeonhole). Resolving that one
		// char must then match more than one entry.
		seen := map[byte]int{}
		var dup string
		for i := 0; i < 17 && dup == ""; i++ {
			id, addErr := s.Add(entry.TierShared, "spec", fmt.Sprintf("e%d", i), "b")
			Expect(addErr).NotTo(HaveOccurred())
			c := id.String()[0]
			if seen[c]++; seen[c] >= 2 {
				dup = string(c)
			}
		}
		Expect(dup).NotTo(BeEmpty(), "expected two ids to share a first hex char within 17 adds")
		_, err = s.Resolve(dup)
		Expect(err).To(MatchError(ContainSubstring("ambiguous")))
	})
})

var _ = Describe("Store update", func() {
	It("appends body and title changes to an existing entry", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "document", "Title v1", "body v1")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.Update(id, "body v2", "Title v2")).To(Succeed())

		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Body).To(Equal("body v2"))
		Expect(got.Title).To(Equal("Title v2"))
	})
})

var _ = Describe("Personal tier", func() {
	It("stores and reads an entry in the personal tier", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierPersonal, "memory", "Mine", "x")
		Expect(err).NotTo(HaveOccurred())
		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Tier).To(Equal("personal"))
	})
})

var _ = Describe("Store SetStatus", func() {
	It("changes an entry's status", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.SetStatus(id, "accepted")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Status).To(Equal("accepted"))
	})
	It("errors for an unknown id", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.SetStatus(entity.Id("deadbeef"), "accepted")).To(HaveOccurred())
	})
})

var _ = Describe("Store List search", func() {
	It("matches a case-insensitive substring of the title or body", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Add(entry.TierShared, "spec", "Auth flow", "about login")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Add(entry.TierShared, "spec", "Billing", "invoices")
		Expect(err).NotTo(HaveOccurred())

		byTitle, err := s.List(ListFilter{Search: "AUTH"})
		Expect(err).NotTo(HaveOccurred())
		Expect(byTitle).To(HaveLen(1))
		Expect(byTitle[0].Title).To(Equal("Auth flow"))

		byBody, err := s.List(ListFilter{Search: "invoice"})
		Expect(err).NotTo(HaveOccurred())
		Expect(byBody).To(HaveLen(1))
		Expect(byBody[0].Title).To(Equal("Billing"))
	})
})

var _ = Describe("Store labels", func() {
	It("adds, removes, and filters by labels (AND)", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.AddLabel(id, "area:auth")).To(Succeed())
		Expect(s.AddLabel(id, "spec")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Labels).To(Equal([]string{"area:auth", "spec"}))

		both, err := s.List(ListFilter{Labels: []string{"area:auth", "spec"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(both).To(HaveLen(1))
		none, err := s.List(ListFilter{Labels: []string{"area:auth", "missing"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(none).To(HaveLen(0))

		Expect(s.RemoveLabel(id, "spec")).To(Succeed())
		snap, err = s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Labels).To(Equal([]string{"area:auth"}))
	})
})

var _ = Describe("Store provenance", func() {
	It("records an origin event on an entry", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.RecordOrigin(id, "claude", "agent", "docs/n.md", "ingest")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Provenance).To(HaveLen(1))
		Expect(snap.Provenance[0].SourcePath).To(Equal("docs/n.md"))
	})
	It("normalizes a path to repo-root-relative, basename if outside", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.RepoRelative(filepath.Join(dir, "docs", "n.md"))).To(Equal("docs/n.md"))
		Expect(s.RepoRelative("/etc/secret/notes.md")).To(Equal("notes.md"))
	})
})

var _ = Describe("Store history", func() {
	It("returns the op log and body versions", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "first")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Update(id, "second", "")).To(Succeed())

		log, err := s.Log(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(log)).To(BeNumerically(">=", 2))

		vs, err := s.BodyVersions(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(vs[len(vs)-1].Body).To(Equal("second"))
	})
	It("reports Merged=false for a linear entry", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())
		merged, err := s.Merged(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(merged).To(BeFalse())
	})
})

var _ = Describe("Store Merged on real divergence", func() {
	It("reports Merged=true after concurrent edits sync into a merge commit", func() {
		// Use a bare hub as the shared remote so both sides can push/pull without
		// non-fast-forward conflicts (direct peer-to-peer push to a non-bare repo
		// would fail when the target's ref has already advanced).
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())

		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(gitRepo(), "B", "b@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })

		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		// A creates the entry and publishes it; B pulls to get a common base.
		id, err := a.Add(entry.TierShared, "spec", "T", "base")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		// Concurrent edits: A edits and pushes; B edits locally without pulling first.
		// This creates a fork in the commit graph for the entry.
		Expect(a.Update(id, "edit-from-A", "")).To(Succeed())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Update(id, "edit-from-B", "")).To(Succeed())

		// B pulls — dag.Pull runs Scenario 5 (MergeAll) and creates a merge commit.
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		merged, err := b.Merged(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(merged).To(BeTrue())

		vs, err := b.BodyVersions(id)
		Expect(err).NotTo(HaveOccurred())
		bodies := make([]string, 0, len(vs))
		for _, v := range vs {
			bodies = append(bodies, v.Body)
		}
		Expect(bodies).To(ContainElement("edit-from-A"))
		Expect(bodies).To(ContainElement("edit-from-B"))
	})

	It("clears the flag after kref resolve, and re-flags on fresh divergence", func() {
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())
		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(gitRepo(), "B", "b@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		id, err := a.Add(entry.TierShared, "spec", "T", "base")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())
		Expect(a.Update(id, "edit-A", "")).To(Succeed())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Update(id, "edit-B", "")).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())

		merged, err := b.Merged(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(merged).To(BeTrue())

		n, err := b.AcknowledgeMerge(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeNumerically(">=", 1))

		merged, err = b.Merged(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(merged).To(BeFalse(), "flag clears after resolve")

		n, err = b.AcknowledgeMerge(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(0), "resolve is a no-op when nothing is unacknowledged")
	})

	It("clears the flag on the OTHER machine after an ack syncs through the hub", func() {
		// This proves the portability claim: the acknowledged merge-commit hashes
		// are content-addressed, so an ack made on b clears the flag on a. If the
		// merge commits were NOT identical across machines, a would never recognize
		// b's ack and this test would fail at the final assertion.
		origin := GinkgoT().TempDir()
		_, err := repository.InitBareGoGitRepo(origin, "kref")
		Expect(err).NotTo(HaveOccurred())
		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		b, err := Init(gitRepo(), "B", "b@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		Expect(a.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())
		Expect(b.SetRemote(entry.TierShared, "origin", origin)).To(Succeed())

		id, err := a.Add(entry.TierShared, "spec", "T", "base")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())
		Expect(a.Update(id, "edit-A", "")).To(Succeed())
		Expect(a.Push(entry.TierShared)).To(Succeed())
		Expect(b.Update(id, "edit-B", "")).To(Succeed())
		Expect(b.Pull(entry.TierShared)).To(Succeed())
		Expect(b.Push(entry.TierShared)).To(Succeed())

		// A pulls the merge commit B created and now sees the same divergence.
		Expect(a.Pull(entry.TierShared)).To(Succeed())
		merged, err := a.Merged(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(merged).To(BeTrue(), "a sees the merge after pulling b's merge commit")

		// b acknowledges on its own machine and publishes the ack.
		n, err := b.AcknowledgeMerge(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeNumerically(">=", 1))
		Expect(b.Push(entry.TierShared)).To(Succeed())

		// a pulls the ack and the flag clears — without a ever running resolve.
		Expect(a.Pull(entry.TierShared)).To(Succeed())
		merged, err = a.Merged(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(merged).To(BeFalse(), "b's ack cleared the merged flag on a")
	})
})

var _ = Describe("Store.SetKind", func() {
	It("changes an entry's kind", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "document", "T", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.SetKind(id, "spec")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Kind).To(Equal("spec"))
	})
})

var _ = Describe("Store.RemoveLink", func() {
	It("removes an outgoing link", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		a, err := s.Add(entry.TierShared, "spec", "A", "ba")
		Expect(err).NotTo(HaveOccurred())
		b, err := s.Add(entry.TierShared, "spec", "B", "bb")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.AddLink(a, b.String(), "relates")).To(Succeed())
		Expect(s.RemoveLink(a, b.String())).To(Succeed())
		snap, err := s.Get(a)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Links).To(BeEmpty())
	})
})

var _ = Describe("MostRecent", func() {
	It("returns the latest-UpdatedAt entry and errors on an empty store", func() {
		s, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		_, err = s.MostRecent()
		Expect(err).To(MatchError(ErrNoEntries))

		_, err = s.Add(entry.TierPersonal, "spec", "First", "x")
		Expect(err).NotTo(HaveOccurred())
		time.Sleep(time.Second) // ensure "second" has a strictly later Unix timestamp
		second, err := s.Add(entry.TierShared, "spec", "Second", "y")
		Expect(err).NotTo(HaveOccurred())

		got, err := s.MostRecent()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal(second)) // most recently created/modified
	})
})

var _ = Describe("Store.LinkWouldLeak", func() {
	It("flags a shared source linking a more-private target", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		shared, err := s.Add(entry.TierShared, "spec", "S", "bs")
		Expect(err).NotTo(HaveOccurred())
		priv, err := s.Add(entry.TierPrivate, "spec", "P", "bp")
		Expect(err).NotTo(HaveOccurred())

		leak, err := s.LinkWouldLeak(shared, priv)
		Expect(err).NotTo(HaveOccurred())
		Expect(leak).To(BeTrue(), "shared→private leaks a private id onto a syncable source")
	})
	It("does not flag a same-tier link", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		a, err := s.Add(entry.TierShared, "spec", "A", "ba")
		Expect(err).NotTo(HaveOccurred())
		b, err := s.Add(entry.TierShared, "spec", "B", "bb")
		Expect(err).NotTo(HaveOccurred())
		leak, err := s.LinkWouldLeak(a, b)
		Expect(err).NotTo(HaveOccurred())
		Expect(leak).To(BeFalse())
	})
	It("does not flag a private source linking a more-public target", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		priv, err := s.Add(entry.TierPrivate, "spec", "P", "bp")
		Expect(err).NotTo(HaveOccurred())
		shared, err := s.Add(entry.TierShared, "spec", "S", "bs")
		Expect(err).NotTo(HaveOccurred())
		leak, err := s.LinkWouldLeak(priv, shared)
		Expect(err).NotTo(HaveOccurred())
		Expect(leak).To(BeFalse())
	})
})

var _ = Describe("content type persistence", func() {
	It("stores a content type on create and changes it with SetContentType", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.AddWithContentType(entry.TierShared, "document", "Cfg", `{"a":1}`, "application/json")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.ContentType).To(Equal("application/json"))

		Expect(s.SetContentType(id, "text/x-go")).To(Succeed())
		snap, err = s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.ContentType).To(Equal("text/x-go"))
	})

	It("defaults Add to text/markdown", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.ContentType).To(Equal("text/markdown"))
	})
})

var _ = Describe("Store.Track / Untrack", func() {
	It("marks an entry tracked with a repo-relative path and clears it on untrack", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierPersonal, "spec", "Tracked", "b")
		Expect(err).NotTo(HaveOccurred())

		Expect(s.Track(id, "docs/foo.md")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tracked).To(BeTrue())
		Expect(snap.TrackedPath).To(Equal("docs/foo.md"))

		Expect(s.Untrack(id)).To(Succeed())
		snap, err = s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tracked).To(BeFalse())
		Expect(snap.TrackedPath).To(BeEmpty())
	})

	It("re-points the path when an already-tracked entry is tracked again", func() {
		dir := gitRepo()
		s, err := Init(dir, "Tester", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierPersonal, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Track(id, "docs/old.md")).To(Succeed())
		Expect(s.Track(id, "docs/new.md")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tracked).To(BeTrue())
		Expect(snap.TrackedPath).To(Equal("docs/new.md"))
	})
})
