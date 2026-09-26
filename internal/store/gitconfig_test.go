package store

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// appendGitConfig writes raw lines into dir's repo-local config, for the shapes
// `git config` cannot produce — a key with no value being the one that matters.
func appendGitConfig(dir, body string) {
	GinkgoHelper()
	f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0o600)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.WriteString(body)
	Expect(err).NotTo(HaveOccurred())
	Expect(f.Close()).To(Succeed())
}

// gitBoolVerdict is what real git makes of a key, and is the oracle the parser
// below is measured against. refused covers git exiting non-zero for a value
// --type=bool will not accept.
func gitBoolVerdict(dir, key string) (value, refused bool) {
	GinkgoHelper()
	out, err := runGit(nil, "", "-C", dir, "config", "--get", "--type=bool", key)
	if err != nil {
		return false, true
	}
	return strings.TrimSpace(out) == "true", false
}

// Reading the whole config in one shot means kref parses git's booleans itself
// again, and git's boolean grammar is wider than it looks: three word pairs in
// any case, the empty string, a key with no value at all, and any integer in any
// base git accepts. Getting one of those wrong is how a user who turned signing
// on gets entries that are not signed.
//
// So these specs do not encode what git's grammar is believed to be. They ask
// git, for every spelling, and require kref to give the same answer — including
// on the values git refuses. A change in either parser fails the suite.
var _ = Describe("gitConfigSnapshot", func() {
	DescribeTable("resolves booleans exactly as git config --type=bool does",
		func(raw string) {
			dir := gitRepo()
			gitConfig(dir, "kref.sign", raw)
			wantValue, refused := gitBoolVerdict(dir, "kref.sign")

			cfg, err := loadGitConfigSnapshot(dir, nil)
			Expect(err).NotTo(HaveOccurred())

			got, set, err := cfg.getBool("kref.sign")
			Expect(set).To(BeTrue(), "git has the key, so kref must report it set")
			if refused {
				Expect(err).To(HaveOccurred(), "git refused %q; kref accepted it", raw)
				return
			}
			Expect(err).NotTo(HaveOccurred(), "git accepted %q; kref refused it", raw)
			Expect(got).To(Equal(wantValue), "git and kref disagree about %q", raw)
		},
		Entry("true", "true"),
		Entry("yes", "yes"),
		Entry("on", "on"),
		Entry("false", "false"),
		Entry("no", "no"),
		Entry("off", "off"),
		Entry("upper case", "TRUE"),
		Entry("mixed case", "On"),
		Entry("upper case false", "OFF"),
		Entry("one", "1"),
		Entry("zero", "0"),
		Entry("other positive integer", "2"),
		Entry("negative integer", "-1"),
		Entry("leading zeroes", "007"),
		Entry("hexadecimal", "0x2"),
		Entry("hexadecimal zero", "0x0"),
		Entry("integer with a unit suffix", "1k"),
		Entry("zero with a unit suffix", "0k"),
		// Each of these is a place Go's base-0 ParseInt and git's reader disagree:
		// Go takes the underscores and the 0o prefix that git rejects, and reads
		// 0b as binary where git happens to agree by another route. They are here
		// because they were found by probing git, not by reading its source.
		Entry("underscore separators, which git rejects", "1_000"),
		Entry("an 0o octal prefix, which git rejects", "0o17"),
		Entry("an 0b binary prefix", "0b101"),
		Entry("an explicit plus sign", "+5"),
		Entry("the empty string", ""),
		Entry("a word git does not accept", "maybe"),
		Entry("a number git cannot hold", "99999999999999999999999"),
	)

	It("reads a key with no value as true, exactly as git does", func() {
		dir := gitRepo()
		appendGitConfig(dir, "[kref]\n\tsign\n")
		wantValue, refused := gitBoolVerdict(dir, "kref.sign")
		Expect(refused).To(BeFalse())
		Expect(wantValue).To(BeTrue(), "precondition: git reads a valueless key as true")

		cfg, err := loadGitConfigSnapshot(dir, nil)
		Expect(err).NotTo(HaveOccurred())

		got, set, err := cfg.getBool("kref.sign")
		Expect(err).NotTo(HaveOccurred())
		Expect(set).To(BeTrue())
		Expect(got).To(BeTrue())
	})

	It("distinguishes a key with no value from one set to the empty string", func() {
		dir := gitRepo()
		appendGitConfig(dir, "[kref]\n\tsign =\n")

		cfg, err := loadGitConfigSnapshot(dir, nil)
		Expect(err).NotTo(HaveOccurred())

		got, set, err := cfg.getBool("kref.sign")
		Expect(err).NotTo(HaveOccurred())
		Expect(set).To(BeTrue())
		// The distinction go-git could not express, and the reason a valueless
		// `[commit] gpgsign` used to read as "do not sign".
		Expect(got).To(BeFalse())
	})

	It("reports an unset key as unset rather than as false", func() {
		cfg, err := loadGitConfigSnapshot(gitRepo(), nil)
		Expect(err).NotTo(HaveOccurred())

		got, set, err := cfg.getBool("kref.sign")
		Expect(err).NotTo(HaveOccurred())
		// kref.sign falls through to commit.gpgsign when unset, and overrides it
		// when set to false. Collapsing the two loses the override.
		Expect(set).To(BeFalse())
		Expect(got).To(BeFalse())
	})

	It("takes the last value of a key set more than once, as git config --get does", func() {
		dir := gitRepo()
		appendGitConfig(dir, "[kref \"author\"]\n\tname = First\n\tname = Second\n")

		cfg, err := loadGitConfigSnapshot(dir, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.get("kref.author.name")).To(Equal("Second"))
	})

	It("keeps a value that spans lines intact", func() {
		dir := gitRepo()
		gitConfig(dir, "kref.author.name", "One\nTwo")

		cfg, err := loadGitConfigSnapshot(dir, nil)
		Expect(err).NotTo(HaveOccurred())
		// --null delimits RECORDS with NUL and splits key from value at the FIRST
		// newline, so an embedded one survives. Splitting on newline would not.
		Expect(cfg.get("kref.author.name")).To(Equal("One\nTwo"))
	})

	It("reads the layers git reads, including one reached through an includeIf", func() {
		dir := gitRepo()
		includeGitConfig(dir, "[kref \"author\"]\n\tname = Included\n")

		cfg, err := loadGitConfigSnapshot(dir, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.get("kref.author.name")).To(Equal("Included"))
	})

	It("reports an unreadable config rather than answering as if it were empty", func() {
		dir := gitRepo()
		appendGitConfig(dir, "this is not a config file\n")

		_, err := loadGitConfigSnapshot(dir, nil)
		Expect(err).To(HaveOccurred())
	})
})
