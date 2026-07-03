package store

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/riotbox/kref/internal/entry"
)

var _ = Describe("Retier", func() {
	It("moves an entry to a new tier preserving id and all facets", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierPersonal, "spec", "Doc", "the body")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.AddLabel(id, "area:auth")).To(Succeed())
		Expect(s.SetStatus(id, "accepted")).To(Succeed())

		Expect(s.Retier(id, entry.TierShared, "tester", "human")).To(Succeed())

		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.ID).To(Equal(id))
		Expect(snap.Tier).To(Equal("shared"))
		Expect(snap.Status).To(Equal("accepted"))
		Expect(snap.Body).To(Equal("the body"))
		Expect(snap.Labels).To(ContainElement("area:auth"))

		_, err = entry.Read(s.repo, entry.TierPersonal, id)
		Expect(err).To(HaveOccurred()) // removed from the old tier

		triggers := make([]string, 0, len(snap.Provenance))
		for _, p := range snap.Provenance {
			triggers = append(triggers, p.Trigger)
		}
		Expect(triggers).To(ContainElement("retier"))
	})

	It("blocks promotion to shared when the entry carries a secret (even in history)", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierPersonal, "spec", "Leaky", "ghp_012345678901234567890123456789abcdef")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Update(id, "now clean", "")).To(Succeed()) // secret only in history

		err = s.Retier(id, entry.TierShared, "tester", "human")
		Expect(err).To(HaveOccurred())
		var rb *RetierBlockedError
		Expect(errors.As(err, &rb)).To(BeTrue())
		Expect(rb.Offenders).To(HaveLen(1))
		Expect(err.Error()).NotTo(ContainSubstring("ghp_012345678901234567890123456789abcdef"))

		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal("personal")) // not moved
	})

	It("allows promotion of a clean entry to shared", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierPersonal, "spec", "Fine", "just prose")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Retier(id, entry.TierShared, "tester", "human")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal("shared"))
	})

	It("is a no-op when the entry is already in the target tier", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierPersonal, "spec", "Doc", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Retier(id, entry.TierPersonal, "tester", "human")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal("personal"))
	})

	It("CrossTierLinks finds outgoing links to entries below the target tier", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		from, err := s.Add(entry.TierPersonal, "spec", "From", "b")
		Expect(err).NotTo(HaveOccurred())
		priv, err := s.Add(entry.TierPrivate, "spec", "Secret dep", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.AddLink(from, priv.String(), "relates")).To(Succeed())

		dangling, err := s.CrossTierLinks(from, entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(dangling).To(HaveLen(1))
		Expect(dangling[0].ID).To(Equal(priv))
	})

	It("WasPushed reports false before any push", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "X", "b")
		Expect(err).NotTo(HaveOccurred())
		pushed, err := s.WasPushed(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(pushed).To(BeFalse())
	})

	It("demotes an already-pushed entry out of shared (warn-and-proceed: the move is not blocked)", func() {
		dir := gitRepo()
		s, err := Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "Shared", "clean prose")
		Expect(err).NotTo(HaveOccurred())
		// Simulate a prior clean push by recording pushed-state directly.
		Expect(s.recordPushed(entry.TierShared, []entity.Id{id})).To(Succeed())
		pushed, err := s.WasPushed(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(pushed).To(BeTrue())

		// Demote still proceeds — the warning is a CLI affordance; the core never blocks demotes.
		Expect(s.Retier(id, entry.TierPrivate, "tester", "human")).To(Succeed())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Tier).To(Equal("private"))
	})
})
