//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// gitConfigGlobal sets a key in the env's isolated global gitconfig — the file
// GIT_CONFIG_GLOBAL points at for every command this env runs, and the layer a
// real user would put their signing config in.
func gitConfigGlobal(e *krefEnv, key, value string) {
	GinkgoHelper()
	out, err := exec.Command("git", "config", "--file",
		filepath.Join(e.home, ".gitconfig"), key, value).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git config --file: %s", string(out))
}

func allowedSignersPath(e *krefEnv) string { return filepath.Join(e.home, "allowed_signers") }

// verifier lets an env CHECK signatures without making any of its own: an
// allowed-signers file plus the ssh format. This is the collaborator who reads
// signed material but does not sign.
func verifier(e *krefEnv) {
	GinkgoHelper()
	path := allowedSignersPath(e)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		Expect(os.WriteFile(path, nil, 0o600)).To(Succeed())
	}
	gitConfigGlobal(e, "gpg.format", "ssh")
	gitConfigGlobal(e, "gpg.ssh.allowedSignersFile", path)
}

// signer turns on SSH commit signing for e with a key generated inside e's own
// isolated HOME, and returns the allowed-signers LINE a peer needs in order to
// trust it. The key never leaves the env, which is what makes the cross-user
// trust in these specs real rather than shared-by-accident.
//
// The key file is named after the email so calling this twice on one env is a
// key ROTATION rather than an ssh-keygen overwrite prompt: the new key becomes
// the one that signs, and the old one keeps whatever trust it already had.
func signer(e *krefEnv, email string) string {
	GinkgoHelper()
	key := filepath.Join(e.home, "id_"+email)
	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519",
		"-N", "", "-C", email, "-f", key).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "ssh-keygen: %s", string(out))
	pub, err := os.ReadFile(key + ".pub")
	Expect(err).NotTo(HaveOccurred())

	verifier(e)
	line := email + " " + strings.TrimSpace(string(pub))
	trust(e, line) // a signer trusts its own key, as a real setup would
	gitConfigGlobal(e, "user.signingkey", key)
	gitConfigGlobal(e, "commit.gpgsign", "true")
	return line
}

// trust adds one allowed-signers line to e, so e can vouch for that key.
func trust(e *krefEnv, line string) {
	GinkgoHelper()
	verifier(e)
	f, err := os.OpenFile(allowedSignersPath(e), os.O_APPEND|os.O_WRONLY, 0o600)
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(f.Close()).To(Succeed()) }()
	_, err = f.WriteString(line + "\n")
	Expect(err).NotTo(HaveOccurred())
}

// entrySig is the signature half of `kref show --json`: the verdict for the
// whole commit chain, plus — when an attestation is what made that verdict —
// the identity git read off the ATTESTING COMMIT'S SIGNATURE and the claim the
// attester recorded.
type entrySig struct {
	SigState      string `json:"sig_state"`
	AttestedBy    string `json:"attested_by"`
	AttestedClaim string `json:"attested_claim"`
}

// sigOf reads an entry's signature verdict through the JSON contract rather
// than the rendered table.
func sigOf(e *krefEnv, id string) entrySig {
	GinkgoHelper()
	var v entrySig
	Expect(json.Unmarshal([]byte(e.mustRun("show", id, "--json")), &v)).To(Succeed())
	return v
}

// sigStateOf reads just the verdict, for the specs that have nothing to say
// about attestation.
func sigStateOf(e *krefEnv, id string) string {
	GinkgoHelper()
	return sigOf(e, id).SigState
}

// gitConfigLocal sets a key in the REPO's own config. It is separate from
// gitConfigGlobal because an active identity profile is exported to kref's git
// subprocesses AS the global layer, replacing the env's .gitconfig — so
// anything that must survive alongside a profile has to live here.
func gitConfigLocal(e *krefEnv, key, value string) {
	GinkgoHelper()
	gitSignedIn(e, "config", "--local", key, value)
}

// profileSigner gives e a signing key that exists ONLY in an identity profile
// under e's XDG_CONFIG_HOME — never in the repo config and never in the env's
// global gitconfig. Verification config stays repo-local, because the profile
// displaces the global layer for every git subprocess kref runs.
//
// That asymmetry is the entire point: any code path that fails to export the
// profile cannot find a key at all, so the failure is a missing signature
// rather than a signature made under the wrong identity. It is modelled on
// .claude/handoff/signing-sandbox.sh, which builds exactly this shape by hand.
func profileSigner(e *krefEnv, profile, email string) {
	GinkgoHelper()
	key := filepath.Join(e.home, "profile_"+email)
	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519",
		"-N", "", "-C", email, "-f", key).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "ssh-keygen: %s", string(out))
	pub, err := os.ReadFile(key + ".pub")
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(allowedSignersPath(e),
		[]byte(email+" "+strings.TrimSpace(string(pub))+"\n"), 0o600)).To(Succeed())

	dir := filepath.Join(e.home, ".config", "kref", "identities")
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	// The profile carries the identity AND the key as one unit — the pairing is
	// the feature, so splitting them here would not be testing the feature.
	body := "[user]\n\tname = Profile " + email +
		"\n\temail = " + email + "\n\tsigningkey = " + key + "\n[gpg]\n\tformat = ssh\n"
	Expect(os.WriteFile(filepath.Join(dir, profile), []byte(body), 0o600)).To(Succeed())

	gitConfigLocal(e, "gpg.ssh.allowedSignersFile", allowedSignersPath(e))
	gitConfigLocal(e, "kref.identity", profile)
}

// gitIn runs a git command in a repo directly, with none of an env's isolation
// to inherit — for reaching into the bare origin, and for reading refs the CLI
// does not expose.
func gitIn(dir string, args ...string) string {
	GinkgoHelper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = []string{"HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git -C %s %v: %s", dir, args, string(out))
	return strings.TrimSpace(string(out))
}

// gitSignedIn runs git inside e's OWN repo under e's isolated config, so the
// command can sign and verify with e's key. gitIn deliberately runs with no
// config at all, which is right for reaching into a bare origin and useless for
// anything that has to produce a real signature — an attacker forging a chain
// signs it for real, with a key the reader trusts.
func gitSignedIn(e *krefEnv, args ...string) string {
	GinkgoHelper()
	cmd := exec.Command("git", append([]string{"-C", e.dir}, args...)...)
	cmd.Env = e.osEnv()
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git -C %s %v: %s", e.dir, args, string(out))
	return strings.TrimSpace(string(out))
}

// Signing only earns its keep across a trust boundary: one person's key, another
// person's judgement about it, with a bare remote in between. Everything below
// runs through the real binary, because the store-level round trip cannot prove
// the CLI asks for a verdict at all.
var _ = Describe("signing across a bare remote", func() {
	// Two people, two keys, one origin. Ada signs; Bob has never heard of her key.
	setup := func() (origin string, a, b *krefEnv, adaKey string) {
		GinkgoHelper()
		origin = bareRepo()
		a = newKrefEnv("Ada", "ada@example.com")
		b = newKrefEnv("Bob", "bob@example.com")
		adaKey = signer(a, "ada@example.com")
		a.mustRun("init")
		b.mustRun("init")
		a.mustRun("remote", "set", "shared", "origin", origin)
		b.mustRun("remote", "set", "shared", "origin", origin)
		return origin, a, b, adaKey
	}

	It("signs what it writes and says so on both the JSON and the table", func() {
		_, a, _, _ := setup()
		id := idOf(a.mustRun("new", "--tier", "shared", "--title", "Signed", "--body", "x", "--json"))

		Expect(sigStateOf(a, id)).To(Equal("good"))
		// A good signature is deliberately silent in the table, and the filter
		// for entries needing `kref resign` must not claim this one.
		Expect(a.mustRun("list")).NotTo(ContainSubstring("⚠"))
		Expect(a.mustRun("list", "--unsigned")).NotTo(ContainSubstring(id[:12]))
	})

	// The verdict is one reader's judgement about another reader's key, so it
	// must differ per clone for the same bytes — and it must change when trust
	// changes, WITHOUT any ref moving.
	It("reports a peer's signature as untrusted until the peer's key is trusted", func() {
		_, a, b, adaKey := setup()
		id := idOf(a.mustRun("new", "--tier", "shared", "--title", "From Ada", "--body", "x", "--json"))
		a.mustRun("sync", "push", "--tier", "shared")
		b.mustRun("sync", "pull", "--tier", "shared")

		By("Bob has the entry but cannot vouch for the key that signed it")
		Expect(sigStateOf(b, id)).To(Equal("untrusted"))
		Expect(b.mustRun("list")).To(ContainSubstring("⚠"))
		// Untrusted is NOT unsigned: the signature is there, it just cannot be
		// checked, so `resign`'s filter must not offer to re-sign someone else's
		// entry.
		Expect(b.mustRun("list", "--unsigned")).NotTo(ContainSubstring(id[:12]))

		before := gitIn(b.dir, "rev-parse", "refs/kref-shared/"+id)

		By("trusting Ada's key flips the verdict with no ref moving")
		trust(b, adaKey)
		Expect(sigStateOf(b, id)).To(Equal("good"))
		Expect(b.mustRun("list")).NotTo(ContainSubstring("⚠"))
		Expect(gitIn(b.dir, "rev-parse", "refs/kref-shared/"+id)).To(Equal(before),
			"the verdict changed because trust changed, not because anything was rewritten")
	})

	// The commonest real configuration is asymmetric: one collaborator signs and
	// the other has not set a key up yet.
	It("reports an unsigned peer's entries as unsigned, and finds them with --unsigned", func() {
		_, a, b, adaKey := setup()
		trust(a, adaKey) // Ada already trusts herself; Bob simply never signs

		signed := idOf(a.mustRun("new", "--tier", "shared", "--title", "Ada signed", "--body", "x", "--json"))
		a.mustRun("sync", "push", "--tier", "shared")
		b.mustRun("sync", "pull", "--tier", "shared")

		bare := idOf(b.mustRun("new", "--tier", "shared", "--title", "Bob bare", "--body", "x", "--json"))
		b.mustRun("sync", "push", "--tier", "shared")
		a.mustRun("sync", "pull", "--tier", "shared")

		Expect(sigStateOf(a, signed)).To(Equal("good"))
		Expect(sigStateOf(a, bare)).To(Equal("unsigned"))

		By("--unsigned selects exactly the entry that carries no signature")
		unsigned := a.mustRun("list", "--unsigned")
		Expect(unsigned).To(ContainSubstring(bare[:12]))
		Expect(unsigned).NotTo(ContainSubstring(signed[:12]))
	})

	// SECURITY.md's named threat: someone with write access to the shared remote
	// alters material after it was signed. The reader must be told loudly, not
	// quietly served the altered content.
	It("reports a signed entry altered on the remote as BAD, not good", func() {
		origin, a, b, adaKey := setup()
		trust(b, adaKey) // Bob trusts Ada, so a bad verdict means tampering, not setup
		id := idOf(a.mustRun("new", "--tier", "shared", "--title", "Genuine", "--body", "x", "--json"))
		a.mustRun("sync", "push", "--tier", "shared")

		By("rewriting the pushed commit's message while keeping its signature header")
		ref := "refs/kref-shared/" + id
		raw := gitIn(origin, "cat-file", "commit", ref)
		Expect(raw).To(ContainSubstring("gpgsig"), "precondition: the pushed tip must be signed")
		forged := filepath.Join(GinkgoT().TempDir(), "forged")
		Expect(os.WriteFile(forged, []byte(raw+"\n\ntampered\n"), 0o600)).To(Succeed())
		hash := gitIn(origin, "hash-object", "-t", "commit", "-w", forged)
		gitIn(origin, "update-ref", ref, hash)

		b.mustRun("sync", "pull", "--tier", "shared")
		Expect(sigStateOf(b, id)).To(Equal("bad"))
		Expect(b.mustRun("list")).To(ContainSubstring("BAD SIGNATURE"))
	})

	// The gate that stops `kref resign` rewriting published history reads
	// refs/kref-pushed/*, which only a real push writes. Every unit spec
	// fabricates that ref with update-ref, so nothing until here proves the two
	// halves are connected: if push stopped recording, resign would quietly
	// rewrite material collaborators already hold.
	It("refuses to resign an entry that a real push published, until --force", func() {
		_, a, b, adaKey := setup()
		trust(b, adaKey)

		By("writing an entry BEFORE signing was configured, then publishing it")
		gitConfigGlobal(a, "commit.gpgsign", "false")
		id := idOf(a.mustRun("new", "--tier", "shared", "--title", "Legacy", "--body", "x", "--json"))
		Expect(sigStateOf(a, id)).To(Equal("unsigned"))
		a.mustRun("sync", "push", "--tier", "shared")
		b.mustRun("sync", "pull", "--tier", "shared")

		By("turning signing on and asking resign to fix the history")
		gitConfigGlobal(a, "commit.gpgsign", "true")
		_, stderr, err := a.run("", "resign", id)
		// Non-zero, not just a printed line: a refusal a script cannot see is a
		// silent decline, and this one leaves history unsigned.
		Expect(err).To(HaveOccurred())
		Expect(stderr).To(ContainSubstring("already pushed"))
		Expect(sigStateOf(a, id)).To(Equal("unsigned"), "the refusal must not have rewritten anything")

		By("a machine gets the same failure envelope every other kref error produces")
		// Only the real binary can show this: the envelope is written by main(),
		// which the in-process command harness never runs.
		jsonOut, jsonErr, err := a.run("", "resign", id, "--json")
		Expect(err).To(HaveOccurred())
		var env struct {
			Error string `json:"error"`
		}
		Expect(json.Unmarshal([]byte(jsonOut+jsonErr), &env)).
			To(Succeed(), "stdout %q stderr %q", jsonOut, jsonErr)
		Expect(env.Error).To(ContainSubstring("already pushed"))
		Expect(jsonOut).NotTo(ContainSubstring(`"results"`),
			"a refused run must not also emit a success payload")

		By("--force overrides it, and only it")
		Expect(a.mustRun("resign", id, "--force")).To(ContainSubstring("signed"))
		Expect(sigStateOf(a, id)).To(Equal("good"))
	})

	// The pushed gate's justification, which was a comment rather than a fact
	// until this ran. Rewriting published history does NOT corrupt a
	// collaborator: the remote cannot fast-forward to the new chain, so kref's
	// own push refuses it and the rewrite never leaves the machine. What breaks
	// is the rewriter's own copy, on their next pull.
	It("cannot publish a forced rewrite, and the peer is untouched by the attempt", func() {
		_, a, b, adaKey := setup()
		trust(b, adaKey)

		gitConfigGlobal(a, "commit.gpgsign", "false")
		id := idOf(a.mustRun("new", "--tier", "shared", "--title", "Published", "--body", "original", "--json"))
		a.mustRun("sync", "push", "--tier", "shared")
		b.mustRun("sync", "pull", "--tier", "shared")
		peerTip := gitIn(b.dir, "rev-parse", "refs/kref-shared/"+id)

		gitConfigGlobal(a, "commit.gpgsign", "true")
		a.mustRun("resign", id, "--force")
		Expect(sigStateOf(a, id)).To(Equal("good"))

		By("the remote refuses the rewritten chain, so it never reaches anyone")
		_, stderr, err := a.run("", "sync", "push", "--tier", "shared")
		Expect(err).To(HaveOccurred())
		Expect(stderr).To(ContainSubstring("non-fast-forward"))

		By("the peer's copy is untouched and still readable")
		b.mustRun("sync", "pull", "--tier", "shared")
		Expect(gitIn(b.dir, "rev-parse", "refs/kref-shared/"+id)).To(Equal(peerTip))
		Expect(b.mustRun("show", id)).To(ContainSubstring("original"))

		By("pulling is what breaks the rewriter's OWN copy, and only theirs")
		_, _, err = a.run("", "sync", "pull", "--tier", "shared")
		Expect(err).To(HaveOccurred())
		_, showErr, err := a.run("", "show", id)
		Expect(err).To(HaveOccurred())
		Expect(showErr).To(ContainSubstring("multiple leafs"))
		// The backup ref is the documented way back, so the damage is
		// recoverable rather than terminal.
		Expect(gitIn(a.dir, "rev-parse",
			"refs/kref-resign-backup/kref-shared/"+id)).NotTo(BeEmpty())
	})

	// A sweep must skip published entries by itself, without the user naming
	// them: --all is the command people actually run.
	It("keeps a published entry out of a --all sweep", func() {
		_, a, _, _ := setup()
		gitConfigGlobal(a, "commit.gpgsign", "false")
		published := idOf(a.mustRun("new", "--tier", "shared", "--title", "Published", "--body", "x", "--json"))
		a.mustRun("sync", "push", "--tier", "shared")
		local := idOf(a.mustRun("new", "--tier", "personal", "--title", "Local", "--body", "x", "--json"))

		gitConfigGlobal(a, "commit.gpgsign", "true")
		// A sweep that skipped what it must not touch has done its job, so it
		// stays successful — unlike naming that entry directly.
		out, _, err := a.run("", "resign", "--all")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("already pushed"))

		Expect(sigStateOf(a, published)).To(Equal("unsigned"))
		Expect(sigStateOf(a, local)).To(Equal("good"))
	})

	// The scenario the whole attestation feature exists for, in one spec because
	// the value is in the sequence: history that was published before signing was
	// adopted cannot be rewritten, so it has to be vouched for in place.
	It("adopts signing after publication: resign refuses, an attestation publishes", func() {
		_, a, b, adaKey := setup()

		By("Ada writes and publishes before she has signing turned on")
		gitConfigGlobal(a, "commit.gpgsign", "false")
		id := idOf(a.mustRun("new", "--tier", "shared", "--title", "Legacy", "--body", "original", "--json"))
		a.mustRun("sync", "push", "--tier", "shared")
		b.mustRun("sync", "pull", "--tier", "shared")
		Expect(sigStateOf(b, id)).To(Equal("unsigned"))

		By("turning signing on: resign cannot help here, and names what can")
		gitConfigGlobal(a, "commit.gpgsign", "true")
		_, stderr, err := a.run("", "resign", id)
		Expect(err).To(HaveOccurred())
		Expect(stderr).To(ContainSubstring("kref attest"),
			"a refusal that does not name the remedy strands the operator")

		By("attesting instead, and claiming authorship because every operation is hers")
		var got struct {
			Results []struct {
				Attested bool   `json:"attested"`
				Claim    string `json:"claim"`
				Reason   string `json:"reason"`
			} `json:"results"`
		}
		Expect(json.Unmarshal([]byte(a.mustRun("attest", id, "--json")), &got)).To(Succeed())
		Expect(got.Results).To(HaveLen(1))
		Expect(got.Results[0].Attested).To(BeTrue())
		Expect(got.Results[0].Reason).To(BeEmpty())
		Expect(got.Results[0].Claim).To(Equal("authored"))
		Expect(sigStateOf(a, id)).To(Equal("good"))

		// THE claim the feature rests on. A rewrite cannot get past here — the
		// spec above proves the remote rejects one as non-fast-forward — so if
		// this push ever fails, attestation solves nothing that resign did not.
		By("the attestation FAST-FORWARDS, so the ordinary push accepts it")
		out, pushErr, err := a.run("", "sync", "push", "--tier", "shared")
		Expect(err).NotTo(HaveOccurred(),
			"an attestation MUST publish: stdout %q stderr %q", out, pushErr)
		Expect(out).To(ContainSubstring("shared"))

		By("Bob reads a vouched-for history once he trusts the key that vouched")
		b.mustRun("sync", "pull", "--tier", "shared")
		// An attestation is worth exactly what its signature is worth, so before
		// Bob trusts the key it buys him nothing.
		Expect(sigStateOf(b, id)).To(Equal("untrusted"))
		trust(b, adaKey)
		bobSees := sigOf(b, id)
		Expect(bobSees.SigState).To(Equal("good"))
		Expect(bobSees.AttestedBy).To(Equal("ada@example.com"),
			"the attester is read off the commit SIGNATURE, not off the payload")
		Expect(bobSees.AttestedClaim).To(Equal("authored"))

		By("an operation appended AFTER the attestation falls outside its coverage")
		b.mustRun("update", id, "--body", "bob revises this")
		b.mustRun("sync", "push", "--tier", "shared")
		a.mustRun("sync", "pull", "--tier", "shared")
		Expect(sigStateOf(a, id)).To(Equal("unsigned"),
			"coverage is git ancestry: a later commit cannot be beneath an earlier one")

		By("re-attesting is honest about whose work it now covers")
		Expect(json.Unmarshal([]byte(a.mustRun("attest", id, "--json")), &got)).To(Succeed())
		Expect(got.Results).To(HaveLen(1))
		Expect(got.Results[0].Claim).To(Equal("received"),
			"the history now holds a peer's operation, so the claim is derived as received")
		Expect(sigOf(a, id).AttestedClaim).To(Equal("received"))
	})

	// This env stands alone deliberately: setup() puts a signing key in the
	// global gitconfig, and the property under test is a key that is NOT there.
	//
	// If this reads `unsigned`, Attest is signing through a subprocess that never
	// received the identity profile — the bug 34ea8cf fixed for resign, which
	// every unit spec missed because they configure the key globally.
	It("signs an attestation with a key that exists only in an identity profile", func() {
		e := newKrefEnv("Plain User", "plain@example.com")
		profileSigner(e, "work", "work@example.com")
		e.mustRun("init")

		gitConfigLocal(e, "commit.gpgsign", "false")
		id := idOf(e.mustRun("new", "--tier", "shared", "--title", "Profile", "--body", "x", "--json"))
		Expect(sigStateOf(e, id)).To(Equal("unsigned"))

		gitConfigLocal(e, "commit.gpgsign", "true")
		e.mustRun("attest", id)

		attested := sigOf(e, id)
		Expect(attested.SigState).To(Equal("good"),
			"unsigned here means the attestation was written without the profile's key")
		Expect(attested.AttestedBy).To(Equal("work@example.com"),
			"the profile's key signed it, not the repository's plain identity")
	})

	// An attacker with write access to the shared remote can push exactly this
	// shape — a tampered commit with a genuinely signed attestation stacked on
	// top of it — so the reader must still be told the content was altered. This
	// is the one case where an attestation must NOT be allowed to help.
	It("keeps a BAD signature visible through an attestation that covers it", func() {
		_, a, _, _ := setup()

		By("building a history that needs attesting and holds one signed commit")
		gitConfigGlobal(a, "commit.gpgsign", "false")
		id := idOf(a.mustRun("new", "--tier", "shared", "--title", "Genuine", "--body", "original", "--json"))
		gitConfigGlobal(a, "commit.gpgsign", "true")
		a.mustRun("update", id, "--body", "a second, signed operation")
		a.mustRun("attest", id)
		Expect(sigStateOf(a, id)).To(Equal("good"))

		ref := "refs/kref-shared/" + id
		victim := gitIn(a.dir, "rev-parse", ref+"^")
		tree := gitIn(a.dir, "rev-parse", ref+"^{tree}")

		By("altering that signed commit and re-stacking the attestation above it")
		raw := gitIn(a.dir, "cat-file", "commit", victim)
		Expect(raw).To(ContainSubstring("gpgsig"),
			"precondition: the commit being tampered with must carry a signature")
		forgedFile := filepath.Join(GinkgoT().TempDir(), "forged")
		Expect(os.WriteFile(forgedFile, []byte(raw+"\n\ntampered\n"), 0o600)).To(Succeed())
		forged := gitIn(a.dir, "hash-object", "-t", "commit", "-w", forgedFile)
		Expect(gitSignedIn(a, "show", "-s", "--format=%G?", forged)).To(Equal("B"),
			"precondition: altering a signed commit must break its signature")
		// Re-signed for real, with a key the reader trusts: the attestation on top
		// is genuine, and only the commit beneath it was altered.
		restacked := gitSignedIn(a, "commit-tree", "-S", "-p", forged, tree)
		gitIn(a.dir, "update-ref", ref, restacked)

		By("the tampered commit is inside the attestation's ancestry, and still wins")
		tampered := sigOf(a, id)
		Expect(tampered.SigState).To(Equal("bad"),
			"an attestation may absorb unsigned and untrusted, never altered content")
		Expect(tampered.AttestedBy).To(BeEmpty(),
			"reporting an attester beside a bad verdict would read as an endorsement")
		Expect(a.mustRun("list")).To(ContainSubstring("BAD SIGNATURE"))
	})

	// An attestation is worth exactly what the key that signed it is worth. That
	// cuts both ways, and the second half is what makes key expiry survivable:
	// the mechanism has to compose with itself.
	It("stops vouching when the attester's key loses trust, and recovers by re-attesting", func() {
		_, a, _, _ := setup()

		gitConfigGlobal(a, "commit.gpgsign", "false")
		id := idOf(a.mustRun("new", "--tier", "shared", "--title", "Vouched", "--body", "x", "--json"))
		gitConfigGlobal(a, "commit.gpgsign", "true")
		a.mustRun("attest", id)
		Expect(sigStateOf(a, id)).To(Equal("good"))

		By("un-trusting the attester's key, which moves no ref at all")
		ref := "refs/kref-shared/" + id
		vouched := gitIn(a.dir, "rev-parse", ref)
		Expect(os.WriteFile(allowedSignersPath(a), nil, 0o600)).To(Succeed())
		withdrawn := sigOf(a, id)
		Expect(withdrawn.SigState).NotTo(Equal("good"))
		Expect(withdrawn.SigState).To(Equal("untrusted"))
		Expect(withdrawn.AttestedBy).To(BeEmpty(),
			"an attestation nobody can verify has not vouched for anything")
		Expect(gitIn(a.dir, "rev-parse", ref)).To(Equal(vouched),
			"the verdict changed because trust changed, not because anything was rewritten")

		By("a second attestation, under a rotated key, vouches for the first one too")
		rotated := signer(a, "ada-rotated@example.com")
		Expect(rotated).NotTo(BeEmpty())
		a.mustRun("attest", id)
		recovered := sigOf(a, id)
		Expect(recovered.SigState).To(Equal("good"))
		Expect(recovered.AttestedBy).To(Equal("ada-rotated@example.com"))
		Expect(gitIn(a.dir, "rev-parse", ref)).NotTo(Equal(vouched),
			"recovery appends; it does not rewrite what was already published")
	})
})
