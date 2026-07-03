package main

import (
	"github.com/spf13/cobra"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("argGuide", func() {
	guide := argGuide{
		noun:  "an entry id",
		find:  "kref list",
		usage: "kref purge <id>",
		examples: []string{
			"kref purge a1b2c3d4        # delete the entry's ref",
			"kref purge a1b2c3d4 --gc   # ...and gc objects now",
		},
	}

	newCmd := func() *cobra.Command {
		root := &cobra.Command{Use: "kref"}
		c := &cobra.Command{Use: "purge <id>", RunE: func(*cobra.Command, []string) error { return nil }}
		applyGuide(c, cobra.ExactArgs(1), guide)
		root.AddCommand(c)
		return c
	}

	It("renders actionable steps when the arg count is wrong", func() {
		c := newCmd()
		err := c.Args(c, []string{})
		Expect(err).To(HaveOccurred())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("kref purge needs an entry id."))
		Expect(msg).To(ContainSubstring("find one:  kref list"))
		Expect(msg).To(ContainSubstring("then:      kref purge <id>"))
		Expect(msg).To(ContainSubstring("kref purge a1b2c3d4        # delete the entry's ref"))
		Expect(msg).To(ContainSubstring("details:   kref purge --help"))
	})

	It("passes a correct arg count through to the inner validator", func() {
		c := newCmd()
		Expect(c.Args(c, []string{"a1b2c3d4"})).To(Succeed())
	})

	It("populates the Example block from the same examples", func() {
		c := newCmd()
		Expect(c.Example).To(ContainSubstring("kref purge a1b2c3d4        # delete the entry's ref"))
	})
})
