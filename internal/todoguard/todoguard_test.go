package todoguard_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/todoguard"
)

var _ = Describe("Guard", func() {
	It("is a no-op for a non-todo kind", func() {
		out, err := todoguard.Guard("spec", "anything at all", todoguard.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("anything at all"))
	})

	It("formats a todo body (moves done items under Done)", func() {
		in := "# T\n\n## Open\n- [x] done\n- [ ] a\n\n## Done (compact)\n"
		out, err := todoguard.Guard("todo", in, todoguard.Options{})
		Expect(err).NotTo(HaveOccurred())
		// todo.Format emits a blank line after the "## Done (compact)" heading.
		Expect(out).To(ContainSubstring("## Done (compact)\n\n- [x] done"))
	})

	It("rejects a todo that stays malformed, carrying the formatted body", func() {
		in := "# T\n\n## Opne\n\n## Open\n\n## Done (compact)\n"
		out, err := todoguard.Guard("todo", in, todoguard.Options{})
		var rej *todoguard.RejectedError
		Expect(errors.As(err, &rej)).To(BeTrue())
		Expect(rej.Violations).NotTo(BeEmpty())
		Expect(rej.Body).To(Equal(out))
		Expect(rej.Error()).To(ContainSubstring("unknown-heading"))
	})

	It("skips the linter under NoLint (writes even if malformed)", func() {
		in := "# T\n\n## Opne\n\n## Open\n\n## Done (compact)\n"
		out, err := todoguard.Guard("todo", in, todoguard.Options{NoLint: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("## Opne"))
	})

	It("skips the formatter under NoFmt (leaves placement untouched)", func() {
		in := "# T\n\n## Open\n- [x] done\n\n## Done (compact)\n"
		out, err := todoguard.Guard("todo", in, todoguard.Options{NoFmt: true, NoLint: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(in))
	})
})

var _ = Describe("CheckVersion", func() {
	It("passes when base equals head for a todo", func() {
		Expect(todoguard.CheckVersion("todo", 7, 7)).NotTo(HaveOccurred())
	})

	It("rejects a stale base with a *StaleError carrying both versions", func() {
		err := todoguard.CheckVersion("todo", 5, 7)
		var st *todoguard.StaleError
		Expect(errors.As(err, &st)).To(BeTrue())
		Expect(st.Base).To(Equal(5))
		Expect(st.Head).To(Equal(7))
		Expect(st.Error()).To(ContainSubstring("v5"))
		Expect(st.Error()).To(ContainSubstring("v7"))
	})

	It("is a no-op for a non-todo kind even when versions differ", func() {
		Expect(todoguard.CheckVersion("spec", 5, 7)).NotTo(HaveOccurred())
	})
})

var _ = Describe("WriteRejected", func() {
	It("preserves the rejected body under the state dir and returns the path", func() {
		state := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_STATE_HOME", state)

		path, err := todoguard.WriteRejected("abcd1234", "# broken\n\n## Opne\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(HavePrefix(filepath.Join(state, "kref", "rejected", "abcd1234-")))
		Expect(path).To(HaveSuffix(".md"))

		got, rerr := os.ReadFile(path)
		Expect(rerr).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("# broken\n\n## Opne\n"))
	})
})
