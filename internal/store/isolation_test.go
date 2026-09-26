package store

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/testenv"
)

// This suite shells out to `git commit-tree -S`, and under gpg.format=openpgp
// git hands the signing to gpg, which reads $GNUPGHOME and falls back to
// $HOME/.gnupg. A suite that can reach real key material is a suite that can
// mutate it: gpg-agent writes to that keyring and spawns against its socket dir.
//
// The HOME half of the fence — and the canaries for it — live in
// internal/testenv, which every suite in the module hangs off. This one stays
// here because GNUPGHOME is set here: it is the second fence, and it is the one
// that names gpg.
var _ = Describe("GPG isolation", func() {
	It("points GNUPGHOME at a throwaway keyring, never the developer's own", func() {
		home := os.Getenv("GNUPGHOME")
		Expect(home).NotTo(BeEmpty(),
			"GNUPGHOME is unset, so gpg falls back to $HOME/.gnupg")

		Expect(home).NotTo(Equal(filepath.Join(testenv.RealHome, ".gnupg")),
			"GNUPGHOME is the developer's own keyring")

		info, err := os.Stat(home)
		Expect(err).NotTo(HaveOccurred(), "GNUPGHOME does not exist")
		Expect(info.IsDir()).To(BeTrue(), "GNUPGHOME is not a directory")
		// gpg refuses to use a keyring others can read, and says so on stderr in a
		// way that reads as an unrelated test failure.
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)),
			"GNUPGHOME must be 0700 or gpg complains about unsafe permissions")
	})
})
