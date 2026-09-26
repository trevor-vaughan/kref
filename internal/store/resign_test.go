package store

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

// absentID is a well-formed entry id that no store holds, so locate fails on it
// the way a real mid-sweep git error would.
const absentID = entity.Id("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

// unsignedHistory builds an entry with two operation-pack commits while signing
// is OFF, then turns signing on and reopens — the exact situation `kref resign`
// exists for: material written before the key was configured.
func unsignedHistory(dir string) (*Store, entity.Id) {
	GinkgoHelper()
	setupSSHSigningKey(dir, testSignerEmail)

	s, err := Init(dir, testSignerName, testSignerEmail)
	Expect(err).NotTo(HaveOccurred())
	id, err := s.Add(entry.TierShared, "spec", "Legacy", "first")
	Expect(err).NotTo(HaveOccurred())
	Expect(s.Update(id, "second", "")).To(Succeed())
	Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("N"))
	Expect(s.Close()).To(Succeed())

	gitConfig(dir, "commit.gpgsign", "true")
	reopened, err := Open(dir)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = reopened.Close() })
	return reopened, id
}

// unsignedProfileHistory builds an unsigned entry authored by the "work"
// identity profile, whose signing key and gpg.format live ONLY in that profile
// and never in the repo's own config. Resigning it can therefore only succeed if
// resign's git subprocesses inherit the profile as their global config layer,
// the way every other kref write already does.
func unsignedProfileHistory(dir string) (*Store, entity.Id) {
	GinkgoHelper()
	const email = "work@example.com"
	// Configures verification for the profile's principal only -- no signing
	// identity is written to the repo.
	key := writeSigningKey(dir, email)
	writeProfile("work", "[user]\n\tname = Work Me\n\temail = "+email+"\n"+
		"\tsigningkey = "+key+"\n[gpg]\n\tformat = ssh\n")

	s, err := Init(dir, testSignerName, testSignerEmail)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.Close()).To(Succeed())

	// The entry must be authored by the profile identity: resign refuses an
	// entry carrying another author's operations, which would mask this spec.
	gitConfig(dir, "kref.identity", "work")
	authored, err := Open(dir)
	Expect(err).NotTo(HaveOccurred())
	id, err := authored.Add(entry.TierShared, "spec", "Legacy", "first")
	Expect(err).NotTo(HaveOccurred())
	Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("N"))
	Expect(authored.Close()).To(Succeed())

	gitConfig(dir, "commit.gpgsign", "true")
	reopened, err := Open(dir)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = reopened.Close() })
	return reopened, id
}

var _ = Describe("resign", func() {
	// resign establishes a second `git commit-tree -S` path of its own. Without
	// the profile environment it resolves user.signingkey and gpg.format from
	// whatever ambient global config the user has, signing as one identity while
	// kref writes as another -- the exact split identity profiles exist to stop.
	It("signs with the active identity profile's key, not the ambient global config", func() {
		dir := gitRepo()
		s, id := unsignedProfileHistory(dir)

		res, err := s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(1))
		Expect(res[0].Reason).To(BeEmpty())
		Expect(res[0].Signed).To(Equal(1))

		state, err := s.SigState(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(entry.SigGood))
	})

	It("signs an entry's existing history in place, preserving id and content", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)

		res, err := s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(1))
		Expect(res[0].Reason).To(BeEmpty())
		Expect(res[0].Signed).To(Equal(2))

		// The id is derived from the op-pack blob, so it must not move.
		Expect(res[0].ID).To(Equal(id))
		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Legacy"))
		Expect(got.Body).To(Equal("second"))

		state, err := s.SigState(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(entry.SigGood))
	})

	// resign parks the old tip under refs/kref-resign-backup/, which sits inside
	// the refs/kref-* namespace tier discovery scans. Unless the name is
	// reserved, every resign invents a tier called "resign-backup" whose entry
	// refs carry an extra path segment, so resolving them fails and takes the
	// whole store read down with it.
	It("does not turn its own backup namespace into a discovered tier", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)
		_, err := s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())

		// Reopen: tiers are resolved at open time, so the phantom only appears
		// to the next process — which is every command after `kref resign`.
		Expect(s.Close()).To(Succeed())
		reopened, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })

		Expect(reopened.TierNames()).NotTo(ContainElement(entry.Tier("resign-backup")))

		// The consequence worth pinning: an unfiltered List walks every
		// discovered tier and resolves each entry's ref.
		all, err := reopened.List(ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(1))
	})

	It("records the resign in the entry's provenance, so it is not mistaken for signed-at-write", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)
		_, err := s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())

		log, err := s.Log(id)
		Expect(err).NotTo(HaveOccurred())
		var triggers []string
		for _, le := range log {
			if le.Op == "origin" {
				triggers = append(triggers, le.Detail)
			}
		}
		Expect(triggers).To(ContainElement(ContainSubstring("resign")))
	})

	It("leaves a backup ref so the rewrite is reversible", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)
		before := gitOut(dir, "rev-parse", tierRef(entry.TierShared, id.String()))

		_, err := s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())

		backup := gitOut(dir, "rev-parse", resignBackupRef(entry.TierShared, id))
		Expect(backup).To(Equal(before))
	})

	It("refuses an entry that has already been pushed", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)
		ref := tierRef(entry.TierShared, id.String())
		gitOut(dir, "update-ref", pushedRef(entry.TierShared, id), gitOut(dir, "rev-parse", ref))

		res, err := s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(1))
		Expect(res[0].Reason).To(ContainSubstring("pushed"))
		// The refusal is a dead end unless it names the way forward: published
		// history can never be rewritten, only attested to.
		Expect(res[0].Reason).To(ContainSubstring("kref attest"))
		Expect(res[0].Signed).To(BeZero())
		Expect(sigStatus(dir, ref)).To(Equal("N"), "the refusal must not have rewritten anything")
	})

	It("rewrites a pushed entry under --force", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)
		ref := tierRef(entry.TierShared, id.String())
		gitOut(dir, "update-ref", pushedRef(entry.TierShared, id), gitOut(dir, "rev-parse", ref))

		res, err := s.Resign([]entity.Id{id}, false, true, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(BeEmpty())
		Expect(sigStatus(dir, ref)).To(Equal("G"))
		// The one rewrite that cannot be published afterwards, so the caller has
		// to be able to tell it apart from an ordinary one.
		Expect(res[0].Forced).To(BeTrue())
	})

	It("does not mark an ordinary rewrite as forced, even when --force is passed", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir) // never pushed
		res, err := s.Resign([]entity.Id{id}, false, true, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(BeEmpty())
		Expect(res[0].Forced).To(BeFalse(), "nothing was overridden, so nothing to warn about")
	})

	It("changes nothing on a dry run but still reports the plan", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)
		ref := tierRef(entry.TierShared, id.String())
		before := gitOut(dir, "rev-parse", ref)

		res, err := s.Resign([]entity.Id{id}, true, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Signed).To(Equal(2))
		Expect(res[0].Reason).To(BeEmpty())

		Expect(gitOut(dir, "rev-parse", ref)).To(Equal(before))
		Expect(sigStatus(dir, ref)).To(Equal("N"))
	})

	// Signing someone else's operations with our key would misrepresent them,
	// and the commit ident cannot be used to tell whose they are: git-bug leaves
	// it empty. The operation pack's author is the only honest source.
	It("refuses an entry containing another author's operations", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)

		GinkgoT().Setenv("KREF_AUTHOR_NAME", "Someone Else")
		GinkgoT().Setenv("KREF_AUTHOR_EMAIL", "else@example.com")
		other, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = other.Close() })
		Expect(other.AddLabel(id, "theirs")).To(Succeed())

		ref := tierRef(entry.TierShared, id.String())
		before := gitOut(dir, "rev-parse", ref)

		res, err := s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(ContainSubstring("else@example.com"))
		Expect(res[0].Signed).To(BeZero())
		// The label commit above was itself written with signing on, so the tip
		// is legitimately signed; what must hold is that resign left it alone.
		Expect(gitOut(dir, "rev-parse", ref)).To(Equal(before))
	})

	// The tips are the operator's undo handle: without them, output that says an
	// entry was rewritten gives no way back to what it was rewritten from.
	It("reports the tips the rewrite moved between", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)
		ref := tierRef(entry.TierShared, id.String())
		before := gitOut(dir, "rev-parse", ref)

		res, err := s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].OldTip).To(Equal(before))
		Expect(res[0].NewTip).NotTo(Equal(res[0].OldTip))
		// OldTip is exactly what the backup ref holds, which is what makes the
		// reported value usable as an undo handle rather than a curiosity.
		Expect(gitOut(dir, "rev-parse", resignBackupRef(entry.TierShared, id))).
			To(Equal(res[0].OldTip))
		// NewTip is where the REWRITE landed, not where the ref ends up: the
		// `origin ... resign` provenance record is a further operation committed
		// on top of it. So the ref must contain NewTip without equalling it.
		Expect(gitOut(dir, "rev-list", ref)).To(ContainSubstring(res[0].NewTip))
		Expect(gitOut(dir, "rev-parse", ref)).NotTo(Equal(res[0].NewTip))
	})

	It("reports the old tip but no new one for a dry run, which moves nothing", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)

		res, err := s.Resign([]entity.Id{id}, true, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].OldTip).To(Equal(gitOut(dir, "rev-parse", tierRef(entry.TierShared, id.String()))))
		Expect(res[0].NewTip).To(BeEmpty())
	})

	// Every other spec here resigns a single id, so nothing pins that a batch
	// keeps one result per entry, in order, with each entry's own outcome.
	It("keeps one result per entry, in order, across a mixed batch", func() {
		dir := gitRepo()
		s, mine := unsignedHistory(dir)

		theirs, err := s.Add(entry.TierShared, "spec", "Theirs", "b")
		Expect(err).NotTo(HaveOccurred())
		GinkgoT().Setenv("KREF_AUTHOR_NAME", "Someone Else")
		GinkgoT().Setenv("KREF_AUTHOR_EMAIL", "else@example.com")
		other, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = other.Close() })
		Expect(other.AddLabel(theirs, "theirs")).To(Succeed())

		res, err := s.Resign([]entity.Id{theirs, mine}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(2))
		Expect(res[0].ID).To(Equal(theirs))
		Expect(res[0].Reason).To(ContainSubstring("else@example.com"))
		Expect(res[0].Signed).To(BeZero())
		Expect(res[1].ID).To(Equal(mine))
		Expect(res[1].Reason).To(BeEmpty())
		Expect(res[1].Signed).To(Equal(2))
	})

	// A held write is the reviewer's to approve or reject; rewriting it would
	// sign material nobody has agreed to keep. --all skips these, but naming one
	// by id reaches it, so the guard has to hold on its own.
	It("refuses an entry held in a system tier", func() {
		dir := gitRepo()
		s, _ := unsignedHistory(dir)
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "b", "",
			[]scan.Finding{{RuleID: "github-pat", Description: "GitHub PAT", StartLine: 1}}, "", "human")
		Expect(err).NotTo(HaveOccurred())

		ref := tierRef(entry.TierQuarantine, parked.ItemID.String())
		before := gitOut(dir, "rev-parse", ref)

		res, err := s.Resign([]entity.Id{parked.ItemID}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(1))
		Expect(res[0].Reason).To(ContainSubstring("approve or reject"))
		Expect(res[0].Signed).To(BeZero())
		Expect(gitOut(dir, "rev-parse", ref)).To(Equal(before),
			"the refusal must not have rewritten anything")

		ids, err := s.ResignableIDs()
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).NotTo(ContainElement(parked.ItemID), "a sweep must not reach it either")
	})

	// --force exists for exactly one refusal: the pushed one, where the damage is
	// to collaborators' copies and the backup ref makes it locally reversible.
	// The other two protect things --force must never reach, and the only thing
	// keeping it out is the ORDER of the gates in resignOne. Nothing else pins
	// that order, so a reshuffle would silently turn --force into a way to sign
	// someone else's work, or to rewrite material awaiting human review.
	It("does not let --force sign another author's operations", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)

		GinkgoT().Setenv("KREF_AUTHOR_NAME", "Someone Else")
		GinkgoT().Setenv("KREF_AUTHOR_EMAIL", "else@example.com")
		other, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = other.Close() })
		Expect(other.AddLabel(id, "theirs")).To(Succeed())

		ref := tierRef(entry.TierShared, id.String())
		before := gitOut(dir, "rev-parse", ref)

		res, err := s.Resign([]entity.Id{id}, false, true, nil) // force = true
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(ContainSubstring("else@example.com"))
		Expect(res[0].Signed).To(BeZero())
		Expect(gitOut(dir, "rev-parse", ref)).To(Equal(before))
	})

	It("does not let --force rewrite an entry held for review", func() {
		dir := gitRepo()
		s, _ := unsignedHistory(dir)
		parked, err := s.QuarantineNewEntry(entry.TierShared, "spec", "Draft", "b", "",
			[]scan.Finding{{RuleID: "github-pat", Description: "GitHub PAT", StartLine: 1}}, "", "human")
		Expect(err).NotTo(HaveOccurred())

		ref := tierRef(entry.TierQuarantine, parked.ItemID.String())
		before := gitOut(dir, "rev-parse", ref)

		res, err := s.Resign([]entity.Id{parked.ItemID}, false, true, nil) // force = true
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(ContainSubstring("approve or reject"))
		Expect(res[0].Signed).To(BeZero())
		Expect(gitOut(dir, "rev-parse", ref)).To(Equal(before))
	})

	// A batch is a composite of independent rewrites, each landing on disk as it
	// goes. Discarding the completed ones when a later entry fails leaves the
	// operator unable to tell which refs moved -- and they HAVE moved.
	It("returns the entries it already rewrote when a later one fails", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)
		ref := tierRef(entry.TierShared, id.String())

		res, err := s.Resign([]entity.Id{id, absentID}, false, false, nil)
		Expect(err).To(HaveOccurred())
		Expect(res).To(HaveLen(1), "the completed rewrite must be reported, not discarded")
		Expect(res[0].ID).To(Equal(id))
		Expect(res[0].Reason).To(BeEmpty())
		Expect(sigStatus(dir, ref)).To(Equal("G"), "its rewrite really did land")
	})

	// resignOne surfaces bare git errors (RefExist, the locked rewrite) with no
	// entry id, so a sweep that trips over one entry reports a raw git failure the
	// operator cannot attribute.
	It("names the entry that failed", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir)

		_, err := s.Resign([]entity.Id{id, absentID}, false, false, nil)
		Expect(err).To(MatchError(MatchRegexp(`^resign ` + absentID.String() + `: `)))
	})

	// A sweep re-derives and re-signs every commit of every entry through its own
	// git subprocesses, so it can run for a long time. Without a hook the caller
	// has nothing to report while it does.
	It("reports progress as a sweep advances", func() {
		dir := gitRepo()
		s, first := unsignedHistory(dir)
		second, err := s.Add(entry.TierShared, "spec", "Another", "body")
		Expect(err).NotTo(HaveOccurred())

		type step struct {
			done, total int
			id          entity.Id
		}
		var steps []step
		_, err = s.Resign([]entity.Id{first, second}, false, false,
			func(done, total int, id entity.Id) {
				steps = append(steps, step{done, total, id})
			})
		Expect(err).NotTo(HaveOccurred())
		Expect(steps).To(Equal([]step{{1, 2, first}, {2, 2, second}}))
	})

	// Store.Signing() is false both when signing is off and when kref could not
	// install the signing decorator at all. Collapsing the two tells someone whose
	// key is already configured to go configure it, which is the one thing that is
	// not broken.
	It("says signature support is missing rather than blaming the signing config", func() {
		dir := gitRepo()
		s, id := unsignedHistory(dir) // key configured, commit.gpgsign on
		s.repo = unsupportedSigRepo{s.repo}
		s.sigUnavailable = "signing and signature verification are unavailable: /x could not be reopened"

		_, err := s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).To(MatchError(ContainSubstring("could not be reopened")))
		Expect(err).NotTo(MatchError(ContainSubstring("commit.gpgsign")))
	})

	It("refuses to run at all when the repo is not configured to sign", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail) // key present, signing off
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())

		_, err = s.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).To(MatchError(ContainSubstring("not configured to sign")))
	})
})
