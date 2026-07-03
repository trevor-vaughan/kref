package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

func run(args ...string) string {
	GinkgoHelper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	Expect(cmd.Execute()).To(Succeed())
	return out.String()
}

func runIn(stdin string, args ...string) string {
	GinkgoHelper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	Expect(cmd.Execute()).To(Succeed())
	return out.String()
}

var _ = Describe("formatCLIError", func() {
	It("emits a JSON envelope under --json", func() {
		Expect(formatCLIError(errors.New("entry x not found"), true)).
			To(Equal(`{"error":"entry x not found"}`))
	})

	It("emits a plain error line without --json", func() {
		Expect(formatCLIError(errors.New("entry x not found"), false)).
			To(Equal("error: entry x not found"))
	})
})

var _ = Describe("kref CLI", func() {
	It("round-trips init/add/list/show", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")
		out := run("--dir", dir, "new", "--kind", "spec", "--title", "Auth", "--body", "design", "--json")

		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		Expect(added.ID).NotTo(BeEmpty())

		Expect(run("--dir", dir, "list", "--json")).To(ContainSubstring("Auth"))
		Expect(run("--dir", dir, "show", added.ID, "--json")).To(ContainSubstring("design"))
	})

	It("collapses duplicate-title entries in the human list and reveals them with --all", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--kind", "note", "--title", "Auth Design", "--body", "one")
		run("--dir", dir, "new", "--kind", "note", "--title", "Auth Design", "--body", "two")

		def := run("--dir", dir, "list")
		Expect(def).To(ContainSubstring("(×2)"))
		Expect(def).To(ContainSubstring("1 entry"))

		all := run("--dir", dir, "list", "--all")
		Expect(all).To(ContainSubstring("2 entries"))
		Expect(all).NotTo(ContainSubstring("(×2)"))
	})

	It("hides superseded entries by default but --json still returns them", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--kind", "note", "--title", "Old", "--body", "b", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		run("--dir", dir, "status", added.ID, "superseded")

		Expect(run("--dir", dir, "list")).NotTo(ContainSubstring("Old"))
		Expect(run("--dir", dir, "list", "--all")).To(ContainSubstring("Old"))
		Expect(run("--dir", dir, "list", "--json")).To(ContainSubstring("Old"))
	})

	It("supersede drops the old entry from the default list", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		o := run("--dir", dir, "new", "--title", "Old", "--body", "a", "--json")
		n := run("--dir", dir, "new", "--title", "New", "--body", "b", "--json")
		var oldE, newE struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(o), &oldE)).To(Succeed())
		Expect(json.Unmarshal([]byte(n), &newE)).To(Succeed())

		run("--dir", dir, "supersede", oldE.ID, newE.ID)

		list := run("--dir", dir, "list")
		Expect(list).NotTo(ContainSubstring("Old"))
		Expect(list).To(ContainSubstring("New"))
	})
})

var _ = Describe("kref purge", func() {
	add := func(dir, title string) string {
		GinkgoHelper()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", title, "--body", "x", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		return added.ID
	}

	It("hard-deletes with --force", func() {
		dir := gitRepo()
		id := add(dir, "Doomed")
		Expect(run("--dir", dir, "purge", "--force", id)).To(ContainSubstring("purged"))
		Expect(run("--dir", dir, "list", "--json")).NotTo(ContainSubstring(id))
	})

	It("aborts when the prompt is declined", func() {
		dir := gitRepo()
		id := add(dir, "Keep")
		Expect(runIn("no\n", "--dir", dir, "purge", id)).To(ContainSubstring("aborted"))
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring("Keep"))
	})

	It("proceeds when the prompt is confirmed with yes", func() {
		dir := gitRepo()
		id := add(dir, "Doomed2")
		out := runIn("yes\n", "--dir", dir, "purge", id)
		Expect(out).To(ContainSubstring("purged"))
		Expect(run("--dir", dir, "list", "--json")).NotTo(ContainSubstring(id))
	})

	It("reports gc=false by default and gc=true with --gc", func() {
		dir := gitRepo()
		id := add(dir, "GcDefault")
		Expect(run("--dir", dir, "purge", "--force", "--json", id)).To(ContainSubstring(`"gc": false`))

		id2 := add(dir, "GcOptIn")
		Expect(run("--dir", dir, "purge", "--force", "--gc", "--json", id2)).To(ContainSubstring(`"gc": true`))
	})
})

var _ = Describe("kref sync CLI", func() {
	It("pushes from A and pulls into B via configured remotes", func() {
		dirA := gitRepo()
		dirB := gitRepo()
		run("--dir", dirA, "init", "--name", "A", "--email", "a@e.com")
		run("--dir", dirB, "init", "--name", "B", "--email", "b@e.com")
		run("--dir", dirA, "remote", "set", "shared", "peer", dirB)
		run("--dir", dirB, "remote", "set", "shared", "peer", dirA)

		out := run("--dir", dirA, "new", "--tier", "shared", "--title", "Hello", "--body", "x", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		run("--dir", dirA, "sync", "push", "--tier", "shared")
		run("--dir", dirB, "sync", "pull", "--tier", "shared")
		Expect(run("--dir", dirB, "list", "--json")).To(ContainSubstring("Hello"))
	})

	It("errors when pushing the private tier", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "A", "--email", "a@e.com")
		var out bytes.Buffer
		cmd := newRootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"--dir", dir, "sync", "push", "--tier", "private"})
		Expect(cmd.Execute()).To(HaveOccurred())
	})

	It("kref sync push blocks a secret with a remediation runbook", func() {
		dirA := gitRepo()
		dirB := gitRepo()
		run("--dir", dirA, "init", "--name", "A", "--email", "a@e.com")
		run("--dir", dirB, "init", "--name", "B", "--email", "b@e.com")
		run("--dir", dirA, "remote", "set", "shared", "peer", dirB)
		run("--dir", dirA, "new", "--tier", "shared", "--title", "Leaky", "--body", "ghp_012345678901234567890123456789abcdef")

		// The push must fail at the scan gate; run() asserts success, so drive
		// the command directly to capture the error.
		cmd := newRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"--dir", dirA, "sync", "push", "--tier", "shared"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("push blocked"))
		Expect(err.Error()).To(ContainSubstring("kref purge"))
		Expect(err.Error()).NotTo(ContainSubstring("ghp_012345678901234567890123456789abcdef"))
	})

	It("list --new shows incoming and unpushed; log --since-pull shows post-pull edits", func() {
		// Use a bare hub so A-push and B-pull are independent — avoids the
		// peer-to-peer problem where A's push would directly populate B's refs
		// before B's pull, making MergeAll see nothing new.
		hub := GinkgoT().TempDir()
		Expect(exec.Command("git", "init", "--bare", hub).Run()).To(Succeed())

		dirA := gitRepo()
		dirB := gitRepo()
		run("--dir", dirA, "init", "--name", "A", "--email", "a@e.com")
		run("--dir", dirB, "init", "--name", "B", "--email", "b@e.com")
		run("--dir", dirA, "remote", "set", "shared", "hub", hub)
		run("--dir", dirB, "remote", "set", "shared", "hub", hub)

		out := run("--dir", dirA, "new", "--tier", "shared", "--title", "FromA", "--body", "x", "--json")
		var fromA struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &fromA)).To(Succeed())
		run("--dir", dirA, "sync", "push", "--tier", "shared")
		run("--dir", dirB, "sync", "pull", "--tier", "shared")
		run("--dir", dirB, "new", "--tier", "shared", "--title", "LocalB", "--body", "y")

		view := run("--dir", dirB, "list", "--new")
		Expect(view).To(ContainSubstring("Incoming"))
		Expect(view).To(ContainSubstring("FromA"))
		Expect(view).To(ContainSubstring("Unpushed"))
		Expect(view).To(ContainSubstring("LocalB"))

		Expect(run("--dir", dirB, "log", fromA.ID, "--since-pull")).NotTo(ContainSubstring("set-body"))
	})
})

var _ = Describe("kref --tier", func() {
	It("stores into the named tier and rejects unknown tiers", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Mine", "--body", "x", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		Expect(run("--dir", dir, "show", added.ID, "--json")).To(ContainSubstring(`"tier": "personal"`))

		var buf bytes.Buffer
		c := newRootCmd()
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "new", "--tier", "bogus", "--title", "X"})
		Expect(c.Execute()).To(HaveOccurred())
	})
})

var _ = Describe("JSON schema", func() {
	It("uses snake_case keys consistently across add and show", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "Schema", "--body", "b", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		show := run("--dir", dir, "show", added.ID, "--json")
		Expect(show).To(ContainSubstring(`"id":`))
		Expect(show).To(ContainSubstring(`"created_at":`))
		Expect(show).To(ContainSubstring(`"created_by_email":`))
		Expect(show).NotTo(ContainSubstring(`"ID":`))
		Expect(show).NotTo(ContainSubstring(`"CreatedAt":`))
	})
})

var _ = Describe("kref version", func() {
	It("reports a version (default dev under go test)", func() {
		out := run("version")
		Expect(out).To(ContainSubstring("kref"))
		Expect(out).To(ContainSubstring("dev"))
	})
})

var _ = Describe("version output", func() {
	It("emits an identical one-line `kref <version>` for the flag and the subcommand", func() {
		flag := run("--version")
		Expect(flag).To(MatchRegexp(`^kref \S+\n$`))
		Expect(flag).NotTo(ContainSubstring("{"))

		// The subcommand now follows the emit() convention: same plain text as
		// the flag by default, JSON only under --json.
		sub := run("version")
		Expect(sub).To(Equal(flag))
		Expect(sub).NotTo(ContainSubstring("{"))
	})

	It("emits JSON for the subcommand under --json", func() {
		out := run("version", "--json")
		Expect(out).To(ContainSubstring(`"version"`))
	})
})

var _ = Describe("sync with no remotes", func() {
	It("reports nothing-to-push rather than pushed", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "sync", "push", "--json")
		Expect(out).To(ContainSubstring("nothing-to-push"))
		Expect(out).NotTo(ContainSubstring(`"pushed"`))
	})
})

var _ = Describe("ingest CLI", func() {
	It("ingests a directory and emits a JSON array of results", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		sub := filepath.Join(dir, "specs")
		Expect(os.MkdirAll(sub, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(sub, "a.md"), []byte("# A\n"), 0o644)).To(Succeed())

		out := run("--dir", dir, "ingest", sub, "--json")
		var arr []map[string]any
		Expect(json.Unmarshal([]byte(out), &arr)).To(Succeed())
		Expect(arr).To(HaveLen(1))
		Expect(arr[0]["action"]).To(Equal("created"))
	})
})

var _ = Describe("command aliases", func() {
	// The canonical name (left) is what the docs use; the aliases (right) are
	// syntactic sugar resolved natively by cobra. Keep this in sync with the
	// alias convention recorded in AGENTS.md.
	expected := map[string][]string{
		"new":       {"create"},
		"update":    {"set"},
		"ingest":    {"import", "add"},
		"show":      {"cat", "view", "get"},
		"list":      {"ls"},
		"log":       {"audit"},
		"rm":        {"remove", "delete", "del"},
		"purge":     {"destroy"},
		"remote":    {"remotes"},
		"version":   {"ver"},
		"retier":    {"mv"},
		"agents_md": {"agents-md"},
		"fav":       {"alt"},
	}

	It("declares the documented aliases on each command", func() {
		got := map[string][]string{}
		for _, c := range newRootCmd().Commands() {
			if len(c.Aliases) > 0 {
				got[c.Name()] = c.Aliases
			}
		}
		Expect(got).To(Equal(expected))
	})

	It("lists aliases inline in the top-level --help", func() {
		out := run("--help")
		Expect(out).To(ContainSubstring("new (create)"))
		Expect(out).To(ContainSubstring("ingest (import, add)"))
		Expect(out).To(ContainSubstring("list (ls)"))
		Expect(out).To(ContainSubstring("rm (remove, delete, del)"))
		Expect(out).To(ContainSubstring("purge (destroy)"))
		// commands without aliases stay bare.
		Expect(out).To(MatchRegexp(`(?m)^\s+init\s+Initialize`))
	})

	It("resolves commands invoked by an alias", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")

		// `create` is an alias for `new`; `ls` for `list`.
		out := run("--dir", dir, "create", "--kind", "spec", "--title", "Aliased", "--body", "x", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		Expect(added.ID).NotTo(BeEmpty())

		Expect(run("--dir", dir, "ls", "--json")).To(ContainSubstring("Aliased"))
		// `cat` and `get` are aliases for `show`.
		Expect(run("--dir", dir, "cat", added.ID, "--json")).To(ContainSubstring("Aliased"))
		Expect(run("--dir", dir, "get", added.ID, "--json")).To(ContainSubstring("Aliased"))

		// `remove` is an alias for `rm` (soft-delete).
		Expect(run("--dir", dir, "remove", added.ID)).To(ContainSubstring("tombstoned"))

		// `ver` is an alias for `version`.
		Expect(run("ver")).To(MatchRegexp(`^kref \S+\n$`))
	})
})

var _ = Describe("list short ids", func() {
	It("prints a 12-hex short id in human output but the full id in JSON", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--title", "Shorty", "--body", "b")
		human := run("--dir", dir, "list")
		Expect(human).To(MatchRegexp(`[0-9a-f]{12}`))
		Expect(human).NotTo(MatchRegexp(`[0-9a-f]{64}`))
		Expect(run("--dir", dir, "list", "--json")).To(MatchRegexp(`[0-9a-f]{64}`))
	})
})

var _ = Describe("list human output", func() {
	It("prints a header and a tier column by default", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--kind", "spec", "--title", "Auth flow spec", "--tier", "shared")
		out := run("--dir", dir, "list")
		Expect(out).To(MatchRegexp(`(?m)^TIER\s+ID\s+KIND\s+STATUS\s+TITLE$`))
		Expect(out).To(ContainSubstring("○ shared"))
		Expect(out).To(ContainSubstring("1 entry"))
	})
	It("accepts --no-pager and prints the table unpaged", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--title", "Paged entry")
		out := run("--dir", dir, "list", "--no-pager")
		Expect(out).To(ContainSubstring("Paged entry"))
		Expect(out).To(MatchRegexp(`(?m)^TIER\s+ID\s+KIND\s+STATUS\s+TITLE$`))
	})
	It("filters by --tier", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--title", "Shared one", "--tier", "shared")
		run("--dir", dir, "new", "--title", "Private one", "--tier", "private")
		out := run("--dir", dir, "list", "--tier", "private")
		Expect(out).To(ContainSubstring("Private one"))
		Expect(out).NotTo(ContainSubstring("Shared one"))
	})
})

var _ = Describe("sorting list and search output", func() {
	seed := func() string {
		GinkgoHelper()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--title", "Bravo", "--kind", "spec", "--body", "auth")
		run("--dir", dir, "new", "--title", "Alpha", "--kind", "note", "--body", "auth auth")
		run("--dir", dir, "new", "--title", "Charlie", "--kind", "adr", "--body", "auth")
		return dir
	}

	It("sorts the table by a field, ascending by default", func() {
		dir := seed()
		out := run("--dir", dir, "list", "--sort", "title")
		Expect(strings.Index(out, "Alpha")).To(BeNumerically("<", strings.Index(out, "Bravo")))
		Expect(strings.Index(out, "Bravo")).To(BeNumerically("<", strings.Index(out, "Charlie")))
	})

	It("reverses with :desc and sorts --json output too", func() {
		dir := seed()
		out := run("--dir", dir, "list", "--sort", "title:desc", "--json")
		var items []struct {
			Title string `json:"title"`
		}
		Expect(json.Unmarshal([]byte(out), &items)).To(Succeed())
		Expect(items[0].Title).To(Equal("Charlie"))
		Expect(items[len(items)-1].Title).To(Equal("Alpha"))
	})

	It("sorts by updated recency", func() {
		dir := seed()
		// The default sort is edited:desc, which a title-only update must NOT
		// perturb (EditedAt tracks body edits, not metadata). Capture the default
		// order by stable id before the update so we can assert it is invariant.
		defOrderIDs := func() []string {
			GinkgoHelper()
			lines := strings.Split(strings.TrimSpace(
				run("--dir", dir, "list", "--plain", "--columns=id")), "\n")
			return lines
		}
		before := defOrderIDs()

		// Op timestamps are git-commit second-precision; guarantee the update
		// lands in a later second than the seed entries.
		time.Sleep(1100 * time.Millisecond)
		run("--dir", dir, "update", run("--dir", dir, "list", "--plain", "--columns=id", "--sort", "title")[:12], "--title", "Alpha touched")
		out := run("--dir", dir, "list", "--sort", "updated:desc", "--json")
		var items []struct {
			Title string `json:"title"`
		}
		Expect(json.Unmarshal([]byte(out), &items)).To(Succeed())
		Expect(items[0].Title).To(Equal("Alpha touched"))

		// A bare date key means newest first — no :desc needed.
		bare := run("--dir", dir, "list", "--sort", "updated", "--json")
		var bareItems []struct {
			Title string `json:"title"`
		}
		Expect(json.Unmarshal([]byte(bare), &bareItems)).To(Succeed())
		Expect(bareItems[0].Title).To(Equal("Alpha touched"))

		asc := run("--dir", dir, "list", "--sort", "updated:asc", "--json")
		var ascItems []struct {
			Title string `json:"title"`
		}
		Expect(json.Unmarshal([]byte(asc), &ascItems)).To(Succeed())
		Expect(ascItems[len(ascItems)-1].Title).To(Equal("Alpha touched"))

		// No --sort at all: the default order is edited:desc, and the title-only
		// update left every entry's EditedAt untouched — so the default order is
		// byte-for-byte invariant (compared by stable id, immune to the rename).
		Expect(defOrderIDs()).To(Equal(before))
	})

	It("rejects an unknown sort key", func() {
		dir := seed()
		c := newRootCmd()
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "list", "--sort", "flavor"})
		err := c.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown sort key"))
	})

	It("orders --plain output as well", func() {
		dir := seed()
		out := run("--dir", dir, "list", "--plain", "--columns=title", "--sort", "title:desc")
		lines := strings.Split(strings.TrimSpace(out), "\n")
		Expect(lines[0]).To(Equal("Charlie"))
		Expect(lines[len(lines)-1]).To(Equal("Alpha"))
	})

	It("search accepts --sort to override the match-count order", func() {
		dir := seed()
		out := run("--dir", dir, "search", "auth", "--sort", "title")
		Expect(strings.Index(out, "Alpha")).To(BeNumerically("<", strings.Index(out, "Bravo")))
		Expect(strings.Index(out, "Bravo")).To(BeNumerically("<", strings.Index(out, "Charlie")))
	})
})

var _ = Describe("rm / restore round-trip", func() {
	It("hides a tombstoned entry, surfaces it with --include-deleted, and restores it", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "Revivable", "--body", "b", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		run("--dir", dir, "rm", added.ID)
		Expect(run("--dir", dir, "list")).NotTo(ContainSubstring("Revivable"))
		withDeleted := run("--dir", dir, "list", "--include-deleted")
		Expect(withDeleted).To(ContainSubstring("Revivable"))
		Expect(withDeleted).To(ContainSubstring("(deleted)"))

		Expect(run("--dir", dir, "restore", added.ID)).To(ContainSubstring("restored"))
		Expect(run("--dir", dir, "list")).To(ContainSubstring("Revivable"))
	})
})

var _ = Describe("kref agents_md", func() {
	It("emits the AGENTS.md policy block with the core disciplines", func() {
		out := run("agents_md")
		Expect(out).To(ContainSubstring("## kref"))
		Expect(out).To(ContainSubstring("--json"))
		Expect(out).To(ContainSubstring("kref log"), "guarded-write discipline")
		Expect(out).To(ContainSubstring("--actor"))
		Expect(out).To(ContainSubstring("kref_patch"))
		Expect(out).To(ContainSubstring("private tier"))
		Expect(out).To(ContainSubstring("kref status"), "lifecycle-currency discipline")
		Expect(out).To(ContainSubstring("plan to its spec"), "cross-linking design material")
		Expect(out).To(ContainSubstring("kref fav add"), "favorites + config guidance")
		// Version-accurate: never mention unshipped features.
		Expect(out).NotTo(ContainSubstring("kref alias"))
		Expect(out).NotTo(ContainSubstring("kref comment"))
		Expect(out).NotTo(ContainSubstring("--tier agent"))
	})

	It("emits a complete SKILL.md with --skill", func() {
		out := run("agents_md", "--skill")
		Expect(out).To(HavePrefix("---\n"))
		Expect(out).To(ContainSubstring("name: kref"))
		Expect(out).To(ContainSubstring("description:"))
		Expect(out).To(ContainSubstring("kref search"))
		Expect(out).To(ContainSubstring("kref_patch"))
		Expect(out).To(ContainSubstring("--plain"))
	})

	It("works without a repository (no store access)", func() {
		out := run("--dir", GinkgoT().TempDir(), "agents_md")
		Expect(out).To(ContainSubstring("## kref"))
	})
})

var _ = Describe("help grouping", func() {
	It("renders titled groups with aliases inline", func() {
		out := run("--help")
		Expect(out).To(ContainSubstring("Core Commands:"))
		Expect(out).To(ContainSubstring("Lifecycle Commands:"))
		Expect(out).To(ContainSubstring("Sync Commands:"))
		Expect(out).To(ContainSubstring("Setup Commands:"))
		Expect(out).To(ContainSubstring("new (create)"))
		Expect(strings.Index(out, "Core Commands:")).To(BeNumerically("<", strings.Index(out, "Sync Commands:")))
	})
})

var _ = Describe("ingest post-trailer content", func() {
	It("absorbs text appended after the kref-id trailer on re-ingest", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		md := filepath.Join(dir, "n.md")
		Expect(os.WriteFile(md, []byte("# N\n\noriginal body\n"), 0o644)).To(Succeed())
		run("--dir", dir, "ingest", md) // stamps the trailer at EOF

		data, err := os.ReadFile(md)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(md, append(data, []byte("\nappended paragraph\n")...), 0o644)).To(Succeed())

		out := run("--dir", dir, "ingest", md, "--json")
		var arr []map[string]any
		Expect(json.Unmarshal([]byte(out), &arr)).To(Succeed())
		Expect(arr[0]["action"]).To(Equal("updated"))

		id := arr[0]["id"].(string)
		Expect(run("--dir", dir, "show", id)).To(ContainSubstring("appended paragraph"))
	})
})

var _ = Describe("kref hooks", func() {
	It("prints and installs the lefthook config", func() {
		Expect(run("hooks", "print")).To(ContainSubstring("pre-push:"))
		dir := GinkgoT().TempDir()
		out := run("--dir", dir, "hooks", "install")
		// The command only writes .lefthook.yml; the hooks are dormant until
		// `lefthook install` registers them, so the status must not claim they
		// are live (see review P1-4).
		Expect(out).To(ContainSubstring(`"status": "written"`))
		Expect(out).To(ContainSubstring("lefthook install"))
		data, err := os.ReadFile(filepath.Join(dir, ".lefthook.yml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("sync push"))
	})

	It("honors --ingest-path overrides", func() {
		out := run("hooks", "print", "--ingest-path", "foo", "--ingest-path", "bar")
		Expect(out).To(ContainSubstring("-- foo bar"))
		Expect(out).NotTo(ContainSubstring("openspec"))
	})

	It("uses the default paths when no override is given", func() {
		Expect(run("hooks", "print")).To(ContainSubstring("docs/superpowers/plans specs .specify openspec"))
	})
})

var _ = Describe("add human output", func() {
	It("prints a confirmation echoing the tier and kind", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--kind", "spec", "--title", "Auth", "--tier", "shared")
		Expect(out).To(HavePrefix("added ○ shared"))
		Expect(out).To(ContainSubstring(`spec  "Auth"`))
	})
})

var _ = Describe("ingest human output", func() {
	It("prints a per-file line and a summary by default", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		md := filepath.Join(GinkgoT().TempDir(), "note.md")
		Expect(os.WriteFile(md, []byte("# Hello\n\nbody\n"), 0o644)).To(Succeed())
		out := run("--dir", dir, "ingest", md, "--tier", "shared")
		Expect(out).To(ContainSubstring("created"))
		Expect(out).To(ContainSubstring("○ shared"))
		Expect(out).To(MatchRegexp(`\d+ created`))
	})
})

var _ = Describe("add title derivation", func() {
	It("derives the title from the body's H1 when --title is omitted", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--body", "# Derived heading\n\nprose")
		out := run("--dir", dir, "list")
		Expect(out).To(ContainSubstring("Derived heading"))
	})
	It("errors when neither --title nor a usable body is given", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		var buf bytes.Buffer
		c := newRootCmd()
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "new", "--body", "   "})
		Expect(c.Execute()).To(HaveOccurred())
	})
})

var _ = Describe("update command", func() {
	add := func(dir string) string {
		GinkgoHelper()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "Orig", "--body", "old body", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}

	It("replaces the body via --body", func() {
		dir := gitRepo()
		id := add(dir)
		Expect(run("--dir", dir, "update", id, "--body", "new body")).To(ContainSubstring("updated"))
		Expect(run("--dir", dir, "show", id)).To(ContainSubstring("new body"))
	})
	It("reads the body from stdin when --body is omitted", func() {
		dir := gitRepo()
		id := add(dir)
		runIn("piped body\n", "--dir", dir, "update", id)
		Expect(run("--dir", dir, "show", id)).To(ContainSubstring("piped body"))
	})
	It("also sets the title when --title is given", func() {
		dir := gitRepo()
		id := add(dir)
		run("--dir", dir, "update", id, "--body", "x", "--title", "Renamed")
		Expect(run("--dir", dir, "show", id)).To(ContainSubstring("Renamed"))
	})
})

var _ = Describe("kref update rework", func() {
	setup := func() (string, string) {
		GinkgoHelper()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--kind", "document", "--title", "Orig", "--body", "# Orig\n\nbody", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return dir, a.ID
	}
	It("renames with --title only (no body)", func() {
		dir, id := setup()
		run("--dir", dir, "update", id, "--title", "Renamed")
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring(`"title": "Renamed"`))
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring(`"body": "# Orig`))
	})
	It("re-kinds with --kind only", func() {
		dir, id := setup()
		run("--dir", dir, "update", id, "--kind", "spec")
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring(`"kind": "spec"`))
	})
	It("overrides the body from --file (trailer stripped)", func() {
		dir, id := setup()
		f := filepath.Join(dir, "new.md")
		Expect(os.WriteFile(f, []byte("# New\n\nfresh body\n\n<!-- kref-id: "+strings.Repeat("d", 64)+" -->\n"), 0o644)).To(Succeed())
		run("--dir", dir, "update", id, "--file", f)
		out := run("--dir", dir, "show", id, "--json")
		Expect(out).To(ContainSubstring("fresh body"))
		Expect(out).NotTo(ContainSubstring("kref-id"))
	})
	It("errors when both --body and --file are given", func() {
		dir, id := setup()
		var buf bytes.Buffer
		c := newRootCmd()
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "update", id, "--body", "x", "--file", "y"})
		Expect(c.Execute()).To(HaveOccurred())
	})
	It("errors with no mutation and empty stdin", func() {
		dir, id := setup()
		var buf bytes.Buffer
		c := newRootCmd()
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetIn(strings.NewReader(""))
		c.SetArgs([]string{"--dir", dir, "update", id})
		Expect(c.Execute()).To(HaveOccurred())
	})
	// Regression: `kref update <id> --kind todo` on an interactive terminal hung
	// forever, because the stdin body read only short-circuited for
	// reattribute/content-type metadata updates — a tty stdin never EOFs.
	Describe("stdinBodyAllowed", func() {
		It("never consumes an interactive terminal, whatever the flags", func() {
			Expect(stdinBodyAllowed(true, false, false, false, false)).To(BeFalse()) // bare update
			Expect(stdinBodyAllowed(true, false, false, false, true)).To(BeFalse())  // --kind only (the reported hang)
			Expect(stdinBodyAllowed(true, false, false, true, false)).To(BeFalse())  // --title only
			Expect(stdinBodyAllowed(true, true, true, true, true)).To(BeFalse())
		})
		It("reads a piped body for bare, --title, and --kind updates", func() {
			Expect(stdinBodyAllowed(false, false, false, false, false)).To(BeTrue()) // pipe a body on stdin
			Expect(stdinBodyAllowed(false, false, false, true, false)).To(BeTrue())  // --title + piped body
			Expect(stdinBodyAllowed(false, false, false, false, true)).To(BeTrue())  // --kind + piped body
		})
		It("keeps reattribute/content-type-only updates off the stream", func() {
			Expect(stdinBodyAllowed(false, true, false, false, false)).To(BeFalse()) // --reset-author/--author only
			Expect(stdinBodyAllowed(false, false, true, false, false)).To(BeFalse()) // --content-type only
			Expect(stdinBodyAllowed(false, true, true, false, false)).To(BeFalse())
			// ...but combined with title/kind the piped body still applies.
			Expect(stdinBodyAllowed(false, true, false, false, true)).To(BeTrue())
		})
	})
	It("fails closed on a secret in --file for a syncable entry", func() {
		dir, id := setup()
		// Retier to shared so the entry is syncable (non-private).
		run("--dir", dir, "retier", id, "shared", "--yes")
		f := filepath.Join(dir, "leak.md")
		Expect(os.WriteFile(f, []byte("# L\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"), 0o644)).To(Succeed())
		var buf bytes.Buffer
		c := newRootCmd()
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "update", id, "--file", f})
		Expect(c.Execute()).To(HaveOccurred())
	})

	It("allows a secret in --file for a private entry (stays local)", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "private", "--title", "P", "--body", "x", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		f := filepath.Join(dir, "leak.md")
		Expect(os.WriteFile(f, []byte("# L\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"), 0o644)).To(Succeed())
		run("--dir", dir, "update", a.ID, "--file", f)
		Expect(run("--dir", dir, "show", a.ID, "--json")).To(ContainSubstring("ghp_012345678901234567890123456789abcdef"))
	})
})

var _ = Describe("kref update by path", func() {
	It("resolves the entry via its file's kref-id trailer", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		md := filepath.Join(dir, "note.md")
		Expect(os.WriteFile(md, []byte("# Note\n\nbody\n"), 0o644)).To(Succeed())
		run("--dir", dir, "ingest", md) // stamps the kref-id trailer into the file

		run("--dir", dir, "update", md, "--title", "Renamed")
		Expect(run("--dir", dir, "show", md, "--json")).To(ContainSubstring(`"title": "Renamed"`))
	})
})

var _ = Describe("kref link add/rm", func() {
	two := func(tierA, tierB string) (string, string, string) {
		GinkgoHelper()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		ja := run("--dir", dir, "new", "--tier", tierA, "--title", "A", "--body", "a", "--json")
		jb := run("--dir", dir, "new", "--tier", tierB, "--title", "B", "--body", "b", "--json")
		var a, b struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(ja), &a)).To(Succeed())
		Expect(json.Unmarshal([]byte(jb), &b)).To(Succeed())
		return dir, a.ID, b.ID
	}
	It("adds a free-form typed link visible via kref links", func() {
		dir, a, b := two("shared", "shared")
		Expect(run("--dir", dir, "link", "add", a, b, "--type", "depends-on")).To(ContainSubstring("linked"))
		out := run("--dir", dir, "links", a)
		Expect(out).To(ContainSubstring("Outgoing:"))
		Expect(out).To(ContainSubstring("depends-on"))
	})
	It("removes a link", func() {
		dir, a, b := two("shared", "shared")
		run("--dir", dir, "link", "add", a, b)
		run("--dir", dir, "link", "rm", a, b)
		Expect(run("--dir", dir, "links", a)).To(ContainSubstring("no links"))
	})
	It("warns on stderr but succeeds when linking shared→private (id rides along)", func() {
		dir, a, b := two("shared", "private")
		var out bytes.Buffer
		c := newRootCmd()
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs([]string{"--dir", dir, "link", "add", a, b})
		Expect(c.Execute()).To(Succeed())
		Expect(out.String()).To(ContainSubstring("private"))
	})
})

var _ = Describe("kref ingest --kind", func() {
	It("ingests a file as the requested kind", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		p := filepath.Join(dir, "s.md")
		Expect(os.WriteFile(p, []byte("# Spec\nbody\n"), 0o644)).To(Succeed())
		run("--dir", dir, "ingest", p, "--kind", "spec")
		Expect(run("--dir", dir, "list", "--kind", "spec", "--json")).To(ContainSubstring("Spec"))
	})
})

var _ = Describe("kref resolve", func() {
	add := func(dir string) string {
		GinkgoHelper()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "X", "--body", "x", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}
	It("is a friendly no-op on a non-merged entry", func() {
		dir := gitRepo()
		id := add(dir)
		Expect(run("--dir", dir, "resolve", id)).To(ContainSubstring("nothing to resolve"))
	})
	It("reports nothing-to-resolve in JSON on the no-op path", func() {
		dir := gitRepo()
		id := add(dir)
		Expect(run("--dir", dir, "resolve", id, "--json")).To(ContainSubstring(`"status": "nothing-to-resolve"`))
	})
})

var _ = Describe("status command", func() {
	It("sets an entry's status and rejects unknown statuses", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "S", "--body", "b", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		Expect(run("--dir", dir, "status", added.ID, "accepted")).To(ContainSubstring("accepted"))
		Expect(run("--dir", dir, "show", added.ID, "--json")).To(ContainSubstring(`"status": "accepted"`))

		var buf bytes.Buffer
		c := newRootCmd()
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "status", added.ID, "bogus"})
		Expect(c.Execute()).To(HaveOccurred())
	})

	It("accepts the obsolete status", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "S", "--body", "b", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		Expect(run("--dir", dir, "status", added.ID, "obsolete")).To(ContainSubstring("obsolete"))
		Expect(run("--dir", dir, "show", added.ID, "--json")).To(ContainSubstring(`"status": "obsolete"`))
	})
})

var _ = Describe("kref search", func() {
	seed := func() string {
		GinkgoHelper()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--title", "Auth flow", "--body", "auth token and auth cookie")
		run("--dir", dir, "new", "--title", "Authz notes", "--body", "roles")
		run("--dir", dir, "new", "--title", "Billing", "--body", "invoices")
		return dir
	}

	It("finds entries case-insensitively and counts matches per entry, most first", func() {
		dir := seed()
		out := run("--dir", dir, "search", "auth", "--no-pager")
		Expect(out).To(MatchRegexp(`(?m)^MATCHES\s`))
		Expect(out).To(MatchRegexp(`(?m)^\s*3\s+.*Auth flow`)) // title 1 + body 2
		Expect(out).To(MatchRegexp(`(?m)^\s*1\s+.*Authz notes`))
		Expect(out).NotTo(ContainSubstring("Billing"))
		Expect(strings.Index(out, "Auth flow")).To(BeNumerically("<", strings.Index(out, "Authz notes")))
		Expect(out).To(ContainSubstring("2 entries, 4 matches"))
	})

	It("carries the match count in --json", func() {
		dir := seed()
		out := run("--dir", dir, "search", "auth", "--json")
		var res []struct {
			Title   string `json:"title"`
			Matches int    `json:"matches"`
		}
		Expect(json.Unmarshal([]byte(out), &res)).To(Succeed())
		Expect(res).To(HaveLen(2))
		Expect(res[0].Title).To(Equal("Auth flow"))
		Expect(res[0].Matches).To(Equal(3))
	})

	It("composes with the list filters", func() {
		dir := seed()
		out := run("--dir", dir, "search", "auth", "--kind", "document")
		Expect(out).To(ContainSubstring("Auth flow"))
	})

	It("no longer accepts list --search", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		var out bytes.Buffer
		cmd := newRootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"--dir", dir, "list", "--search", "auth"})
		Expect(cmd.Execute()).NotTo(Succeed())
	})

	It("search --plain emits TSV rows without header or footer", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")
		run("--dir", dir, "new", "--title", "Auth flow", "--kind", "spec", "--body", "auth auth auth")
		out := run("--dir", dir, "search", "auth", "--plain")
		lines := strings.Split(strings.TrimSpace(out), "\n")
		Expect(lines).To(HaveLen(1))
		fields := strings.Split(lines[0], "\t")
		Expect(fields).To(HaveLen(5)) // matches, tier, id, kind, title
		Expect(fields[3]).To(Equal("spec"))
		Expect(fields[4]).To(Equal("Auth flow"))
		Expect(out).NotTo(ContainSubstring("MATCHES")) // no table header
	})
})

var _ = Describe("labels CLI", func() {
	addID := func(dir string, args ...string) string {
		GinkgoHelper()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run(append([]string{"--dir", dir, "new", "--title", "L", "--body", "b", "--json"}, args...)...)
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}

	It("attaches labels at add time", func() {
		dir := gitRepo()
		id := addID(dir, "--label", "area:auth", "--label", "spec")
		Expect(run("--dir", dir, "show", id)).To(ContainSubstring("area:auth, spec"))
	})
	It("adds and removes labels via kref label", func() {
		dir := gitRepo()
		id := addID(dir)
		Expect(run("--dir", dir, "label", "add", id, "x", "y")).To(ContainSubstring("labeled"))
		Expect(run("--dir", dir, "show", id)).To(ContainSubstring("x, y"))
		run("--dir", dir, "label", "rm", id, "x")
		shown := run("--dir", dir, "show", id)
		Expect(shown).To(MatchRegexp(`(?m)^Labels\s+y$`))
		Expect(shown).NotTo(ContainSubstring("x, y"))
	})
})

var _ = Describe("list --label", func() {
	It("filters entries that carry all requested labels", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--title", "Tagged", "--body", "b", "--label", "area:auth", "--label", "spec")
		run("--dir", dir, "new", "--title", "Untagged", "--body", "b")
		out := run("--dir", dir, "list", "--label", "area:auth")
		Expect(out).To(ContainSubstring("Tagged"))
		Expect(out).NotTo(ContainSubstring("Untagged"))
	})
})

var _ = Describe("edit command", func() {
	It("opens $EDITOR on the body and saves the result", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "Before", "--body", "# Before\n\nold", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())

		// A non-interactive "editor": a shell script that overwrites the file.
		ed := filepath.Join(GinkgoT().TempDir(), "ed.sh")
		Expect(os.WriteFile(ed, []byte("#!/bin/sh\nprintf '# After\\n\\nnew text\\n' > \"$1\"\n"), 0o755)).To(Succeed())
		GinkgoT().Setenv("KREF_EDITOR", ed)

		Expect(run("--dir", dir, "edit", a.ID)).To(ContainSubstring("updated"))
		shown := run("--dir", dir, "show", a.ID)
		Expect(shown).To(ContainSubstring("new text"))
		Expect(shown).To(ContainSubstring("After")) // title re-derived from the new H1
	})
})

var _ = Describe("ingest provenance", func() {
	It("records an ingest-origin with the repo-relative source path", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		Expect(os.MkdirAll(filepath.Join(dir, "docs"), 0o755)).To(Succeed())
		md := filepath.Join(dir, "docs", "note.md")
		Expect(os.WriteFile(md, []byte("# Note\n\nbody\n"), 0o644)).To(Succeed())

		out := run("--dir", dir, "ingest", md, "--json")
		var arr []map[string]any
		Expect(json.Unmarshal([]byte(out), &arr)).To(Succeed())
		id := arr[0]["id"].(string)

		shown := run("--dir", dir, "show", id, "--json")
		Expect(shown).To(ContainSubstring(`"trigger": "ingest"`))
		Expect(shown).To(ContainSubstring(`"source_path": "docs/note.md"`))
	})
})

var _ = Describe("path addressing", func() {
	It("shows and restores an entry by its source file path", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		md := filepath.Join(dir, "note.md")
		Expect(os.WriteFile(md, []byte("# Note\n\nfindme\n"), 0o644)).To(Succeed())
		run("--dir", dir, "ingest", md) // stamps the trailer

		Expect(run("--dir", dir, "show", md)).To(ContainSubstring("findme"))
		run("--dir", dir, "rm", md)
		Expect(run("--dir", dir, "list")).NotTo(ContainSubstring("Note"))
		Expect(run("--dir", dir, "restore", md)).To(ContainSubstring("restored"))
		Expect(run("--dir", dir, "list")).To(ContainSubstring("Note"))
	})
})

var _ = Describe("add provenance", func() {
	It("records a human create-origin by default", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Alice", "--email", "a@e.com")
		out := run("--dir", dir, "new", "--title", "X", "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		shown := run("--dir", dir, "show", a.ID, "--json")
		Expect(shown).To(ContainSubstring(`"trigger": "create"`))
		Expect(shown).To(ContainSubstring(`"actor_kind": "human"`))
		Expect(shown).To(ContainSubstring(`"actor": "Alice"`))
	})
	It("records an agent create-origin when --actor is set", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Alice", "--email", "a@e.com")
		out := run("--dir", dir, "new", "--actor", "claude", "--title", "Y", "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		shown := run("--dir", dir, "show", a.ID, "--json")
		Expect(shown).To(ContainSubstring(`"actor_kind": "agent"`))
		Expect(shown).To(ContainSubstring(`"actor": "claude"`))
	})
	It("records an agent create-origin from the KREF_ACTOR env", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Alice", "--email", "a@e.com")
		GinkgoT().Setenv("KREF_ACTOR", "claude-env")
		out := run("--dir", dir, "new", "--title", "Z", "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		shown := run("--dir", dir, "show", a.ID, "--json")
		Expect(shown).To(ContainSubstring(`"actor_kind": "agent"`))
		Expect(shown).To(ContainSubstring(`"actor": "claude-env"`))
	})
})

var _ = Describe("merged flag wiring", func() {
	It("a linear entry is not flagged merged", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "Linear", "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		Expect(run("--dir", dir, "show", a.ID)).NotTo(ContainSubstring("◆ merged"))
		Expect(run("--dir", dir, "list")).NotTo(ContainSubstring("◆ merged"))
		Expect(run("--dir", dir, "show", a.ID, "--json")).To(ContainSubstring(`"merged": false`))
	})
})

var _ = Describe("log and diff", func() {
	add := func(dir string) string {
		GinkgoHelper()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "H", "--body", "v1", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}
	It("kref log shows the operation timeline", func() {
		dir := gitRepo()
		id := add(dir)
		run("--dir", dir, "update", id, "--body", "v2")
		out := run("--dir", dir, "log", id)
		Expect(out).To(ContainSubstring("create"))
		Expect(out).To(ContainSubstring("set-body"))
	})
	It("kref audit is an alias for log", func() {
		dir := gitRepo()
		id := add(dir)
		run("--dir", dir, "update", id, "--body", "v2")
		out := run("--dir", dir, "audit", id)
		Expect(out).To(ContainSubstring("create"))
		Expect(out).To(ContainSubstring("set-body"))
	})
	It("kref diff shows each body version", func() {
		dir := gitRepo()
		id := add(dir)
		run("--dir", dir, "update", id, "--body", "v2")
		out := run("--dir", dir, "diff", id)
		Expect(out).To(ContainSubstring("v1"))
		Expect(out).To(ContainSubstring("v2"))
	})
	It("kref diff --no-pager shows each body version without paging", func() {
		dir := gitRepo()
		id := add(dir)
		run("--dir", dir, "update", id, "--body", "v2")
		out := run("--dir", dir, "diff", id, "--no-pager")
		Expect(out).To(ContainSubstring("v1"))
		Expect(out).To(ContainSubstring("v2"))
	})
	It("emits snake_case JSON for log and diff", func() {
		dir := gitRepo()
		id := add(dir)
		logJSON := run("--dir", dir, "log", id, "--json")
		Expect(logJSON).To(ContainSubstring(`"op":`))
		Expect(logJSON).To(ContainSubstring(`"author":`))
		Expect(logJSON).NotTo(ContainSubstring(`"Op":`))
		diffJSON := run("--dir", dir, "diff", id, "--json")
		Expect(diffJSON).To(ContainSubstring(`"body":`))
		Expect(diffJSON).NotTo(ContainSubstring(`"Body":`))
	})
	It("kref log numbers body versions with change stats", func() {
		dir := gitRepo()
		id := add(dir)
		run("--dir", dir, "update", id, "--body", "second body")
		out := run("--dir", dir, "log", id)
		Expect(out).To(ContainSubstring("v1  +"))
		Expect(out).To(ContainSubstring("v2  +"))
		Expect(out).To(ContainSubstring("chars"))
		Expect(out).To(ContainSubstring("lines"))
	})
	It("kref diff defaults to inline diffs with +/- markers", func() {
		dir := gitRepo()
		id := add(dir) // body "v1"
		run("--dir", dir, "update", id, "--body", "hello kref")
		out := run("--dir", dir, "diff", id)
		Expect(out).To(ContainSubstring("=== v1 —"))
		Expect(out).To(ContainSubstring("=== v1 → v2 —"))
		Expect(out).To(ContainSubstring("- v1"))
		Expect(out).To(ContainSubstring("+ hello kref"))
	})
	It("kref diff <id> <n> shows just what vN changed", func() {
		dir := gitRepo()
		id := add(dir)
		run("--dir", dir, "update", id, "--body", "two")
		run("--dir", dir, "update", id, "--body", "three")
		out := run("--dir", dir, "diff", id, "3")
		Expect(out).To(ContainSubstring("=== v2 → v3 —"))
		Expect(out).NotTo(ContainSubstring("=== v1"))
	})
	It("kref diff <id> <m> <n> spans a version range", func() {
		dir := gitRepo()
		id := add(dir)
		run("--dir", dir, "update", id, "--body", "two")
		run("--dir", dir, "update", id, "--body", "three")
		out := run("--dir", dir, "diff", id, "1", "3")
		Expect(out).To(ContainSubstring("=== v1 → v3 —"))
	})
	It("kref diff --full keeps the whole-bodies view", func() {
		dir := gitRepo()
		id := add(dir)
		run("--dir", dir, "update", id, "--body", "second body")
		out := run("--dir", dir, "diff", id, "--full")
		Expect(out).To(ContainSubstring("=== version 1 —"))
		Expect(out).To(ContainSubstring("=== version 2 —"))
		Expect(out).To(ContainSubstring("second body"))
	})
	It("kref diff errors on a version that does not exist", func() {
		dir := gitRepo()
		id := add(dir)
		c := newRootCmd()
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "diff", id, "9"})
		err := c.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("v9 does not exist"))
	})

	It("tidy reports duplicate-title groups as JSON", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--kind", "note", "--title", "Auth Design", "--body", "one")
		run("--dir", dir, "new", "--kind", "note", "--title", "auth design", "--body", "two")

		out := run("--dir", dir, "tidy", "--json")
		var report struct {
			Duplicates []struct {
				NormalizedTitle string `json:"normalized_title"`
				Entries         []struct {
					ID string `json:"id"`
				} `json:"entries"`
			} `json:"duplicates"`
		}
		Expect(json.Unmarshal([]byte(out), &report)).To(Succeed())
		Expect(report.Duplicates).To(HaveLen(1))
		Expect(report.Duplicates[0].NormalizedTitle).To(Equal("auth design"))
		Expect(report.Duplicates[0].Entries).To(HaveLen(2))
	})
})

var _ = Describe("kref retier", func() {
	It("retier to shared moves an entry and records provenance", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Doc", "--body", "clean prose", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		Expect(run("--dir", dir, "retier", added.ID, "shared", "--yes")).To(ContainSubstring("shared"))
		show := run("--dir", dir, "show", added.ID, "--json")
		Expect(show).To(ContainSubstring(`"tier": "shared"`))
		Expect(show).To(ContainSubstring(`"trigger": "retier"`))
	})

	It("retier to private demotes an entry without prompting", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Doc", "--body", "b", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		Expect(run("--dir", dir, "retier", added.ID, "private")).To(ContainSubstring("private"))
		Expect(run("--dir", dir, "show", added.ID, "--json")).To(ContainSubstring(`"tier": "private"`))
	})

	It("retier to shared of a secret-bearing entry is blocked", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Leaky", "--body", "ghp_012345678901234567890123456789abcdef", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		cmd := newRootCmd()
		var b bytes.Buffer
		cmd.SetOut(&b)
		cmd.SetErr(&b)
		cmd.SetArgs([]string{"--dir", dir, "retier", added.ID, "shared", "--yes"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("retier blocked"))
		Expect(err.Error()).NotTo(ContainSubstring("ghp_012345678901234567890123456789abcdef"))
		Expect(run("--dir", dir, "show", added.ID, "--json")).To(ContainSubstring(`"tier": "personal"`))
	})

	It("retier to private warns when the entry was already pushed but still demotes", func() {
		dirA := gitRepo()
		dirB := gitRepo()
		run("--dir", dirA, "init", "--name", "A", "--email", "a@e.com")
		run("--dir", dirB, "init", "--name", "B", "--email", "b@e.com")
		run("--dir", dirA, "remote", "set", "shared", "peer", dirB)
		out := run("--dir", dirA, "new", "--tier", "shared", "--title", "Shared", "--body", "clean prose", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		run("--dir", dirA, "sync", "push", "--tier", "shared") // records pushed-state

		demote := run("--dir", dirA, "retier", added.ID, "private")
		Expect(demote).To(ContainSubstring("already pushed"))
		Expect(run("--dir", dirA, "show", added.ID, "--json")).To(ContainSubstring(`"tier": "private"`))
	})

	It("promote and private are gone (retier is the only movement verb)", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		for _, gone := range [][]string{{"promote", "x"}, {"private", "x"}, {"share", "x"}} {
			cmd := newRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(append([]string{"--dir", dir}, gone...))
			err := cmd.Execute()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown command"))
		}
	})
})

var _ = Describe("kref actionable arg errors", func() {
	guidedError := func(args ...string) string {
		var buf bytes.Buffer
		c := newRootCmd()
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs(args)
		err := c.Execute()
		Expect(err).To(HaveOccurred())
		return err.Error()
	}

	It("purge with no id explains how to find one and what happens", func() {
		msg := guidedError("--dir", gitRepo(), "purge")
		Expect(msg).To(ContainSubstring("kref purge needs an entry id."))
		Expect(msg).To(ContainSubstring("find one:  kref list"))
		Expect(msg).To(ContainSubstring("then:      kref purge <id>"))
		Expect(msg).To(ContainSubstring("# delete the entry's ref"))
		Expect(msg).To(ContainSubstring("details:   kref purge --help"))
	})

	It("status with no args names both required values", func() {
		msg := guidedError("--dir", gitRepo(), "status")
		Expect(msg).To(ContainSubstring("kref status needs an entry id and a status."))
		Expect(msg).To(ContainSubstring("then:      kref status <id> <status>"))
	})

	It("link add with no args coaches the source/target form", func() {
		msg := guidedError("--dir", gitRepo(), "link", "add")
		Expect(msg).To(ContainSubstring("kref link add needs a source and a target entry."))
		Expect(msg).To(ContainSubstring("--type blocks"))
	})

	It("label add with only an id asks for a label too", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "X", "--body", "x", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		msg := guidedError("--dir", dir, "label", "add", added.ID)
		Expect(msg).To(ContainSubstring("kref label add needs an entry id and at least one label."))
	})

	It("ingest with no path coaches the path form", func() {
		msg := guidedError("--dir", gitRepo(), "ingest")
		Expect(msg).To(ContainSubstring("kref ingest needs at least one path."))
		Expect(msg).To(ContainSubstring("kref ingest ."))
	})
})

var _ = Describe("kref inspection defaults", func() {
	It("show with no id resolves to the most-recent entry and notes it on stderr", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--title", "Older", "--body", "x")
		time.Sleep(time.Second) // UpdatedAt is Unix-second precision; force a distinct, later timestamp for "Newest"
		run("--dir", dir, "new", "--title", "Newest", "--body", "y")

		out := run("--dir", dir, "show")
		Expect(out).To(ContainSubstring("no id given — showing most recent"))
		Expect(out).To(ContainSubstring("Newest"))
	})

	It("suppresses the recency note under --json", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--title", "Only", "--body", "x")

		out := run("--dir", dir, "show", "--json")
		Expect(out).NotTo(ContainSubstring("no id given"))
		var snap map[string]any
		Expect(json.Unmarshal([]byte(out), &snap)).To(Succeed())
		Expect(snap["title"]).To(Equal("Only"))
	})

	It("errors helpfully when the store is empty", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		var buf bytes.Buffer
		c := newRootCmd()
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "show"})
		err := c.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no entries yet"))
		Expect(err.Error()).To(ContainSubstring("kref new"))
	})
})

var _ = Describe("kref help completeness", func() {
	It("every runnable leaf command carries an Example block", func() {
		var missing []string
		var walk func(c *cobra.Command)
		walk = func(c *cobra.Command) {
			isLeaf := true
			for _, ch := range c.Commands() {
				if ch.IsAvailableCommand() {
					isLeaf = false
				}
				walk(ch)
			}
			if c.Name() == "kref" || c.Name() == "help" || c.Name() == "completion" {
				return
			}
			if isLeaf && c.Example == "" {
				missing = append(missing, c.CommandPath())
			}
		}
		walk(newRootCmd())
		Expect(missing).To(BeEmpty(), "leaf commands without an Example: %v", missing)
	})

	It("renders the Example in --help", func() {
		out := run("purge", "--help")
		Expect(out).To(ContainSubstring("Examples:"))
		Expect(out).To(ContainSubstring("# delete the entry's ref"))
	})

	It("teaches the no-id most-recent default in show/log/diff/links/tree help", func() {
		for _, cmd := range []string{"show", "log", "diff", "links", "tree"} {
			out := run(cmd, "--help")
			Expect(out).To(ContainSubstring("most-recently-modified"),
				"%s --help should show the no-id (most-recent) example", cmd)
		}
	})

	It("points add at ingest for files and directories", func() {
		out := run("new", "--help")
		Expect(out).To(ContainSubstring("kref ingest"))
	})

	It("teaches that re-ingesting is idempotent", func() {
		out := run("ingest", "--help")
		Expect(out).To(ContainSubstring("idempotent"))
	})
})

var _ = Describe("kref track / untrack", func() {
	initRepo := func() string {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		return dir
	}

	It("tracks an in-repo file and records its repo-relative path", func() {
		dir := initRepo()
		p := filepath.Join(dir, "docs", "note.md")
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte("# Note\n\nbody\n"), 0o644)).To(Succeed())

		out := run("--dir", dir, "track", p, "--json")
		var r struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		Expect(json.Unmarshal([]byte(out), &r)).To(Succeed())
		Expect(r.Path).To(Equal("docs/note.md"))

		show := run("--dir", dir, "show", r.ID, "--json")
		Expect(show).To(ContainSubstring(`"tracked": true`))
		Expect(show).To(ContainSubstring(`"tracked_path": "docs/note.md"`))
	})

	It("copies a floater under .kref/ and tracks it there", func() {
		dir := initRepo()
		ext := filepath.Join(GinkgoT().TempDir(), "floater.md")
		Expect(os.WriteFile(ext, []byte("# Floater\n\nb\n"), 0o644)).To(Succeed())

		out := run("--dir", dir, "track", ext, "--json")
		var r struct {
			Path string `json:"path"`
		}
		Expect(json.Unmarshal([]byte(out), &r)).To(Succeed())
		Expect(r.Path).To(Equal(".kref/floater.md"))
		_, statErr := os.Stat(filepath.Join(dir, ".kref", "floater.md"))
		Expect(statErr).NotTo(HaveOccurred())
	})

	It("untracks an entry and leaves the file on disk", func() {
		dir := initRepo()
		p := filepath.Join(dir, "note.md")
		Expect(os.WriteFile(p, []byte("# N\n\nb\n"), 0o644)).To(Succeed())
		out := run("--dir", dir, "track", p, "--json")
		var r struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &r)).To(Succeed())

		run("--dir", dir, "untrack", r.ID)
		show := run("--dir", dir, "show", r.ID, "--json")
		Expect(show).To(ContainSubstring(`"tracked": false`))
		_, statErr := os.Stat(p)
		Expect(statErr).NotTo(HaveOccurred())
	})
})

var _ = Describe("kref reconcile", func() {
	initRepo := func() string {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		return dir
	}
	trackFile := func(dir, rel, content string) string {
		p := filepath.Join(dir, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
		out := run("--dir", dir, "track", p, "--json")
		var r struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &r)).To(Succeed())
		return r.ID
	}
	runWithIn := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		c := newRootCmd()
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetIn(strings.NewReader(stdin))
		c.SetArgs(args)
		err := c.Execute()
		return out.String(), err
	}

	It("reconciles a single tracked file by id without prompting", func() {
		dir := initRepo()
		id := trackFile(dir, "docs/n.md", "# N\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "docs/n.md"), []byte("# N\n\nnew\n"), 0o644)).To(Succeed())

		out := run("--dir", dir, "reconcile", id, "--json")
		Expect(out).To(ContainSubstring(`"action": "synced"`))
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring("new"))
	})

	It("reconciles a tracked file by path after its kref-id trailer was stripped", func() {
		dir := initRepo()
		p := filepath.Join(dir, "docs/track.md")
		id := trackFile(dir, "docs/track.md", "# T\n\noriginal\n")
		// A markdown formatter (prettier, a markdownlint rule) strips the trailing
		// HTML comment; rewrite the file edited and without the trailer.
		Expect(os.WriteFile(p, []byte("# T\n\nedited\n"), 0o644)).To(Succeed())

		// The path form must still resolve via the stored tracked-path mapping
		// even though the file no longer carries its trailer.
		out := run("--dir", dir, "reconcile", p, "--json")
		Expect(out).To(ContainSubstring(`"action": "synced"`))
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring("edited"))
	})

	It("sweeps all tracked files with -y", func() {
		dir := initRepo()
		id1 := trackFile(dir, "a.md", "# A\n\nold\n")
		_ = trackFile(dir, "b.md", "# B\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\nfresh\n"), 0o644)).To(Succeed())

		out := run("--dir", dir, "reconcile", "-y", "--json")
		Expect(out).To(ContainSubstring(`"action": "synced"`))
		Expect(run("--dir", dir, "show", id1, "--json")).To(ContainSubstring("fresh"))
	})

	It("aborts the bulk sweep when the prompt is declined", func() {
		dir := initRepo()
		id := trackFile(dir, "a.md", "# A\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\nnope\n"), 0o644)).To(Succeed())

		out, err := runWithIn("no\n", "--dir", dir, "reconcile")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("aborted"))
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring("old")) // not reconciled
	})

	It("proceeds through the prompt on yes", func() {
		dir := initRepo()
		id := trackFile(dir, "a.md", "# A\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\nyep\n"), 0o644)).To(Succeed())

		_, err := runWithIn("yes\n", "--dir", dir, "reconcile")
		Expect(err).NotTo(HaveOccurred())
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring("yep"))
	})
})

var _ = Describe("kref drift visibility", func() {
	initRepo := func() string {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		return dir
	}
	trackFile := func(dir, rel, content string) string {
		p := filepath.Join(dir, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
		out := run("--dir", dir, "track", p, "--json")
		var r struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &r)).To(Succeed())
		return r.ID
	}

	It("show reports a tracked entry's drift state", func() {
		dir := initRepo()
		id := trackFile(dir, "docs/note.md", "# Note\n\nold\n")
		Expect(run("--dir", dir, "show", id)).To(ContainSubstring("in-sync"))

		Expect(os.WriteFile(filepath.Join(dir, "docs/note.md"), []byte("# Note\n\nchanged\n"), 0o644)).To(Succeed())
		out := run("--dir", dir, "show", id)
		Expect(out).To(ContainSubstring("docs/note.md"))
		Expect(out).To(ContainSubstring("drifted"))
	})

	It("reconcile --dry-run reports would-sync without mutating", func() {
		dir := initRepo()
		id := trackFile(dir, "n.md", "# N\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "n.md"), []byte("# N\n\nnew\n"), 0o644)).To(Succeed())

		out := run("--dir", dir, "reconcile", id, "--dry-run", "--json")
		Expect(out).To(ContainSubstring(`"action": "synced"`))
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring("old")) // unmutated
		Expect(run("--dir", dir, "show", id, "--json")).NotTo(ContainSubstring("new"))
	})

	It("list --check shows drift, but bare list does not", func() {
		dir := initRepo()
		_ = trackFile(dir, "a.md", "# A\n\nbody\n")
		_ = trackFile(dir, "b.md", "# B\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "b.md"), []byte("# B\n\nchanged\n"), 0o644)).To(Succeed())

		checked := run("--dir", dir, "list", "--check")
		Expect(checked).To(ContainSubstring("drifted"))
		Expect(checked).To(ContainSubstring("b.md"))

		Expect(run("--dir", dir, "list")).NotTo(ContainSubstring("drifted"))
	})
})

var _ = Describe("kref reconcile --write", func() {
	initRepo := func() string {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		return dir
	}
	trackFile := func(dir, rel, content string) string {
		p := filepath.Join(dir, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
		out := run("--dir", dir, "track", p, "--json")
		var r struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &r)).To(Succeed())
		return r.ID
	}
	runWithIn := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		c := newRootCmd()
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetIn(strings.NewReader(stdin))
		c.SetArgs(args)
		err := c.Execute()
		return out.String(), err
	}

	It("pushes an entry edit out to its file (fast-forward)", func() {
		dir := initRepo()
		id := trackFile(dir, "docs/n.md", "# N\n\nold\n")
		run("--dir", dir, "update", id, "--body", "# N\n\nnew")

		out := run("--dir", dir, "reconcile", id, "--write", "--json")
		Expect(out).To(ContainSubstring(`"action": "written"`))
		after, err := os.ReadFile(filepath.Join(dir, "docs/n.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("new"))
		Expect(string(after)).NotTo(ContainSubstring("old"))
	})

	It("refuses a diverged file, shows a diff, and exits nonzero", func() {
		dir := initRepo()
		id := trackFile(dir, "docs/n.md", "# N\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "docs/n.md"), []byte("# N\n\nlocal\n"), 0o644)).To(Succeed())

		out, err := runWithIn("", "--dir", dir, "reconcile", id, "--write")
		Expect(err).To(HaveOccurred()) // nonzero exit on unresolved divergence
		Expect(out).To(ContainSubstring("diverged"))
		Expect(out).To(ContainSubstring("--- entry"))
		Expect(out).To(ContainSubstring("--write --force")) // resolution hint (push)
		after, err2 := os.ReadFile(filepath.Join(dir, "docs/n.md"))
		Expect(err2).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("local")) // not overwritten
	})

	It("overwrites a diverged file with --force", func() {
		dir := initRepo()
		id := trackFile(dir, "docs/n.md", "# N\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "docs/n.md"), []byte("# N\n\nlocal\n"), 0o644)).To(Succeed())

		out := run("--dir", dir, "reconcile", id, "--write", "--force", "--json")
		Expect(out).To(ContainSubstring(`"action": "forced"`))
		after, err := os.ReadFile(filepath.Join(dir, "docs/n.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("old"))
		Expect(string(after)).NotTo(ContainSubstring("local"))
	})

	It("aborts the bulk write sweep when the prompt is declined", func() {
		dir := initRepo()
		id := trackFile(dir, "a.md", "# A\n\nold\n")
		run("--dir", dir, "update", id, "--body", "# A\n\nnew")

		out, err := runWithIn("no\n", "--dir", dir, "reconcile", "--write")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("aborted"))
		after, err2 := os.ReadFile(filepath.Join(dir, "a.md"))
		Expect(err2).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("old")) // not written
	})

	It("sweeps with -y, writing fast-forwards", func() {
		dir := initRepo()
		id := trackFile(dir, "a.md", "# A\n\nold\n")
		run("--dir", dir, "update", id, "--body", "# A\n\nfresh")

		out := run("--dir", dir, "reconcile", "--write", "-y", "--json")
		Expect(out).To(ContainSubstring(`"action": "written"`))
		after, err := os.ReadFile(filepath.Join(dir, "a.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("fresh"))
	})

	It("dry-run shows the diff for a diverged file without writing", func() {
		dir := initRepo()
		id := trackFile(dir, "a.md", "# A\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\nlocal\n"), 0o644)).To(Succeed())

		out, err := runWithIn("", "--dir", dir, "reconcile", id, "--write", "--dry-run")
		Expect(err).To(HaveOccurred()) // still nonzero: divergence unresolved
		Expect(out).To(ContainSubstring("--- entry"))
		after, err2 := os.ReadFile(filepath.Join(dir, "a.md"))
		Expect(err2).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("local"))
	})

	It("leaves the default reconcile as pull-only (no --write)", func() {
		dir := initRepo()
		id := trackFile(dir, "a.md", "# A\n\nold\n")
		run("--dir", dir, "update", id, "--body", "# A\n\nentry-only")
		// Plain reconcile must NOT write the file (pull-only); the file is a past
		// version, so it is simply unchanged on disk.
		run("--dir", dir, "reconcile", id, "--json")
		after, err := os.ReadFile(filepath.Join(dir, "a.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("old"))
		Expect(string(after)).NotTo(ContainSubstring("entry-only"))
	})
})

var _ = Describe("list output flags", func() {
	seed := func(dir string) {
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--kind", "spec", "--title", "Alpha", "--body", "a", "--tier", "shared")
		run("--dir", dir, "new", "--kind", "plan", "--title", "Beta", "--body", "b", "--tier", "shared")
	}

	It("--plain --columns=id emits bare ids honoring filters", func() {
		dir := gitRepo()
		seed(dir)
		out := run("--dir", dir, "list", "--tier", "shared", "--plain", "--columns=id")
		lines := strings.Split(strings.TrimSpace(out), "\n")
		Expect(lines).To(HaveLen(2))
		for _, ln := range lines {
			Expect(ln).To(MatchRegexp(`^[0-9a-f]{12}$`))
		}
	})

	It("rejects --plain combined with --json anywhere in the tree", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")
		for _, args := range [][]string{
			{"--dir", dir, "list", "--plain", "--json"},
			{"--dir", dir, "show", "--plain", "--json"},
		} {
			cmd := newRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(args)
			err := cmd.Execute()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mutually exclusive"))
		}
	})

	It("--wide shows an AUTHOR column", func() {
		dir := gitRepo()
		seed(dir)
		Expect(run("--dir", dir, "list", "--wide")).To(ContainSubstring("AUTHOR"))
	})

	It("bare --columns lists the available columns", func() {
		dir := gitRepo()
		seed(dir)
		out := run("--dir", dir, "list", "--columns")
		Expect(out).To(ContainSubstring("Available columns"))
		Expect(out).To(ContainSubstring("author"))
		Expect(out).To(ContainSubstring("updated"))
		// it must NOT have listed entries
		Expect(out).NotTo(ContainSubstring("Alpha"))
	})

	It("the space form --columns id errors with a hint to use '='", func() {
		dir := gitRepo()
		seed(dir)
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "list", "--columns", "id,author"})
		err := c.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("--columns=id,author"))
	})

	It("rejects --columns with --json", func() {
		dir := gitRepo()
		seed(dir)
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "list", "--columns=id", "--json"})
		Expect(c.Execute()).To(HaveOccurred())
	})

	It("rejects --columns with --wide", func() {
		dir := gitRepo()
		seed(dir)
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "list", "--columns=id", "--wide"})
		Expect(c.Execute()).To(HaveOccurred())
	})

	It("rejects an unknown column", func() {
		dir := gitRepo()
		seed(dir)
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "list", "--columns=bogus"})
		Expect(c.Execute()).To(HaveOccurred())
	})
})

var _ = Describe("update reattribution", func() {
	addEntry := func(dir string) string {
		out := run("--dir", dir, "new", "--kind", "spec", "--title", "T", "--body", "b", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		return added.ID
	}

	It("--reset-author sets the entry author to the current identity", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Owner Self", "--email", "self@example.com")
		id := addEntry(dir)
		run("--dir", dir, "update", id, "--author", "Old One <old@example.com>")
		Expect(run("--dir", dir, "show", id)).To(ContainSubstring("old@example.com"))
		run("--dir", dir, "update", id, "--reset-author")
		show := run("--dir", dir, "show", id)
		Expect(show).To(ContainSubstring("Owner Self <self@example.com>"))
		Expect(show).NotTo(ContainSubstring("old@example.com"))
	})

	It("--author sets an explicit author", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := addEntry(dir)
		run("--dir", dir, "update", id, "--author", "Jane Roe <jane@example.com>")
		Expect(run("--dir", dir, "show", id)).To(ContainSubstring("Jane Roe <jane@example.com>"))
	})

	It("rejects --reset-author with --author together", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := addEntry(dir)
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "update", id, "--reset-author", "--author", "A <a@x.com>"})
		Expect(c.Execute()).To(HaveOccurred())
	})

	It("rejects a malformed --author", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := addEntry(dir)
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "update", id, "--author", "no-email-here"})
		Expect(c.Execute()).To(HaveOccurred())
	})
})

var _ = Describe("archive lifecycle", func() {
	addID := func(dir, title string) string {
		GinkgoHelper()
		out := run("--dir", dir, "new", "--title", title, "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}
	seed := func() string {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		return dir
	}

	It("archive hides from the default list; --archived shows it; unarchive restores", func() {
		dir := seed()
		_ = addID(dir, "Keep")
		gone := addID(dir, "Gone")
		run("--dir", dir, "archive", gone)

		def := run("--dir", dir, "list")
		Expect(def).To(ContainSubstring("Keep"))
		Expect(def).NotTo(ContainSubstring("Gone"))

		arch := run("--dir", dir, "list", "--archived")
		Expect(arch).To(ContainSubstring("Gone"))
		Expect(arch).To(ContainSubstring("(archived)"))
		Expect(arch).NotTo(ContainSubstring("Keep"))

		run("--dir", dir, "unarchive", gone)
		Expect(run("--dir", dir, "list")).To(ContainSubstring("Gone"))
	})

	It("archive --obsolete -y archives every obsolete entry", func() {
		dir := seed()
		o1 := addID(dir, "Old1")
		o2 := addID(dir, "Old2")
		_ = addID(dir, "Live")
		run("--dir", dir, "status", o1, "obsolete")
		run("--dir", dir, "status", o2, "obsolete")
		run("--dir", dir, "archive", "--obsolete", "-y")

		def := run("--dir", dir, "list")
		Expect(def).To(ContainSubstring("Live"))
		Expect(def).NotTo(ContainSubstring("Old1"))
		Expect(def).NotTo(ContainSubstring("Old2"))
		arch := run("--dir", dir, "list", "--archived")
		Expect(arch).To(ContainSubstring("Old1"))
		Expect(arch).To(ContainSubstring("Old2"))
	})

	It("archive --obsolete proceeds on 'y' and aborts otherwise", func() {
		dir := seed()
		o := addID(dir, "Ob")
		run("--dir", dir, "status", o, "obsolete")

		runIn("n\n", "--dir", dir, "archive", "--obsolete")
		Expect(run("--dir", dir, "list")).To(ContainSubstring("Ob")) // aborted: not archived

		runIn("y\n", "--dir", dir, "archive", "--obsolete")
		Expect(run("--dir", dir, "list")).NotTo(ContainSubstring("Ob"))
		Expect(run("--dir", dir, "list", "--archived")).To(ContainSubstring("Ob"))
	})

	It("rejects archive with neither an id nor --obsolete, and with both", func() {
		dir := seed()
		id := addID(dir, "X")
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "archive"})
		Expect(c.Execute()).To(HaveOccurred())
		c2 := newRootCmd()
		c2.SetArgs([]string{"--dir", dir, "archive", id, "--obsolete"})
		Expect(c2.Execute()).To(HaveOccurred())
	})
})

var _ = Describe("author override workflow", func() {
	setEnv := func(k, v string) {
		old, had := os.LookupEnv(k)
		Expect(os.Setenv(k, v)).To(Succeed())
		DeferCleanup(func() {
			if had {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}
	// initRepo makes a git repo + kref store whose init identity is "Init User".
	initRepo := func() string {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Init User", "--email", "init@example.com")
		return dir
	}
	// globalGitconfig points HOME (and XDG) at a temp home holding a .gitconfig
	// with the given [kref "author"] section, the way a user mounts ~/.gitconfig
	// into a container.
	globalGitconfig := func(name, email string) {
		home := GinkgoT().TempDir()
		body := "[kref \"author\"]\n\tname = " + name + "\n\temail = " + email + "\n"
		Expect(os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(body), 0o600)).To(Succeed())
		setEnv("HOME", home)
		setEnv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	}
	addID := func(dir, title string) string {
		out := run("--dir", dir, "new", "--title", title, "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}
	authorOf := func(dir, id string) string {
		out := run("--dir", dir, "show", id, "--json")
		var v struct {
			CreatedBy      string `json:"created_by"`
			CreatedByEmail string `json:"created_by_email"`
		}
		Expect(json.Unmarshal([]byte(out), &v)).To(Succeed())
		return v.CreatedBy + " <" + v.CreatedByEmail + ">"
	}

	It("attributes new entries via a global git config [kref \"author\"] file", func() {
		globalGitconfig("Config Human", "cfg@example.com")
		dir := initRepo()
		Expect(authorOf(dir, addID(dir, "X"))).To(Equal("Config Human <cfg@example.com>"))
	})

	It("attributes new entries via KREF_AUTHOR_* env vars", func() {
		dir := initRepo()
		setEnv("KREF_AUTHOR_NAME", "Env Human")
		setEnv("KREF_AUTHOR_EMAIL", "env@example.com")
		Expect(authorOf(dir, addID(dir, "X"))).To(Equal("Env Human <env@example.com>"))
	})

	It("lets env override the global git config file", func() {
		globalGitconfig("Config Human", "cfg@example.com")
		setEnv("KREF_AUTHOR_NAME", "Env Human")
		setEnv("KREF_AUTHOR_EMAIL", "env@example.com")
		dir := initRepo()
		Expect(authorOf(dir, addID(dir, "X"))).To(Equal("Env Human <env@example.com>"))
	})

	It("falls back to the init identity when no override is configured", func() {
		dir := initRepo()
		Expect(authorOf(dir, addID(dir, "X"))).To(Equal("Init User <init@example.com>"))
	})

	It("--reset-author resets to the configured override, not the init identity", func() {
		dir := initRepo()
		id := addID(dir, "X")
		run("--dir", dir, "update", id, "--author", "Someone Else <else@example.com>")
		Expect(authorOf(dir, id)).To(Equal("Someone Else <else@example.com>"))
		setEnv("KREF_AUTHOR_NAME", "Env Human")
		setEnv("KREF_AUTHOR_EMAIL", "env@example.com")
		run("--dir", dir, "update", id, "--reset-author")
		Expect(authorOf(dir, id)).To(Equal("Env Human <env@example.com>"))
	})

	It("--reset-author with no override resets to the init identity", func() {
		dir := initRepo()
		id := addID(dir, "X")
		run("--dir", dir, "update", id, "--author", "Someone Else <else@example.com>")
		run("--dir", dir, "update", id, "--reset-author")
		Expect(authorOf(dir, id)).To(Equal("Init User <init@example.com>"))
	})

	It("errors when only one of the KREF_AUTHOR_* pair is set", func() {
		dir := initRepo()
		setEnv("KREF_AUTHOR_NAME", "Only Name")
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "new", "--title", "X", "--body", "b"})
		Expect(c.Execute()).To(HaveOccurred())
	})
})

var _ = Describe("init on an already-initialized store", func() {
	It("prints the existing identity instead of re-initializing or duplicating it", func() {
		dir := gitRepo()
		Expect(run("--dir", dir, "init", "--name", "First", "--email", "first@x")).To(ContainSubstring("initialized"))

		out := run("--dir", dir, "init")
		Expect(out).To(ContainSubstring("already initialized"))
		Expect(out).To(ContainSubstring("First <first@x>"))

		// re-init with different flags must NOT change the primary author
		out2 := run("--dir", dir, "init", "--name", "Second", "--email", "second@x")
		Expect(out2).To(ContainSubstring("already initialized"))
		Expect(out2).To(ContainSubstring("First <first@x>"))

		add := run("--dir", dir, "new", "--title", "X", "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(add), &a)).To(Succeed())
		show := run("--dir", dir, "show", a.ID, "--json")
		Expect(show).To(ContainSubstring(`"created_by": "First"`))
		Expect(show).NotTo(ContainSubstring("Second"))
	})
})

var _ = Describe("export/import/vault commands", func() {
	addID := func(dir, tier, title string) string {
		GinkgoHelper()
		out := run("--dir", dir, "new", "--tier", tier, "--title", title, "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}

	It("exports private to a file and imports it into a fresh clone, preserving author", func() {
		dirA := gitRepo()
		run("--dir", dirA, "init", "--name", "Ada", "--email", "ada@x")
		pid := addID(dirA, "private", "Secret")
		bundle := filepath.Join(GinkgoT().TempDir(), "p.bundle")
		run("--dir", dirA, "bundle", "export", "--tier", "private", bundle)

		dirB := gitRepo()
		run("--dir", dirB, "init", "--name", "Bob", "--email", "bob@x")
		run("--dir", dirB, "bundle", "import", "--tier", "private", bundle)
		show := run("--dir", dirB, "show", pid, "--json")
		Expect(show).To(ContainSubstring("Secret"))
		Expect(show).To(ContainSubstring(`"created_by": "Ada"`))
	})

	It("vault backup then restore recovers a purged private entry", func() {
		data := GinkgoT().TempDir()
		old, had := os.LookupEnv("XDG_DATA_HOME")
		Expect(os.Setenv("XDG_DATA_HOME", data)).To(Succeed())
		DeferCleanup(func() {
			if had {
				_ = os.Setenv("XDG_DATA_HOME", old)
			} else {
				_ = os.Unsetenv("XDG_DATA_HOME")
			}
		})

		dir := gitRepo()
		run("--dir", dir, "init", "--name", "A", "--email", "a@x")
		pid := addID(dir, "private", "Keep")
		run("--dir", dir, "vault", "backup")
		run("--dir", dir, "purge", pid, "--force")

		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "show", pid})
		Expect(c.Execute()).To(HaveOccurred())

		run("--dir", dir, "vault", "restore")
		Expect(run("--dir", dir, "show", pid, "--json")).To(ContainSubstring("Keep"))
	})
})

var _ = Describe("list -w alias and bulk update", func() {
	mkID := func(dir, title string) string {
		GinkgoHelper()
		out := run("--dir", dir, "new", "--kind", "spec", "--title", title, "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}
	kindOf := func(dir, id string) string {
		GinkgoHelper()
		out := run("--dir", dir, "show", id, "--json")
		var v struct {
			Kind string `json:"kind"`
		}
		Expect(json.Unmarshal([]byte(out), &v)).To(Succeed())
		return v.Kind
	}
	seed := func() string {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		return dir
	}

	It("-w is shorthand for --wide", func() {
		dir := seed()
		mkID(dir, "A")
		Expect(run("--dir", dir, "list", "-w")).To(ContainSubstring("AUTHOR"))
	})

	It("updates multiple ids at once", func() {
		dir := seed()
		a, b := mkID(dir, "A"), mkID(dir, "B")
		run("--dir", dir, "update", a, b, "--kind", "plan")
		Expect(kindOf(dir, a)).To(Equal("plan"))
		Expect(kindOf(dir, b)).To(Equal("plan"))
	})

	It("updates every entry with --all -y", func() {
		dir := seed()
		a, b := mkID(dir, "A"), mkID(dir, "B")
		run("--dir", dir, "update", "--all", "--kind", "memory", "-y")
		Expect(kindOf(dir, a)).To(Equal("memory"))
		Expect(kindOf(dir, b)).To(Equal("memory"))
	})

	It("--all aborts on a non-y answer and changes nothing", func() {
		dir := seed()
		a := mkID(dir, "A")
		runIn("n\n", "--dir", dir, "update", "--all", "--kind", "plan")
		Expect(kindOf(dir, a)).To(Equal("spec"))
	})

	It("refuses per-entry content flags in bulk", func() {
		dir := seed()
		a, b := mkID(dir, "A"), mkID(dir, "B")
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "update", a, b, "--title", "X"})
		Expect(c.Execute()).To(HaveOccurred())
	})

	It("rejects --all combined with ids, and a no-target update", func() {
		dir := seed()
		a := mkID(dir, "A")
		c := newRootCmd()
		c.SetArgs([]string{"--dir", dir, "update", a, "--all", "--kind", "plan"})
		Expect(c.Execute()).To(HaveOccurred())
		c2 := newRootCmd()
		c2.SetArgs([]string{"--dir", dir, "update", "--kind", "plan"})
		Expect(c2.Execute()).To(HaveOccurred())
	})

	It("sets content type on add and changes it with update", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")

		out := run("--dir", dir, "new", "--title", "Cfg", "--kind", "document",
			"--content-type", "application/json", "--body", `{"a":1}`, "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		show := run("--dir", dir, "show", added.ID, "--json")
		Expect(show).To(ContainSubstring(`"content_type": "application/json"`))

		run("--dir", dir, "update", added.ID, "--content-type", "text/x-go")
		show = run("--dir", dir, "show", added.ID, "--json")
		Expect(show).To(ContainSubstring(`"content_type": "text/x-go"`))
	})

	It("rejects an unsupported content type on add", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")
		var out bytes.Buffer
		cmd := newRootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"--dir", dir, "new", "--title", "X", "--content-type", "image/png"})
		Expect(cmd.Execute()).To(HaveOccurred())
	})

	It("show --plain emits the stored body verbatim with no header; --raw is gone", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")
		out := run("--dir", dir, "new", "--title", "Doc", "--kind", "spec",
			"--body", "# Heading\n\nwrapped line one\nwrapped line two", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		plain := run("--dir", dir, "show", added.ID, "--plain")
		Expect(plain).To(ContainSubstring("# Heading\n\nwrapped line one\nwrapped line two")) // verbatim, unreflowed
		Expect(plain).NotTo(ContainSubstring("Tester <tester@example.com>"))                  // no header block

		rendered := run("--dir", dir, "show", added.ID, "--no-header")
		Expect(rendered).To(ContainSubstring("wrapped line one wrapped line two")) // reflowed
		Expect(rendered).NotTo(ContainSubstring("Tester <tester@example.com>"))

		cmd := newRootCmd()
		cmd.SetArgs([]string{"--dir", dir, "show", added.ID, "--raw"})
		Expect(cmd.Execute()).To(HaveOccurred()) // --raw removed
	})

	It("show (default, piped) renders markdown rather than printing raw source", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")
		out := run("--dir", dir, "new", "--title", "Doc", "--kind", "spec",
			"--body", "# Heading\n\nprose", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())

		shown := run("--dir", dir, "show", added.ID)
		Expect(shown).To(ContainSubstring("Heading"))
		Expect(shown).NotTo(ContainSubstring("# Heading")) // glamour-rendered, not raw
	})
})

var _ = Describe("fullByDefault", func() {
	It("is false for a non-file writer such as a test buffer", func() {
		Expect(fullByDefault(&bytes.Buffer{})).To(BeFalse())
	})
	It("is true for a non-terminal file (a pipe)", func() {
		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = r.Close() }()
		defer func() { _ = w.Close() }()
		Expect(fullByDefault(w)).To(BeTrue())
	})
})

var _ = Describe("remote list and get", func() {
	// setup returns a repo with a shared-tier remote configured and one
	// remote-less tier (personal), so list/get cover both states.
	setup := func() (string, string) {
		dir := gitRepo()
		peer := gitRepo()
		run("--dir", dir, "init", "--name", "A", "--email", "a@e.com")
		run("--dir", dir, "remote", "set", "shared", "peer", peer)
		return dir, peer
	}

	It("lists every tier with its remote, URL, and syncability", func() {
		dir, peer := setup()
		out := run("--dir", dir, "remote", "list")
		Expect(out).To(ContainSubstring("private"))
		Expect(out).To(ContainSubstring("never syncs"))
		Expect(out).To(ContainSubstring("personal"))
		Expect(out).To(ContainSubstring("not configured"))
		Expect(out).To(ContainSubstring("shared"))
		Expect(out).To(ContainSubstring("peer"))
		Expect(out).To(ContainSubstring(peer)) // the URL
	})

	It("runs list when invoked bare", func() {
		dir, _ := setup()
		Expect(run("--dir", dir, "remote")).To(Equal(run("--dir", dir, "remote", "list")))
	})

	It("emits the full tier map under --json", func() {
		dir, peer := setup()
		out := run("--dir", dir, "remote", "list", "--json")
		var v struct {
			Remotes []struct {
				Tier     string `json:"tier"`
				Remote   string `json:"remote"`
				URL      string `json:"url"`
				Syncable bool   `json:"syncable"`
			} `json:"remotes"`
		}
		Expect(json.Unmarshal([]byte(out), &v)).To(Succeed())
		Expect(v.Remotes).To(HaveLen(3))
		byTier := map[string]int{}
		for i, r := range v.Remotes {
			byTier[r.Tier] = i
		}
		Expect(v.Remotes[byTier["private"]].Syncable).To(BeFalse())
		Expect(v.Remotes[byTier["private"]].Remote).To(BeEmpty())
		Expect(v.Remotes[byTier["personal"]].Syncable).To(BeTrue())
		Expect(v.Remotes[byTier["personal"]].Remote).To(BeEmpty())
		Expect(v.Remotes[byTier["shared"]].Remote).To(Equal("peer"))
		Expect(v.Remotes[byTier["shared"]].URL).To(Equal(peer))
	})

	It("gets a configured tier's remote", func() {
		dir, peer := setup()
		out := run("--dir", dir, "remote", "get", "shared")
		Expect(out).To(ContainSubstring("peer"))
		Expect(out).To(ContainSubstring(peer))
		jout := run("--dir", dir, "remote", "get", "shared", "--json")
		Expect(jout).To(ContainSubstring(`"remote": "peer"`))
	})

	It("errors on get for an unconfigured tier", func() {
		dir, _ := setup()
		c := newRootCmd()
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "remote", "get", "personal"})
		err := c.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no remote configured"))
	})

	It("errors on get for the private tier", func() {
		dir, _ := setup()
		c := newRootCmd()
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dir, "remote", "get", "private"})
		Expect(c.Execute()).To(HaveOccurred())
	})
})

var _ = Describe("renderFull", func() {
	It("emits the global preamble and recurses into second-level commands", func() {
		var buf bytes.Buffer
		root := newRootCmd()
		renderFull(&buf, root)
		out := buf.String()
		Expect(out).To(ContainSubstring("GLOBAL FLAGS"))
		Expect(out).To(ContainSubstring("OUTPUT CONTRACT"))
		Expect(out).To(ContainSubstring("ENVIRONMENT"))
		Expect(out).To(ContainSubstring("KREF_COLOR"))
		Expect(out).To(ContainSubstring("NO_COLOR"))
		Expect(out).To(ContainSubstring("--json"))
		Expect(out).To(ContainSubstring("--dir"))
		Expect(out).To(ContainSubstring("--actor"))
		Expect(out).To(ContainSubstring("Core Commands:"))
		// "backup" is a child of "vault" — only the recursive walk reaches it.
		Expect(out).To(ContainSubstring("backup"))
	})
	It("scopes to the named command's subtree, omitting group headers", func() {
		var buf bytes.Buffer
		root := newRootCmd()
		sync, _, err := root.Find([]string{"sync"})
		Expect(err).NotTo(HaveOccurred())
		renderFull(&buf, sync)
		out := buf.String()
		Expect(out).To(ContainSubstring("push"))
		Expect(out).To(ContainSubstring("pull"))
		// group titles are emitted only for the root walk:
		Expect(out).NotTo(ContainSubstring("Sync Commands:"))
	})
})

var _ = Describe("helpTargetCompletions", func() {
	It("completes top-level command names with no args", func() {
		names, _ := helpTargetCompletions(newRootCmd(), nil, "")
		Expect(names).To(ContainElement("new"))
		Expect(names).To(ContainElement("sync"))
	})
	It("completes a parent's subcommands", func() {
		names, _ := helpTargetCompletions(newRootCmd(), []string{"sync"}, "")
		Expect(names).To(ContainElement("push"))
		Expect(names).To(ContainElement("pull"))
	})
})

var _ = Describe("kref help --long / --short", func() {
	It("expands the whole tree with the output contract under --long", func() {
		out := run("help", "--long")
		Expect(out).To(ContainSubstring("OUTPUT CONTRACT"))
		Expect(out).To(ContainSubstring("--json"))
		Expect(out).To(ContainSubstring("backup")) // second-level command
	})
	It("scopes --long to the named command's subtree", func() {
		out := run("help", "sync", "--long")
		Expect(out).To(ContainSubstring("push"))
		Expect(out).To(ContainSubstring("pull"))
		Expect(out).NotTo(ContainSubstring("Sync Commands:")) // no group headers off-root
	})
	It("forces the concise grouped list under --short", func() {
		out := run("help", "--short")
		Expect(out).To(ContainSubstring("Core Commands:"))
		Expect(out).NotTo(ContainSubstring("OUTPUT CONTRACT"))
		Expect(out).NotTo(ContainSubstring("backup"))
	})
	It("accepts the -l and -s shorthands", func() {
		Expect(run("help", "-l")).To(ContainSubstring("OUTPUT CONTRACT"))
		short := run("help", "-s")
		Expect(short).To(ContainSubstring("Core Commands:"))
		Expect(short).NotTo(ContainSubstring("OUTPUT CONTRACT"))
	})
	It("defaults to concise for a buffer writer (deterministic in tests)", func() {
		out := run("--help")
		Expect(out).To(ContainSubstring("Core Commands:"))
		Expect(out).NotTo(ContainSubstring("OUTPUT CONTRACT"))
	})
	It("rejects --long and --short together", func() {
		var buf bytes.Buffer
		c := newRootCmd()
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"help", "--long", "--short"})
		err := c.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cannot be combined"))
	})
})

var _ = Describe("help preamble drift guard", func() {
	It("names only global flags that actually exist on the root", func() {
		root := newRootCmd()
		for _, name := range []string{"json", "dir", "actor"} {
			Expect(root.PersistentFlags().Lookup(name)).NotTo(BeNil(),
				"global flag --%s referenced by the help preamble must exist", name)
		}
	})
	It("renders every global flag in the full preamble", func() {
		out := run("help", "--long")
		Expect(out).To(ContainSubstring("--json"))
		Expect(out).To(ContainSubstring("--dir"))
		Expect(out).To(ContainSubstring("--actor"))
	})
})

var _ = Describe("missing betterleaks policy", func() {
	// Point resolution at a nonexistent binary; PATH fallback is irrelevant
	// because KREF_BETTERLEAKS has top precedence.
	brokenScanner := func() { GinkgoT().Setenv("KREF_BETTERLEAKS", "/nonexistent/betterleaks") }

	It("ingest proceeds unscanned with a loud warning instead of failing", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		f := filepath.Join(dir, "note.md")
		Expect(os.WriteFile(f, []byte("# Note\n\nbody\n"), 0o644)).To(Succeed())

		brokenScanner()
		out := run("--dir", dir, "ingest", f)
		Expect(out).To(ContainSubstring("created"))
		Expect(out).To(ContainSubstring("UNSCANNED"))
		Expect(out).To(ContainSubstring("go install github.com/betterleaks/betterleaks@latest"))
	})

	It("update --file proceeds with a warning", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--title", "N", "--body", "x", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		f := filepath.Join(dir, "body.md")
		Expect(os.WriteFile(f, []byte("fresh\n"), 0o644)).To(Succeed())

		brokenScanner()
		upd := run("--dir", dir, "update", a.ID, "--file", f)
		Expect(upd).To(ContainSubstring("UNSCANNED"))
		Expect(run("--dir", dir, "show", a.ID, "--json")).To(ContainSubstring("fresh"))
	})

	It("sync push stays fail-closed — the secret boundary never silently opens", func() {
		dirA := gitRepo()
		dirB := gitRepo()
		run("--dir", dirA, "init", "--name", "A", "--email", "a@e.com")
		run("--dir", dirA, "remote", "set", "shared", "peer", dirB)
		run("--dir", dirA, "new", "--tier", "shared", "--title", "S", "--body", "x")

		brokenScanner()
		c := newRootCmd()
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dirA, "sync", "push", "--tier", "shared"})
		err := c.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("betterleaks not found"))
		Expect(err.Error()).To(ContainSubstring("--force"))
	})

	It("sync push --force pushes UNSCANNED with a loud warning", func() {
		hub := GinkgoT().TempDir()
		Expect(exec.Command("git", "init", "--bare", hub).Run()).To(Succeed())
		dirA := gitRepo()
		dirB := gitRepo()
		run("--dir", dirA, "init", "--name", "A", "--email", "a@e.com")
		run("--dir", dirB, "init", "--name", "B", "--email", "b@e.com")
		run("--dir", dirA, "remote", "set", "shared", "hub", hub)
		run("--dir", dirB, "remote", "set", "shared", "hub", hub)
		run("--dir", dirA, "new", "--tier", "shared", "--title", "Forced", "--body", "x")

		brokenScanner()
		out := run("--dir", dirA, "sync", "push", "--tier", "shared", "--force")
		Expect(out).To(ContainSubstring("UNSCANNED"))
		Expect(out).To(ContainSubstring("pushed: shared"))

		run("--dir", dirB, "sync", "pull", "--tier", "shared") // pull never scans
		Expect(run("--dir", dirB, "list", "--json")).To(ContainSubstring("Forced"))
	})

	It("sync push --force does not override a positive secret finding", func() {
		dirA := gitRepo()
		dirB := gitRepo()
		run("--dir", dirA, "init", "--name", "A", "--email", "a@e.com")
		run("--dir", dirA, "remote", "set", "shared", "peer", dirB)
		run("--dir", dirA, "new", "--tier", "shared", "--title", "Leaky", "--body", "ghp_012345678901234567890123456789abcdef")

		c := newRootCmd()
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.SetErr(&buf)
		c.SetArgs([]string{"--dir", dirA, "sync", "push", "--tier", "shared", "--force"})
		err := c.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("push blocked"))
	})
})

var _ = Describe("no-remote data-loss warning", func() {
	It("init notes that no sync remote is configured yet", func() {
		dir := gitRepo()
		out := run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		Expect(out).To(ContainSubstring("no sync remote"))
		Expect(out).To(ContainSubstring("kref remote set"))
	})

	It("warns once per interval after a mutation leaves syncable entries remote-less", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		first := run("--dir", dir, "new", "--title", "A", "--body", "x")
		Expect(first).To(ContainSubstring("no sync remote"))
		second := run("--dir", dir, "new", "--title", "B", "--body", "y")
		Expect(second).NotTo(ContainSubstring("no sync remote"), "marked as warned for the interval")
	})

	It("stays quiet under --json and once a remote is configured", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		Expect(run("--dir", dir, "new", "--title", "A", "--body", "x", "--json")).
			NotTo(ContainSubstring("no sync remote"))

		peer := gitRepo()
		run("--dir", dir, "remote", "set", "personal", "peer", peer)
		Expect(run("--dir", dir, "new", "--title", "B", "--body", "y")).
			NotTo(ContainSubstring("no sync remote"))
	})

	It("does not nag on read-only commands", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "new", "--title", "A", "--body", "x", "--json") // json: no warn, no mark
		Expect(run("--dir", dir, "list")).NotTo(ContainSubstring("no sync remote"))
	})
})

var _ = Describe("kref tier", func() {
	It("declares, lists, and removes a custom tier", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")

		Expect(run("--dir", dir, "tier", "add", "research", "--type", "personal")).
			To(ContainSubstring("research"))

		list := run("--dir", dir, "tier", "list")
		Expect(list).To(ContainSubstring("◐ research"))
		Expect(list).To(ContainSubstring("personal"))

		var parsed struct {
			Tiers []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Declared bool   `json:"declared"`
				Remote   string `json:"remote"`
			} `json:"tiers"`
		}
		Expect(json.Unmarshal([]byte(run("--dir", dir, "tier", "list", "--json")), &parsed)).To(Succeed())
		names := []string{}
		for _, t := range parsed.Tiers {
			names = append(names, t.Name)
		}
		Expect(names).To(Equal([]string{"private", "personal", "research", "shared"}))

		Expect(run("--dir", dir, "tier", "rm", "research")).To(ContainSubstring("removed"))
		Expect(run("--dir", dir, "tier", "list")).NotTo(ContainSubstring("research"))
	})
})

var _ = Describe("--tier validation against the store", func() {
	It("rejects writes to undeclared tiers with guidance", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		var out bytes.Buffer
		cmd := newRootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"--dir", dir, "new", "--tier", "ghost", "--title", "X", "--body", "b"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown tier"))
	})

	It("filters list and search by a custom tier", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "tier", "add", "research", "--type", "personal")
		run("--dir", dir, "new", "--tier", "research", "--title", "Rsrch note", "--body", "quantum")
		run("--dir", dir, "new", "--title", "Other", "--body", "plain")
		Expect(run("--dir", dir, "list", "--tier", "research")).To(ContainSubstring("Rsrch note"))
		Expect(run("--dir", dir, "list", "--tier", "research")).NotTo(ContainSubstring("Other"))
		Expect(run("--dir", dir, "search", "quantum", "--tier", "research")).To(ContainSubstring("Rsrch note"))
	})

	It("retiers into a custom tier via the CLI", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		run("--dir", dir, "tier", "add", "research", "--type", "personal")
		out := run("--dir", dir, "new", "--title", "Doc", "--body", "b", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		Expect(run("--dir", dir, "retier", added.ID, "research")).To(ContainSubstring("personal → research"))
	})
})

var _ = Describe("favorites workflow", func() {
	// Favorites live in the user config, so isolate XDG_CONFIG_HOME/HOME to a
	// throwaway home per spec (matching the author-override suite's pattern).
	isolateHome := func() {
		home := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)
		GinkgoT().Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	}
	newID := func(dir, title string) string {
		out := run("--dir", dir, "new", "--title", title, "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}

	It("adds a favorite as `fav add <id> <name>` (entry first, name second)", func() {
		isolateHome()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")
		id := newID(dir, "Deploy runbook")

		run("--dir", dir, "fav", "add", id, "todo")

		out := run("--dir", dir, "fav", "ls", "--json")
		var favs []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &favs)).To(Succeed())
		Expect(favs).To(HaveLen(1))
		Expect(favs[0].Name).To(Equal("todo"))
		Expect(favs[0].ID).To(Equal(id))
		// The name resolves as an id alias.
		Expect(run("--dir", dir, "show", "todo", "--json")).To(ContainSubstring("Deploy runbook"))
	})

	It("defaults `kref fav` with no arguments to `kref fav ls`", func() {
		isolateHome()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")
		id := newID(dir, "Runbook")
		run("--dir", dir, "fav", "add", id, "todo")

		bare := run("--dir", dir, "fav")
		Expect(bare).To(ContainSubstring("todo"))
		Expect(bare).To(ContainSubstring("(user)"))
		Expect(bare).To(Equal(run("--dir", dir, "fav", "ls")), "bare `fav` matches `fav ls`")
	})

	It("shows favorited entries at the top of `kref list`, even against an explicit sort", func() {
		isolateHome()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "tester@example.com")
		zebraID := newID(dir, "Zzz entry")
		_ = newID(dir, "Aaa entry")
		run("--dir", dir, "fav", "add", zebraID, "pin")

		out := run("--dir", dir, "list", "--sort", "title")
		Expect(strings.Index(out, "Zzz entry")).To(BeNumerically("<", strings.Index(out, "Aaa entry")),
			"favorite Zzz pins above Aaa despite the ascending title sort")
	})
})
