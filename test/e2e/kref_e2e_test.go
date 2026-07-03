//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("kref end-to-end", func() {
	Describe("version", func() {
		It("reports a version", func() {
			e := newKrefEnv("E2E Tester", "e2e@example.com")
			// Default output is the plain `kref <version>` line (matching
			// `kref --version`); --json switches it to a {"version": …} object.
			Expect(e.mustRun("version")).To(MatchRegexp(`kref \S+`))
			Expect(e.mustRun("version", "--json")).To(ContainSubstring(`"version"`))
		})
	})

	Describe("hermetic identity", func() {
		It("adopts the isolated git identity, never the developer's ~/.gitconfig", func() {
			e := newKrefEnv("Isolated Person", "isolated@example.com")
			out := e.mustRun("init", "--json")
			var r struct {
				Author string `json:"author"`
				Email  string `json:"email"`
			}
			Expect(json.Unmarshal([]byte(out), &r)).To(Succeed())
			Expect(r.Author).To(Equal("Isolated Person"))
			Expect(r.Email).To(Equal("isolated@example.com"))
		})
	})

	Describe("entry lifecycle + attribution", func() {
		It("init -> new -> show -> list with author attribution and kind filtering", func() {
			e := newKrefEnv("Ada Lovelace", "ada@example.com")
			e.mustRun("init")
			id := idOf(e.mustRun("new", "--kind", "spec", "--title", "Auth design", "--body", "the body", "--json"))

			show := e.mustRun("show", id, "--json")
			Expect(show).To(ContainSubstring("Auth design"))
			Expect(show).To(ContainSubstring("Ada Lovelace"))

			Expect(e.mustRun("list", "--json")).To(ContainSubstring("Auth design"))
			Expect(e.mustRun("list", "--kind", "spec", "--json")).To(ContainSubstring("Auth design"))
			Expect(e.mustRun("list", "--kind", "adr", "--json")).NotTo(ContainSubstring("Auth design"))

			// Human-readable (non-JSON) output must go to stdout, not stderr.
			Expect(e.mustRun("list")).To(ContainSubstring("Auth design"))
			Expect(e.mustRun("show", id)).To(ContainSubstring("Ada Lovelace"))
		})
	})

	Describe("edited vs updated sort", func() {
		It("metadata churn reorders --sort updated but leaves the default (edited) order", func() {
			e := newKrefEnv("Grace Hopper", "grace@example.com")
			e.mustRun("init")
			idAlpha := idOf(e.mustRun("new", "--kind", "document", "--title", "Alpha", "--body", "a", "--json"))
			e.mustRun("new", "--kind", "document", "--title", "Bravo", "--body", "b", "--json")

			// list defaults to edited:desc. Capture the order before any metadata op.
			before := e.mustRun("list", "--plain")

			// A metadata-only op (label) after a >1s gap bumps `updated` but not `edited`.
			// Op timestamps are second-resolution, so the sleep guarantees updated(Alpha)
			// lands in a strictly later second than either entry's edited/created time.
			time.Sleep(1100 * time.Millisecond)
			e.mustRun("label", "add", idAlpha, "area:x")

			// edited is unchanged by the label, so the default order is invariant.
			Expect(e.mustRun("list", "--plain")).To(Equal(before))

			// updated moved: Alpha (just relabeled) now sorts first under updated:desc.
			upd := e.mustRun("list", "--sort", "updated:desc", "--plain")
			Expect(strings.Index(upd, "Alpha")).To(BeNumerically("<", strings.Index(upd, "Bravo")))

			// The edited column renders a YYYY-MM-DD date.
			Expect(e.mustRun("list", "--columns=edited", "--plain")).To(MatchRegexp(`\d{4}-\d{2}-\d{2}`))
		})
	})

	Describe("tiers", func() {
		It("stores into private, personal, and shared", func() {
			e := newKrefEnv("T", "t@example.com")
			e.mustRun("init")
			for _, tier := range []string{"private", "personal", "shared"} {
				id := idOf(e.mustRun("new", "--tier", tier, "--title", "x-"+tier, "--body", "b", "--json"))
				Expect(e.mustRun("show", id, "--json")).To(ContainSubstring(`"tier": "` + tier + `"`))
			}
			Expect(e.mustRun("list", "--json")).To(SatisfyAll(
				ContainSubstring("x-private"),
				ContainSubstring("x-personal"),
				ContainSubstring("x-shared"),
			))
		})
		It("rejects an unknown tier", func() {
			e := newKrefEnv("T", "t@example.com")
			e.mustRun("init")
			_, _, err := e.run("", "new", "--tier", "bogus", "--title", "x")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("custom tiers", func() {
		It("declares a tier, writes into it, lists it, retiers out, and removes it", func() {
			e := newKrefEnv("Tier Tester", "tiers@example.com")
			e.mustRun("init")
			e.mustRun("tier", "add", "research", "--type", "personal")
			Expect(e.mustRun("tier", "list")).To(ContainSubstring("research"))

			id := idOf(e.mustRun("new", "--tier", "research", "--title", "Notes", "--body", "b", "--json"))
			Expect(e.mustRun("list", "--tier", "research")).To(ContainSubstring("Notes"))

			e.mustRun("retier", id, "personal")
			e.mustRun("tier", "rm", "research")
			Expect(e.mustRun("tier", "list")).NotTo(ContainSubstring("research"))
		})
	})

	Describe("ingest + secret quarantine", func() {
		It("ingests clean markdown into the chosen tier", func() {
			e := newKrefEnv("T", "t@example.com")
			e.mustRun("init")
			f := filepath.Join(e.dir, "clean.md")
			Expect(os.WriteFile(f, []byte("# Clean Note\nplain prose\n"), 0o644)).To(Succeed())
			out := e.mustRun("ingest", f, "--json")
			Expect(out).To(ContainSubstring(`"tier": "personal"`))
			Expect(out).To(ContainSubstring(`"quarantined": false`))
			Expect(out).To(ContainSubstring("Clean Note"))
		})
		It("quarantines secret content to the private tier", func() {
			e := newKrefEnv("T", "t@example.com")
			e.mustRun("init")
			f := filepath.Join(e.dir, "leak.md")
			Expect(os.WriteFile(f, []byte("# Notes\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"), 0o644)).To(Succeed())
			out := e.mustRun("ingest", f, "--json")
			Expect(out).To(ContainSubstring(`"tier": "private"`))
			Expect(out).To(ContainSubstring(`"quarantined": true`))
		})
	})

	Describe("delete", func() {
		It("rm tombstones an entry (hidden from list)", func() {
			e := newKrefEnv("T", "t@example.com")
			e.mustRun("init")
			id := idOf(e.mustRun("new", "--title", "Doomed", "--body", "x", "--json"))
			e.mustRun("rm", id)
			Expect(e.mustRun("list", "--json")).NotTo(ContainSubstring(id))
		})

		It("restore brings a tombstoned entry back", func() {
			e := newKrefEnv("T", "t@example.com")
			e.mustRun("init")
			id := idOf(e.mustRun("new", "--title", "Revivable", "--body", "x", "--json"))
			e.mustRun("rm", id)
			Expect(e.mustRun("list")).NotTo(ContainSubstring("Revivable"))
			Expect(e.mustRun("list", "--include-deleted")).To(ContainSubstring("Revivable"))
			Expect(e.mustRun("list", "--include-deleted")).To(ContainSubstring("(deleted)"))
			Expect(e.mustRun("restore", id)).To(ContainSubstring("restored"))
			Expect(e.mustRun("list")).To(ContainSubstring("Revivable"))
		})
		It("purge --force hard-deletes an entry", func() {
			e := newKrefEnv("T", "t@example.com")
			e.mustRun("init")
			id := idOf(e.mustRun("new", "--title", "Gone", "--body", "x", "--json"))
			e.mustRun("purge", "--force", id)
			Expect(e.mustRun("list", "--json")).NotTo(ContainSubstring(id))
		})
		It("purge prompts and aborts when the confirmation is declined", func() {
			e := newKrefEnv("T", "t@example.com")
			e.mustRun("init")
			id := idOf(e.mustRun("new", "--title", "Keep", "--body", "x", "--json"))
			out, _, err := e.run("no\n", "purge", id)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("aborted"))
			Expect(e.mustRun("list", "--json")).To(ContainSubstring("Keep"))
		})
	})

	Describe("sync over a bare remote", func() {
		It("pushes from A and pulls into B with attribution; private cannot leave", func() {
			origin := bareRepo()
			a := newKrefEnv("Ada", "ada@example.com")
			b := newKrefEnv("Bob", "bob@example.com")
			a.mustRun("init")
			b.mustRun("init")
			a.mustRun("remote", "set", "shared", "origin", origin)
			b.mustRun("remote", "set", "shared", "origin", origin)

			id := idOf(a.mustRun("new", "--tier", "shared", "--title", "Hello", "--body", "x", "--json"))
			a.mustRun("sync", "push", "--tier", "shared")
			b.mustRun("sync", "pull", "--tier", "shared")

			got := b.mustRun("show", id, "--json")
			Expect(got).To(ContainSubstring("Hello"))
			Expect(got).To(ContainSubstring("Ada")) // author propagated through the remote

			By("refusing to push the private tier")
			_, _, err := a.run("", "sync", "push", "--tier", "private")
			Expect(err).To(HaveOccurred())

			By("refusing to configure a remote for the private tier")
			_, _, err = a.run("", "remote", "set", "private", "origin", origin)
			Expect(err).To(HaveOccurred())
		})

		It("purge --push deletes the entry on the remote so a fresh clone loses it", func() {
			origin := bareRepo()
			a := newKrefEnv("Ada", "ada@example.com")
			a.mustRun("init")
			a.mustRun("remote", "set", "shared", "origin", origin)
			id := idOf(a.mustRun("new", "--tier", "shared", "--title", "Doomed", "--body", "x", "--json"))
			a.mustRun("sync", "push", "--tier", "shared")
			a.mustRun("purge", "--push", "--force", id)

			c := newKrefEnv("Cara", "cara@example.com")
			c.mustRun("init")
			c.mustRun("remote", "set", "shared", "origin", origin)
			c.mustRun("sync", "pull", "--tier", "shared")
			_, _, err := c.run("", "show", id, "--json")
			Expect(err).To(HaveOccurred()) // gone from the remote
		})
	})

	Describe("hooks", func() {
		It("installs and prints the lefthook config", func() {
			e := newKrefEnv("T", "t@example.com")
			e.mustRun("init")
			Expect(e.mustRun("hooks", "print")).To(ContainSubstring("pre-push:"))
			e.mustRun("hooks", "install")
			data, err := os.ReadFile(filepath.Join(e.dir, ".lefthook.yml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("kref sync push"))
		})
	})
})

var _ = Describe("purge prompt stream", func() {
	It("writes the confirmation to stderr, leaving stdout pure JSON", func() {
		e := newKrefEnv("Purger", "p@example.com")
		e.mustRun("init")
		id := idOf(e.mustRun("new", "--title", "doomed", "--json"))
		stdout, stderr, _ := e.run("n\n", "purge", id)
		Expect(stderr).To(ContainSubstring("About to PURGE"))
		Expect(stdout).NotTo(ContainSubstring("About to PURGE"))
		Expect(stdout).To(ContainSubstring("aborted"))
	})
})

var _ = Describe("init in a git repo", func() {
	It("adopts an existing repo and refuses a non-git dir", func() {
		e := newKrefEnv("Repo Adopter", "adopt@example.com")
		// newKrefEnv already `git init`-ed e.dir; init must now succeed.
		out := e.mustRun("init")
		Expect(out).To(ContainSubstring("initialized"))

		// A sibling dir that is NOT a git repo must fail with guidance.
		bare := GinkgoT().TempDir()
		full := []string{"--dir", bare, "init"}
		cmd := exec.Command(krefBin, full...)
		cmd.Env = e.osEnv()
		var sout, serr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &sout, &serr
		err := cmd.Run()
		Expect(err).To(HaveOccurred())
		Expect(serr.String()).To(ContainSubstring("git init"))
	})
})

var _ = Describe("kref slice-5 reconciled verbs", func() {
	It("update sets title-only and kind-only without rewriting the body", func() {
		e := newKrefEnv("T", "t@example.com")
		e.mustRun("init")
		id := idOf(e.mustRun("new", "--title", "Orig", "--body", "# Orig\n\nbody", "--json"))
		e.mustRun("update", id, "--title", "Renamed")
		e.mustRun("update", id, "--kind", "spec")
		show := e.mustRun("show", id, "--json")
		Expect(show).To(SatisfyAll(
			ContainSubstring(`"title": "Renamed"`),
			ContainSubstring(`"kind": "spec"`),
			ContainSubstring(`# Orig`),
		))
	})

	It("creates a generic typed link that the links viewer shows, and removes it", func() {
		e := newKrefEnv("T", "t@example.com")
		e.mustRun("init")
		a := idOf(e.mustRun("new", "--tier", "shared", "--title", "A", "--body", "a", "--json"))
		b := idOf(e.mustRun("new", "--tier", "shared", "--title", "B", "--body", "b", "--json"))
		e.mustRun("link", "add", a, b, "--type", "depends-on")
		Expect(e.mustRun("links", a)).To(ContainSubstring("depends-on"))
		e.mustRun("link", "rm", a, b)
		Expect(e.mustRun("links", a)).NotTo(ContainSubstring("depends-on"))
	})

	It("warns but proceeds (exit 0) on a cross-tier link to a more-private entry", func() {
		e := newKrefEnv("T", "t@example.com")
		e.mustRun("init")
		shared := idOf(e.mustRun("new", "--tier", "shared", "--title", "S", "--body", "s", "--json"))
		priv := idOf(e.mustRun("new", "--tier", "private", "--title", "P", "--body", "p", "--json"))
		out, errOut, err := e.run("", "link", "add", shared, priv)
		Expect(err).NotTo(HaveOccurred(), "cross-tier link must succeed (warn, not refuse)")
		Expect(out + errOut).To(ContainSubstring("private id rides along"))
	})

	It("resolve is a friendly no-op on a non-merged entry", func() {
		e := newKrefEnv("T", "t@example.com")
		e.mustRun("init")
		id := idOf(e.mustRun("new", "--title", "X", "--body", "x", "--json"))
		Expect(e.mustRun("resolve", id)).To(ContainSubstring("nothing to resolve"))
	})

	It("ingest --kind sets the kind on the new entry", func() {
		e := newKrefEnv("T", "t@example.com")
		e.mustRun("init")
		f := filepath.Join(e.dir, "spec.md")
		Expect(os.WriteFile(f, []byte("# Spec\nbody\n"), 0o644)).To(Succeed())
		e.mustRun("ingest", f, "--kind", "spec")
		Expect(e.mustRun("list", "--kind", "spec", "--json")).To(ContainSubstring("Spec"))
	})
})

var _ = Describe("bulk update via list --plain | xargs (the common workflow)", func() {
	It("re-kinds every selected entry through a real shell pipe", func() {
		e := newKrefEnv("Ada", "ada@example.com")
		e.mustRun("init")
		for _, t := range []string{"One", "Two", "Three"} {
			e.mustRun("new", "--kind", "spec", "--title", t, "--body", "b")
		}

		// the documented pattern: select ids with `list --plain`, pipe to xargs,
		// hit `kref update` once with all of them.
		pipe := fmt.Sprintf("%q --dir %q list --plain --columns=id | xargs %q --dir %q update --kind plan",
			krefBin, e.dir, krefBin, e.dir)
		cmd := exec.Command("sh", "-c", pipe)
		cmd.Env = e.osEnv()
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "pipe failed: %s", out)

		listed := e.mustRun("list", "--plain", "--columns=id,kind")
		lines := strings.Split(strings.TrimSpace(listed), "\n")
		Expect(lines).To(HaveLen(3))
		for _, line := range lines {
			Expect(line).To(HaveSuffix("\tplan"), "every entry should be re-kinded to plan")
		}
	})

	It("bulk-reattributes selected entries through the pipe", func() {
		e := newKrefEnv("Ada", "ada@example.com")
		e.mustRun("init")
		first := idOf(e.mustRun("new", "--title", "X", "--body", "b", "--json"))
		_ = idOf(e.mustRun("new", "--title", "Y", "--body", "b", "--json"))

		pipe := fmt.Sprintf("%q --dir %q list --plain --columns=id | xargs %q --dir %q update --author %q",
			krefBin, e.dir, krefBin, e.dir, "Bob Boss <bob@example.com>")
		cmd := exec.Command("sh", "-c", pipe)
		cmd.Env = e.osEnv()
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "pipe failed: %s", out)

		Expect(e.mustRun("show", first, "--json")).To(ContainSubstring(`"created_by": "Bob Boss"`))
	})
})

var _ = Describe("global --plain and markdown reflow", func() {
	It("show --plain round-trips the stored body; rendered show reflows it", func() {
		e := newKrefEnv("Ada", "ada@example.com")
		e.mustRun("init")
		body := "# Title\n\nwrapped line one\nwrapped line two\n\n- bullet a\n  continued here\n"
		id := idOf(e.mustRun("new", "--title", "Doc", "--body", body, "--json"))

		plain := e.mustRun("show", id, "--plain")
		Expect(plain).To(ContainSubstring("wrapped line one\nwrapped line two")) // verbatim
		Expect(plain).NotTo(ContainSubstring("Ada"))                             // no header

		rendered := e.mustRun("show", id)
		Expect(rendered).To(ContainSubstring("wrapped line one wrapped line two")) // reflowed
		Expect(rendered).To(ContainSubstring("bullet a continued here"))
	})

	It("search --plain emits TSV; --plain --json errors", func() {
		e := newKrefEnv("Ada", "ada@example.com")
		e.mustRun("init")
		e.mustRun("new", "--title", "Auth flow", "--body", "auth auth")
		out := e.mustRun("search", "auth", "--plain")
		Expect(strings.Split(strings.TrimSpace(out), "\n")[0]).To(MatchRegexp(`^\d+\t\S+\t[0-9a-f]{12}\t\S+\tAuth flow$`))

		// mutual exclusion — list exercises the same flag-check path as show/search
		_, errOut, err := e.run("", "list", "--plain", "--json")
		Expect(err).To(HaveOccurred())
		Expect(errOut).To(ContainSubstring("mutually exclusive"))
	})
})

var _ = Describe("repo discovery without --dir", func() {
	It("resolves the enclosing repo from a subdirectory, git-style", func() {
		e := newKrefEnv("Dora", "dora@example.com")
		e.mustRun("init")
		e.mustRun("new", "--title", "Discoverable", "--body", "x")

		sub := filepath.Join(e.dir, "internal", "deep")
		Expect(os.MkdirAll(sub, 0o755)).To(Succeed())
		out, errOut, err := e.runAt(sub, "", "list", "--plain", "--columns=title")
		Expect(err).NotTo(HaveOccurred(), "stderr: %s", errOut)
		Expect(out).To(ContainSubstring("Discoverable"))
	})

	It("errors cleanly when run outside any git repo", func() {
		e := newKrefEnv("Dora", "dora@example.com")
		outside := GinkgoT().TempDir()
		_, errOut, err := e.runAt(outside, "", "list")
		Expect(err).To(HaveOccurred())
		Expect(errOut).To(ContainSubstring(".git not found"))
	})
})
