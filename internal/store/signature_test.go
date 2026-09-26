package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// forgeTamperedSignature rewrites rev's commit message while keeping its
// signature header, and returns the new hash. The signature no longer covers
// the content, which is what git reports as BADSIG.
//
// It alters the MESSAGE rather than the tree on purpose: the tree carries the
// operation pack, so replacing it would make the entry unreadable and the spec
// would never reach the verdict it is testing.
func forgeTamperedSignature(dir, rev string) string {
	GinkgoHelper()
	headers := gitOut(dir, "cat-file", "commit", rev)
	Expect(headers).To(ContainSubstring("gpgsig"), "precondition: rev must be signed")

	// gitOut trims the trailing blank line, so headers ends at the signature's
	// END marker; re-add the separator before the tampered message.
	return gitWithStdin(dir, headers+"\n\ntampered\n",
		"hash-object", "-t", "commit", "-w", "--stdin")
}

var _ = Describe("signature verification", func() {
	// Every case builds one entry and asks the store for its verdict.
	verdict := func(configure func(dir string)) entry.SigState {
		GinkgoHelper()
		dir := gitRepo()
		configure(dir)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Subject", "body")
		Expect(err).NotTo(HaveOccurred())

		got, err := s.SigState(id)
		Expect(err).NotTo(HaveOccurred())
		return got
	}

	It("reports an unsigned entry as unsigned", func() {
		Expect(verdict(func(dir string) {
			setupSSHSigningKey(dir, testSignerEmail)
		})).To(Equal(entry.SigUnsigned))
	})

	It("reports a verifiable signature as good", func() {
		Expect(verdict(func(dir string) {
			enableSSHSigning(dir, testSignerEmail)
		})).To(Equal(entry.SigGood))
	})

	// The verdict describes the ENTRY, and an entry is its whole operation
	// chain. Reading only the ref tip meant one ordinary edit after turning
	// signing on made history written before the key report `good` -- and drop
	// out of `list --unsigned`, so it could never be found or resigned again.
	// "This entry is attested" has to mean every operation in it, or it means
	// nothing.
	It("reports unsigned history as unsigned even when the newest operation is signed", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail) // key available, signing OFF

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierShared, "spec", "Legacy", "written before the key")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		gitConfig(dir, "commit.gpgsign", "true")
		signing, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = signing.Close() })
		Expect(signing.Update(id, "one ordinary edit, signed", "")).To(Succeed())

		// The tip really is signed; the point is that the entry is not.
		Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("G"))

		got, err := signing.SigState(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(entry.SigUnsigned))

		unsigned, err := signing.List(ListFilter{UnsignedOnly: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(unsigned).To(HaveLen(1), "an entry with unsigned history must stay findable")
	})

	// A signature that no longer covers its content anywhere in the chain is the
	// loudest thing kref can say, so it must win over a gap or an unverifiable
	// key elsewhere in the same entry.
	It("reports the worst state in the chain, not the newest", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "Tampered mid-chain", "body")
		Expect(err).NotTo(HaveOccurred())
		ref := tierRef(entry.TierShared, id.String())
		Expect(s.SigState(id)).To(Equal(entry.SigGood))

		// Break the TIP's signature, then add a fresh good commit on top of it.
		gitOut(dir, "update-ref", ref, forgeTamperedSignature(dir, ref))
		Expect(s.AddLabel(id, "later")).To(Succeed())
		Expect(sigStatus(dir, ref)).To(Equal("G"), "the newest commit is fine")

		got, err := s.SigState(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(entry.SigBad), "a bad signature anywhere must win")
	})

	// The regression that motivates reading signature PRESENCE from the commit
	// object: git's %G? reports a signed commit as "N" when it has no
	// allowed-signers file, indistinguishable from never having been signed.
	It("reports a signature it cannot check as untrusted, not unsigned", func() {
		Expect(verdict(func(dir string) {
			setupSSHSigningKey(dir, testSignerEmail)
			gitConfig(dir, "commit.gpgsign", "true")
			gitConfig(dir, "gpg.ssh.allowedSignersFile", "")
		})).To(Equal(entry.SigUntrusted))
	})

	// `untrusted` is five different situations wearing one label, and they need
	// five different next steps. These two are the ones a real setup produces:
	// ssh with no allowed-signers configured at all, and ssh with a signer who
	// is not listed in it. Measured letters, not guesses — N and U respectively.
	It("says verification could not run when ssh has no allowed-signers file", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)
		gitConfig(dir, "commit.gpgsign", "true")
		// UNSET, not empty: git treats an empty value as a configured path and
		// still answers U. Only an absent key makes it decline to look (N).
		Expect(gitConfigUnset(dir, "gpg.ssh.allowedSignersFile")).To(Succeed())

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())

		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.SigState).To(Equal(entry.SigUntrusted))
		Expect(got.SigReason).To(Equal(entry.SigReasonUnverifiable))
	})

	It("says the signer is unvouched-for when ssh has a file that omits them", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Get(id)).To(HaveField("SigReason", entry.SigReasonNone)) // vouched for

		// The file exists and is configured; the signer just is not in it — the
		// shape a new collaborator's entries actually take.
		empty := filepath.Join(GinkgoT().TempDir(), "allowed_signers")
		Expect(os.WriteFile(empty, nil, 0o600)).To(Succeed())
		gitConfig(dir, "gpg.ssh.allowedSignersFile", empty)

		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.SigState).To(Equal(entry.SigUntrusted))
		Expect(got.SigReason).To(Equal(entry.SigReasonKeyUntrusted))
	})

	// The list path resolves through the excerpt cache, so the reason has to
	// arrive there too or the marker cannot tell a revoked key from a new peer.
	It("carries the reason through the excerpt cache as well as Get", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)
		gitConfig(dir, "commit.gpgsign", "true")
		Expect(gitConfigUnset(dir, "gpg.ssh.allowedSignersFile")).To(Succeed())

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())

		excerpts, err := s.ListExcerpts(ListFilter{WithSigState: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(excerpts).To(HaveLen(1))
		Expect(excerpts[0].SigReason).To(Equal(entry.SigReasonUnverifiable))

		listed, err := s.List(ListFilter{WithSigState: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(listed[0].SigReason).To(Equal(entry.SigReasonUnverifiable))
	})

	// The excerpt cache must agree with a direct DAG read, or a list view and a
	// show of the same entry would disagree about its signature. List and
	// ListExcerpts must be ASKED for the verdict (WithSigState); Get always
	// resolves it, because it serves `kref show`, which always displays it.
	It("carries the verdict identically through Get, List and the excerpt cache", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Signed", "body")
		Expect(err).NotTo(HaveOccurred())

		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.SigState).To(Equal(entry.SigGood))

		listed, err := s.List(ListFilter{WithSigState: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(1))
		Expect(listed[0].SigState).To(Equal(entry.SigGood))

		excerpts, err := s.ListExcerpts(ListFilter{WithSigState: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(excerpts).To(HaveLen(1))
		Expect(excerpts[0].SigState).To(Equal(entry.SigGood))
	})

	// The counterpart to the ⚠ marker: bad and untrusted entries announce
	// themselves on every listing, so the filter exists to find the ones that
	// need `kref resign` — strictly the unsigned.
	It("filters to unsigned entries on both the cached and uncached paths", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Add(entry.TierShared, "spec", "Unsigned one", "b")
		Expect(err).NotTo(HaveOccurred())

		gitConfig(dir, "commit.gpgsign", "true")
		signing, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = signing.Close() })
		_, err = signing.Add(entry.TierShared, "spec", "Signed one", "b")
		Expect(err).NotTo(HaveOccurred())

		listed, err := signing.List(ListFilter{UnsignedOnly: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(1))
		Expect(listed[0].Title).To(Equal("Unsigned one"))

		excerpts, err := signing.ListExcerpts(ListFilter{UnsignedOnly: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(excerpts).To(HaveLen(1))
		Expect(excerpts[0].Title).To(Equal("Unsigned one"))

		// Third path, and the one that silently degrades: completion filters
		// cached excerpts through the same shared predicate, which no longer
		// answers UnsignedOnly because the cache carries no verdict. The call
		// above left every tier's cache fresh, so this takes the cached branch.
		forCompletion, err := signing.listForCompletion(ListFilter{UnsignedOnly: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(forCompletion).To(HaveLen(1))
		Expect(forCompletion[0].Title).To(Equal("Unsigned one"))
	})

	// Get resolves the verdict because it serves `kref show`. Callers that want
	// only the entry's content must not pay for it -- `kref list` runs Merged
	// over every row it just resolved, and the quarantine queue resolves each
	// held op's target title, so a verdict inside those would double or
	// reinstate the per-entry subprocess this change removes.
	It("compiles without a verdict for callers that need only content", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "Signed", "body")
		Expect(err).NotTo(HaveOccurred())

		lean, err := s.getUnverified(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(lean.Title).To(Equal("Signed"))
		Expect(lean.SigState).To(BeEmpty())

		full, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(full.SigState).To(Equal(entry.SigGood))
	})

	// The verdict depends on inputs outside the DAG -- the allowed-signers file,
	// key trust, expiry, revocation -- none of which move a ref. A verdict cached
	// against the ref OID therefore goes stale in place, and the human list view
	// keeps showing the trusting answer long after it stopped being true.
	It("does not serve a cached verdict after the verification config changes", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Add(entry.TierShared, "spec", "Signed", "body")
		Expect(err).NotTo(HaveOccurred())

		warm, err := s.ListExcerpts(ListFilter{WithSigState: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(warm).To(HaveLen(1))
		Expect(warm[0].SigState).To(Equal(entry.SigGood))

		// Break trust without touching a single ref: the signature is unchanged,
		// only the file it is checked against is.
		empty := filepath.Join(GinkgoT().TempDir(), "allowed_signers")
		Expect(os.WriteFile(empty, nil, 0o600)).To(Succeed())
		gitConfig(dir, "gpg.ssh.allowedSignersFile", empty)

		again, err := s.ListExcerpts(ListFilter{WithSigState: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(HaveLen(1))
		Expect(again[0].SigState).To(Equal(entry.SigUntrusted))
	})

	// Resolving a verdict costs a `git show --format=%G?` subprocess per signed
	// entry. compileSnapshot is the choke point every entry-to-Snapshot
	// conversion goes through, so charging it there bills every internal caller
	// -- Tidy, the quarantine queue, the kref.conf lookup, and the post-command
	// no-remote warning -- for a field none of them read.
	It("leaves the verdict unresolved for callers that do not ask for it", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Add(entry.TierShared, "spec", "Signed", "body")
		Expect(err).NotTo(HaveOccurred())

		plain, err := s.List(ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(plain).To(HaveLen(1))
		Expect(plain[0].SigState).To(BeEmpty())

		asked, err := s.List(ListFilter{WithSigState: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(asked).To(HaveLen(1))
		Expect(asked[0].SigState).To(Equal(entry.SigGood))

		// The excerpt-cache path carries its own copy of the opt-in guard, and
		// it is the one `kref list` actually takes for the table view.
		leanExcerpts, err := s.ListExcerpts(ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(leanExcerpts).To(HaveLen(1))
		Expect(leanExcerpts[0].SigState).To(BeEmpty())
	})

	It("reports a signature that no longer covers its content as bad", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Tampered", "body")
		Expect(err).NotTo(HaveOccurred())
		ref := tierRef(entry.TierShared, id.String())

		gitOut(dir, "update-ref", ref, forgeTamperedSignature(dir, ref))

		got, err := s.SigState(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(entry.SigBad))
	})

	// Verification shells out, so it can fail for reasons that have nothing to do
	// with the signature -- git missing, a corrupt object, a ref that moved out
	// from under the walk. That must surface as an error naming the entry, not as
	// a verdict, because reporting it as `untrusted` would blame the signature
	// for kref's own inability to look.
	It("reports a lookup failure as an error naming the entry, not as a verdict", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		id, err := s.Add(entry.TierShared, "spec", "Signed", "body")
		Expect(err).NotTo(HaveOccurred())

		s.repo = brokenSigRepo{s.repo}

		_, err = s.List(ListFilter{WithSigState: true})
		Expect(err).To(MatchError(ContainSubstring("signature state for " + id.String())))
		Expect(err).To(MatchError(ContainSubstring("no such object")))

		_, err = s.SigState(id)
		Expect(err).To(MatchError(ContainSubstring("no such object")))
	})

	It("reports no attestation for an entry that has none", func() {
		// The negative half of the probe's contract, and it needs nothing but
		// the probe. The positive half — write an attestation, find it on the
		// tip — lands with Store.Attest, which is the only thing that can
		// create one; proving the probe against a hand-forged commit would
		// test the forgery, not the writer.
		dir := gitRepo()
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "note", "unattested", "body")
		Expect(err).NotTo(HaveOccurred())

		repo, ok := s.repo.(*signingRepo)
		Expect(ok).To(BeTrue(), "fixture must be a signing store")

		hashes, err := repo.ListCommits(entryRef(entry.TierShared, id))
		Expect(err).NotTo(HaveOccurred())
		Expect(hashes).NotTo(BeEmpty())
		for _, h := range hashes {
			att, err := repo.commitAttests(h)
			Expect(err).NotTo(HaveOccurred())
			Expect(att).To(BeNil(), "nothing has attested to this entry")
		}
	})

	// Store.Attest is the only thing that can create an attestation, so the
	// round trip belongs here: it is what catches a JSON-tag drift between
	// commitAttests and git-bug's OpBase, which would otherwise fail silently.
	It("writes an attestation the probe can find on the tip", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id := addUnsignedThenEnableSigning(s)

		repo, ok := s.repo.(*signingRepo)
		Expect(ok).To(BeTrue(), "fixture must be a signing store")
		before, err := repo.ListCommits(entryRef(entry.TierShared, id))
		Expect(err).NotTo(HaveOccurred())

		Expect(s.Attest([]entity.Id{id})).Error().NotTo(HaveOccurred())

		after, err := repo.ListCommits(entryRef(entry.TierShared, id))
		Expect(err).NotTo(HaveOccurred())
		Expect(len(after)).To(BeNumerically(">", len(before)),
			"attesting fast-forwards; it must ADD a commit, not rewrite")

		att, err := repo.commitAttests(after[len(after)-1])
		Expect(err).NotTo(HaveOccurred())
		Expect(att).NotTo(BeNil(), "the tip commit carries the attestation")
		Expect(att.Claim).To(Equal(entry.ClaimAuthored))
		Expect(att.At).NotTo(BeZero(),
			"a zero time means the timestamp JSON tag drifted and unmarshalled to the epoch")
	})

	It("pins git-bug's ops tree entry name, which commitAttests restates", func() {
		dir := gitRepo()
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "note", "canary", "body")
		Expect(err).NotTo(HaveOccurred())
		repo, ok := s.repo.(*signingRepo)
		Expect(ok).To(BeTrue())

		hashes, err := repo.ListCommits(entryRef(entry.TierShared, id))
		Expect(err).NotTo(HaveOccurred())
		commit, err := repo.gg.CommitObject(plumbing.NewHash(hashes[0].String()))
		Expect(err).NotTo(HaveOccurred())
		tree, err := commit.Tree()
		Expect(err).NotTo(HaveOccurred())
		_, err = tree.File(opsTreeEntryName)
		Expect(err).NotTo(HaveOccurred(),
			"git-bug renamed its ops tree entry; commitAttests is now blind")
	})

	// Attest.Validate enforces the claim vocabulary, but git-bug runs operation
	// validation only from Commit -- read goes through operationPack.Validate,
	// which checks the pack's author and not its ops. So on a chain fetched from
	// a peer the claim is whatever bytes are in the blob, and the write-side
	// spec in the entry package proves nothing about this path.
	//
	// The commit is forged rather than written, because the writer cannot
	// produce this shape: s.Attest goes through Validate. It is left unsigned
	// because commitAttests reads the tree and never looks at the signature --
	// signature eligibility is a separate check, pinned by its own spec.
	It("refuses to read an attestation whose claim is outside the vocabulary", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id := addUnsignedThenEnableSigning(s)
		Expect(s.Attest([]entity.Id{id})).Error().NotTo(HaveOccurred())

		repo, ok := s.repo.(*signingRepo)
		Expect(ok).To(BeTrue())
		ref := tierRef(entry.TierShared, id.String())

		att, err := repo.commitAttests(repository.Hash(strings.TrimSpace(gitOut(dir, "rev-parse", ref))))
		Expect(err).NotTo(HaveOccurred())
		Expect(att).NotTo(BeNil(), "precondition: the tip must carry a readable attestation")

		forged := forgeAttestClaim(dir, ref, "vouched")
		got, err := repo.commitAttests(repository.Hash(forged))
		Expect(err).NotTo(HaveOccurred(), "an unknown claim must not break the read")
		Expect(got).To(BeNil(), "an unrecognised claim vouches for nothing")
	})
})

// forgeAttestClaim rewrites the operation blob of rev's commit so the
// attestation it carries names claim, and returns the new commit's hash. It is
// how a peer's chain could arrive carrying a claim this kref does not know --
// a shape the local writer cannot produce, because Attest.Validate refuses it.
func forgeAttestClaim(dir, rev, claim string) string {
	GinkgoHelper()
	ops := gitOut(dir, "cat-file", "blob", rev+":"+opsTreeEntryName)
	Expect(ops).To(ContainSubstring(`"claim":"authored"`),
		"precondition: rev must carry an authored attestation")
	blob := strings.TrimSpace(gitWithStdin(dir,
		strings.Replace(ops, `"claim":"authored"`, `"claim":"`+claim+`"`, 1),
		"hash-object", "-t", "blob", "-w", "--stdin"))

	var tree strings.Builder
	for line := range strings.SplitSeq(gitOut(dir, "ls-tree", rev), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasSuffix(line, "\t"+opsTreeEntryName) {
			line = "100644 blob " + blob + "\t" + opsTreeEntryName
		}
		tree.WriteString(line)
		tree.WriteString("\n")
	}
	treeHash := strings.TrimSpace(gitWithStdin(dir, tree.String(), "mktree"))
	return strings.TrimSpace(gitOut(dir, "commit-tree", treeHash, "-p", rev+"^", "-m", "forged claim"))
}

// A canary, not a feature test. signatureState decides whether a commit is
// signed by looking for a `gpgsig` header through go-git, which recognises
// `gpgsig-sha256` but deliberately does not expose it. A SHA-256 repository
// writes ONLY that header, so kref would report signed entries there as
// `unsigned` — under-claiming rather than over-claiming, but wrong.
//
// It cannot happen today because go-git refuses the objectformat extension, so
// kref never opens such a repo at all. This asserts that refusal. If it ever
// starts failing — go-git gains SHA-256 support, or kref is built with
// `-tags sha256` — the presence check needs the second header BEFORE anything
// else here will be correct.
var _ = Describe("SHA-256 repositories", func() {
	It("cannot be opened at all, which is what keeps the gpgsig presence check safe", func() {
		dir := GinkgoT().TempDir()
		if out, err := exec.Command("git", "init", "-q", "--object-format=sha256", dir).CombinedOutput(); err != nil {
			Skip("git here cannot create a SHA-256 repository: " + string(out))
		}
		_, err := Open(dir)
		Expect(err).To(MatchError(ContainSubstring("objectformat")),
			"kref can now open a SHA-256 repo — signatureState must learn gpgsig-sha256")
	})
})

// The whole point of resolving on demand is that no verdict may be stored
// against a ref tip. Nothing in the type system enforces that: Excerpt.SigState
// is an exported field inside the gob-encoded cache, so it would persist happily
// if a build path ever handed toExcerpt a resolved snapshot. This asserts the
// invariant against the BYTES on disk, which is the only place it can be
// checked, and holds the two build paths honest.
var _ = Describe("excerpt cache signature persistence", func() {
	It("never writes a signature verdict to the on-disk cache", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Add(entry.TierShared, "spec", "Signed", "body")
		Expect(err).NotTo(HaveOccurred())

		// Resolve verdicts through every path that fills them in, so anything
		// that wrote them back would have done so by now.
		_, err = s.ListExcerpts(ListFilter{WithSigState: true})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.ListExcerpts(ListFilter{UnsignedOnly: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(s.excerpts.refreshAll()).To(Succeed())

		dc, err := loadDiskCache(s.repo.LocalStorage(), entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		Expect(dc.Excerpts).NotTo(BeEmpty())
		for id, e := range dc.Excerpts {
			Expect(e.SigState).To(Equal(entry.SigUnresolved),
				"cached %s carries a verdict that no ref change can invalidate", id)
			Expect(e.SigReason).To(Equal(entry.SigReasonNone),
				"cached %s carries a verdict REASON, which is just as stale", id)
		}

		// And the in-memory copy the cache hands out on the next read, which is
		// the one a resolve pass mutates.
		fresh, err := s.excerpts.ensureFresh(entry.TierShared)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range fresh.Excerpts {
			Expect(e.SigState).To(Equal(entry.SigUnresolved))
		}
	})
})

// An attestation is the answer for history that can no longer be rewritten: it
// is a signed commit, and a commit hash already commits to everything beneath
// it, so its own signature covers its whole ancestry. Coverage is git ancestry
// and nothing else — there is no digest, because a second Merkle structure over
// the same data is only something that can disagree with the first.
var _ = Describe("attested history", func() {
	var s *Store

	BeforeEach(func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		var err error
		s, err = Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
	})

	It("absorbs an unsigned prefix, so adopting signing late works", func() {
		id := addUnsignedThenEnableSigning(s)
		Expect(s.Attest([]entity.Id{id})).Error().NotTo(HaveOccurred())

		state, reason, att, err := s.refSigState(entryRef(entry.TierShared, id))
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(entry.SigGood))
		Expect(reason).To(Equal(entry.SigReasonNone))
		Expect(att).NotTo(BeNil(), "an attestation did the absorbing")
	})

	// This chain cannot be built through Store.Attest — its third gate refuses to
	// attest over a bad signature. It CAN arrive from a peer: an attacker who
	// tampers a commit and appends their own attestation on top pushes a chain in
	// exactly this shape, and a fetch accepts it. chainState is the only thing
	// between that and a `good` verdict, so build the shape directly rather than
	// through the gated writer.
	//
	// Order matters: tamper FIRST, then append the attestation, so the tampered
	// commit lands inside the attestation's ancestry. A commit cannot be tampered
	// in place once it has children — its hash changes.
	It("does NOT absorb a bad signature", func() {
		id, err := s.Add(entry.TierShared, "note", "born signed", "body")
		Expect(err).NotTo(HaveOccurred())
		ref := tierRef(entry.TierShared, id.String())
		Expect(s.SigState(id)).To(Equal(entry.SigGood))

		gitOut(s.dir, "update-ref", ref, forgeTamperedSignature(s.dir, ref))
		Expect(s.mutate(id, func(e *entry.Entry) error {
			e.Append(entry.NewAttest(s.author, entry.ClaimAuthored))
			return nil
		})).To(Succeed())

		state, _, _, err := s.refSigState(ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(entry.SigBad),
			"an attestation must never mask a commit altered after signing")
	})

	It("absorbs nothing when the attestation itself is untrusted", func() {
		id := addUnsignedThenEnableSigning(s)
		Expect(s.Attest([]entity.Id{id})).Error().NotTo(HaveOccurred())
		untrustAllKeys(s.dir)

		state, _, _, err := s.refSigState(entryRef(entry.TierShared, id))
		Expect(err).NotTo(HaveOccurred())
		Expect(state).NotTo(Equal(entry.SigGood))
	})

	It("does not cover an operation appended AFTER it", func() {
		id := addUnsignedThenEnableSigning(s)
		Expect(s.Attest([]entity.Id{id})).Error().NotTo(HaveOccurred())
		stopSigning(s)
		Expect(s.Update(id, "edited after the attestation", "")).To(Succeed())

		state, _, _, err := s.refSigState(entryRef(entry.TierShared, id))
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(entry.SigUnsigned),
			"coverage is ancestry; a later commit is outside it")
	})

	It("reports no attester for an ordinarily-signed chain", func() {
		id, err := s.Add(entry.TierShared, "note", "born signed", "body")
		Expect(err).NotTo(HaveOccurred())
		state, _, att, err := s.refSigState(entryRef(entry.TierShared, id))
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(entry.SigGood))
		Expect(att).To(BeNil(), "nothing was absorbed, so nothing vouched")
	})

	It("names the attester on the resolved snapshot", func() {
		id := addUnsignedThenEnableSigning(s)
		Expect(s.Attest([]entity.Id{id})).Error().NotTo(HaveOccurred())

		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.SigState).To(Equal(entry.SigGood))
		// The VALUE, not just non-emptiness. This is what separates "read from
		// the signature" from "read from the payload": git-bug leaves the commit
		// author ident empty and kref stamps it, so %an and %GS can both be
		// non-empty while naming different things. Asserting the signing
		// principal is what makes swapping %GS for %an fail here rather than
		// only in the tag-gated e2e suite.
		Expect(snap.AttestedBy).To(Equal(testSignerEmail),
			"the attester must be the key git verified, not the payload's author")
		Expect(snap.AttestedAt).NotTo(BeZero())
		Expect(snap.AttestedClaim).To(Equal(entry.ClaimAuthored))
	})

	It("leaves the attested fields empty for an ordinarily-signed entry", func() {
		id, err := s.Add(entry.TierShared, "note", "born signed", "body")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.SigState).To(Equal(entry.SigGood))
		Expect(snap.AttestedBy).To(BeEmpty())
	})
})

// brokenSigRepo is a repo whose signature lookups always fail. kref reaches
// signature support by type-asserting its repo handle to sigReader, so shadowing
// that one method is enough to drive the failure path without breaking any of
// the reads that get the spec to it.
type brokenSigRepo struct {
	repository.ClockedRepo
}

func (brokenSigRepo) chainState(string) (entry.SigState, entry.SigReason, *entry.Attestation, error) {
	return "", entry.SigReasonNone, nil, errors.New("read commit: no such object")
}
func (brokenSigRepo) commitAttests(repository.Hash) (*entry.Attestation, error) {
	return nil, errors.New("read commit: no such object")
}
func (brokenSigRepo) signing() bool        { return true }
func (brokenSigRepo) signingEnv() []string { return nil }

// kref reaches signature support by type-ASSERTING to sigReader, so a fake that
// drifts out of the interface stops being used without any error: the assertion
// just fails and the caller takes the no-signature-support path. This spec file
// lost an error path that way once. Assert the shape at compile time instead.
var _ sigReader = brokenSigRepo{}

// unsupportedSigRepo is a repo with NO signature support: embedding the
// interface hides everything the dynamic type has beyond it, so the sigReader
// assertion fails — the same shape newSigningRepo returns when it cannot open
// its second go-git handle.
type unsupportedSigRepo struct {
	repository.ClockedRepo
}
