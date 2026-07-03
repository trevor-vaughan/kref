package store

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entities/identity"
	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("author identity override", func() {
	// initStore creates a store whose baked identity is "Init User".
	initStore := func() string {
		dir := gitRepo()
		s, err := Init(dir, "Init User", "init@example.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		return dir
	}
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
	authorOf := func(s *Store, id entity.Id) [2]string {
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		return [2]string{snap.CreatedBy, snap.CreatedByEmail}
	}
	addEntry := func(s *Store, title string) entity.Id {
		id, err := s.Add(entry.TierShared, "spec", title, "b")
		Expect(err).NotTo(HaveOccurred())
		return id
	}

	It("attributes entries to KREF_AUTHOR_NAME/EMAIL when set", func() {
		dir := initStore()
		setEnv("KREF_AUTHOR_NAME", "Human Dev")
		setEnv("KREF_AUTHOR_EMAIL", "human@example.com")
		s, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(authorOf(s, addEntry(s, "T"))).To(Equal([2]string{"Human Dev", "human@example.com"}))
	})

	It("falls back to git config kref.author.* when env is unset", func() {
		dir := initStore()
		s0, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(s0.repo.LocalConfig().StoreString("kref.author.name", "Config Dev")).To(Succeed())
		Expect(s0.repo.LocalConfig().StoreString("kref.author.email", "config@example.com")).To(Succeed())
		Expect(s0.Close()).To(Succeed())

		s, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(authorOf(s, addEntry(s, "T"))).To(Equal([2]string{"Config Dev", "config@example.com"}))
	})

	It("env beats git config", func() {
		dir := initStore()
		s0, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(s0.repo.LocalConfig().StoreString("kref.author.name", "Config Dev")).To(Succeed())
		Expect(s0.repo.LocalConfig().StoreString("kref.author.email", "config@example.com")).To(Succeed())
		Expect(s0.Close()).To(Succeed())
		setEnv("KREF_AUTHOR_NAME", "Env Dev")
		setEnv("KREF_AUTHOR_EMAIL", "env@example.com")

		s, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(authorOf(s, addEntry(s, "T"))).To(Equal([2]string{"Env Dev", "env@example.com"}))
	})

	It("errors when only one of the env pair is set", func() {
		dir := initStore()
		setEnv("KREF_AUTHOR_NAME", "Only Name")
		_, err := Open(dir)
		Expect(err).To(HaveOccurred())
	})

	It("falls back to the init identity when no override is set", func() {
		dir := initStore()
		s, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(authorOf(s, addEntry(s, "T"))).To(Equal([2]string{"Init User", "init@example.com"}))
	})

	It("reuses one identity for the same name+email across runs (no duplicate)", func() {
		dir := initStore()
		setEnv("KREF_AUTHOR_NAME", "Human Dev")
		setEnv("KREF_AUTHOR_EMAIL", "human@example.com")
		s1, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		_ = addEntry(s1, "A")
		Expect(s1.Close()).To(Succeed())

		s2, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		_ = addEntry(s2, "B")

		ids, err := identity.ListLocalIds(s2.repo)
		Expect(err).NotTo(HaveOccurred())
		n := 0
		for _, iid := range ids {
			i, err := identity.ReadLocal(s2.repo, iid)
			Expect(err).NotTo(HaveOccurred())
			if i.Name() == "Human Dev" && i.Email() == "human@example.com" {
				n++
			}
		}
		Expect(n).To(Equal(1))
	})
})
