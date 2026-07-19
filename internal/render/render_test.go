package render_test

import (
	"bytes"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
)

var _ = Describe("RenderComments wrapping", func() {
	It("word-wraps a long comment body to the given width (like the todo cockpit)", func() {
		body := strings.TrimSpace(strings.Repeat("word ", 40))
		comments := []entry.Comment{{ID: "c1", Author: "A", Body: body, Time: time.Now()}}
		var b bytes.Buffer
		render.RenderComments(&b, comments, false, 40)
		lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
		for _, ln := range lines {
			Expect(utf8.RuneCountInString(ln)).To(BeNumerically("<=", 40))
		}
		Expect(len(lines)).To(BeNumerically(">", 4)) // header + several wrapped body lines
	})

	It("leaves the body verbatim when width is 0 (back-compat)", func() {
		body := strings.TrimSpace(strings.Repeat("word ", 40))
		comments := []entry.Comment{{ID: "c1", Author: "A", Body: body, Time: time.Now()}}
		var b bytes.Buffer
		render.RenderComments(&b, comments, false, 0)
		Expect(b.String()).To(ContainSubstring(body)) // one long line, unwrapped
	})
})

var _ = Describe("ListLines", func() {
	It("returns one line per entry with a parallel id slice in display order", func() {
		a := &entry.Snapshot{ID: "aaaa", Tier: "personal", TierType: "personal", Kind: "document", Status: "open", Title: "Alpha"}
		b := &entry.Snapshot{ID: "bbbb", Tier: "personal", TierType: "personal", Kind: "todo", Status: "open", Title: "Beta"}
		header, lines, ids := render.ListLines([]*entry.Snapshot{b, a}, render.ListOptions{Columns: render.DefaultColumns})
		Expect(header).To(ContainSubstring("TITLE"))
		Expect(lines).To(HaveLen(2))
		Expect(ids).To(HaveLen(2))
		// default order is tier→kind→title: document "Alpha" before todo "Beta"
		Expect(ids[0]).To(Equal(entity.Id("aaaa")))
		Expect(lines[0]).To(ContainSubstring("Alpha"))
		Expect(ids[1]).To(Equal(entity.Id("bbbb")))
	})
})

var _ = Describe("ColumnHelp", func() {
	It("lists every column with a non-empty description (guards description drift)", func() {
		help := render.ColumnHelp()
		Expect(help).To(ContainSubstring("Available columns"))
		for _, c := range render.AllColumns {
			Expect(help).To(MatchRegexp(`(?m)^\s+`+regexp.QuoteMeta(string(c))+`\s+\S`),
				"column %q must appear with a description", c)
		}
	})
})

var _ = Describe("ShortID", func() {
	It("truncates a long id to 12 chars", func() {
		id := entity.Id("fdd23cc786c4ff4b732b38773a69a55cbc70aab1") // DevSkim: ignore DS173237
		Expect(render.ShortID(id)).To(Equal("fdd23cc786c4"))
	})
	It("leaves a short id untouched", func() {
		Expect(render.ShortID(entity.Id("abc"))).To(Equal("abc"))
	})
	It("leaves an exactly-12-char id untouched", func() {
		Expect(render.ShortID(entity.Id("0123456789ab"))).To(Equal("0123456789ab"))
	})
})

var _ = Describe("Tier", func() {
	It("prefixes a glyph and omits ANSI when color is off", func() {
		out := render.Tier("private", "private", false)
		Expect(out).To(Equal("● private"))
		Expect(out).NotTo(ContainSubstring("\x1b["))
	})
	It("wraps the badge in ANSI when color is on", func() {
		out := render.Tier("private", "private", true)
		Expect(out).To(ContainSubstring("● private"))
		Expect(out).To(HavePrefix("\x1b["))
		Expect(out).To(HaveSuffix("\x1b[0m"))
	})
	It("falls back to a neutral glyph for an unknown tier type", func() {
		Expect(render.Tier("weird", "weird", false)).To(Equal("• weird"))
		Expect(render.Tier("weird", "weird", true)).NotTo(ContainSubstring("\x1b["))
	})
})

func snap(tier, kind, status, title string, deleted bool) *entry.Snapshot {
	return &entry.Snapshot{
		ID: entity.Id(strings.Repeat("a", 40)), Tier: tier, TierType: tier,
		Kind: kind, Status: status, Title: title, Deleted: deleted,
	}
}

var _ = Describe("List", func() {
	It("prints a header row and a tier column", func() {
		var b bytes.Buffer
		render.List(&b, []*entry.Snapshot{snap("shared", "spec", "open", "Auth", false)}, false, true)
		out := b.String()
		Expect(out).To(MatchRegexp(`(?m)^TIER\s+ID\s+KIND\s+STATUS\s+TITLE$`))
		Expect(out).To(ContainSubstring("○ shared"))
		Expect(out).To(ContainSubstring("Auth"))
	})
	It("sorts by tier rank then kind then title", func() {
		var b bytes.Buffer
		render.List(&b, []*entry.Snapshot{
			snap("shared", "spec", "open", "Z", false),
			snap("private", "memory", "open", "A", false),
			snap("personal", "adr", "open", "M", false),
		}, false, true)
		lines := strings.Split(strings.TrimSpace(b.String()), "\n")
		Expect(lines[1]).To(ContainSubstring("private"))
		Expect(lines[2]).To(ContainSubstring("personal"))
		Expect(lines[3]).To(ContainSubstring("shared"))
		Expect(b.String()).To(ContainSubstring("3 entries"))
	})
	It("marks deleted rows and prints a count footer", func() {
		var b bytes.Buffer
		render.List(&b, []*entry.Snapshot{snap("shared", "spec", "open", "T", true)}, false, true)
		Expect(b.String()).To(ContainSubstring("(deleted)"))
		Expect(b.String()).To(ContainSubstring("1 entry"))
	})
	It("says 'no entries' for an empty list", func() {
		var b bytes.Buffer
		render.List(&b, nil, false, true)
		Expect(strings.TrimSpace(b.String())).To(Equal("no entries"))
	})
	It("emits ANSI only when color is on", func() {
		var plain, colored bytes.Buffer
		items := []*entry.Snapshot{snap("private", "memory", "open", "T", false)}
		render.List(&plain, items, false, true)
		render.List(&colored, items, true, true)
		Expect(plain.String()).NotTo(ContainSubstring("\x1b["))
		Expect(colored.String()).To(ContainSubstring("\x1b[31m"))
	})
})

var _ = Describe("labels rendering", func() {
	It("appends labels to the list row title", func() {
		var b bytes.Buffer
		it := &entry.Snapshot{ID: entity.Id("a"), Tier: "shared", Kind: "spec", Status: "open", Title: "Auth", Labels: []string{"area:auth", "spec"}}
		render.List(&b, []*entry.Snapshot{it}, false, true)
		Expect(b.String()).To(ContainSubstring("Auth  [area:auth, spec]"))
	})
})

var _ = Describe("Show header table", func() {
	base := func() *entry.Snapshot {
		return &entry.Snapshot{
			ID:   entity.Id("fdd23cc786c4ff4b732b38773a69a55cbc70aab1"), // DevSkim: ignore DS173237
			Tier: "private", Status: "open", Title: "Secret",
			CreatedBy: "Tester", CreatedByEmail: "t@t.io", Body: "body text",
		}
	}

	It("renders id, status, title and author rows plus a rule", func() {
		var b bytes.Buffer
		render.Show(&b, base(), render.ShowOptions{Raw: true})
		out := b.String()
		Expect(out).To(ContainSubstring("fdd23cc786c4ff4b732b38773a69a55cbc70aab1")) // DevSkim: ignore DS173237
		Expect(out).To(ContainSubstring("private / open"))
		Expect(out).To(ContainSubstring("Secret"))
		Expect(out).To(ContainSubstring("Tester <t@t.io>"))
		Expect(out).To(ContainSubstring("─"))
	})

	It("with HeaderOnly renders the header and rule but omits the body", func() {
		var b bytes.Buffer
		render.Show(&b, base(), render.ShowOptions{HeaderOnly: true})
		out := b.String()
		Expect(out).To(ContainSubstring("Tester <t@t.io>")) // header present
		Expect(out).To(ContainSubstring("─"))               // rule present
		Expect(out).NotTo(ContainSubstring("body text"))    // body omitted
	})

	It("shows a Labels row when labels exist and omits it otherwise", func() {
		s := base()
		s.Labels = []string{"area:auth", "spec"}
		var b bytes.Buffer
		render.Show(&b, s, render.ShowOptions{Raw: true})
		Expect(b.String()).To(ContainSubstring("area:auth, spec"))

		var b2 bytes.Buffer
		render.Show(&b2, base(), render.ShowOptions{Raw: true})
		Expect(b2.String()).NotTo(ContainSubstring("Labels"))
	})

	It("renders one Origin row per provenance event, omitting absent source", func() {
		s := base()
		s.Provenance = []entry.OriginEvent{
			{Actor: "alice", ActorKind: "human", Trigger: "create"},
			{Actor: "claude", ActorKind: "agent", SourcePath: "docs/n.md", Trigger: "ingest"},
		}
		var b bytes.Buffer
		render.Show(&b, s, render.ShowOptions{Raw: true})
		out := b.String()
		Expect(out).To(ContainSubstring("create by alice (human)"))
		Expect(out).To(ContainSubstring("ingest by claude (agent) from docs/n.md"))
	})

	It("omits Origin rows when there is no provenance", func() {
		var b bytes.Buffer
		render.Show(&b, base(), render.ShowOptions{Raw: true})
		Expect(b.String()).NotTo(ContainSubstring("Origin"))
	})

	It("shows a Favorites row when favorites exist and omits it otherwise", func() {
		var withFav bytes.Buffer
		render.Show(&withFav, base(), render.ShowOptions{Raw: true, Favorites: []string{"todo", "spec"}})
		out := withFav.String()
		Expect(out).To(ContainSubstring("Favorites"))
		Expect(out).To(ContainSubstring("todo, spec"))

		var without bytes.Buffer
		render.Show(&without, base(), render.ShowOptions{Raw: true, Favorites: []string{}})
		Expect(without.String()).NotTo(ContainSubstring("Favorites"))
	})

	It("shows a Tracked row only when TrackedNote is set", func() {
		var withNote bytes.Buffer
		render.Show(&withNote, base(), render.ShowOptions{Raw: true, TrackedNote: "docs/n.md [clean]"})
		Expect(withNote.String()).To(ContainSubstring("docs/n.md [clean]"))

		var without bytes.Buffer
		render.Show(&without, base(), render.ShowOptions{Raw: true})
		Expect(without.String()).NotTo(ContainSubstring("Tracked"))
	})
})

var _ = Describe("Show with Raw (--plain) and comments", func() {
	It("renders comments even when Raw is true (regression: --plain was skipping them)", func() {
		s := &entry.Snapshot{
			ID:   entity.Id("fdd23cc786c4ff4b732b38773a69a55cbc70aab1"), // DevSkim: ignore DS173237
			Tier: "private", Status: "open", Title: "T",
			CreatedBy: "Tester", CreatedByEmail: "t@t.io",
			Body: "notevisible",
			Comments: []entry.Comment{
				{ID: "c1", Author: "alice", Body: "comment body here", Question: true},
			},
		}
		var b bytes.Buffer
		render.Show(&b, s, render.ShowOptions{NoHeader: true, Raw: true, Color: false})
		out := b.String()
		Expect(out).To(ContainSubstring("notevisible"), "body must appear")
		Expect(out).To(ContainSubstring("Comments (1)"), "comment header must appear in --plain mode")
		Expect(out).To(ContainSubstring("comment body here"), "comment text must appear in --plain mode")
	})
})

var _ = Describe("Action", func() {
	It("renders a one-line confirmation with verb, tier, short id, kind, title", func() {
		s := &entry.Snapshot{
			ID:   entity.Id("a5745cf9056545771011318e3c4179134ab5e624"), // DevSkim: ignore DS173237
			Tier: "shared", TierType: "shared", Kind: "spec", Title: "Auth flow spec",
		}
		var b bytes.Buffer
		render.Action(&b, "added", s, false)
		Expect(strings.TrimSpace(b.String())).To(Equal(`added ○ shared a5745cf90565  spec  "Auth flow spec"`))
	})
})

var _ = Describe("Log rendering", func() {
	It("prints one line per op with time, op, author, detail", func() {
		var b bytes.Buffer
		render.Log(&b, []entry.LogEntry{
			{Op: "set-body", Author: "alice", Time: time.Unix(0, 0).UTC(), Detail: "7 chars"},
		})
		out := b.String()
		Expect(out).To(ContainSubstring("set-body"))
		Expect(out).To(ContainSubstring("alice"))
		Expect(out).To(ContainSubstring("7 chars"))
	})
})

var _ = Describe("BodyVersions rendering", func() {
	It("prints each version with an author/time header and the body", func() {
		var b bytes.Buffer
		render.BodyVersions(&b, []entry.BodyVersion{
			{Author: "alice", Time: time.Unix(0, 0).UTC(), Body: "first"},
			{Author: "claude", Time: time.Unix(0, 0).UTC(), Body: "second"},
		})
		out := b.String()
		Expect(out).To(ContainSubstring("=== version 1 — alice @"))
		Expect(out).To(ContainSubstring("first"))
		Expect(out).To(ContainSubstring("=== version 2 — claude @"))
		Expect(out).To(ContainSubstring("second"))
	})
})

var _ = Describe("VersionDiff rendering", func() {
	versions := []entry.BodyVersion{
		{Author: "alice", Time: time.Unix(0, 0).UTC(), Body: "alpha\nbeta\n"},
		{Author: "claude", Time: time.Unix(0, 0).UTC(), Body: "alpha\ngamma\n"},
	}

	It("renders v1 as an all-added diff from nothing", func() {
		var b bytes.Buffer
		render.VersionDiff(&b, versions, 0, 1, false)
		out := b.String()
		Expect(out).To(ContainSubstring("=== v1 — alice @"))
		Expect(out).To(ContainSubstring("+9/-0 chars, +2/-0 lines"))
		Expect(out).To(ContainSubstring("+ alpha"))
		Expect(out).To(ContainSubstring("+ beta"))
	})

	It("renders a version pair with +/- markers and context", func() {
		var b bytes.Buffer
		render.VersionDiff(&b, versions, 1, 2, false)
		out := b.String()
		Expect(out).To(ContainSubstring("=== v1 → v2 — claude @"))
		Expect(out).To(ContainSubstring("+5/-4 chars, +1/-1 lines"))
		Expect(out).To(ContainSubstring("  alpha")) // unchanged context, no marker
		Expect(out).To(ContainSubstring("- beta"))
		Expect(out).To(ContainSubstring("+ gamma"))
	})

	It("colors added green and removed red when color is on", func() {
		var b bytes.Buffer
		render.VersionDiff(&b, versions, 1, 2, true)
		out := b.String()
		Expect(out).To(ContainSubstring("\x1b[32m+ gamma\x1b[0m"))
		Expect(out).To(ContainSubstring("\x1b[31m- beta\x1b[0m"))
	})

	It("DiffChain walks every consecutive pair from v1", func() {
		var b bytes.Buffer
		render.DiffChain(&b, versions, false)
		out := b.String()
		Expect(out).To(ContainSubstring("=== v1 — alice @"))
		Expect(out).To(ContainSubstring("=== v1 → v2 — claude @"))
	})
})

var _ = Describe("merged marker", func() {
	It("shows a merged marker in show and list when set", func() {
		var sb, lb bytes.Buffer
		s := &entry.Snapshot{ID: entity.Id("a"), Tier: "shared", Status: "open", Title: "T", Merged: true}
		render.Show(&sb, s, render.ShowOptions{Raw: true})
		Expect(sb.String()).To(ContainSubstring("◆ merged"))
		Expect(sb.String()).To(ContainSubstring("clear with `kref resolve`"))
		render.List(&lb, []*entry.Snapshot{s}, false, true)
		Expect(lb.String()).To(ContainSubstring("◆ merged"))
	})
	It("omits the marker when not merged", func() {
		var sb, lb bytes.Buffer
		s := &entry.Snapshot{ID: entity.Id("a"), Tier: "shared", Status: "open", Title: "T"}
		render.Show(&sb, s, render.ShowOptions{Raw: true})
		Expect(sb.String()).NotTo(ContainSubstring("◆ merged"))
		render.List(&lb, []*entry.Snapshot{s}, false, true)
		Expect(lb.String()).NotTo(ContainSubstring("◆ merged"))
	})
})

var _ = Describe("List clean view", func() {
	It("collapses duplicate normalized titles into one (×N) row when showAll is false", func() {
		var b bytes.Buffer
		items := []*entry.Snapshot{
			{ID: entity.Id("aaa1"), Tier: "shared", Kind: "note", Status: "open", Title: "Auth Design"},
			{ID: entity.Id("bbb2"), Tier: "shared", Kind: "note", Status: "open", Title: "auth   design"},
		}
		render.List(&b, items, false, false)
		out := b.String()
		Expect(out).To(ContainSubstring("(×2)"))
		Expect(out).To(ContainSubstring("1 entry"))
	})
	It("hides superseded entries when showAll is false but shows them when true", func() {
		items := []*entry.Snapshot{
			{ID: entity.Id("aaa1"), Tier: "shared", Kind: "note", Status: "superseded", Title: "Gone"},
			{ID: entity.Id("bbb2"), Tier: "shared", Kind: "note", Status: "open", Title: "Here"},
		}
		var clean bytes.Buffer
		render.List(&clean, items, false, false)
		Expect(clean.String()).NotTo(ContainSubstring("Gone"))
		Expect(clean.String()).To(ContainSubstring("Here"))

		var all bytes.Buffer
		render.List(&all, items, false, true)
		Expect(all.String()).To(ContainSubstring("Gone"))
	})
	It("shows the most-recently-updated entry as the collapsed group's representative", func() {
		older := &entry.Snapshot{ID: entity.Id("aaa1older"), Tier: "shared", Kind: "note", Status: "open", Title: "Dup", UpdatedAt: time.Unix(100, 0)}
		newer := &entry.Snapshot{ID: entity.Id("bbb2newer"), Tier: "shared", Kind: "note", Status: "open", Title: "dup", UpdatedAt: time.Unix(200, 0)}
		var b bytes.Buffer
		render.List(&b, []*entry.Snapshot{older, newer}, false, false)
		out := b.String()
		Expect(out).To(ContainSubstring("(×2)"))
		Expect(out).To(ContainSubstring(render.ShortID(newer.ID)))
		Expect(out).NotTo(ContainSubstring(render.ShortID(older.ID)))
	})
})

var _ = Describe("RenderList columns + plain", func() {
	items := []*entry.Snapshot{
		{ID: "aaaaaaaaaaaa1111", Kind: "spec", Status: "open", Tier: string(entry.TierShared),
			Title: "Alpha", CreatedBy: "Jane Roe", CreatedByEmail: "jane@x.com",
			UpdatedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)},
	}

	It("ParseColumns rejects an unknown token", func() {
		_, err := render.ParseColumns("id,bogus,title")
		Expect(err).To(MatchError(ContainSubstring("bogus")))
	})

	It("plain mode is TSV with no header/footer/color and a bare tier word", func() {
		var b bytes.Buffer
		cols, _ := render.ParseColumns("tier,id,author")
		render.RenderList(&b, items, render.ListOptions{Columns: cols, Plain: true})
		Expect(b.String()).To(Equal("shared\taaaaaaaaaaaa\tJane Roe\n"))
	})

	It("table mode prints headers for selected columns", func() {
		var b bytes.Buffer
		cols, _ := render.ParseColumns("id,author,updated,title")
		render.RenderList(&b, items, render.ListOptions{Columns: cols})
		out := b.String()
		Expect(out).To(ContainSubstring("ID"))
		Expect(out).To(ContainSubstring("AUTHOR"))
		Expect(out).To(ContainSubstring("UPDATED"))
		Expect(out).To(ContainSubstring("Jane Roe"))
		Expect(out).To(ContainSubstring("2026-06-27"))
	})

	It("WideColumns includes author and edited", func() {
		Expect(render.WideColumns).To(ContainElement(render.ColAuthor))
		Expect(render.WideColumns).To(ContainElement(render.ColEdited))
	})
})

var _ = Describe("RenderList column registry consistency", func() {
	sentinel := &entry.Snapshot{
		ID: "0123456789abcdef0000", Kind: "KIND_SENT", Status: "STATUS_SENT",
		Tier: string(entry.TierShared), Title: "TITLE_SENT",
		Labels: []string{"l1", "l2"}, Tracked: true, TrackedPath: "PATH_SENT",
		CreatedBy: "AUTHOR_SENT", CreatedByEmail: "EMAIL_SENT",
		CreatedAt:  time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
		EditedAt:   time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC),
		Provenance: []entry.OriginEvent{{Trigger: "ingest", SourcePath: "SOURCE_SENT"}},
	}
	expected := map[render.Column]string{
		render.ColTier: "shared", render.ColID: "0123456789ab", render.ColFullID: "0123456789abcdef0000",
		render.ColKind: "KIND_SENT", render.ColStatus: "STATUS_SENT", render.ColTitle: "TITLE_SENT",
		render.ColAuthor: "AUTHOR_SENT", render.ColEmail: "EMAIL_SENT", render.ColCreated: "2026-01-02",
		render.ColUpdated: "2026-03-04", render.ColEdited: "2026-05-06", render.ColLabels: "l1, l2", render.ColTracked: "yes",
		render.ColPath: "PATH_SENT", render.ColSource: "SOURCE_SENT",
	}

	It("every column in AllColumns parses and has a header", func() {
		for _, c := range render.AllColumns {
			cols, err := render.ParseColumns(string(c))
			Expect(err).NotTo(HaveOccurred(), "column %q must parse", c)
			Expect(cols).To(Equal([]render.Column{c}))
			Expect(render.HeaderFor(c)).NotTo(BeEmpty(), "column %q must have a header", c)
		}
	})

	It("the expected-cell map covers exactly AllColumns", func() {
		Expect(expected).To(HaveLen(len(render.AllColumns)))
		for _, c := range render.AllColumns {
			_, ok := expected[c]
			Expect(ok).To(BeTrue(), "column %q missing from expected map", c)
		}
	})

	It("each column renders its own field (catches field-swap regressions)", func() {
		for _, c := range render.AllColumns {
			var b bytes.Buffer
			render.RenderList(&b, []*entry.Snapshot{sentinel}, render.ListOptions{Columns: []render.Column{c}, Plain: true})
			Expect(strings.TrimRight(b.String(), "\n")).To(Equal(expected[c]), "wrong field for column %q", c)
		}
	})

	It("DefaultColumns and WideColumns contain only valid columns", func() {
		valid := map[render.Column]bool{}
		for _, c := range render.AllColumns {
			valid[c] = true
		}
		for _, c := range append(append([]render.Column{}, render.DefaultColumns...), render.WideColumns...) {
			Expect(valid[c]).To(BeTrue(), "preset references unknown column %q", c)
		}
	})
})

var _ = Describe("archived marker", func() {
	It("tags an archived entry's title with (archived) in the table", func() {
		var b bytes.Buffer
		it := &entry.Snapshot{ID: "a", Tier: string(entry.TierShared), Kind: "spec", Status: "open", Title: "T", Archived: true}
		render.RenderList(&b, []*entry.Snapshot{it}, render.ListOptions{})
		Expect(b.String()).To(ContainSubstring("(archived)"))
	})
})

var _ = Describe("RenderBody", func() {
	It("renders markdown (no raw '# ' marker) in plain mode", func() {
		var b bytes.Buffer
		render.RenderBody(&b, "# Heading\n\nbody text", "text/markdown", false, 0)
		out := b.String()
		Expect(out).To(ContainSubstring("body text"))
		Expect(out).NotTo(ContainSubstring("# "))    // rendered, not raw markdown source
		Expect(out).NotTo(ContainSubstring("\x1b[")) // no ANSI when color is off
	})

	It("emits code verbatim in plain mode (no ANSI)", func() {
		var b bytes.Buffer
		render.RenderBody(&b, "package main", "text/x-go", false, 0)
		Expect(b.String()).To(ContainSubstring("package main"))
		Expect(b.String()).NotTo(ContainSubstring("\x1b["))
	})

	It("emits unknown/plain content verbatim", func() {
		var b bytes.Buffer
		render.RenderBody(&b, "just text", "text/plain", false, 0)
		Expect(b.String()).To(Equal("just text\n"))
	})

	It("emits ANSI for markdown when color is on (the KREF_COLOR=1 pipe contract)", func() {
		var b bytes.Buffer
		render.RenderBody(&b, "# Heading\n\nsome **bold** prose", "text/markdown", true, 80)
		Expect(b.String()).To(ContainSubstring("\x1b["))
	})
})

var _ = Describe("RenderBody width", func() {
	It("wraps markdown to the given width", func() {
		body := strings.Repeat("lorem ipsum ", 30)
		var b bytes.Buffer
		render.RenderBody(&b, body, "text/markdown", false, 20)
		out := strings.TrimRight(b.String(), "\n")
		lines := strings.Split(out, "\n")
		Expect(len(lines)).To(BeNumerically(">", 1))
		for _, ln := range lines {
			Expect(utf8.RuneCountInString(ln)).To(BeNumerically("<=", 20))
		}
	})

	It("does not hard-wrap when width is zero", func() {
		body := strings.Repeat("lorem ipsum ", 30)
		var b bytes.Buffer
		render.RenderBody(&b, body, "text/markdown", false, 0)
		Expect(strings.TrimSpace(b.String())).NotTo(ContainSubstring("\n"))
	})

	It("reflows a hard-wrapped bullet to the display width", func() {
		body := "- a bullet that was wrapped\n  tightly across several\n  source lines by an LLM\n"
		var b bytes.Buffer
		render.RenderBody(&b, body, "text/markdown", false, 100)
		Expect(b.String()).To(ContainSubstring(
			"a bullet that was wrapped tightly across several source lines by an LLM"))
	})

	It("joins wrapped paragraph lines even at width zero (the pipe path)", func() {
		var b bytes.Buffer
		render.RenderBody(&b, "one two\nthree four\n", "text/markdown", false, 0)
		Expect(b.String()).To(ContainSubstring("one two three four"))
	})
})

var _ = Describe("custom tier rendering", func() {
	It("renders a custom tier with its type's glyph and the tier's own name", func() {
		Expect(render.Tier("research", "personal", false)).To(Equal("◐ research"))
		Expect(render.Tier("team-x", "shared", false)).To(Equal("○ team-x"))
		Expect(render.Tier("private", "private", false)).To(Equal("● private"))
	})

	It("orders list rows by type rank, builtin-first, then name", func() {
		mk := func(name, typ string) *entry.Snapshot {
			return &entry.Snapshot{ID: entity.Id(name + "0000000000000000"), Title: name, Tier: name, TierType: typ}
		}
		items := []*entry.Snapshot{
			mk("team-x", "shared"),
			mk("shared", "shared"),
			mk("research", "personal"),
			mk("personal", "personal"),
			mk("private", "private"),
		}
		var buf bytes.Buffer
		render.List(&buf, items, false, false)
		out := buf.String()
		Expect(strings.Index(out, "● private")).To(BeNumerically("<", strings.Index(out, "◐ personal")))
		Expect(strings.Index(out, "◐ personal")).To(BeNumerically("<", strings.Index(out, "◐ research")))
		Expect(strings.Index(out, "◐ research")).To(BeNumerically("<", strings.Index(out, "○ shared")))
		Expect(strings.Index(out, "○ shared")).To(BeNumerically("<", strings.Index(out, "○ team-x")))
	})
})

var _ = Describe("Show options", func() {
	snap := &entry.Snapshot{
		ID: "abc", Kind: "spec", Title: "T", Status: "open", Tier: "shared",
		Body: "# Heading\n\nprose", ContentType: "text/markdown",
		CreatedBy: "Tester", CreatedByEmail: "t@example.com",
	}

	It("with Raw emits the body verbatim and still shows the header", func() {
		var b bytes.Buffer
		render.Show(&b, snap, render.ShowOptions{Raw: true})
		Expect(b.String()).To(ContainSubstring("# Heading\n\nprose"))
		Expect(b.String()).To(ContainSubstring("Tester <t@example.com>"))
	})

	It("with NoHeader omits the metadata block", func() {
		var b bytes.Buffer
		render.Show(&b, snap, render.ShowOptions{NoHeader: true, Raw: true})
		Expect(b.String()).NotTo(ContainSubstring("by Tester"))
		Expect(b.String()).To(ContainSubstring("# Heading"))
	})
})

var _ = Describe("ParseSort date defaults", func() {
	It("defaults bare date keys to newest-first (descending)", func() {
		for _, key := range []string{"created", "updated"} {
			spec, err := render.ParseSort(key)
			Expect(err).NotTo(HaveOccurred())
			Expect(spec.Desc).To(BeTrue(), key)
		}
	})

	It("keeps bare non-date keys ascending and honors explicit directions", func() {
		spec, err := render.ParseSort("title")
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.Desc).To(BeFalse())

		spec, err = render.ParseSort("updated:asc")
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.Desc).To(BeFalse())

		spec, err = render.ParseSort("created:desc")
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.Desc).To(BeTrue())
	})
})

var _ = Describe("PlainSearchResults", func() {
	It("emits one TSV row per hit — matches, tier, id, kind, title — no chrome", func() {
		snap := &entry.Snapshot{
			ID:   entity.Id("fdd23cc786c4ff4b732b38773a69a55cbc70aab1"), // DevSkim: ignore DS173237
			Kind: "spec", Title: "Auth flow", Tier: "shared", TierType: "shared",
		}
		var b bytes.Buffer
		render.PlainSearchResults(&b, []render.SearchHit{{Snap: snap, Matches: 3}})
		Expect(b.String()).To(Equal("3\tshared\tfdd23cc786c4\tspec\tAuth flow\n"))
	})
	It("emits nothing for no hits", func() {
		var b bytes.Buffer
		render.PlainSearchResults(&b, nil)
		Expect(b.String()).To(Equal(""))
	})
})

var _ = Describe("edited column and sort key", func() {
	It("registers ColEdited in AllColumns and sortableColumns", func() {
		Expect(render.AllColumns).To(ContainElement(render.ColEdited))
		Expect(render.SortKeys()).To(ContainElement("edited"))
	})

	It("has a header and a description (registry consistency)", func() {
		Expect(render.HeaderFor(render.ColEdited)).To(Equal("EDITED"))
		Expect(render.ColumnDescription(render.ColEdited)).NotTo(BeEmpty())
	})

	It("defaults a bare --sort edited to descending (newest first)", func() {
		spec, err := render.ParseSort("edited")
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.Key).To(Equal(render.ColEdited))
		Expect(spec.Desc).To(BeTrue())
	})

	It("orders a list by EditedAt when sorted on edited", func() {
		older := &entry.Snapshot{Title: "older", EditedAt: time.Unix(1000, 0)}
		newer := &entry.Snapshot{Title: "newer", EditedAt: time.Unix(2000, 0)}
		// The list command sorts snapshots before handing them to the plain
		// renderer (commands.go), so exercise the same ordering path here.
		items := []*entry.Snapshot{older, newer}
		render.SortSnapshots(items, &render.SortSpec{Key: render.ColEdited, Desc: true}, nil)
		var b bytes.Buffer
		render.RenderList(&b, items, render.ListOptions{
			Columns: []render.Column{render.ColTitle, render.ColEdited},
			Plain:   true,
		})
		out := b.String()
		Expect(strings.Index(out, "newer")).To(BeNumerically("<", strings.Index(out, "older")))
	})
})

var _ = Describe("wide view shows edited", func() {
	It("includes edited and not updated in WideColumns", func() {
		Expect(render.WideColumns).To(ContainElement(render.ColEdited))
		Expect(render.WideColumns).NotTo(ContainElement(render.ColUpdated))
	})
})

var _ = Describe("favorites pinned to top", func() {
	mk := func(id, title string) *entry.Snapshot {
		return &entry.Snapshot{ID: entity.Id(id), Tier: "shared", Kind: "note", Status: "open", Title: title}
	}

	It("floats favorited snapshots above the secondary sort", func() {
		apple := mk("aaa", "Apple")
		banana := mk("bbb", "Banana")
		cherry := mk("ccc", "Cherry")
		items := []*entry.Snapshot{apple, banana, cherry}
		render.SortSnapshots(items, &render.SortSpec{Key: render.ColTitle}, map[string]bool{"ccc": true})
		Expect(items[0].Title).To(Equal("Cherry"), "favorite pins to the top")
		Expect(items[1].Title).To(Equal("Apple"), "non-favorites keep the secondary order")
		Expect(items[2].Title).To(Equal("Banana"))
	})

	It("keeps the secondary order among multiple favorites", func() {
		apple := mk("aaa", "Apple")
		banana := mk("bbb", "Banana")
		cherry := mk("ccc", "Cherry")
		items := []*entry.Snapshot{cherry, apple, banana}
		render.SortSnapshots(items, &render.SortSpec{Key: render.ColTitle}, map[string]bool{"aaa": true, "ccc": true})
		Expect(items[0].Title).To(Equal("Apple"), "favorites sorted among themselves")
		Expect(items[1].Title).To(Equal("Cherry"))
		Expect(items[2].Title).To(Equal("Banana"), "non-favorite last")
	})

	It("is a no-op when no favorites are given", func() {
		apple := mk("aaa", "Apple")
		zebra := mk("zzz", "Zebra")
		items := []*entry.Snapshot{zebra, apple}
		render.SortSnapshots(items, nil, nil)
		Expect(items[0].Title).To(Equal("Zebra"), "nil spec + nil favorites preserves store order")
		Expect(items[1].Title).To(Equal("Apple"))
	})

	It("pins a favorite to the top of the aligned table under an explicit sort", func() {
		apple := mk("aaa", "Apple")
		zebra := mk("zzz", "Zebra")
		var b bytes.Buffer
		render.RenderList(&b, []*entry.Snapshot{apple, zebra}, render.ListOptions{
			Columns:   []render.Column{render.ColTitle},
			Sort:      &render.SortSpec{Key: render.ColTitle},
			Favorites: map[string]bool{"zzz": true},
		})
		out := b.String()
		Expect(strings.Index(out, "Zebra")).To(BeNumerically("<", strings.Index(out, "Apple")),
			"favorite Zebra pins above Apple despite the title sort")
	})
})

// showNoHeader renders snap via Show with NoHeader:true, Color:false, Width:80
// and returns the output string.
func showNoHeader(snap *entry.Snapshot) string {
	var b bytes.Buffer
	render.Show(&b, snap, render.ShowOptions{NoHeader: true, Color: false, Width: 80})
	return b.String()
}

var _ = Describe("Show comments section", func() {
	baseSnap := func(comments []entry.Comment) *entry.Snapshot {
		return &entry.Snapshot{
			ID:          "abc",
			Kind:        "spec",
			Title:       "T",
			Status:      "open",
			Tier:        "shared",
			TierType:    "shared",
			Body:        "body",
			ContentType: "text/plain",
			Comments:    comments,
		}
	}
	t0 := time.Unix(1_700_000_000, 0)

	It("omits the Comments section when there are no comments", func() {
		out := showNoHeader(baseSnap(nil))
		Expect(out).NotTo(ContainSubstring("Comments"))
	})

	It("renders a threaded block with open question, reply, and resolved question", func() {
		comments := []entry.Comment{
			{
				ID:       "aaaa1111",
				Author:   "Alice",
				Body:     "is this correct?",
				Time:     t0,
				Question: true,
			},
			{
				ID:      "bbbb2222",
				Author:  "Bob",
				Body:    "because the spec says so",
				Time:    t0,
				ReplyTo: "aaaa1111",
			},
			{
				ID:         "cccc3333",
				Author:     "Carol",
				Body:       "resolved question",
				Time:       t0,
				Question:   true,
				Resolved:   true,
				ResolvedBy: "Trevor",
			},
		}
		out := showNoHeader(baseSnap(comments))
		Expect(out).To(ContainSubstring("Comments (3)"))
		Expect(out).To(ContainSubstring("◉"))
		Expect(out).To(ContainSubstring("✓"))
		Expect(out).To(ContainSubstring("because"))
		Expect(out).To(ContainSubstring("resolved by Trevor"))
	})

	It("renders a dangling reply-to at top level", func() {
		comments := []entry.Comment{
			{
				ID:      "dddd4444",
				Author:  "Dave",
				Body:    "dangling reply body",
				Time:    t0,
				ReplyTo: "nonexistent",
			},
		}
		out := showNoHeader(baseSnap(comments))
		Expect(out).To(ContainSubstring("dangling reply body"))
	})
})

var _ = Describe("RenderComments edited/deleted", func() {
	It("renders deleted and edited comments", func() {
		var buf bytes.Buffer
		comments := []entry.Comment{
			{ID: "a", Author: "ada", Body: "gone", Deleted: true},
			{ID: "b", Author: "bob", Body: "fixed", Edited: true},
		}
		render.RenderComments(&buf, comments, false, 0)
		out := buf.String()
		Expect(out).To(ContainSubstring("[deleted]"))
		Expect(out).NotTo(ContainSubstring("gone"))
		Expect(out).To(ContainSubstring("edited"))
		Expect(out).To(ContainSubstring("fixed"))
	})
})

var _ = Describe("RenderCommentThreads", func() {
	It("returns one group per root and nests replies under it", func() {
		comments := []entry.Comment{
			{ID: "a", Author: "ada", Body: "root one"},
			{ID: "b", Author: "bob", Body: "reply", ReplyTo: "a"},
			{ID: "c", Author: "cid", Body: "root two"},
		}
		ts := render.RenderCommentThreads(comments, false, nil, 0)
		Expect(ts).To(HaveLen(2))
		Expect(ts[0].RootID).To(Equal("a"))
		Expect(strings.Join(ts[0].Lines, "\n")).To(ContainSubstring("root one"))
		Expect(strings.Join(ts[0].Lines, "\n")).To(ContainSubstring("reply"))
		Expect(ts[1].RootID).To(Equal("c"))
	})

	It("collapses a node: keeps its head+body, hides replies behind a hint", func() {
		comments := []entry.Comment{
			{ID: "q", Author: "ada", Body: "MCP tiers?", Question: true, Resolved: true, ResolvedBy: "bob"},
			{ID: "r", Author: "cid", Body: "the answer", ReplyTo: "q"},
		}
		ts := render.RenderCommentThreads(comments, false, map[string]bool{"q": true}, 0)
		Expect(ts).To(HaveLen(1))
		joined := strings.Join(ts[0].Lines, "\n")
		Expect(joined).To(ContainSubstring("✓"))             // resolved glyph on the head
		Expect(joined).To(ContainSubstring("MCP tiers?"))    // body kept
		Expect(joined).To(ContainSubstring("▸ 1 reply"))     // collapsed hint
		Expect(joined).NotTo(ContainSubstring("the answer")) // reply hidden
		Expect(ts[0].Nodes).To(HaveLen(1))                   // only the collapsed node
	})
})

var _ = Describe("RenderComments wrapper equivalence", func() {
	It("RenderComments equals RenderCommentsCollapsed with a nil set", func() {
		comments := []entry.Comment{
			{ID: "a", Author: "ada", Body: "one\ntwo"},
			{ID: "b", Author: "bob", Body: "reply", ReplyTo: "a"},
			{ID: "d", Author: "eve", Body: "gone", Deleted: true},
		}
		var plain, collapsed bytes.Buffer
		render.RenderComments(&plain, comments, false, 0)
		render.RenderCommentsCollapsed(&collapsed, comments, false, nil, 0)
		Expect(collapsed.String()).To(Equal(plain.String()))
	})
})

var _ = Describe("RenderCommentThreads nodes", func() {
	It("exposes root then replies as nodes with depth and ids", func() {
		comments := []entry.Comment{
			{ID: "a", Author: "ada", Body: "root"},
			{ID: "b", Author: "bob", Body: "reply", ReplyTo: "a"},
		}
		ts := render.RenderCommentThreads(comments, false, nil, 0)
		Expect(ts).To(HaveLen(1))
		Expect(ts[0].Nodes).To(HaveLen(2))
		Expect(ts[0].Nodes[0].ID).To(Equal("a"))
		Expect(ts[0].Nodes[0].Depth).To(Equal(0))
		Expect(ts[0].Nodes[1].ID).To(Equal("b"))
		Expect(ts[0].Nodes[1].Depth).To(Equal(1))
		var joined []string
		for _, n := range ts[0].Nodes {
			joined = append(joined, n.Lines...)
		}
		Expect(ts[0].Lines).To(Equal(joined))
	})

	It("yields a single node for a collapsed root, hiding its replies", func() {
		comments := []entry.Comment{
			{ID: "q", Author: "ada", Body: "q?", Question: true, Resolved: true},
			{ID: "r", Author: "bob", Body: "hidden-child-body", ReplyTo: "q"},
		}
		ts := render.RenderCommentThreads(comments, false, map[string]bool{"q": true}, 0)
		Expect(ts[0].Nodes).To(HaveLen(1))
		Expect(ts[0].Nodes[0].ID).To(Equal("q"))
		Expect(strings.Join(ts[0].Nodes[0].Lines, "\n")).NotTo(ContainSubstring("hidden-child-body"))
	})

	It("word-wraps a long comment body to the given width, preserving the words", func() {
		long := "the quick brown fox jumps over the lazy dog again and again and again"
		comments := []entry.Comment{{ID: "a", Author: "ada", Body: long}}
		ts := render.RenderCommentThreads(comments, false, nil, 24)
		bodyLines := ts[0].Nodes[0].Lines[1:] // skip the head line
		Expect(len(bodyLines)).To(BeNumerically(">", 1))
		var words []string
		for _, ln := range bodyLines {
			Expect(len([]rune(ln))).To(BeNumerically("<=", 24)) // fits the width
			words = append(words, strings.Fields(ln)...)
		}
		Expect(words).To(Equal(strings.Fields(long))) // no words lost or reordered
	})
})
