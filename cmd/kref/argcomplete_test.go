package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// addEntry creates an entry and returns its full id.
func addEntry(dir string, args ...string) string {
	GinkgoHelper()
	out := run(append([]string{"--dir", dir, "new", "--json"}, args...)...)
	var added struct {
		ID string `json:"id"`
	}
	Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
	return added.ID
}

// completionRepo builds a store with one live, one soft-deleted, and one
// archived entry, plus distinct kinds/labels, so the completion specs can assert
// the right entry set and the store-driven flag values.
func completionRepo() (dir, live, deleted, archived string) {
	GinkgoHelper()
	dir = gitRepo()
	run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
	live = addEntry(dir, "--title", "Auth Design", "--kind", "spec", "--label", "security")
	addEntry(dir, "--title", "Login Flow", "--kind", "note", "--label", "ux")
	deleted = addEntry(dir, "--title", "Gone Entry", "--kind", "note")
	run("--dir", dir, "rm", deleted)
	archived = addEntry(dir, "--title", "Old Entry", "--kind", "note")
	run("--dir", dir, "archive", archived)
	return dir, live, deleted, archived
}

var _ = Describe("kref id-argument completion", func() {
	DescribeTable("offers live entry ids with titles",
		func(command string) {
			dir, live, deleted, archived := completionRepo()
			out := run("--dir", dir, "__complete", command, "")
			// id, then a tab, then the updated date and title as the description
			Expect(out).To(MatchRegexp(live[:12] + `\t\d{4}-\d{2}-\d{2}  Auth Design`))
			Expect(out).To(ContainSubstring("ShellCompDirectiveKeepOrder")) // recency order preserved
			// live-only: hidden (deleted/archived) entries are not offered
			Expect(out).NotTo(ContainSubstring(deleted[:12]))
			Expect(out).NotTo(ContainSubstring(archived[:12]))
		},
		Entry("rm", "rm"),
		Entry("edit", "edit"),
		Entry("update", "update"),
		Entry("purge", "purge"),
		Entry("log", "log"),
		Entry("diff", "diff"),
		Entry("links", "links"),
		Entry("tree", "tree"),
		Entry("resolve", "resolve"),
	)

	It("restore offers only soft-deleted entries", func() {
		dir, live, deleted, _ := completionRepo()
		out := run("--dir", dir, "__complete", "restore", "")
		Expect(out).To(MatchRegexp(deleted[:12] + `\t\d{4}-\d{2}-\d{2}  Gone Entry`))
		Expect(out).NotTo(ContainSubstring(live[:12]))
	})

	It("unarchive offers only archived entries", func() {
		dir, live, _, archived := completionRepo()
		out := run("--dir", dir, "__complete", "unarchive", "")
		Expect(out).To(MatchRegexp(archived[:12] + `\t\d{4}-\d{2}-\d{2}  Old Entry`))
		Expect(out).NotTo(ContainSubstring(live[:12]))
	})

	It("supersede offers ids for the second positional too", func() {
		dir, live, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "supersede", live[:12], "")
		Expect(out).To(ContainSubstring(live[:12]))
		Expect(out).To(ContainSubstring("Login Flow"))
	})

	It("label add offers ids for the first positional", func() {
		dir, live, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "label", "add", "")
		Expect(out).To(MatchRegexp(live[:12] + `\t\d{4}-\d{2}-\d{2}  Auth Design`))
	})

	It("orders entries most-recently-updated first and keeps that order", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		older := addEntry(dir, "--title", "Older", "--kind", "note")
		time.Sleep(1100 * time.Millisecond) // Unix-second timestamps: force a distinct UpdatedAt
		newer := addEntry(dir, "--title", "Newer", "--kind", "note")
		out := run("--dir", dir, "__complete", "rm", "")
		Expect(strings.Index(out, newer[:12])).To(BeNumerically("<", strings.Index(out, older[:12])))
		Expect(out).To(ContainSubstring("ShellCompDirectiveKeepOrder"))
	})

	It("caps the list at ten and points to `kref list` for the rest", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		for i := 0; i < 12; i++ {
			addEntry(dir, "--title", fmt.Sprintf("Entry %02d", i), "--kind", "note")
		}
		out := run("--dir", dir, "__complete", "rm", "")
		Expect(strings.Count(out, "\t")).To(Equal(10)) // one tab per offered id row
		Expect(out).To(ContainSubstring("_activeHelp_"))
		Expect(out).To(ContainSubstring("10 most recent of 12"))
	})

	It("defers to file completion when the word looks like a path", func() {
		dir, _, _, _ := completionRepo()
		Expect(run("--dir", dir, "__complete", "rm", "./")).To(ContainSubstring(":0"))
	})
})

var _ = Describe("kref completion guidance when there is nothing to offer", func() {
	// liveOnlyRepo has a single live entry, so the archived and deleted id sets
	// are genuinely empty (not merely filtered by a typed prefix).
	liveOnlyRepo := func() string {
		GinkgoHelper()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		addEntry(dir, "--title", "Live One", "--kind", "note")
		return dir
	}

	It("unarchive explains that no archived entries exist", func() {
		out := run("--dir", liveOnlyRepo(), "__complete", "unarchive", "")
		Expect(out).To(ContainSubstring("_activeHelp_"))
		Expect(out).To(ContainSubstring("no archived entries"))
	})

	It("restore explains that no deleted entries exist", func() {
		out := run("--dir", liveOnlyRepo(), "__complete", "restore", "")
		Expect(out).To(ContainSubstring("_activeHelp_"))
		Expect(out).To(ContainSubstring("no deleted entries"))
	})

	It("id completion points at entry creation in an empty store", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "__complete", "rm", "")
		Expect(out).To(ContainSubstring("_activeHelp_"))
		Expect(out).To(ContainSubstring("no entries yet"))
	})

	It("new surfaces flag guidance instead of an empty completion", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "__complete", "new", "")
		Expect(out).To(ContainSubstring("_activeHelp_"))
		Expect(out).To(ContainSubstring("--title"))
	})
})

var _ = Describe("kref enum-argument completion", func() {
	It("status completes the lifecycle vocabulary on the second arg", func() {
		dir, live, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "status", live[:12], "")
		for _, s := range []string{"open", "active", "accepted", "superseded", "obsolete"} {
			Expect(out).To(ContainSubstring(s))
		}
	})

	It("retier completes the tier vocabulary on the second arg", func() {
		dir, live, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "retier", live[:12], "")
		for _, t := range []string{"private", "personal", "shared"} {
			Expect(out).To(ContainSubstring(t))
		}
	})

	It("remote set completes tiers but not private on the first arg", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "remote", "set", "")
		Expect(out).To(ContainSubstring("personal"))
		Expect(out).To(ContainSubstring("shared"))
		Expect(out).NotTo(ContainSubstring("private"))
	})
})

var _ = Describe("kref no-argument commands suppress file completion", func() {
	DescribeTable("returns NoFileComp instead of a directory listing",
		func(args ...string) {
			dir, _, _, _ := completionRepo()
			out := run(append([]string{"--dir", dir, "__complete"}, append(args, "")...)...)
			Expect(out).To(ContainSubstring(":4"))    // ShellCompDirectiveNoFileComp
			Expect(out).NotTo(ContainSubstring(":0")) // not the file-completion default
		},
		Entry("list", "list"),
		Entry("new", "new"),
		Entry("init", "init"),
		Entry("version", "version"),
		Entry("tidy", "tidy"),
		Entry("mcp", "mcp"),
		Entry("sync push", "sync", "push"),
		Entry("hooks install", "hooks", "install"),
		Entry("vault backup", "vault", "backup"),
	)
})

var _ = Describe("kref command-alias completion", func() {
	It("completes a subcommand alias by prefix", func() {
		// `import` is an alias of `ingest`; cobra's built-in first-word pass offers
		// only canonical names, so this exercises the alias injection. Anchoring on
		// the alias\tdescription row avoids a substring false-positive against
		// another command's help text.
		Expect(run("__complete", "imp")).To(ContainSubstring("import\tIngest"))
	})

	It("offers aliases alongside canonical names for a bare first word", func() {
		out := run("__complete", "")
		Expect(out).To(ContainSubstring("ls\tList entries"))   // alias of list
		Expect(out).To(ContainSubstring("cat\tShow an entry")) // alias of show
		Expect(out).To(ContainSubstring("list\tList entries")) // canonical name still offered
	})

	It("does not offer command aliases in a later word position", func() {
		// A non-command first word leaves finalCmd at root with one arg already
		// present; the alias set is a first-word affordance and must not leak here.
		Expect(run("__complete", "notacommand", "")).NotTo(ContainSubstring("import"))
	})
})

var _ = Describe("kref ingest --kind completion", func() {
	It("completes --kind from the kinds present in the store", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "ingest", "--kind", "")
		Expect(out).To(ContainSubstring("spec"))
		Expect(out).To(ContainSubstring("note"))
		Expect(out).NotTo(ContainSubstring("document")) // no such kind exists yet
	})

	It("falls back to the flag default when the store holds no kinds", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "__complete", "ingest", "--kind", "")
		Expect(out).To(ContainSubstring("document"))
	})

	It("completes --kind through the import alias too", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "import", "--kind", "")
		Expect(out).To(ContainSubstring("spec"))
	})
})

var _ = Describe("kref track --kind completion", func() {
	It("completes --kind from the kinds present in the store", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "track", "--kind", "")
		Expect(out).To(ContainSubstring("spec"))
		Expect(out).To(ContainSubstring("note"))
		Expect(out).NotTo(ContainSubstring("document")) // no such kind exists yet
	})

	It("falls back to the flag default when the store holds no kinds", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "__complete", "track", "--kind", "")
		Expect(out).To(ContainSubstring("document"))
	})
})

var _ = Describe("kref flag-value completion", func() {
	It("completes --kind from the kinds present in the store", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "list", "--kind", "")
		Expect(out).To(ContainSubstring("spec"))
		Expect(out).To(ContainSubstring("note"))
	})

	It("completes --label from the labels present in the store", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "list", "--label", "")
		Expect(out).To(ContainSubstring("security"))
		Expect(out).To(ContainSubstring("ux"))
	})

	It("completes --tier from the fixed tier vocabulary", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "new", "--tier", "")
		Expect(out).To(ContainSubstring("private"))
		Expect(out).To(ContainSubstring("personal"))
		Expect(out).To(ContainSubstring("shared"))
	})

	It("completes --status from the fixed lifecycle vocabulary", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "list", "--status", "")
		Expect(out).To(ContainSubstring("accepted"))
	})

	It("completes --columns= with the column vocabulary and descriptions", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "list", "--columns=")
		Expect(out).To(MatchRegexp(`(?m)^id\t`))
		Expect(out).To(MatchRegexp(`(?m)^kind\t`))
		Expect(out).To(ContainSubstring("ShellCompDirectiveNoFileComp"))
		Expect(out).To(ContainSubstring("ShellCompDirectiveNoSpace"))
	})

	It("completes the segment after a comma, excluding already-chosen columns", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "list", "--columns=id,k")
		Expect(out).To(MatchRegexp(`(?m)^id,kind\t`))
		Expect(out).NotTo(MatchRegexp(`(?m)^id,id\t`))

		out = run("--dir", dir, "__complete", "list", "--columns=kind,")
		Expect(out).To(MatchRegexp(`(?m)^kind,id\t`))
		Expect(out).NotTo(MatchRegexp(`(?m)^kind,kind\t`))
	})

	It("filters --columns candidates by the typed prefix", func() {
		dir, _, _, _ := completionRepo()
		out := run("--dir", dir, "__complete", "list", "--columns=t")
		Expect(out).To(MatchRegexp(`(?m)^tier\t`))
		Expect(out).To(MatchRegexp(`(?m)^title\t`))
		Expect(out).NotTo(MatchRegexp(`(?m)^id\t`))
	})
})

var _ = Describe("tier completion", func() {
	It("offers custom tiers on retier and --tier once declared", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "tier", "add", "research", "--type", "personal")
		Expect(declaredTierNames(&dir)).To(Equal([]string{"private", "personal", "research", "shared"}))
		Expect(remoteTierNames(&dir)).To(Equal([]string{"personal", "research", "shared"}))
		Expect(customTierNames(&dir)).To(Equal([]string{"research"}))
	})
})
