package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newTodo creates a kind:todo entry with the given body and returns its id.
func newTodo(dir, body string) string {
	GinkgoHelper()
	out := run("--dir", dir, "new", "--kind", "todo", "--title", "T", "--body", body, "--json")
	var added struct {
		ID string `json:"id"`
	}
	Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
	Expect(added.ID).NotTo(BeEmpty())
	return added.ID
}

// runErr invokes the CLI in-process like run() but returns the captured
// stdout/stderr and the execution error, so tests can assert non-zero exits.
func runErr(args ...string) (string, error) {
	GinkgoHelper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

var _ = Describe("kref todo fmt/lint", func() {
	It("moves done items under Done and lints clean", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n- [x] done\n- [ ] open\n\n## Done (compact)\n")

		run("--dir", dir, "todo", "fmt", id)

		body := run("--dir", dir, "show", id, "--plain")
		Expect(strings.Index(body, "- [x] done")).
			To(BeNumerically(">", strings.Index(body, "## Done (compact)")))

		out, err := runErr("--dir", dir, "todo", "lint", id)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("todo: ok"))
	})

	It("reports an unknown section heading and exits non-zero", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Opne\n\n## Open\n\n## Done (compact)\n")

		out, err := runErr("--dir", dir, "todo", "lint", id)
		Expect(err).To(HaveOccurred())
		Expect(out).To(ContainSubstring("unknown-heading"))
	})
})

var _ = Describe("kref todo cockpit", func() {
	It("shows the awaiting-you signal and collapses Done", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n- [ ] one\n\n## Done (compact)\n- [x] a\n- [x] b\n")
		// awaiting-you is sourced from open question-comments, not a [?] body marker.
		run("--dir", dir, "comment", id, "-q", "-m", "ship X?")

		out := run("--dir", dir, "todo", id)
		Expect(out).To(ContainSubstring("1 awaiting you"))
		Expect(out).To(ContainSubstring("ship X?"))
		Expect(out).To(ContainSubstring("2 done"))
		Expect(out).NotTo(ContainSubstring("- [x] a"))
	})

	It("renders the same cockpit via `todo show`, by id and for the default", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n- [ ] one\n\n## Done (compact)\n")

		byID := run("--dir", dir, "todo", "show", id)
		Expect(byID).To(ContainSubstring("open 1"))

		// Bare `todo show` resolves the sole todo, matching bare `todo`.
		Expect(run("--dir", dir, "todo", "show")).To(Equal(run("--dir", dir, "todo")))
	})

	It("annotates section headings with open counts and numbers questions", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n### Priority\n- [ ] a\n- [ ] b\n\n## Done (compact)\n")
		// The numbered awaiting-you list comes from open question-comments.
		run("--dir", dir, "comment", id, "-q", "-m", "pick one?")

		out := run("--dir", dir, "todo")
		Expect(out).To(ContainSubstring("Priority (2)"))
		Expect(out).To(ContainSubstring("1. pick one?"))
	})

	It("--full expands the Done section instead of collapsing it", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n- [ ] one\n\n## Done (compact)\n- [x] shipped it\n")

		collapsed := run("--dir", dir, "todo", id)
		Expect(collapsed).NotTo(ContainSubstring("shipped it"))
		Expect(collapsed).To(ContainSubstring("1 done"))

		full := run("--dir", dir, "todo", id, "--full")
		Expect(full).To(ContainSubstring("shipped it"))
	})
})

var _ = Describe("kref todo watermark deltas", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
	})

	const seen = "# T\n\n## Open\n- [ ] a\n- [ ] b\n\n## Done (compact)\n"
	const head = "# T\n\n## Open\n- [ ] a\n- [ ] c new\n\n## Done (compact)\n- [x] b\n"

	It("shows to-review and changed after a human baseline then an edit", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, seen)

		first := run("--dir", dir, "todo", id)
		Expect(first).NotTo(ContainSubstring("to review"))

		run("--dir", dir, "update", id, "--body", head)
		second := run("--dir", dir, "todo", id)
		Expect(second).To(ContainSubstring("1 to review"))
		Expect(second).To(ContainSubstring("1 changed"))
	})

	It("does not advance the watermark on an agent view", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, seen)
		run("--dir", dir, "todo", id)
		run("--dir", dir, "update", id, "--body", head)

		agent := run("--dir", dir, "--actor", "bot", "todo", id)
		Expect(agent).To(ContainSubstring("1 to review"))
		human := run("--dir", dir, "todo", id)
		Expect(human).To(ContainSubstring("1 to review"))
	})
})

var _ = Describe("kref update todo guard", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
	})

	It("auto-formats a todo body on update (moves done under Done)", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n")

		run("--dir", dir, "update", id, "--body",
			"# T\n\n## Open\n- [x] done it\n- [ ] a\n\n## Done (compact)\n")

		body := run("--dir", dir, "show", id, "--plain")
		Expect(strings.Index(body, "- [x] done it")).
			To(BeNumerically(">", strings.Index(body, "## Done (compact)")))
	})

	It("refuses a malformed todo body and preserves it to a recovery file", func() {
		state := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_STATE_HOME", state)
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n\n## Done (compact)\n")

		// The root sets SilenceErrors, so a returned RunE error is NOT written to
		// the command buffer; its text is on the returned error (real main() prints
		// it to os.Stderr). Assert on err, not on out.
		_, err := runErr("--dir", dir, "update", id, "--body",
			"# T\n\n## Opne\n\n## Open\n\n## Done (compact)\n")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown-heading"))
		Expect(err.Error()).To(ContainSubstring("rejected body was saved to"))

		Expect(run("--dir", dir, "show", id, "--plain")).NotTo(ContainSubstring("Opne"))
		matches, _ := filepath.Glob(filepath.Join(state, "kref", "rejected", id+"-*.md"))
		Expect(matches).NotTo(BeEmpty())
	})

	It("--no-lint writes a malformed todo with a loud warning", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n\n## Done (compact)\n")

		out := run("--dir", dir, "update", id, "--no-lint", "--body",
			"# T\n\n## Opne\n\n## Open\n\n## Done (compact)\n")
		Expect(out).To(ContainSubstring("--no-lint"))
		Expect(run("--dir", dir, "show", id, "--plain")).To(ContainSubstring("Opne"))
	})
})

var _ = Describe("kref update --if-version CAS", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
	})

	const v1 = "# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n"
	const v2 = "# T\n\n## Open\n- [ ] a\n- [ ] b\n\n## Done (compact)\n"
	const v3 = "# T\n\n## Open\n- [ ] a\n- [ ] b\n- [ ] c\n\n## Done (compact)\n"

	It("writes when --if-version matches the current head", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, v1) // version 1

		run("--dir", dir, "update", id, "--if-version", "1", "--body", v2)
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring(`"version": 2`))
	})

	It("refuses a stale write and preserves the body to a recovery file", func() {
		state := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_STATE_HOME", state)
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, v1) // version 1

		run("--dir", dir, "update", id, "--body", v2) // now version 2

		// A writer that still thinks it holds v1 must be refused.
		_, err := runErr("--dir", dir, "update", id, "--if-version", "1", "--body", v3)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("stale todo write"))
		Expect(err.Error()).To(ContainSubstring("rejected body was saved to"))

		// The stale content was NOT written; head is still v2's body.
		Expect(run("--dir", dir, "show", id, "--plain")).NotTo(ContainSubstring("- [ ] c"))
		matches, _ := filepath.Glob(filepath.Join(state, "kref", "rejected", id+"-*.md"))
		Expect(matches).NotTo(BeEmpty())
	})

	It("warns but writes when --if-version is omitted on a todo", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, v1)

		out := run("--dir", dir, "update", id, "--body", v2)
		Expect(out).To(ContainSubstring("unguarded todo write"))
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring(`"version": 2`))
	})
})

var _ = Describe("kref edit todo guard", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
	})

	It("reopens the editor until the todo lints clean, then saves", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n\n## Done (compact)\n")

		tmpd := GinkgoT().TempDir()
		ed := filepath.Join(tmpd, "ed.sh")
		script := "#!/bin/sh\n" +
			"n=\"" + tmpd + "/n\"\n" +
			"c=$(cat \"$n\" 2>/dev/null || echo 0)\n" +
			"if [ \"$c\" = 0 ]; then\n" +
			"  printf '# T\\n\\n## Opne\\n\\n## Open\\n\\n## Done (compact)\\n' > \"$1\"\n" +
			"  echo 1 > \"$n\"\n" +
			"else\n" +
			"  printf '# T\\n\\n## Open\\n- [ ] fixed\\n\\n## Done (compact)\\n' > \"$1\"\n" +
			"fi\n"
		Expect(os.WriteFile(ed, []byte(script), 0o755)).To(Succeed())
		GinkgoT().Setenv("KREF_EDITOR", ed)

		out := run("--dir", dir, "edit", id)
		Expect(out).To(ContainSubstring("updated"))
		Expect(run("--dir", dir, "show", id, "--plain")).To(ContainSubstring("- [ ] fixed"))
	})

	It("refuses a stale save when a concurrent write bumped the version, preserving the edit", func() {
		state := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_STATE_HOME", state)
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n") // version 1

		tmpd := GinkgoT().TempDir()
		ready := filepath.Join(tmpd, "ready")
		done := filepath.Join(tmpd, "done")
		ed := filepath.Join(tmpd, "ed.sh")
		// The editor announces it is open (ready), blocks until the concurrent
		// writer has bumped the entry (done), then writes the author's edit.
		script := "#!/bin/sh\n" +
			"touch \"" + ready + "\"\n" +
			"while [ ! -f \"" + done + "\" ]; do sleep 0.02; done\n" +
			"printf '# T\\n\\n## Open\\n- [ ] a\\n- [ ] mine\\n\\n## Done (compact)\\n' > \"$1\"\n"
		Expect(os.WriteFile(ed, []byte(script), 0o755)).To(Succeed())
		GinkgoT().Setenv("KREF_EDITOR", ed)

		// Once the editor is open (edit read version 1), a concurrent writer moves
		// the entry to version 2, then releases the editor. The save must refuse.
		go func() {
			defer GinkgoRecover()
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			run("--dir", dir, "update", id, "--if-version", "1", "--body",
				"# T\n\n## Open\n- [ ] a\n- [ ] concurrent\n\n## Done (compact)\n")
			Expect(os.WriteFile(done, []byte("x"), 0o600)).To(Succeed())
		}()

		_, err := runErr("--dir", dir, "edit", id)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("stale todo write"))

		// The concurrent version stands; the author's edit was preserved, not lost.
		body := run("--dir", dir, "show", id, "--plain")
		Expect(body).To(ContainSubstring("concurrent"))
		Expect(body).NotTo(ContainSubstring("mine"))
		matches, _ := filepath.Glob(filepath.Join(state, "kref", "rejected", id+"-*.md"))
		Expect(matches).NotTo(BeEmpty())
	})

	It("aborts when the reopened editor leaves the todo unchanged and broken", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n\n## Done (compact)\n")

		tmpd := GinkgoT().TempDir()
		ed := filepath.Join(tmpd, "ed.sh")
		Expect(os.WriteFile(ed, []byte(
			"#!/bin/sh\nprintf '# T\\n\\n## Opne\\n\\n## Open\\n\\n## Done (compact)\\n' > \"$1\"\n"),
			0o755)).To(Succeed())
		GinkgoT().Setenv("KREF_EDITOR", ed)

		// "edit discarded" is a returned RunE error; with SilenceErrors it is on
		// err, not in the command buffer. Assert on err.
		_, err := runErr("--dir", dir, "edit", id)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("edit discarded"))
		Expect(run("--dir", dir, "show", id, "--plain")).NotTo(ContainSubstring("Opne"))
	})
})

var _ = Describe("kref todo --no-pager", func() {
	// Isolate watermark state; use --actor bot so no run advances the watermark,
	// keeping both calls in an identical pre-baseline state (ToReview suppressed).
	BeforeEach(func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
	})

	It("prints the static cockpit on --no-pager (same as piped output)", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n- [ ] one\n\n## Done (compact)\n- [x] two\n")

		// --actor bot suppresses watermark advancement so both invocations see
		// identical state; piped (non-TTY) and --no-pager take the same static path.
		piped := run("--dir", dir, "--actor", "bot", "todo", id)
		noPager := run("--dir", dir, "--actor", "bot", "todo", id, "--no-pager")
		Expect(noPager).To(Equal(piped))
	})

	It("--no-pager on todo show matches piped output", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n- [ ] one\n\n## Done (compact)\n")

		piped := run("--dir", dir, "--actor", "bot", "todo", "show", id)
		noPager := run("--dir", dir, "--actor", "bot", "todo", "show", id, "--no-pager")
		Expect(noPager).To(Equal(piped))
	})

	It("shows the quarantine review badge when a write is awaiting review", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := newTodo(dir, "# T\n\n## Open\n- [ ] one\n\n## Done (compact)\n")

		// No pending review yet: no badge.
		Expect(run("--dir", dir, "--actor", "bot", "todo", id, "--no-pager")).
			NotTo(ContainSubstring("awaiting review"))

		// Park an unrelated flagged write so the repo-wide queue is non-empty.
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "S", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		Expect(run("--dir", dir, "update", added.ID, "--body", qSecret)).To(ContainSubstring("quarantined as"))

		Expect(run("--dir", dir, "--actor", "bot", "todo", id, "--no-pager")).
			To(ContainSubstring("1 awaiting review"))
	})
})

var _ = Describe("kref todo lint (no argument)", func() {
	It("passes cleanly when there are no todo entries", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out, err := runErr("--dir", dir, "todo", "lint")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("ok"))
	})

	It("lints every todo and fails when any is malformed", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		newTodo(dir, "# A\n\n## Open\n\n## Done (compact)\n")
		bad := newTodo(dir, "# B\n\n## Opne\n\n## Open\n\n## Done (compact)\n")

		out, err := runErr("--dir", dir, "todo", "lint")
		Expect(err).To(HaveOccurred())
		Expect(out).To(ContainSubstring("unknown-heading"))
		Expect(out).To(ContainSubstring(bad[:8]))
	})
})

var _ = Describe("kref update --kind todo guard", func() {
	It("refuses to change an entry to kind:todo when its body does not lint", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := addEntry(dir, "--title", "Notes", "--kind", "document",
			"--body", "# Notes\n\nfree prose, no todo sections\n")
		_, err := runErr("--dir", dir, "update", id, "--kind", "todo")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kind:todo"))
		Expect(err.Error()).To(ContainSubstring("missing-section"))
		// the kind was NOT changed
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring(`"kind": "document"`))
	})

	It("allows the change when the body satisfies the todo grammar", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		id := addEntry(dir, "--title", "T", "--kind", "document",
			"--body", "# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n")
		run("--dir", dir, "update", id, "--kind", "todo")
		out, err := runErr("--dir", dir, "todo", "lint", id)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("ok"))
	})

	It("refuses a bulk conversion when any body does not lint (no partial change)", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		good := addEntry(dir, "--title", "G", "--kind", "document",
			"--body", "# G\n\n## Open\n\n## Done (compact)\n")
		bad := addEntry(dir, "--title", "B", "--kind", "document",
			"--body", "# B\n\nprose only\n")
		_, err := runErr("--dir", dir, "update", good, bad, "--kind", "todo")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(bad[:12]))
		// neither was converted
		Expect(run("--dir", dir, "show", good, "--json")).To(ContainSubstring(`"kind": "document"`))
		Expect(run("--dir", dir, "show", bad, "--json")).To(ContainSubstring(`"kind": "document"`))
	})
})
