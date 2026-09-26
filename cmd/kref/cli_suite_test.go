package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

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

// enableTestSigning configures dir for real SSH commit signing: a throwaway
// ed25519 key, an allowed-signers file naming it, and commit.gpgsign on. Specs
// that assert signed behaviour need a genuine key, because kref hands signing
// to `git commit-tree -S` and git refuses without one.
func enableTestSigning(dir string) {
	GinkgoHelper()
	keyDir := GinkgoT().TempDir()
	key := filepath.Join(keyDir, "id_ed25519")
	const email = "t@x" // matches the identity the signing specs init with

	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", email, "-f", key).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))

	pub, err := os.ReadFile(key + ".pub")
	Expect(err).NotTo(HaveOccurred())
	allowed := filepath.Join(keyDir, "allowed_signers")
	Expect(os.WriteFile(allowed, append([]byte(email+" "), pub...), 0o600)).To(Succeed())

	for _, kv := range [][2]string{
		{"user.name", "T"},
		{"user.email", email},
		{"gpg.format", "ssh"},
		{"user.signingkey", key},
		{"gpg.ssh.allowedSignersFile", allowed},
		{"commit.gpgsign", "true"},
	} {
		out, err := exec.Command("git", "-C", dir, "config", "--local", kv[0], kv[1]).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
	}
}

// unsignedThenSigning builds one entry's history BEFORE signing is on, then turns
// it on — the state resign rewrites and attest vouches for.
func unsignedThenSigning(dir string) string {
	GinkgoHelper()
	Expect(run("--dir", dir, "init", "--name", "T", "--email", "t@x")).To(ContainSubstring("initialized"))
	out := run("--dir", dir, "new", "--title", "Legacy", "--body", "b", "--json")
	var a struct {
		ID string `json:"id"`
	}
	Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
	enableTestSigning(dir)
	return a.ID
}

// markPushed mirrors an entry's tip under refs/kref-pushed/, which is what a real
// `kref sync push` records and what resign's gate reads. Done by hand here because
// these specs have no remote; the end-to-end suite drives the same gate through an
// actual push.
func markPushed(dir, id string) {
	GinkgoHelper()
	ref := "refs/kref-personal/" + id // `kref new` defaults to the personal tier
	tip, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	Expect(err).NotTo(HaveOccurred())
	out, err := exec.Command("git", "-C", dir, "update-ref",
		"refs/kref-pushed/kref-personal/"+id, strings.TrimSpace(string(tip))).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
}
