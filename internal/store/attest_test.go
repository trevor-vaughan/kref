package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

// nonSigningStore returns a store with a signing key available but signing
// switched off — the state Attest must refuse outright, rather than writing an
// unsigned "attestation" that vouches for nothing.
func nonSigningStore() *Store {
	GinkgoHelper()
	dir := gitRepo()
	setupSSHSigningKey(dir, testSignerEmail)

	s, err := Init(dir, testSignerName, testSignerEmail)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = s.Close() })
	return s
}

// quarantinedEntry parks a flagged write in the hidden quarantine tier and
// returns its id, so a by-id call can reach material awaiting human review.
func quarantinedEntry(s *Store) entity.Id {
	GinkgoHelper()
	parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "body", "",
		[]scan.Finding{{RuleID: "github-pat", Description: "GitHub PAT", StartLine: 1}}, "", "human")
	Expect(err).NotTo(HaveOccurred())
	return parked.ItemID
}

// entryWithForeignOperations returns an entry of s's whose history also carries
// an operation by somebody else. The commit ident cannot express that — git-bug
// leaves it empty — so the second author is established the only honest way:
// by a second store that really does write under a different identity.
func entryWithForeignOperations(s *Store) entity.Id {
	GinkgoHelper()
	id := addUnsignedThenEnableSigning(s)

	GinkgoT().Setenv("KREF_AUTHOR_NAME", "Someone Else")
	GinkgoT().Setenv("KREF_AUTHOR_EMAIL", "else@example.com")
	other, err := Open(s.dir)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = other.Close() })
	Expect(other.AddLabel(id, "theirs")).To(Succeed())
	return id
}

var _ = Describe("Attest", func() {
	var s *Store

	BeforeEach(func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		var err error
		s, err = Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
	})

	It("refuses when the store does not sign", func() {
		// absentID: the refusal precedes any lookup, so the id is never read.
		_, err := nonSigningStore().Attest([]entity.Id{absentID})
		Expect(err).To(MatchError(ContainSubstring("not configured to sign")))
	})

	It("says signature support is missing rather than blaming the signing config", func() {
		// Store.Signing() is false for both "you have not turned signing on" and
		// "kref cannot sign here at all", and the two need opposite advice.
		// Without the sigUnavailable branch this falls through to the refusal
		// above, sending someone to configure a key they already have — the one
		// thing that is not broken. Mirrors the resign spec for the same branch.
		s.repo = unsupportedSigRepo{s.repo}
		s.sigUnavailable = "signing and signature verification are unavailable: /x could not be reopened"

		_, err := s.Attest([]entity.Id{absentID})
		Expect(err).To(MatchError(ContainSubstring("could not be reopened")))
		Expect(err).NotTo(MatchError(ContainSubstring("commit.gpgsign")))
	})

	It("refuses an entry whose history is already good", func() {
		id, err := s.Add(entry.TierShared, "note", "born signed", "body")
		Expect(err).NotTo(HaveOccurred())
		before := gitOut(s.dir, "rev-parse", entryRef(entry.TierShared, id))

		res, err := s.Attest([]entity.Id{id})
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(ContainSubstring("already"))
		Expect(res[0].Attested).To(BeFalse())
		Expect(gitOut(s.dir, "rev-parse", entryRef(entry.TierShared, id))).To(Equal(before),
			"the refusal must not have written anything")
	})

	It("refuses an entry with a bad signature in its history", func() {
		// The tamper needs a signature to invalidate, so this entry is born
		// signed and then altered underneath it.
		id, err := s.Add(entry.TierShared, "note", "tampered", "body")
		Expect(err).NotTo(HaveOccurred())
		ref := entryRef(entry.TierShared, id)
		gitOut(s.dir, "update-ref", ref, forgeTamperedSignature(s.dir, ref))
		before := gitOut(s.dir, "rev-parse", ref)

		res, err := s.Attest([]entity.Id{id})
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(ContainSubstring("bad signature"))
		Expect(res[0].Attested).To(BeFalse())
		Expect(gitOut(s.dir, "rev-parse", ref)).To(Equal(before),
			"vouching for altered content must write nothing")
	})

	It("refuses a held system-tier entry", func() {
		id := quarantinedEntry(s)
		ref := entryRef(entry.TierQuarantine, id)
		before := gitOut(s.dir, "rev-parse", ref)

		res, err := s.Attest([]entity.Id{id})
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(ContainSubstring("approve or reject"))
		Expect(res[0].Attested).To(BeFalse())
		Expect(gitOut(s.dir, "rev-parse", ref)).To(Equal(before),
			"material awaiting review must not be vouched for")
	})

	It("PERMITS a foreign author, unlike resign, and says so in the claim", func() {
		id := entryWithForeignOperations(s)
		res, err := s.Attest([]entity.Id{id})
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(BeEmpty())
		Expect(res[0].Claim).To(Equal(entry.ClaimReceived))
	})

	// The user-visible symptom the Authors() attestation exclusion exists for.
	// A peer attesting my entry adds an operation authored by them, so counting
	// attesters would make my OWN next attestation of my OWN work record the
	// weaker "received" claim. Re-attestation is the prescribed repair after a
	// key expires or is revoked, so this is an ordinary path, not a corner.
	It("still claims authorship after a peer has attested the entry", func() {
		id := addUnsignedThenEnableSigning(s)

		GinkgoT().Setenv("KREF_AUTHOR_NAME", "Someone Else")
		GinkgoT().Setenv("KREF_AUTHOR_EMAIL", "else@example.com")
		other, err := Open(s.dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = other.Close() })
		peer, err := other.Attest([]entity.Id{id})
		Expect(err).NotTo(HaveOccurred())
		Expect(peer[0].Reason).To(BeEmpty())
		Expect(other.Close()).To(Succeed())

		// Their attestation covers the history beneath it, so the entry now reads
		// as good and there is nothing left to attest. Give it something: one more
		// unsigned operation, written by ME, above their attestation.
		GinkgoT().Setenv("KREF_AUTHOR_NAME", testSignerName)
		GinkgoT().Setenv("KREF_AUTHOR_EMAIL", testSignerEmail)
		gitConfig(s.dir, "kref.sign", "false")
		mine, err := Open(s.dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(mine.AddLabel(id, "later")).To(Succeed())
		Expect(mine.Close()).To(Succeed())
		gitConfig(s.dir, "kref.sign", "true")

		res, err := s.Attest([]entity.Id{id})
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(BeEmpty())
		Expect(res[0].Claim).To(Equal(entry.ClaimAuthored))
	})

	It("claims authorship when every operation is ours", func() {
		id := addUnsignedThenEnableSigning(s)
		res, err := s.Attest([]entity.Id{id})
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Claim).To(Equal(entry.ClaimAuthored))
	})

	It("fast-forwards, so the entry id and links survive", func() {
		id := addUnsignedThenEnableSigning(s)
		before, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Attest([]entity.Id{id})).Error().NotTo(HaveOccurred())
		after, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.ID).To(Equal(before.ID))
		Expect(after.Body).To(Equal(before.Body))
		Expect(after.Links).To(Equal(before.Links))
	})

	// Every error path returns what already landed: an earlier entry's
	// attestation is on disk by the time a later one fails, and discarding the
	// results leaves mutations with no record of which happened.
	It("returns the entries it already attested when a later one fails", func() {
		id := addUnsignedThenEnableSigning(s)

		res, err := s.Attest([]entity.Id{id, absentID})
		Expect(err).To(MatchError(ContainSubstring("attest " + absentID.String())))
		Expect(res).To(HaveLen(1))
		Expect(res[0].ID).To(Equal(id))
		Expect(res[0].Attested).To(BeTrue())
		Expect(res[0].NewTip).To(Equal(gitOut(s.dir, "rev-parse", entryRef(entry.TierShared, id))))
	})
})
