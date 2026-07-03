package store

import (
	"bytes"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/riotbox/kref/internal/entry"
)

var _ = Describe("portable export/import", func() {
	setEnv := func(k, v string) {
		old, had := os.LookupEnv(k)
		Expect(os.Setenv(k, v)).To(Succeed())
		DeferCleanup(func() {
			if had {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}

	It("round-trips a private entry into a fresh repo, preserving the author and excluding other tiers", func() {
		a, err := Init(gitRepo(), "Ada", "ada@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		priv, err := a.Add(entry.TierPrivate, "memory", "Secret", "body")
		Expect(err).NotTo(HaveOccurred())
		shared, err := a.Add(entry.TierShared, "spec", "Public", "body")
		Expect(err).NotTo(HaveOccurred())

		bundle := filepath.Join(GinkgoT().TempDir(), "priv.bundle")
		f, err := os.Create(bundle)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Export(f, []entry.Tier{entry.TierPrivate})).To(Succeed())
		Expect(f.Close()).To(Succeed())

		b, err := Init(gitRepo(), "Bob", "bob@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		_, err = b.Import(bundle, []entry.Tier{entry.TierPrivate})
		Expect(err).NotTo(HaveOccurred())

		got, err := b.Get(priv)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.CreatedBy).To(Equal("Ada"))
		Expect(got.Tier).To(Equal(string(entry.TierPrivate)))

		// the shared entry was outside the --tier private filter
		_, err = b.Get(shared)
		Expect(err).To(HaveOccurred())
	})

	It("errors when the selected tiers have nothing to export", func() {
		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		var buf bytes.Buffer
		Expect(a.Export(&buf, []entry.Tier{entry.TierPrivate})).NotTo(Succeed())
	})

	It("resolves the vault path under XDG_DATA_HOME and ends at private.bundle", func() {
		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		data := GinkgoT().TempDir()
		setEnv("XDG_DATA_HOME", data)
		p, err := a.VaultPath()
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(HavePrefix(filepath.Join(data, "kref")))
		Expect(filepath.Base(p)).To(Equal("private.bundle"))
	})

	It("backup then restore recovers a purged private entry", func() {
		data := GinkgoT().TempDir()
		setEnv("XDG_DATA_HOME", data)
		a, err := Init(gitRepo(), "A", "a@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		id, err := a.Add(entry.TierPrivate, "memory", "Keep me", "body")
		Expect(err).NotTo(HaveOccurred())

		_, err = a.VaultBackup()
		Expect(err).NotTo(HaveOccurred())

		// lose it
		Expect(a.Purge(id, false, false)).To(Succeed())
		_, err = a.Get(id)
		Expect(err).To(HaveOccurred())

		// restore from the vault
		_, _, err = a.VaultRestore()
		Expect(err).NotTo(HaveOccurred())
		got, err := a.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Keep me"))
	})
})
