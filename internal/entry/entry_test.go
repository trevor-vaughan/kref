package entry_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entities/identity"
	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// newTestRepo creates a throwaway repo with kref clocks loaded.
func newTestRepo() repository.ClockedRepo {
	GinkgoHelper()
	repo, err := repository.InitGoGitRepo(GinkgoT().TempDir(), "kref")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = repo.Close() })
	return repo
}

func newAuthor(repo repository.ClockedRepo) identity.Interface {
	GinkgoHelper()
	author, err := identity.NewIdentity(repo, "Tester", "tester@example.com")
	Expect(err).NotTo(HaveOccurred())
	Expect(author.Commit(repo)).To(Succeed())
	return author
}

func mustRead(repo repository.ClockedRepo, id entity.Id) *entry.Entry {
	GinkgoHelper()
	got, err := entry.Read(repo, entry.TierShared, id)
	Expect(err).NotTo(HaveOccurred())
	return got
}

var _ = Describe("Entry create and compile", func() {
	It("records kind, title, and default status", func() {
		repo := newTestRepo()
		author := newAuthor(repo)

		e := entry.New(entry.TierShared)
		e.Append(entry.NewCreate(author, "spec", "Auth design"))
		Expect(e.Commit(repo)).To(Succeed())

		snap := mustRead(repo, e.Id()).Compile()
		Expect(snap.Kind).To(Equal("spec"))
		Expect(snap.Title).To(Equal("Auth design"))
		Expect(snap.Status).To(Equal("open"))
		Expect(snap.CreatedBy).To(Equal("Tester"))
		Expect(snap.CreatedByEmail).To(Equal("tester@example.com"))
	})
})
