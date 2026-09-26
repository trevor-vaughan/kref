package store

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// writeProfile creates an identity profile under a throwaway XDG config home
// and returns its path. A profile is an ordinary gitconfig file, which is what
// lets it carry a name, an email and a signing key as one unit.
func writeProfile(name, body string) string {
	GinkgoHelper()
	home := GinkgoT().TempDir()
	GinkgoT().Setenv("XDG_CONFIG_HOME", home)
	dir := filepath.Join(home, "kref", "identities")
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	path := filepath.Join(dir, name)
	Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())
	return path
}

// identityRefs lists the git-bug identities a repo holds. Two refs for one
// person means an identity was minted that should have been reused.
func identityRefs(dir string) []string {
	GinkgoHelper()
	out := gitOut(dir, "for-each-ref", "--format=%(refname)", "refs/identities/")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

var _ = Describe("identity profiles", func() {
	It("lists the profiles on disk", func() {
		writeProfile("work", "[user]\n\tname = Work Me\n\temail = work@example.com\n")
		dir := gitRepo()
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		names, err := IdentityProfiles()
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(ConsistOf("work"))
	})

	It("attributes operations to the active profile's identity", func() {
		writeProfile("work", "[user]\n\tname = Work Me\n\temail = work@example.com\n")
		dir := gitRepo()
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		gitConfig(dir, "kref.identity", "work")
		reopened, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })

		name, email := reopened.Author()
		Expect(name).To(Equal("Work Me"))
		Expect(email).To(Equal("work@example.com"))

		id, err := reopened.Add(entry.TierShared, "spec", "T", "b")
		Expect(err).NotTo(HaveOccurred())
		got, err := reopened.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.CreatedByEmail).To(Equal("work@example.com"))
	})

	// Selecting a profile per working directory is the whole point of profiles,
	// and includeIf "gitdir:" is how git users do per-directory settings — so the
	// two belong together. Resolved through go-git they could not meet: an
	// included kref.identity was invisible, and kref silently used the plain git
	// identity instead of the one the user had selected.
	It("honours a kref.identity reached through an includeIf", func() {
		writeProfile("work", "[user]\n\tname = Work Me\n\temail = work@example.com\n")
		dir := gitRepo()
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		includeGitConfig(dir, "[kref]\n\tidentity = work\n")

		reopened, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })

		active, err := reopened.ActiveIdentity()
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(Equal("work"))

		name, email := reopened.Author()
		Expect(name).To(Equal("Work Me"))
		Expect(email).To(Equal("work@example.com"))
	})

	// The read path resolves kref.identity from every layer git reads, so the
	// clear path has to reach them too. Unsetting touches the repository-local
	// file alone: against a pin that arrived by includeIf it changed nothing,
	// reported success, and left every later entry attributed to -- and signed
	// with -- the profile the user had just disowned.
	It("clears a pin that arrived from outside the repository", func() {
		writeProfile("work", "[user]\n\tname = Work Me\n\temail = work@example.com\n")
		dir := gitRepo()
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		includeGitConfig(dir, "[kref]\n\tidentity = work\n")

		reopened, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })
		active, err := reopened.ActiveIdentity()
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(Equal("work"))

		Expect(reopened.UseIdentity("")).To(Succeed())

		active, err = reopened.ActiveIdentity()
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(BeEmpty())

		// The consequence the user was promised, not just the config key changing:
		// the next command must stop writing as the disowned profile. Asserted
		// across a reopen because that is what the next command does -- the author
		// is resolved at open and deliberately not re-derived mid-process (see
		// refreshGitConfig).
		again, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = again.Close() })
		name, email := again.Author()
		Expect(name).To(Equal(testSignerName))
		Expect(email).To(Equal(testSignerEmail))
	})

	// KREF_IDENTITY overrides git config entirely, so no config write can clear
	// it. Reporting success there would be the same lie in a different layer.
	It("refuses to report success when KREF_IDENTITY pins the profile", func() {
		writeProfile("work", "[user]\n\tname = Work Me\n\temail = work@example.com\n")
		dir := gitRepo()
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		GinkgoT().Setenv("KREF_IDENTITY", "work")
		err = s.UseIdentity("")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("KREF_IDENTITY"))
	})

	It("signs with the profile's own key, so identity and key move together", func() {
		dir := gitRepo()
		key := writeSigningKey(dir, "work@example.com")
		writeProfile("work", "[user]\n\tname = Work Me\n\temail = work@example.com\n"+
			"\tsigningkey = "+key+"\n[gpg]\n\tformat = ssh\n")

		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		gitConfig(dir, "kref.identity", "work")
		gitConfig(dir, "commit.gpgsign", "true")
		reopened, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })

		id, err := reopened.Add(entry.TierShared, "spec", "Signed by profile", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(sigStatus(dir, tierRef(entry.TierShared, id.String()))).To(Equal("G"))
	})

	// Init baked the plain git user regardless of the active profile, so the very
	// next Open resolved a different author and minted a second identity for the
	// same person -- before a single entry had been written.
	It("bakes the active profile's identity at init instead of minting a second one", func() {
		writeProfile("work", "[user]\n\tname = Work Me\n\temail = work@example.com\n")
		dir := gitRepo()
		gitConfig(dir, "kref.identity", "work")

		s, err := Init(dir, "", "")
		Expect(err).NotTo(HaveOccurred())
		name, email := s.Author()
		Expect(name).To(Equal("Work Me"))
		Expect(email).To(Equal("work@example.com"))
		Expect(s.Close()).To(Succeed())

		reopened, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })
		rname, remail := reopened.Author()
		Expect(rname).To(Equal(name))
		Expect(remail).To(Equal(email))

		Expect(identityRefs(dir)).To(HaveLen(1),
			"one person under one profile must not end up with two identities")
	})

	It("reports a named profile that does not exist rather than silently ignoring it", func() {
		writeProfile("work", "[user]\n\tname = W\n\temail = w@example.com\n")
		dir := gitRepo()
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		gitConfig(dir, "kref.identity", "missing")
		_, err = Open(dir)
		Expect(err).To(MatchError(ContainSubstring("missing")))
	})

	It("rejects a profile name that would escape the identities directory", func() {
		writeProfile("work", "[user]\n\tname = W\n\temail = w@example.com\n")
		dir := gitRepo()
		s, err := Init(dir, testSignerName, testSignerEmail)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		gitConfig(dir, "kref.identity", "../../etc/passwd")
		_, err = Open(dir)
		Expect(err).To(MatchError(ContainSubstring("identity name")))
	})

	// The separator check above catches every traversal that spells one out. A
	// bare ".." or "." carries no separator at all and is still a directory, so
	// these two equality clauses are the only thing standing between a repo's
	// git config and GIT_CONFIG_GLOBAL pointing at a directory.
	DescribeTable("rejects a separator-free name that is really a directory",
		func(name string) {
			_, err := identityProfilePath(name)
			Expect(err).To(MatchError(ContainSubstring("invalid identity name")))
		},
		Entry("parent directory", ".."),
		Entry("current directory", "."),
	)

	// A profile supplying only one of the two would silently assemble a mixed
	// identity -- a name from the profile and an email from somewhere else --
	// which is exactly what authorOverride refuses for every other layer.
	DescribeTable("refuses a profile that sets only one of name and email",
		func(body string) {
			writeProfile("half", body)
			dir := gitRepo()
			s, err := Init(dir, testSignerName, testSignerEmail)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Close()).To(Succeed())

			gitConfig(dir, "kref.identity", "half")
			_, err = Open(dir)
			Expect(err).To(MatchError(ContainSubstring("must set both user.name and user.email")))
		},
		Entry("name only", "[user]\n\tname = Half Me\n"),
		Entry("email only", "[user]\n\temail = half@example.com\n"),
	)
})
