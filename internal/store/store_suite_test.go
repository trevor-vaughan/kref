package store

import (
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// The HOME fence this suite needs most — kref resolves kref.sign,
// commit.gpgsign and kref.identity out of git's config, and go-git resolves
// git's GLOBAL layer as $XDG_CONFIG_HOME/git/config and $HOME/.gitconfig
// directly — is testenv's, applied above for every package. A developer who
// signs their own commits used to turn signing on for every write in this suite,
// and each one died in `git commit-tree -S` with "No secret key" for the spec's
// throwaway identity.
func TestStore(t *testing.T) {
	isolateGPGHome(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Store Suite")
}

// isolateGPGHome points this process at a throwaway GPG keyring for the whole
// suite. `task test` sets GNUPGHOME too, but the guarantee cannot live only in
// the driver: a bare `go test ./internal/store/`, or one spec run from an editor,
// otherwise sends gpg wherever it falls back to when GNUPGHOME is unset.
//
// testenv.Main already moves that fallback ($HOME/.gnupg) off the developer's
// own keyring, so this is the second of two fences rather than the only one. It
// stays because it is the one that names gpg: someone rewriting the HOME fence
// has no reason to know a keyring hangs off it, and the cost of being wrong is
// writing to real key material. See the canaries in isolation_test.go, which
// fail if either fence comes down.
//
// Ginkgo runs parallel specs as separate processes, each entering TestStore, so
// every process gets its own keyring rather than sharing one agent.
func isolateGPGHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	// gpg refuses a keyring others can read. TempDir is already 0700, but a
	// hostile umask is cheaper to rule out than to diagnose.
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("tighten throwaway GNUPGHOME: %v", err)
	}
	t.Setenv("GNUPGHOME", home)
}

// includeGitConfig makes body reach dir's git config the way a real per-client
// setup does: a global config whose only content is an includeIf matching this
// repository, pointing at the file that carries the settings.
//
// This is the shape kref used to miss entirely. go-git resolves no include, no
// includeIf and no GIT_CONFIG_GLOBAL, so config arriving this way was invisible
// to every decision kref made — while being perfectly visible to git.
func includeGitConfig(dir, body string) {
	GinkgoHelper()
	// git matches gitdir: against the RESOLVED path, and a temp dir sits behind a
	// symlink on macOS.
	resolved, err := filepath.EvalSymlinks(dir)
	Expect(err).NotTo(HaveOccurred())

	cfgDir := GinkgoT().TempDir()
	included := filepath.Join(cfgDir, "included")
	Expect(os.WriteFile(included, []byte(body), 0o600)).To(Succeed())

	global := filepath.Join(cfgDir, "global")
	Expect(os.WriteFile(global,
		[]byte("[includeIf \"gitdir:"+resolved+"/\"]\n\tpath = "+included+"\n"), 0o600)).To(Succeed())
	GinkgoT().Setenv("GIT_CONFIG_GLOBAL", global)
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
