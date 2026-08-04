package config_test

import (
	"reflect"

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

var _ = Describe("display preferences", func() {
	It("defaults line numbers on when unset", func() {
		Expect((&config.Config{}).LineNumbersOn()).To(BeTrue())
	})

	It("honours an explicit line-number setting", func() {
		Expect((&config.Config{LineNumbers: new(false)}).LineNumbersOn()).To(BeFalse())
		Expect((&config.Config{LineNumbers: new(true)}).LineNumbersOn()).To(BeTrue())
	})

	It("reports no colour preference when unset, so the caller may auto-detect", func() {
		Expect((&config.Config{}).ColorPref()).To(BeNil())
	})

	It("reports an explicit colour preference", func() {
		p := (&config.Config{Color: new(false)}).ColorPref()
		Expect(p).NotTo(BeNil())
		Expect(*p).To(BeFalse())
	})

	It("carries both pointers through Merge from a sparse user layer", func() {
		out := config.Merge(nil, &config.Config{LineNumbers: new(false), Color: new(true)})
		Expect(out.LineNumbersOn()).To(BeFalse())
		Expect(out.ColorPref()).NotTo(BeNil())
		Expect(*out.ColorPref()).To(BeTrue())
	})

	It("does not let a sparse layer clobber a lower one", func() {
		out := config.Merge(&config.Config{LineNumbers: new(false)}, &config.Config{})
		Expect(out.LineNumbersOn()).To(BeFalse()) // user layer set neither
	})
})

// fullConfig is a Config with EVERY field set — the fixture the reflection guard
// below polices, so a new field cannot be added without being considered here.
func fullConfig() *config.Config {
	return &config.Config{
		Version:       config.CurrentVersion,
		WarnUnscanned: new(false),
		Favorites:     map[string]string{"todo": "abcd1234abcd"},
		TrustedKeys:   []string{"favorites"},
		TodoGlyphs:    new("emoji"),
		TodoDefault:   new("abcd1234abcd"),
		LineNumbers:   new(false),
		Color:         new(true),
	}
}

var _ = Describe("Template round-trip", func() {
	// WriteFile renders through Template, so a field Template forgets is silently
	// dropped when any code path rewrites the file — a user could lose an
	// unrelated setting by toggling a display preference. Every settable field
	// must survive the round-trip.
	It("preserves every field a user can set", func() {
		in := fullConfig()
		b, err := config.Template(in)
		Expect(err).NotTo(HaveOccurred())

		out, err := config.Parse(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.WarnUnscannedOn()).To(BeFalse())
		Expect(out.Favorites).To(HaveKeyWithValue("todo", "abcd1234abcd"))
		Expect(out.TrustedKeys).To(ContainElement("favorites"))
		Expect(out.GlyphTheme()).To(Equal("emoji"))
		Expect(out.DefaultTodo()).To(Equal("abcd1234abcd"))
		Expect(out.LineNumbersOn()).To(BeFalse())
		Expect(out.ColorPref()).NotTo(BeNil())
		Expect(*out.ColorPref()).To(BeTrue())
	})

	It("fails when a new Config field is not carried through Template", func() {
		// The guard the hand-written round-trip above cannot provide: reflect over
		// the fixture and demand every field be set, so adding a field to Config
		// forces this test to be updated — at which point the round-trip below
		// catches a Template that forgot to render it.
		in := fullConfig()
		v := reflect.ValueOf(*in)
		for i := range v.NumField() {
			name := v.Type().Field(i).Name
			Expect(v.Field(i).IsZero()).To(BeFalse(),
				"config.Config.%s is not set in fullConfig(): add it there AND to Template, "+
					"or WriteFile will silently drop it", name)
		}

		b, err := config.Template(in)
		Expect(err).NotTo(HaveOccurred())
		out, err := config.Parse(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.DeepEqual(*out, *in)).To(BeTrue(),
			"a field did not survive Template -> Parse:\n got %+v\nwant %+v", *out, *in)
	})

	It("leaves unset fields unset, so the file stays a sparse override", func() {
		b, err := config.Template(&config.Config{Version: config.CurrentVersion})
		Expect(err).NotTo(HaveOccurred())
		out, err := config.Parse(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.LineNumbers).To(BeNil())
		Expect(out.Color).To(BeNil())
		Expect(out.TodoGlyphs).To(BeNil())
		Expect(out.TodoDefault).To(BeNil())
	})
})
