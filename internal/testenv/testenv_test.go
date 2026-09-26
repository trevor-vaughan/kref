package testenv

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMain(m *testing.M) { Main(m) }

func TestTestenv(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Testenv Suite")
}

// These pin the fence every suite in the module hangs off, in the one place it
// is implemented. Without them the fence can be weakened — or quietly stop
// applying — and nothing fails.
//
// They compare against RealHome rather than os.UserHomeDir(), which resolves
// through $HOME and so reports the throwaway directory once the fence is up. A
// canary that cannot see what it guards is not a canary; the GNUPGHOME check
// this pattern came from had exactly that flaw.
var _ = Describe("the test-environment fence", func() {
	It("captures the real home before redirecting HOME", func() {
		Expect(RealHome).NotTo(BeEmpty(),
			"the real home was never captured, so nothing here can tell a fence from a leak")
	})

	It("points HOME at a throwaway directory, never the developer's own", func() {
		home := os.Getenv("HOME")
		Expect(home).NotTo(BeEmpty(),
			"HOME is unset, so os.UserHomeDir fails and every ~-derived path with it")
		Expect(home).NotTo(Equal(RealHome),
			"HOME is the developer's own, so their config decides what the suites assert")
	})

	// Setting HOME is not enough on its own: a developer's shell commonly exports
	// XDG_CONFIG_HOME already, and an inherited one points straight back at the
	// real ~/.config/kref that moving HOME was meant to fence off. Each of these
	// is a path kref genuinely reads or writes.
	DescribeTable("points every XDG base directory inside the throwaway home",
		func(key string, want ...string) {
			home := os.Getenv("HOME")
			Expect(os.Getenv(key)).To(Equal(filepath.Join(append([]string{home}, want...)...)),
				"%s must follow the throwaway HOME", key)
		},
		Entry("kref's config and identity profiles", "XDG_CONFIG_HOME", ".config"),
		Entry("kref's scratch tree, which carries entry bodies", "XDG_CACHE_HOME", ".cache"),
		Entry("the private-tier vault", "XDG_DATA_HOME", ".local", "share"),
		Entry("kref's durable state", "XDG_STATE_HOME", ".local", "state"),
	)

	It("leaves the throwaway home private, since private-tier content lands there", func() {
		info, err := os.Stat(os.Getenv("HOME"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)))
	})
})
