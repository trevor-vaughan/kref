package main

import (
	"testing"

	gogit "github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCLI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CLI Suite")
}

// gitRepo returns a fresh temp dir that is already a (non-bare) git repo,
// matching how a real user runs `kref init` inside their project.
func gitRepo() string {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	_, err := gogit.PlainInit(dir, false)
	Expect(err).NotTo(HaveOccurred())
	return dir
}
