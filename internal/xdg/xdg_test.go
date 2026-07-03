package xdg

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CacheTempDir", func() {
	It("returns $XDG_CACHE_HOME/kref/tmp and creates it user-only", func() {
		base := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CACHE_HOME", base)

		dir := CacheTempDir()

		Expect(dir).To(Equal(filepath.Join(base, "kref", "tmp")))
		info, err := os.Stat(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.IsDir()).To(BeTrue())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)))
	})

	It("defaults to ~/.cache/kref/tmp when XDG_CACHE_HOME is unset", func() {
		home := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CACHE_HOME", "")
		GinkgoT().Setenv("HOME", home)

		Expect(CacheTempDir()).To(Equal(filepath.Join(home, ".cache", "kref", "tmp")))
	})

	It("falls back to the system temp dir when no home can be resolved", func() {
		GinkgoT().Setenv("XDG_CACHE_HOME", "")
		GinkgoT().Setenv("HOME", "")

		// os.CreateTemp treats "" as the system temp dir, so the fallback
		// keeps kref working in HOME-less environments (containers, CI).
		Expect(CacheTempDir()).To(Equal(""))
	})

	It("falls back to the system temp dir when the cache dir is not creatable", func() {
		base := GinkgoT().TempDir()
		blocker := filepath.Join(base, "kref")
		Expect(os.WriteFile(blocker, []byte("in the way"), 0o600)).To(Succeed())
		GinkgoT().Setenv("XDG_CACHE_HOME", base)

		Expect(CacheTempDir()).To(Equal(""))
	})
})
