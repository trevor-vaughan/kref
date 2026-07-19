package main

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("cache refresh command", func() {
	It("__cache-refresh warms the excerpt cache without error", func() {
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		addEntry(dir, "--title", "A", "--kind", "note")
		run("--dir", dir, "__cache-refresh") // run() asserts Execute() succeeds
		matches, err := filepath.Glob(filepath.Join(dir, ".git", "kref", "cache", "excerpt-*"))
		Expect(err).NotTo(HaveOccurred())
		Expect(matches).NotTo(BeEmpty())
	})
})
