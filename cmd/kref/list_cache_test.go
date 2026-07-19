package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("list table cache parity", func() {
	It("renders the same --plain table whether served from a cold or warm cache", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		addEntry(dir, "--title", "Alpha", "--kind", "spec")
		addEntry(dir, "--title", "Beta", "--kind", "note")
		addEntry(dir, "--title", "Gamma", "--kind", "spec")

		cold := run("--dir", dir, "list", "--plain") // builds the cache
		warm := run("--dir", dir, "list", "--plain") // reads the cache
		Expect(warm).To(Equal(cold))
		Expect(cold).To(ContainSubstring("Alpha"))
	})

	It("list --json still emits full snapshots including the body", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		addEntry(dir, "--title", "WithBody", "--kind", "note", "--body", "UNIQUEBODYTOKEN123")
		out := run("--dir", dir, "list", "--json")
		Expect(out).To(ContainSubstring("UNIQUEBODYTOKEN123"))
	})
})
