package store

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/config"
	"github.com/trevor-vaughan/kref/internal/entry"
)

// writeUserConfig writes a user config file under an isolated XDG dir and
// returns the dir. It creates the kref subdir first (config.UserPath expects
// $XDG_CONFIG_HOME/kref/config.yaml).
func writeUserConfig(xdg, body string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Join(xdg, "kref"), 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(xdg, "kref", "config.yaml"), []byte(body), 0o644)).To(Succeed())
}

var _ = Describe("Store two-layer config load", func() {
	It("takes favorites from a project entry when no user file exists", func() {
		GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		_, err = s.Add(entry.TierPersonal, "config", "kref.conf", "favorites:\n  todo: aaaaaaaa\n")
		Expect(err).NotTo(HaveOccurred())

		// Reopen so loadConfig runs with the entry present.
		s2, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })

		Expect(s2.Favorites()).To(HaveKeyWithValue("todo", "aaaaaaaa"))
	})

	It("trusts warn_unscanned from an entry by default, then drops it when the user narrows trusted_keys", func() {
		xdg := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CONFIG_HOME", xdg)
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		_, err = s.Add(entry.TierPersonal, "config", "kref.conf",
			"warn_unscanned: false\nfavorites:\n  todo: aaaaaaaa\n")
		Expect(err).NotTo(HaveOccurred())

		// No user file: default trusted_keys includes warn_unscanned, so the
		// entry's warn_unscanned=false is honored.
		s2, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		Expect(s2.EffectiveConfig().WarnUnscannedOn()).To(BeFalse())
		Expect(s2.Favorites()).To(HaveKeyWithValue("todo", "aaaaaaaa"))

		// A user file that trusts only favorites drops warn_unscanned trust: the
		// entry's value is discarded and warn_unscanned resets to the default true.
		writeUserConfig(xdg, "version: 1\ntrusted_keys: [favorites]\n")
		s3, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s3.Close() })
		Expect(s3.EffectiveConfig().WarnUnscannedOn()).To(BeTrue())
		Expect(s3.Favorites()).To(HaveKeyWithValue("todo", "aaaaaaaa"))
	})

	It("auto-migrates a versionless user file on load, backing up the original", func() {
		xdg := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CONFIG_HOME", xdg)
		writeUserConfig(xdg, "favorites:\n  todo: aaaaaaaa\n")

		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		Expect(s.Favorites()).To(HaveKeyWithValue("todo", "aaaaaaaa"))

		cfgPath := filepath.Join(xdg, "kref", "config.yaml")
		rewritten, err := os.ReadFile(cfgPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(rewritten)).To(ContainSubstring("version: 1"))

		_, err = os.Stat(cfgPath + ".bck")
		Expect(err).NotTo(HaveOccurred())
	})

	It("resolves a user favorite name to its id and reports the user origin", func() {
		xdg := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CONFIG_HOME", xdg)
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierPersonal, "note", "Fav target", "body")
		Expect(err).NotTo(HaveOccurred())

		writeUserConfig(xdg, "favorites:\n  todo: "+id.String()+"\n")

		// Reopen so loadConfig picks up the user file.
		s2, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })

		resolved, err := s2.Resolve("todo")
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(id))
		Expect(s2.FavoriteOrigin("todo")).To(Equal("user"))

		// Favorites do not break plain hex-prefix resolution.
		byPrefix, err := s2.Resolve(id.String()[:8])
		Expect(err).NotTo(HaveOccurred())
		Expect(byPrefix).To(Equal(id))
	})

	It("writes a favorite to the project entry the way `fav add --shared` does", func() {
		GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierPersonal, "note", "Fav target", "body")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Add(entry.TierPersonal, "config", "kref.conf", "version: 1\n")
		Expect(err).NotTo(HaveOccurred())

		// Simulate the `fav add --shared` write path at the store layer.
		body, entryID, ok := s.ProjectConfigEntry()
		Expect(ok).To(BeTrue())
		pc, err := config.Parse([]byte(body))
		Expect(err).NotTo(HaveOccurred())
		if pc.Favorites == nil {
			pc.Favorites = map[string]string{}
		}
		pc.Favorites["road"] = id.String()
		Expect(config.Validate(pc)).To(Succeed())
		newBody, err := config.MarshalEntry(pc)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Update(entryID, string(newBody), "")).To(Succeed())

		// Reopen so loadConfig picks up the rewritten entry.
		s2, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })

		Expect(s2.Favorites()).To(HaveKeyWithValue("road", id.String()))
		Expect(s2.FavoriteOrigin("road")).To(Equal("shared"))
	})

	It("falls back to defaults with no entry and no user file", func() {
		GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		Expect(s.Favorites()).To(BeEmpty())
		Expect(s.EffectiveConfig().WarnUnscannedOn()).To(BeTrue())
	})

	It("resolves the reserved name kref.conf to the project config entry", func() {
		GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		id, err := s.Add(entry.TierPersonal, "config", "kref.conf", "version: 1\n")
		Expect(err).NotTo(HaveOccurred())

		resolved, err := s.Resolve("kref.conf")
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(id))
	})

	It("returns not-found for kref.conf when no config entry exists", func() {
		GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		s, err := Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		_, err = s.Resolve("kref.conf")
		Expect(err).To(MatchError(ContainSubstring("not found")))
	})
})
