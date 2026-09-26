package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// The identity the signing specs sign as. It must match the principal in the
// allowed-signers file, because that is what git checks a signature against.
const (
	testSignerName  = "Tester"
	testSignerEmail = "tester@example.com"
)

// gitConfig sets one repo-local git config key.
func gitConfig(dir, key, value string) {
	GinkgoHelper()
	out, err := exec.Command("git", "-C", dir, "config", "--local", key, value).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
}

// gitOut runs a git command in dir and returns its trimmed stdout.
//
// It pins the author and committer explicitly: the test driver exports
// GIT_COMMITTER_EMAIL for isolation, and git's environment beats repo config, so
// without this a commit created here would be signed as the driver's identity
// and fail to match the allowed-signers principal.
func gitOut(dir string, args ...string) string {
	GinkgoHelper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+testSignerName, "GIT_AUTHOR_EMAIL="+testSignerEmail,
		"GIT_COMMITTER_NAME="+testSignerName, "GIT_COMMITTER_EMAIL="+testSignerEmail,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	return strings.TrimSpace(string(out))
}

// writeSigningKey generates an ed25519 key outside the repo, points dir's
// allowed-signers at it for the given principal, and returns the private key
// path. It configures verification but NOT the signing identity, so a caller can
// supply that from wherever it is testing (repo config, identity profile).
func writeSigningKey(dir, email string) string {
	GinkgoHelper()
	keyDir := GinkgoT().TempDir()
	key := filepath.Join(keyDir, "id_ed25519")

	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", email, "-f", key).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))

	pub, err := os.ReadFile(key + ".pub")
	Expect(err).NotTo(HaveOccurred())
	allowed := filepath.Join(keyDir, "allowed_signers")
	Expect(os.WriteFile(allowed, append([]byte(email+" "), pub...), 0o600)).To(Succeed())
	gitConfig(dir, "gpg.ssh.allowedSignersFile", allowed)
	return key
}

// setupSSHSigningKey gives dir a usable SSH signing identity — key, allowed
// signers, and the three gpg.*/user.* keys — WITHOUT turning signing on, so
// specs can exercise the policy separately from the mechanism.
//
// SSH is the format git-bug's PGP-only path structurally cannot reach, which
// makes it the regression test for the whole decorator. The key lives outside
// the repo so it is never a candidate for ingest or the secret scanner.
func setupSSHSigningKey(dir, email string) {
	GinkgoHelper()
	key := writeSigningKey(dir, email)
	// A real repo always has these; raw `git commit-tree` calls in specs need
	// them, and the allowed-signers principal is matched against the email.
	gitConfig(dir, "user.name", testSignerName)
	gitConfig(dir, "user.email", email)
	gitConfig(dir, "gpg.format", "ssh")
	gitConfig(dir, "user.signingkey", key)
}

// enableSSHSigning configures an SSH signing identity and switches signing on
// through git's own commit.gpgsign.
func enableSSHSigning(dir, email string) {
	GinkgoHelper()
	setupSSHSigningKey(dir, email)
	gitConfig(dir, "commit.gpgsign", "true")
}

// addUnsignedThenEnableSigning gives an already-signing store an entry whose
// existing commits are genuinely unsigned, and returns its id — the situation
// resign and attest both exist for: material written before the key was
// configured.
//
// The entry is written through a SECOND store opened with kref.sign off, rather
// than by reaching into s: the commits then take git-bug's own unsigned write
// path, so their lack of a signature is real rather than simulated. s keeps
// signing, because that is what the caller is about to test.
func addUnsignedThenEnableSigning(s *Store) entity.Id {
	GinkgoHelper()
	gitConfig(s.dir, "kref.sign", "false")

	unsigned, err := Open(s.dir)
	Expect(err).NotTo(HaveOccurred())
	Expect(unsigned.Signing()).To(BeFalse(), "precondition: the entry must be written unsigned")
	id, err := unsigned.Add(entry.TierShared, "spec", "Legacy", "written before the key")
	Expect(err).NotTo(HaveOccurred())
	Expect(unsigned.Close()).To(Succeed())

	gitConfig(s.dir, "kref.sign", "true")
	Expect(sigStatus(s.dir, tierRef(entry.TierShared, id.String()))).To(Equal("N"))
	Expect(s.Signing()).To(BeTrue())
	return id
}

// untrustAllKeys leaves dir's allowed-signers file in place but EMPTY, so every
// ssh signature verifies as "U": the key is readable, nobody has vouched for it.
//
// Unsetting gpg.ssh.allowedSignersFile would answer "N" instead — verification
// could not run at all — and the two are different situations. The interesting
// one for an attestation is a signature that is genuinely present and genuinely
// not trusted, because that is what a rotated or unshared key looks like.
func untrustAllKeys(dir string) {
	GinkgoHelper()
	empty := filepath.Join(GinkgoT().TempDir(), "allowed_signers")
	Expect(os.WriteFile(empty, nil, 0o600)).To(Succeed())
	gitConfig(dir, "gpg.ssh.allowedSignersFile", empty)
}

// stopSigning switches an already-open store's writes back to unsigned, so a
// spec can append an operation that the preceding attestation cannot cover.
//
// The signing decision is read once when the repo handle is built, so the config
// key alone would not reach a live store; both are set, and the result is
// asserted rather than assumed.
func stopSigning(s *Store) {
	GinkgoHelper()
	gitConfig(s.dir, "kref.sign", "false")
	sr, ok := s.repo.(*signingRepo)
	Expect(ok).To(BeTrue(), "precondition: the store must have signature support")
	sr.sign = false
	Expect(s.Signing()).To(BeFalse())
}

// gitWithStdin is gitOut with stdin supplied, for plumbing that reads objects
// from a pipe.
func gitWithStdin(dir, stdin string, args ...string) string {
	GinkgoHelper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+testSignerName, "GIT_AUTHOR_EMAIL="+testSignerEmail,
		"GIT_COMMITTER_NAME="+testSignerName, "GIT_COMMITTER_EMAIL="+testSignerEmail,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	return strings.TrimSpace(string(out))
}

// sigStatus returns git's one-character signature verdict for a commit-ish:
// "G" good, "B" bad, "U" good but untrusted, "E" cannot check, "N" unsigned.
func sigStatus(dir, rev string) string {
	GinkgoHelper()
	out, err := exec.Command("git", "-C", dir, "show", "-s", "--format=%G?", rev).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	return strings.TrimSpace(string(out))
}

func tierRef(t entry.Tier, id string) string {
	return "refs/" + t.Namespace() + "/" + id
}

var _ = Describe("native git commit signing", func() {
	It("signs operation-pack commits so git verify-commit accepts them", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Signed", "body")
		Expect(err).NotTo(HaveOccurred())

		ref := tierRef(entry.TierShared, id.String())
		out, err := exec.Command("git", "-C", dir, "verify-commit", ref).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		Expect(sigStatus(dir, ref)).To(Equal("G"))

		// A signed entry must still be readable: go-git has to carry the
		// non-PGP signature header as an opaque value.
		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Signed"))
		Expect(got.Body).To(Equal("body"))
	})

	It("leaves commits unsigned when nothing asks for signing", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail) // key available but unused

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Unsigned", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("N"))
	})

	It("honours git's non-Go boolean spellings for commit.gpgsign", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)
		gitConfig(dir, "commit.gpgsign", "yes") // strconv.ParseBool rejects this

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Signed", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("G"))
	})

	// The decision to sign and the signing itself have to resolve config the same
	// way. They did not: the decision was read through go-git, which knows only
	// $XDG_CONFIG_HOME/git/config and $HOME/.gitconfig, while `git commit-tree -S`
	// is git and knows the lot. A per-client signing identity behind an includeIf
	// is the case that matters in practice — it is what includeIf exists for — and
	// such a user signed their code while kref quietly wrote unsigned entries.
	It("honours a commit.gpgsign reached through GIT_CONFIG_GLOBAL and an includeIf", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)
		includeGitConfig(dir, "[commit]\n\tgpgsign = true\n")

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Signed via includeIf", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("G"))
	})

	It("treats a valueless [commit] gpgsign as true, the way git does", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)

		// `git config` cannot write a key with no value, and a hand-edited
		// .git/config is exactly where one comes from.
		appendGitConfig(dir, "[commit]\n\tgpgsign\n")

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Valueless", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("G"))
	})

	// Asking git rather than go-git means the answer can now fail to arrive at
	// all — an unparseable value, or no git on PATH. Falling back to "do not sign"
	// in silence is the one outcome that must not happen: it is indistinguishable
	// from a store that was never set up to sign, which is what sends a reader off
	// configuring a key they already configured.
	It("says so instead of silently not signing when the decision cannot be read", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)
		gitConfig(dir, "commit.gpgsign", "maybe") // git: bad boolean config value

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		Expect(s.Signing()).To(BeFalse())
		Expect(s.ConfigWarnings()).To(ContainElement(ContainSubstring("commit.gpgsign")))
	})

	It("lets kref.sign=false opt out even when git signs everything else", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)
		gitConfig(dir, "kref.sign", "false")

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Opted out", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("N"))
	})

	It("lets kref.sign=true sign kref alone, leaving commit.gpgsign off", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)
		gitConfig(dir, "kref.sign", "true")

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "kref only", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("G"))
	})

	// The write path must fail loudly. A signing configuration that cannot
	// actually sign has to stop the write rather than fall back to an unsigned
	// commit, which would look identical to a store that never signs.
	It("fails the write when git cannot sign, rather than storing it unsigned", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)
		gitConfig(dir, "user.signingkey", filepath.Join(GinkgoT().TempDir(), "absent_key"))
		gitConfig(dir, "commit.gpgsign", "true")

		s, err := Init(dir, testSignerName, testSignerEmail)
		// Init writes the author identity, so the failure may surface there;
		// either way it must be reported, never swallowed.
		if err == nil {
			DeferCleanup(func() { _ = s.Close() })
			_, err = s.Add(entry.TierShared, "spec", "Unsignable", "body")
		}
		Expect(err).To(MatchError(ContainSubstring("sign commit for tree")))
	})
})

// setupGPGSigningKey gives dir a working native-GPG signing identity in a
// throwaway keyring, and returns the key's fingerprint. It skips the spec when
// gpg is unavailable rather than failing: the capability under test is git's,
// and a machine without gpg cannot answer the question either way.
func setupGPGSigningKey(dir, email string) string {
	GinkgoHelper()
	if _, err := exec.LookPath("gpg"); err != nil {
		Skip("gpg is not installed, so native-GPG signing cannot be exercised here")
	}
	home := GinkgoT().TempDir()
	Expect(os.Chmod(home, 0o700)).To(Succeed())
	GinkgoT().Setenv("GNUPGHOME", home)
	// Leaving an agent per spec would pile them up across a suite run.
	DeferCleanup(func() { _ = exec.Command("gpgconf", "--kill", "gpg-agent").Run() })

	params := filepath.Join(home, "params")
	Expect(os.WriteFile(params, []byte(
		"Key-Type: eddsa\nKey-Curve: Ed25519\nName-Real: Pgp Signer\nName-Email: "+
			email+"\nExpire-Date: 0\n%no-protection\n%commit\n"), 0o600)).To(Succeed())
	out, err := exec.Command("gpg", "--batch", "--gen-key", params).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "gpg --gen-key: %s", string(out))

	listed, err := exec.Command("gpg", "--list-secret-keys", "--with-colons", email).Output()
	Expect(err).NotTo(HaveOccurred())
	var fpr string
	for line := range strings.SplitSeq(string(listed), "\n") {
		if f, ok := strings.CutPrefix(line, "fpr:"); ok {
			fpr = strings.Trim(f, ":")
			break
		}
	}
	Expect(fpr).NotTo(BeEmpty(), "no fingerprint in: %s", string(listed))

	gitConfig(dir, "user.name", "Pgp Signer")
	gitConfig(dir, "user.email", email)
	gitConfig(dir, "gpg.format", "openpgp")
	gitConfig(dir, "user.signingkey", fpr)
	return fpr
}

// Every other signing spec uses SSH, but openpgp is a materially different path
// and the docs promise it works. On READ a PGP-armored signature is the one case
// signingRepo hands back to git-bug's own ReadCommit — the dearmoring
// implementation the decorator exists to bypass everywhere else — so this is the
// branch most likely to break and the least likely to be noticed.
//
// What this does NOT cover: peer trust. The key is generated in the spec's own
// keyring, which gives it ultimate ownertrust for free, so `git verify-commit`
// answers G immediately. A key IMPORTED from a collaborator answers U (unknown
// validity) until it is trusted or signed, and kref reports that as `untrusted`
// — the same verdict as a key that is absent entirely. That ladder is gpg's
// behaviour rather than kref's, and gpg-agent is not what resolves it: the agent
// does private-key operations, while validity comes from the trustdb.
var _ = Describe("native GPG signing", func() {
	It("signs, reads back, verifies and resigns with the system keyring", func() {
		dir := gitRepo()
		const email = "pgp@example.com"
		setupGPGSigningKey(dir, email)

		By("history written before signing was on")
		s, err := Init(dir, "Pgp Signer", email)
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierShared, "spec", "Legacy", "written before")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		gitConfig(dir, "commit.gpgsign", "true")
		signing, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = signing.Close() })

		By("a new entry is PGP-signed and still readable through the delegation")
		fresh, err := signing.Add(entry.TierShared, "spec", "Signed", "body text")
		Expect(err).NotTo(HaveOccurred())
		ref := tierRef(entry.TierShared, fresh.String())
		Expect(gitOut(dir, "cat-file", "commit", ref)).To(ContainSubstring("gpgsig"))
		Expect(sigStatus(dir, ref)).To(Equal("G"))

		got, err := signing.Get(fresh)
		Expect(err).NotTo(HaveOccurred(), "a PGP-armored commit must stay readable")
		Expect(got.Body).To(Equal("body text"))
		Expect(got.SigState).To(Equal(entry.SigGood))

		By("resign signs the pre-key history with the same keyring")
		res, err := signing.Resign([]entity.Id{id}, false, false, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Reason).To(BeEmpty())
		Expect(signing.SigState(id)).To(Equal(entry.SigGood))
	})
})

// Losing the second go-git handle costs signature support but not the store, and
// the degraded state is indistinguishable from a store that never signed: every
// entry reads `unsigned` and resign refuses with "not configured to sign". It has
// to say so rather than let the reader go configure a key they already have.
var _ = Describe("signature support unavailable", func() {
	It("reports the degradation instead of silently disabling signing", func() {
		dir := gitRepo()
		raw, err := repository.OpenGoGitRepo(dir, "kref", nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = raw.Close() })

		cfg, err := loadGitConfigSnapshot(dir, nil)
		Expect(err).NotTo(HaveOccurred())

		repo, warn := newSigningRepo(raw, filepath.Join(GinkgoT().TempDir(), "not-a-repo"), cfg)
		Expect(warn).To(ContainSubstring("signing and signature verification are unavailable"))
		// The unwrapped handle comes back, so the caller still has a usable store.
		Expect(repo).To(BeIdenticalTo(raw))
		_, isReader := repo.(sigReader)
		Expect(isReader).To(BeFalse())
	})
})

// Init signs the author identity too, and the identity is read through
// ListCommits — so every path that loads it must go through the signature-aware
// handle, including the ones that run BEFORE the author is known.
var _ = Describe("reopening a signed store", func() {
	It("reopens and reads entries when the identity commits are signed", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
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

	It("reports an initialized store when the identity commits are signed", func() {
		dir := gitRepo()
		enableSSHSigning(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		name, email, ok, err := Initialized(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(name).To(Equal(testSignerName))
		Expect(email).To(Equal(testSignerEmail))
	})
})

// The resign design rests on this: entry ids come from the operation-pack BLOB
// (entity.DeriveId over the tree's op payload), never from the commit, so
// rebuilding a commit to add a signature must leave the entry's identity — and
// therefore its ref name, links and favorites — untouched.
var _ = Describe("entry identity under commit rewriting", func() {
	It("keeps the entry id and body when a commit is rebuilt with a signature", func() {
		dir := gitRepo()
		setupSSHSigningKey(dir, testSignerEmail)

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierShared, "spec", "Rewritten", "body")
		Expect(err).NotTo(HaveOccurred())
		ref := tierRef(entry.TierShared, id.String())
		Expect(sigStatus(dir, ref)).To(Equal("N"))

		// One Commit() writes one operation pack, hence one parentless commit.
		Expect(gitOut(dir, "rev-list", "--count", ref)).To(Equal("1"))

		// Rebuild the tip over the SAME tree, signed. This is the resign
		// primitive in miniature.
		before := gitOut(dir, "rev-parse", ref)
		tree := gitOut(dir, "rev-parse", ref+"^{tree}")
		rebuilt := gitOut(dir, "commit-tree", "-S", tree)
		gitOut(dir, "update-ref", ref, rebuilt)

		Expect(rebuilt).NotTo(Equal(before), "the rewrite must produce a new commit")
		Expect(sigStatus(dir, ref)).To(Equal("G"))

		got, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID.String()).To(Equal(id.String()))
		Expect(got.Title).To(Equal("Rewritten"))
		Expect(got.Body).To(Equal("body"))
	})
})
