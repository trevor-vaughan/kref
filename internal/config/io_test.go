package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/riotbox/kref/internal/config"
)

var _ = Describe("Parse", func() {
	It("rejects an unknown key", func() {
		_, err := config.Parse([]byte("version: 1\nbogus: true\n"))
		Expect(err).To(HaveOccurred())
	})

	It("leaves WarnUnscanned unset (sparse) when the key is absent", func() {
		// Parse returns a sparse layer: an absent key is nil, NOT the default.
		// Defaults are applied by Merge, so the layer can override per-key.
		c, err := config.Parse([]byte("version: 1\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.WarnUnscanned).To(BeNil())
		Expect(c.WarnUnscannedOn()).To(BeTrue()) // resolves to the default
	})

	It("honors an explicit false for WarnUnscanned", func() {
		c, err := config.Parse([]byte("version: 1\nwarn_unscanned: false\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.WarnUnscanned).NotTo(BeNil())
		Expect(c.WarnUnscannedOn()).To(BeFalse())
	})

	It("returns an empty sparse layer for empty bytes", func() {
		c, err := config.Parse(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.WarnUnscanned).To(BeNil())
		Expect(c.Version).To(BeZero())
		Expect(c.TrustedKeys).To(BeEmpty())
	})
})

var _ = Describe("MarshalEntry", func() {
	It("round-trips version and favorites but never writes trusted_keys", func() {
		c := &config.Config{
			Version:     config.CurrentVersion,
			Favorites:   map[string]string{"todo": "aaaaaaaa"},
			TrustedKeys: []string{"favorites", "warn_unscanned"},
		}
		out, err := config.MarshalEntry(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).NotTo(ContainSubstring("trusted_keys"))

		parsed, err := config.Parse(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed.Version).To(Equal(config.CurrentVersion))
		Expect(parsed.Favorites).To(HaveKeyWithValue("todo", "aaaaaaaa"))
		Expect(parsed.TrustedKeys).To(BeEmpty())
	})
})

var _ = Describe("UserPath", func() {
	It("honors XDG_CONFIG_HOME", func() {
		getenv := func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return "/xdg"
			}
			return ""
		}
		p, err := config.UserPath(getenv)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/xdg/kref/config.yaml"))
	})

	It("falls back to HOME/.config", func() {
		getenv := func(k string) string {
			if k == "HOME" {
				return "/home/u"
			}
			return ""
		}
		p, err := config.UserPath(getenv)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/home/u/.config/kref/config.yaml"))
	})

	It("errors when neither XDG_CONFIG_HOME nor HOME is set", func() {
		_, err := config.UserPath(func(string) string { return "" })
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("WriteFile", func() {
	It("backs up an existing file to .bck", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "config.yaml")
		Expect(os.WriteFile(path, []byte("old contents\n"), 0o600)).To(Succeed())

		Expect(config.WriteFile(path, config.Default())).To(Succeed())

		bck, err := os.ReadFile(path + ".bck")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(bck)).To(Equal("old contents\n"))

		written, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(written)).To(ContainSubstring("version: 1"))
	})

	It("creates the parent directory when absent", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "nested", "config.yaml")
		Expect(config.WriteFile(path, config.Default())).To(Succeed())
		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("WriteBytes", func() {
	It("writes the bytes verbatim, preserving comments and formatting", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "config.yaml")
		content := []byte("# a comment the user wrote\nversion: 1\nwarn_unscanned: true\n")

		Expect(config.WriteBytes(path, content)).To(Succeed())

		written, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(written).To(Equal(content))
	})

	It("backs up an existing file to .bck before writing", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "config.yaml")
		Expect(os.WriteFile(path, []byte("old contents\n"), 0o600)).To(Succeed())

		Expect(config.WriteBytes(path, []byte("new contents\n"))).To(Succeed())

		bck, err := os.ReadFile(path + ".bck")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(bck)).To(Equal("old contents\n"))

		written, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(written)).To(Equal("new contents\n"))
	})
})

var _ = Describe("Template round-trip", func() {
	It("parses back the rendered default template", func() {
		out, err := config.Template(config.Default())
		Expect(err).NotTo(HaveOccurred())
		c, err := config.Parse(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.WarnUnscanned).NotTo(BeNil())
		Expect(c.WarnUnscannedOn()).To(BeTrue())
		Expect(c.TrustedKeys).To(HaveLen(2))
	})

	It("leaves warn_unscanned COMMENTED (sparse) for the init config", func() {
		out, err := config.Template(config.InitialUserConfig())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).To(ContainSubstring("# warn_unscanned: true"))
		// Re-parsing an init file yields a sparse layer that does not set the key.
		c, err := config.Parse(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.WarnUnscanned).To(BeNil())
		Expect(c.TrustedKeys).To(HaveLen(2)) // trusted_keys stays active
	})

	It("renders warn_unscanned ACTIVE when the layer set it", func() {
		out, err := config.Template(&config.Config{Version: 1, WarnUnscanned: bp(false)})
		Expect(err).NotTo(HaveOccurred())
		c, err := config.Parse(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.WarnUnscanned).NotTo(BeNil())
		Expect(c.WarnUnscannedOn()).To(BeFalse())
	})
})
