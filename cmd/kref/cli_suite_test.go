package main

import (
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCLI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CLI Suite")
}

// Every spec gets a throwaway HOME. The commands under test read and WRITE the
// user config — a `,` menu toggle persists — so without this the suite edits the
// developer's own ~/.config/kref/config.yaml. A spec that needs its own HOME
// still sets it; this runs first and is overridden.
var _ = BeforeEach(func() {
	home := GinkgoT().TempDir()
	GinkgoT().Setenv("HOME", home)
	GinkgoT().Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
})

// gitRepo returns a fresh temp dir that is already a (non-bare) git repo,
// matching how a real user runs `kref init` inside their project.
func gitRepo() string {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	_, err := gogit.PlainInit(dir, false)
	Expect(err).NotTo(HaveOccurred())
	return dir
}
