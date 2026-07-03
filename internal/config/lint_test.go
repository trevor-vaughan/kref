package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/riotbox/kref/internal/config"
)

var _ = DescribeTable("ValidFavoriteName",
	func(name string, ok bool) {
		err := config.ValidFavoriteName(name)
		if ok {
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
		}
	},
	Entry("plain word", "todo", true),
	Entry("another word", "roadmap", true),
	Entry("word with hyphen and digit", "spec-1", true),
	Entry("all hex letters shadows an id", "beef", false),
	Entry("all digits shadows an id", "1234", false),
	Entry("full hex shadows an id", "deadbeef", false),
	Entry("reserved conf alias", "kref.conf", false),
	Entry("leading hyphen", "-x", false),
	Entry("contains whitespace", "a b", false),
	Entry("empty", "", false),
)

var _ = Describe("Validate", func() {
	It("accepts the compiled-in defaults", func() {
		Expect(config.Validate(config.Default())).NotTo(HaveOccurred())
	})

	It("rejects a favorite name that is a hex id-prefix", func() {
		c := config.Default()
		c.Favorites = map[string]string{"beef": "aaaa1"}
		Expect(config.Validate(c)).To(HaveOccurred())
	})

	It("rejects trusted_keys naming an unknown key", func() {
		c := config.Default()
		c.TrustedKeys = []string{"favorites", "scanners"}
		Expect(config.Validate(c)).To(HaveOccurred())
	})

	It("tolerates a missing version on a sparse layer (migration owns version)", func() {
		c := config.Default()
		c.Version = 0
		Expect(config.Validate(c)).NotTo(HaveOccurred())
	})

	It("rejects a negative version", func() {
		c := config.Default()
		c.Version = -1
		Expect(config.Validate(c)).To(HaveOccurred())
	})
})
