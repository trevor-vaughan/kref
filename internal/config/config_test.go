package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/config"
)

var _ = Describe("Default", func() {
	It("compiles in the expected defaults", func() {
		d := config.Default()
		Expect(d.WarnUnscannedOn()).To(BeTrue())
		Expect(d.Version).To(Equal(config.CurrentVersion))
		Expect(d.TrustedKeys).To(ConsistOf("favorites", "warn_unscanned"))
		Expect(d.Favorites).To(BeEmpty())
	})
})

var _ = Describe("Merge", func() {
	It("unions favorites with the user value winning on a shared name", func() {
		project := &config.Config{
			Favorites: map[string]string{"todo": "aaaa1", "roadmap": "bbbb2"},
		}
		user := &config.Config{
			Favorites: map[string]string{"todo": "cccc3", "spec": "dddd4"},
		}
		out := config.Merge(project, user)
		Expect(out.Favorites).To(HaveKeyWithValue("todo", "cccc3"))    // user wins
		Expect(out.Favorites).To(HaveKeyWithValue("roadmap", "bbbb2")) // project-only survives
		Expect(out.Favorites).To(HaveKeyWithValue("spec", "dddd4"))
	})

	It("takes user WarnUnscanned over project when the user set it", func() {
		project := &config.Config{WarnUnscanned: new(true)}
		user := &config.Config{WarnUnscanned: new(false)}
		out := config.Merge(project, user)
		Expect(out.WarnUnscannedOn()).To(BeFalse())
	})

	It("does NOT clobber a project scalar the user left unset (per-key override)", func() {
		// Regression: a present user file that only sets favorites must not reset
		// the project entry's warn_unscanned:false back to the default true.
		project := &config.Config{WarnUnscanned: new(false)}
		user := &config.Config{Favorites: map[string]string{"todo": "aaaa1"}}
		out := config.Merge(project, user)
		Expect(out.WarnUnscannedOn()).To(BeFalse())
		Expect(out.Favorites).To(HaveKeyWithValue("todo", "aaaa1"))
	})

	It("takes TrustedKeys from the user only (root of trust)", func() {
		project := &config.Config{TrustedKeys: []string{"favorites"}}
		user := &config.Config{TrustedKeys: []string{"warn_unscanned"}}
		out := config.Merge(project, user)
		Expect(out.TrustedKeys).To(ConsistOf("warn_unscanned"))
	})

	It("tolerates nil project and nil user", func() {
		out := config.Merge(nil, nil)
		Expect(out.WarnUnscannedOn()).To(BeTrue())
		Expect(out.Version).To(Equal(config.CurrentVersion))
	})
})

var _ = Describe("Filter", func() {
	var c *config.Config
	BeforeEach(func() {
		c = &config.Config{
			Version:       config.CurrentVersion,
			WarnUnscanned: new(false),
			Favorites:     map[string]string{"todo": "aaaa1"},
			TrustedKeys:   []string{"favorites", "warn_unscanned"},
		}
	})

	It("drops favorites when 'favorites' is not trusted", func() {
		out := config.Filter(c, []string{"warn_unscanned"})
		Expect(out.Favorites).To(BeEmpty())
	})

	It("keeps favorites when 'favorites' is trusted", func() {
		out := config.Filter(c, []string{"favorites"})
		Expect(out.Favorites).To(HaveKeyWithValue("todo", "aaaa1"))
	})

	It("always clears TrustedKeys", func() {
		out := config.Filter(c, []string{"favorites", "warn_unscanned"})
		Expect(out.TrustedKeys).To(BeEmpty())
	})

	It("leaves WarnUnscanned unset (defaults to true) when 'warn_unscanned' is not trusted", func() {
		out := config.Filter(c, []string{"favorites"})
		Expect(out.WarnUnscanned).To(BeNil())      // sparse: no override
		Expect(out.WarnUnscannedOn()).To(BeTrue()) // resolves to the default
	})

	It("keeps WarnUnscanned when 'warn_unscanned' is trusted", func() {
		out := config.Filter(c, []string{"warn_unscanned"})
		Expect(out.WarnUnscannedOn()).To(BeFalse())
	})
})

var _ = Describe("todo config keys", func() {
	It("defaults glyphs to geometric and default-todo to empty", func() {
		c := config.Default()
		Expect(c.GlyphTheme()).To(Equal("geometric"))
		Expect(c.DefaultTodo()).To(Equal(""))
	})

	It("merges a user-set glyph theme over the default", func() {
		g := "emoji"
		merged := config.Merge(nil, &config.Config{TodoGlyphs: &g})
		Expect(merged.GlyphTheme()).To(Equal("emoji"))
	})

	It("falls back to geometric for an unrecognized theme", func() {
		bad := "sparkles"
		merged := config.Merge(nil, &config.Config{TodoGlyphs: &bad})
		Expect(merged.GlyphTheme()).To(Equal("geometric"))
	})

	It("merges todo.default", func() {
		d := "myfav"
		merged := config.Merge(nil, &config.Config{TodoDefault: &d})
		Expect(merged.DefaultTodo()).To(Equal("myfav"))
	})
})
