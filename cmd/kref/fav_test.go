package main

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"
)

var _ = Describe("user favorite helpers", func() {
	isolate := func() {
		home := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)
		GinkgoT().Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	}

	It("sets then removes a user-scope favorite", func() {
		isolate()
		Expect(setUserFavorite("api-notes", entity.Id("aaaabbbbcccc"))).To(Succeed())
		_, uc, err := loadUserConfigForEdit()
		Expect(err).NotTo(HaveOccurred())
		Expect(uc.Favorites).To(HaveKeyWithValue("api-notes", "aaaabbbbcccc"))

		Expect(removeUserFavorite("api-notes")).To(Succeed())
		_, uc2, err := loadUserConfigForEdit()
		Expect(err).NotTo(HaveOccurred())
		Expect(uc2.Favorites).NotTo(HaveKey("api-notes"))
	})

	It("rejects a hex-only favorite name (would shadow an id)", func() {
		isolate()
		Expect(setUserFavorite("abcdef", entity.Id("aaaa"))).NotTo(Succeed())
	})

	It("errors removing a name that does not exist", func() {
		isolate()
		Expect(removeUserFavorite("nope")).NotTo(Succeed())
	})
})
