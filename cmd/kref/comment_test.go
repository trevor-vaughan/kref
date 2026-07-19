package main

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// newEntry creates a basic entry and returns its full id.
func newEntry(dir, title string) string {
	GinkgoHelper()
	out := run("--dir", dir, "new", "--title", title, "--body", "body", "--json")
	var added struct {
		ID string `json:"id"`
	}
	Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
	Expect(added.ID).NotTo(BeEmpty())
	return added.ID
}

// entryComments reads the entry via --json and returns its Comments slice.
func entryComments(dir, id string) []entry.Comment {
	GinkgoHelper()
	out := run("--dir", dir, "show", id, "--json")
	var snap entry.Snapshot
	Expect(json.Unmarshal([]byte(out), &snap)).To(Succeed())
	return snap.Comments
}

var _ = Describe("kref comment", func() {
	Describe("add path", func() {
		It("adds a top-level comment via -m", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Alpha")

			out := run("--dir", dir, "comment", id, "-m", "first note")
			Expect(out).To(ContainSubstring("commented"))

			// Comments appear in the rendered (non-plain) show output.
			show := run("--dir", dir, "show", id)
			Expect(show).To(ContainSubstring("first note"))
		})

		It("adds a question and show renders the open-question glyph", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Beta")

			run("--dir", dir, "comment", id, "-m", "why?", "--question")

			// Comments appear in the rendered (non-plain) show output; ◉ marks an open question.
			show := run("--dir", dir, "show", id)
			Expect(show).To(ContainSubstring("why?"))
			Expect(show).To(ContainSubstring("◉"))
		})

		It("reads a comment body from piped stdin", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Gamma")

			out := runIn("stdin comment\n", "--dir", dir, "comment", id)
			Expect(out).To(ContainSubstring("commented"))

			// Comments appear in the rendered (non-plain) show output.
			show := run("--dir", dir, "show", id)
			Expect(show).To(ContainSubstring("stdin comment"))
		})

		It("rejects an empty body", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Delta")

			_, err := runErr("--dir", dir, "comment", id, "-m", "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("empty"))
		})

		It("rejects --question and --resolve together", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Epsilon")

			run("--dir", dir, "comment", id, "-m", "q?", "--question")
			comments := entryComments(dir, id)
			Expect(comments).To(HaveLen(1))
			prefix := comments[0].ID[:6]

			_, err := runErr("--dir", dir, "comment", id, "-q", "--resolve", prefix)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mutually exclusive"))
		})
	})

	Describe("resolve path", func() {
		It("resolves a question by prefix and show renders the resolved glyph", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Zeta")

			run("--dir", dir, "comment", id, "-m", "why?", "--question")
			comments := entryComments(dir, id)
			Expect(comments).To(HaveLen(1))
			prefix := comments[0].ID[:6]

			out := run("--dir", dir, "comment", id, "--resolve", prefix)
			Expect(out).To(ContainSubstring("resolved"))

			// ✓ marks a resolved question in the rendered show output.
			show := run("--dir", dir, "show", id)
			Expect(show).To(ContainSubstring("✓"))
		})

		It("adds a closing note reply when -m is given alongside --resolve", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Eta")

			run("--dir", dir, "comment", id, "-m", "open q?", "--question")
			comments := entryComments(dir, id)
			Expect(comments).To(HaveLen(1))
			prefix := comments[0].ID[:6]

			run("--dir", dir, "comment", id, "--resolve", prefix, "-m", "because reasons")

			comments = entryComments(dir, id)
			Expect(comments).To(HaveLen(2))
			// Second comment is the closing-note reply
			bodies := []string{comments[0].Body, comments[1].Body}
			Expect(strings.Join(bodies, " ")).To(ContainSubstring("because reasons"))
		})

		It("rejects resolving a non-question comment", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Theta")

			run("--dir", dir, "comment", id, "-m", "plain note")
			comments := entryComments(dir, id)
			Expect(comments).To(HaveLen(1))
			prefix := comments[0].ID[:6]

			_, err := runErr("--dir", dir, "comment", id, "--resolve", prefix)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a question"))
		})

		It("errors on an unknown comment prefix", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Iota")

			_, err := runErr("--dir", dir, "comment", id, "--resolve", "000000")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no comment matches"))
		})
	})

	Describe("edit/delete path", func() {
		It("edits a comment body by prefix", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Kappa")

			run("--dir", dir, "comment", id, "-m", "first")
			prefix := entryComments(dir, id)[0].ID[:6]

			out := run("--dir", dir, "comment", id, "--edit", prefix, "-m", "second")
			Expect(out).To(ContainSubstring("edited"))

			comments := entryComments(dir, id)
			Expect(comments).To(HaveLen(1))
			Expect(comments[0].Body).To(Equal("second"))
			Expect(comments[0].Edited).To(BeTrue())
		})

		It("soft-deletes a comment with -y", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Lambda")

			run("--dir", dir, "comment", id, "-m", "doomed")
			prefix := entryComments(dir, id)[0].ID[:6]

			out := run("--dir", dir, "comment", id, "--delete", prefix, "-y")
			Expect(out).To(ContainSubstring("deleted"))

			comments := entryComments(dir, id)
			Expect(comments).To(HaveLen(1))
			Expect(comments[0].Deleted).To(BeTrue())
		})

		It("aborts a delete when the confirmation is not 'yes'", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Mu")

			run("--dir", dir, "comment", id, "-m", "keep me")
			prefix := entryComments(dir, id)[0].ID[:6]

			out := runIn("no\n", "--dir", dir, "comment", id, "--delete", prefix)
			Expect(out).To(ContainSubstring("aborted"))
			Expect(entryComments(dir, id)[0].Deleted).To(BeFalse())
		})

		It("rejects --edit and --delete together", func() {
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Nu")

			run("--dir", dir, "comment", id, "-m", "note")
			prefix := entryComments(dir, id)[0].ID[:6]

			_, err := runErr("--dir", dir, "comment", id, "--edit", prefix, "--delete", prefix)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mutually exclusive"))
		})
	})

	Describe("secret scanning (fail-closed, no work lost)", func() {
		const secretBody = "note: awsToken := \"ghp_012345678901234567890123456789abcdef\""

		It("quarantines a secret comment on a syncable entry; the held comment is not posted", func() {
			GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Gamma") // default personal tier (syncable)

			out := run("--dir", dir, "comment", id, "-m", secretBody)
			Expect(out).To(ContainSubstring("quarantined")) // held, not an error
			Expect(out).NotTo(ContainSubstring("ghp_0123")) // never echoes the secret
			// The target keeps only the review question-comment, not the held one.
			cs := entryComments(dir, id)
			Expect(cs).To(HaveLen(1))
			Expect(cs[0].Question).To(BeTrue())
		})

		It("force-approves a flagged comment in one step (park then approve)", func() {
			GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
			dir := gitRepo()
			run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
			id := newEntry(dir, "Delta")

			out := run("--dir", dir, "comment", id, "-m", secretBody, "--force")
			Expect(out).To(ContainSubstring("force-approved"))
			// The forced comment is applied to the entry (the review thread it
			// briefly parked is resolved on approval).
			bodies := make([]string, 0)
			for _, c := range entryComments(dir, id) {
				bodies = append(bodies, c.Body)
			}
			Expect(bodies).To(ContainElement(ContainSubstring(secretBody)))
		})
	})
})

var _ = Describe("kref list --open-questions", func() {
	It("lists only entries with an unresolved question comment", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		aaID := newEntry(dir, "AA")
		run("--dir", dir, "comment", aaID, "-q", "-m", "why?")
		_ = newEntry(dir, "BB")

		out := run("--dir", dir, "list", "--open-questions", "--no-pager")
		Expect(out).To(ContainSubstring("AA"))
		Expect(out).NotTo(ContainSubstring("BB"))
	})
})
