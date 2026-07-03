package config_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/riotbox/kref/internal/config"
)

var _ = Describe("Migrate", func() {
	It("is a no-op when the file is already at the current version", func() {
		in := []byte("version: 1\nwarn_unscanned: false\n")
		out, changed, err := config.Migrate(in)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
		Expect(out).To(Equal(in))
	})

	It("rejects a version newer than this binary", func() {
		_, _, err := config.Migrate([]byte("version: 999\n"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("newer than this kref"))
	})

	It("stamps a versionless file to the current version, preserving content", func() {
		out, changed, err := config.Migrate([]byte("warn_unscanned: true\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(strings.Contains(string(out), "version: 1")).To(BeTrue())

		c, err := config.Parse(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.WarnUnscannedOn()).To(BeTrue())
	})

	It("reports no change when a v1 file has only the version key", func() {
		_, changed, err := config.Migrate([]byte("version: 1\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
	})
})
