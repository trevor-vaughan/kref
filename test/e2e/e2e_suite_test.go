//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite")
}

var (
	krefBin     string // freshly built kref binary
	betterleaks string // path to the pinned betterleaks (from KREF_BETTERLEAKS or ./bin)
)

var _ = BeforeSuite(func() {
	betterleaks = os.Getenv("KREF_BETTERLEAKS")
	if betterleaks == "" {
		abs, err := filepath.Abs(filepath.Join("..", "..", "bin", "betterleaks"))
		Expect(err).NotTo(HaveOccurred())
		betterleaks = abs
	}
	Expect(betterleaks).To(BeAnExistingFile(), "betterleaks not found; run `task tools` (or set KREF_BETTERLEAKS)")

	binDir, err := os.MkdirTemp("", "kref-e2e-bin-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(binDir) })

	krefBin = filepath.Join(binDir, "kref")
	build := exec.Command("go", "build", "-o", krefBin, "../../cmd/kref")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	out, err := build.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "build kref: %s", string(out))
})

// krefEnv is one isolated workspace for the kref binary. HOME and XDG_CONFIG_HOME
// point into a throwaway dir so git-bug reads OUR git identity and keyring —
// never the developer's real ~/.gitconfig or keyring. The identity written
// here is deliberately distinct from any real config so a leak is obvious.
type krefEnv struct {
	home string
	dir  string
}

func newKrefEnv(name, email string) *krefEnv {
	GinkgoHelper()
	home := GinkgoT().TempDir()
	Expect(os.MkdirAll(filepath.Join(home, ".config"), 0o755)).To(Succeed())
	cfg := "[user]\n\tname = " + name + "\n\temail = " + email + "\n"
	Expect(os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(cfg), 0o644)).To(Succeed())
	dir := GinkgoT().TempDir()
	gi := exec.Command("git", "init", dir)
	gi.Env = []string{"HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "PATH=" + os.Getenv("PATH")}
	out, err := gi.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git init workdir: %s", string(out))
	return &krefEnv{home: home, dir: dir}
}

func (e *krefEnv) osEnv() []string {
	return []string{
		"HOME=" + e.home,
		"XDG_CONFIG_HOME=" + filepath.Join(e.home, ".config"),
		"GIT_CONFIG_GLOBAL=" + filepath.Join(e.home, ".gitconfig"),
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"PATH=" + os.Getenv("PATH"),
		"KREF_BETTERLEAKS=" + betterleaks,
	}
}

// run executes `kref --dir <e.dir> <args...>` with the isolated env. stdin may be
// empty. Returns stdout, stderr, and the run error.
func (e *krefEnv) run(stdin string, args ...string) (string, string, error) {
	GinkgoHelper()
	full := append([]string{"--dir", e.dir}, args...)
	cmd := exec.Command(krefBin, full...)
	cmd.Env = e.osEnv()
	if stdin != "" {
		cmd.Stdin = bytes.NewReader([]byte(stdin))
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// runAt executes `kref <args...>` with NO --dir flag, from the given working
// directory — exercising the git-style enclosing-repo discovery that the
// default --dir="." relies on.
func (e *krefEnv) runAt(dir, stdin string, args ...string) (string, string, error) {
	GinkgoHelper()
	cmd := exec.Command(krefBin, args...)
	cmd.Dir = dir
	cmd.Env = e.osEnv()
	if stdin != "" {
		cmd.Stdin = bytes.NewReader([]byte(stdin))
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// mustRun asserts the command succeeds and returns stdout.
func (e *krefEnv) mustRun(args ...string) string {
	GinkgoHelper()
	out, errOut, err := e.run("", args...)
	Expect(err).NotTo(HaveOccurred(), "kref %v failed:\nstdout: %s\nstderr: %s", args, out, errOut)
	return out
}

// idOf extracts the "id" field from `kref new` JSON output.
func idOf(jsonOut string) string {
	GinkgoHelper()
	var v struct {
		ID string `json:"id"`
	}
	Expect(json.Unmarshal([]byte(jsonOut), &v)).To(Succeed(), "parsing id from %q", jsonOut)
	Expect(v.ID).NotTo(BeEmpty())
	return v.ID
}

// bareRepo creates a bare git repo to use as a sync remote, in an isolated env.
func bareRepo() string {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	cmd := exec.Command("git", "init", "--bare", dir)
	cmd.Env = []string{"HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git init --bare: %s", string(out))
	return dir
}
