package bridge

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/store"
)

var _ = Describe("Ingest", func() {
	It("ingests clean content into the requested tier", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		clean := filepath.Join(dir, "spec.md")
		Expect(os.WriteFile(clean, []byte("# Auth design\nplain prose\n"), 0o644)).To(Succeed())

		res, err := Ingest(s, clean, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Tier).To(Equal("shared"))
		Expect(res.Title).To(Equal("Auth design"))
		Expect(res.Quarantined).To(BeFalse())
		Expect(res.Action).To(Equal("created"))
		Expect(res.Path).To(Equal(clean))
	})

	It("quarantines secret content to the private tier", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		leaky := filepath.Join(dir, "notes.md")
		Expect(os.WriteFile(leaky, []byte("# Notes\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"), 0o644)).To(Succeed())

		res, err := Ingest(s, leaky, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Quarantined).To(BeTrue())
		Expect(res.Tier).To(Equal("private"))
		Expect(res.Action).To(Equal("quarantined"))
		Expect(res.Path).To(Equal(leaky))
	})
})

var _ = Describe("Ingest kind", func() {
	It("sets the kind on create and re-kinds on a later ingest", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		p := filepath.Join(dir, "n.md")
		Expect(os.WriteFile(p, []byte("# Note\nbody\n"), 0o644)).To(Succeed())

		r1, err := Ingest(s, p, entry.TierShared, "spec", "T", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r1.Action).To(Equal("created"))
		snap, err := s.Get(r1.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Kind).To(Equal("spec"))

		// Same content, different kind → updated + re-kinded.
		r2, err := Ingest(s, p, entry.TierShared, "adr", "T", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r2.Action).To(Equal("updated"))
		snap, err = s.Get(r2.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Kind).To(Equal("adr"))
	})

	It("defaults an unspecified kind to document on create", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		p := filepath.Join(dir, "n.md")
		Expect(os.WriteFile(p, []byte("# Note\nbody\n"), 0o644)).To(Succeed())

		r, err := Ingest(s, p, entry.TierShared, "", "T", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Action).To(Equal("created"))
		snap, err := s.Get(r.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Kind).To(Equal("document"))
	})

	It("leaves a deliberately-set kind untouched when re-ingested without a kind", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		p := filepath.Join(dir, "n.md")
		Expect(os.WriteFile(p, []byte("# Note\nbody\n"), 0o644)).To(Succeed())

		r1, err := Ingest(s, p, entry.TierShared, "spec", "T", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r1.Action).To(Equal("created"))
		snap, err := s.Get(r1.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Kind).To(Equal("spec"))

		// Re-ingest the SAME file with no kind (as the kind-less post-commit
		// hook does): the stored kind must STAY "spec", not revert to "document".
		r2, err := Ingest(s, p, entry.TierShared, "", "T", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r2.Action).To(Equal("unchanged"))
		snap, err = s.Get(r2.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Kind).To(Equal("spec"))
	})
})

var _ = Describe("Ingest idempotency", func() {
	newStore := func() (*store.Store, string) {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		return s, dir
	}

	It("stamps a marker on first ingest and reuses it on the second", func() {
		s, dir := newStore()
		p := filepath.Join(dir, "plan.md")
		Expect(os.WriteFile(p, []byte("# Plan\nv1\n"), 0o644)).To(Succeed())

		r1, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r1.Action).To(Equal("created"))

		after, _ := os.ReadFile(p)
		Expect(string(after)).To(ContainSubstring("kref-id: " + r1.ID.String()))

		// Change the H1 too, so an updated entry's new title is reflected.
		Expect(os.WriteFile(p, withMarker([]byte("# Plan v2\nv2\n"), r1.ID.String()), 0o644)).To(Succeed())
		r2, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r2.ID).To(Equal(r1.ID))
		Expect(r2.Action).To(Equal("updated"))
		Expect(r2.Title).To(Equal("Plan v2"))

		got, err := s.Get(r1.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Body).To(Equal("# Plan v2\nv2"))
		Expect(got.Title).To(Equal("Plan v2"))
	})

	It("is a no-op when an already-ingested file is unchanged", func() {
		s, dir := newStore()
		p := filepath.Join(dir, "plan.md")
		Expect(os.WriteFile(p, []byte("# Plan\nv1\n"), 0o644)).To(Succeed())
		r1, _ := Ingest(s, p, entry.TierShared, "", "tester", "human")
		r2, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r2.ID).To(Equal(r1.ID))
		Expect(r2.Action).To(Equal("unchanged"))
		// Anti-spam: an unchanged re-ingest must NOT append a provenance event.
		snap, err := s.Get(r1.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Provenance).To(HaveLen(1))
	})

	It("fails closed when a secret appears in an already-mapped file", func() {
		s, dir := newStore()
		p := filepath.Join(dir, "plan.md")
		Expect(os.WriteFile(p, []byte("# Plan\nv1\n"), 0o644)).To(Succeed())
		r1, _ := Ingest(s, p, entry.TierShared, "", "tester", "human")
		leaky := withMarker([]byte("# Plan\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"), r1.ID.String())
		Expect(os.WriteFile(p, leaky, 0o644)).To(Succeed())
		_, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).To(MatchError(ContainSubstring("secret")))
	})

	It("re-ingests an unchanged file already quarantined to private as a no-op", func() {
		s, dir := newStore()
		p := filepath.Join(dir, "token.md")
		Expect(os.WriteFile(p, []byte("# Token\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"), 0o644)).To(Succeed())

		// First ingest quarantines the secret to private and stamps a marker.
		r1, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r1.Action).To(Equal("quarantined"))
		Expect(r1.Tier).To(Equal("private"))

		// A second run of the same (now-marked, still-private) file must not fail
		// closed: private cannot push, so nothing would leave the machine.
		r2, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r2.ID).To(Equal(r1.ID))
		Expect(r2.Action).To(Equal("unchanged"))
		// Anti-spam: an unchanged re-ingest must not append a provenance event.
		snap, err := s.Get(r1.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Provenance).To(HaveLen(1))
	})

	It("updates a still-private quarantined entry when its body changes", func() {
		s, dir := newStore()
		p := filepath.Join(dir, "token.md")
		Expect(os.WriteFile(p, []byte("# Token\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"), 0o644)).To(Succeed())
		r1, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r1.Action).To(Equal("quarantined"))

		// Edit the body (still secret-bearing) and re-ingest: a private entry can
		// safely take the update because it never pushes.
		edited := withMarker([]byte("# Token rotated soon\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"), r1.ID.String())
		Expect(os.WriteFile(p, edited, 0o644)).To(Succeed())
		r2, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(r2.Action).To(Equal("updated"))
		Expect(r2.Tier).To(Equal("private"))
		snap, err := s.Get(r1.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Title).To(Equal("Token rotated soon"))
	})
})

var _ = Describe("IngestPaths", func() {
	It("walks a directory for *.md and skips missing paths when asked", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		sub := filepath.Join(dir, "specs")
		Expect(os.MkdirAll(sub, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(sub, "a.md"), []byte("# A\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(sub, "b.txt"), []byte("not md\n"), 0o644)).To(Succeed())

		results, err := IngestPaths(s, []string{sub, filepath.Join(dir, "nope")}, entry.TierPersonal, "", true, "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1)) // only a.md; b.txt skipped, missing dir skipped
		Expect(results[0].Title).To(Equal("A"))
	})

	It("errors on a missing path when skipMissing is false", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = IngestPaths(s, []string{filepath.Join(dir, "nope.md")}, entry.TierPersonal, "", false, "tester", "human")
		Expect(err).To(HaveOccurred())
	})

	It("records a per-file failure as an error result without aborting the batch", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		sub := filepath.Join(dir, "specs")
		Expect(os.MkdirAll(sub, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(sub, "ok.md"), []byte("# OK\n"), 0o644)).To(Succeed())
		marked := withMarker([]byte("# Bad\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"),
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef") // DevSkim: ignore DS173237
		Expect(os.WriteFile(filepath.Join(sub, "bad.md"), marked, 0o644)).To(Succeed())

		results, err := IngestPaths(s, []string{sub}, entry.TierPersonal, "", false, "tester", "human")
		Expect(err).NotTo(HaveOccurred()) // batch does not abort
		actions := map[string]int{}
		for _, r := range results {
			actions[r.Action]++
		}
		Expect(actions["created"]).To(Equal(1)) // ok.md
		Expect(actions["error"]).To(Equal(1))   // bad.md, secret fail-closed
	})
})

var _ = Describe("EnsureKrefIgnored", func() {
	It("ignores .kref/ via .git/info/exclude, not a committed .gitignore", func() {
		dir := gitRepo()
		Expect(EnsureKrefIgnored(dir)).To(Succeed())

		exclude, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(exclude)).To(ContainSubstring(".kref/"))

		_, statErr := os.Stat(filepath.Join(dir, ".gitignore"))
		Expect(os.IsNotExist(statErr)).To(BeTrue(), "must not create a committed .gitignore")
	})

	It("is idempotent (one .kref/ entry after repeated calls)", func() {
		dir := gitRepo()
		Expect(EnsureKrefIgnored(dir)).To(Succeed())
		Expect(EnsureKrefIgnored(dir)).To(Succeed())
		data, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Count(string(data), ".kref/")).To(Equal(1))
	})
})

var _ = Describe("IDFromFile", func() {
	It("extracts the kref-id from a file's trailer", func() {
		dir := GinkgoT().TempDir()
		p := filepath.Join(dir, "n.md")
		hex := strings.Repeat("a", 64)
		Expect(os.WriteFile(p, []byte("# N\n\nbody\n\n<!-- kref-id: "+hex+" -->\n"), 0o644)).To(Succeed())
		id, err := IDFromFile(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(hex))
	})
	It("errors when there is no trailer", func() {
		dir := GinkgoT().TempDir()
		p := filepath.Join(dir, "plain.md")
		Expect(os.WriteFile(p, []byte("# N\n\nno trailer\n"), 0o644)).To(Succeed())
		_, err := IDFromFile(p)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("AnchorForTracking", func() {
	newStore := func(dir string) *store.Store {
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		return s
	}

	It("returns an in-repo path unchanged (tracked in place)", func() {
		dir := gitRepo()
		s := newStore(dir)
		p := filepath.Join(dir, "docs", "foo.md")
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte("# Foo\n"), 0o644)).To(Succeed())

		got, err := AnchorForTracking(s, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(p))
	})

	It("copies a floater under .kref/ and ensures it is ignored", func() {
		dir := gitRepo()
		s := newStore(dir)
		ext := filepath.Join(GinkgoT().TempDir(), "bar.md")
		Expect(os.WriteFile(ext, []byte("# Bar\nbody\n"), 0o644)).To(Succeed())

		got, err := AnchorForTracking(s, ext)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(filepath.Join(dir, ".kref", "bar.md")))
		data, err := os.ReadFile(got)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("# Bar\nbody\n")) // original copied verbatim
		excl, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
		Expect(string(excl)).To(ContainSubstring(".kref/"))
	})

	It("disambiguates a floater filename collision without overwriting", func() {
		dir := gitRepo()
		s := newStore(dir)
		mk := func(content string) string {
			p := filepath.Join(GinkgoT().TempDir(), "note.md")
			Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
			return p
		}
		first, err := AnchorForTracking(s, mk("# A\n"))
		Expect(err).NotTo(HaveOccurred())
		second, err := AnchorForTracking(s, mk("# B\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(Equal(filepath.Join(dir, ".kref", "note.md")))
		Expect(second).NotTo(Equal(first))
		Expect(filepath.Dir(second)).To(Equal(filepath.Join(dir, ".kref")))
		a, _ := os.ReadFile(first)
		Expect(string(a)).To(Equal("# A\n")) // first copy untouched
	})
})

var _ = Describe("ReconcileEntry", func() {
	// track ingests rel under dir and marks the entry tracked, returning its snapshot.
	track := func(s *store.Store, dir, rel, content string) *entry.Snapshot {
		p := filepath.Join(dir, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
		res, err := Ingest(s, p, entry.TierPersonal, "", "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Track(res.ID, s.RepoRelative(p))).To(Succeed())
		snap, err := s.Get(res.ID)
		Expect(err).NotTo(HaveOccurred())
		return snap
	}
	newStore := func(dir string) *store.Store {
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		return s
	}

	It("syncs the entry when the tracked file changed", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "docs/note.md", "# Note\n\nold body\n")

		Expect(os.WriteFile(filepath.Join(dir, "docs/note.md"), []byte("# Note\n\nnew body\n"), 0o644)).To(Succeed())
		res, err := ReconcileEntry(s, snap, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("synced"))

		got, err := s.Get(snap.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Body).To(ContainSubstring("new body"))
		Expect(got.Body).NotTo(ContainSubstring("old body"))
	})

	It("is a no-op when the tracked file is unchanged", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "note.md", "# N\n\nbody\n")
		res, err := ReconcileEntry(s, snap, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("unchanged"))
	})

	It("reports missing when the tracked file is gone", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "gone.md", "# G\n\nb\n")
		Expect(os.Remove(filepath.Join(dir, "gone.md"))).To(Succeed())
		res, err := ReconcileEntry(s, snap, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("missing"))
	})

	It("updates the right entry without writing the file when its trailer is gone", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "note.md", "# N\n\nold\n")
		trailerless := "# N\n\nbrand new\n" // no kref-id trailer
		Expect(os.WriteFile(filepath.Join(dir, "note.md"), []byte(trailerless), 0o644)).To(Succeed())

		res, err := ReconcileEntry(s, snap, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("synced"))
		got, err := s.Get(snap.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Body).To(ContainSubstring("brand new"))

		after, err := os.ReadFile(filepath.Join(dir, "note.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(Equal(trailerless)) // strict pull-only: file untouched
		Expect(string(after)).NotTo(ContainSubstring("kref-id"))
	})

	It("fails closed on a secret unless forced", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "plan.md", "# Plan\n\nclean\n")
		Expect(os.WriteFile(filepath.Join(dir, "plan.md"),
			[]byte("# Plan\nawsToken := \"ghp_012345678901234567890123456789abcdef\"\n"), 0o644)).To(Succeed())

		res, err := ReconcileEntry(s, snap, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("error"))
		Expect(res.Error).To(ContainSubstring("secret"))
		got, _ := s.Get(snap.ID)
		Expect(got.Body).To(ContainSubstring("clean")) // not pulled

		forced, err := ReconcileEntry(s, snap, false, true, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(forced.Action).To(Equal("synced"))
		Expect(forced.Forced).To(BeTrue())
	})
})

var _ = Describe("BuildTrailerIndex + Reconcile self-heal", func() {
	newStore := func(dir string) *store.Store {
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		return s
	}
	trackFile := func(s *store.Store, dir, rel, content string) *entry.Snapshot {
		p := filepath.Join(dir, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
		res, err := Ingest(s, p, entry.TierPersonal, "", "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Track(res.ID, s.RepoRelative(p))).To(Succeed())
		snap, err := s.Get(res.ID)
		Expect(err).NotTo(HaveOccurred())
		return snap
	}

	It("indexes .md files by trailer and records copies", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := trackFile(s, dir, "docs/a.md", "# A\n\nbody\n")
		raw, err := os.ReadFile(filepath.Join(dir, "docs/a.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, "docs/copy.md"), raw, 0o644)).To(Succeed())

		idx, err := BuildTrailerIndex(s.Root())
		Expect(err).NotTo(HaveOccurred())
		Expect(idx[snap.ID.String()]).To(ConsistOf("docs/a.md", "docs/copy.md"))
	})

	It("relocates a moved file and re-points TrackedPath", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := trackFile(s, dir, "docs/a.md", "# A\n\nold\n")
		raw, _ := os.ReadFile(filepath.Join(dir, "docs/a.md"))
		moved := strings.Replace(string(raw), "old", "moved body", 1)
		Expect(os.WriteFile(filepath.Join(dir, "docs/b.md"), []byte(moved), 0o644)).To(Succeed())
		Expect(os.Remove(filepath.Join(dir, "docs/a.md"))).To(Succeed())

		idx, err := BuildTrailerIndex(s.Root())
		Expect(err).NotTo(HaveOccurred())
		res, err := Reconcile(s, snap, idx, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("relocated"))
		Expect(res.Path).To(Equal("docs/b.md"))

		got, err := s.Get(snap.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.TrackedPath).To(Equal("docs/b.md"))
		Expect(got.Body).To(ContainSubstring("moved body"))
	})

	It("reports ambiguous when the trailer maps to multiple files", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := trackFile(s, dir, "a.md", "# A\n\nb\n")
		raw, _ := os.ReadFile(filepath.Join(dir, "a.md"))
		Expect(os.WriteFile(filepath.Join(dir, "c1.md"), raw, 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "c2.md"), raw, 0o644)).To(Succeed())
		Expect(os.Remove(filepath.Join(dir, "a.md"))).To(Succeed())

		idx, _ := BuildTrailerIndex(s.Root())
		res, err := Reconcile(s, snap, idx, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("ambiguous"))
	})

	It("reports missing when no file carries the trailer", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := trackFile(s, dir, "a.md", "# A\n\nb\n")
		Expect(os.Remove(filepath.Join(dir, "a.md"))).To(Succeed())
		idx, _ := BuildTrailerIndex(s.Root())
		res, err := Reconcile(s, snap, idx, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("missing"))
	})
})

var _ = Describe("DriftState + dry-run", func() {
	newStore := func(dir string) *store.Store {
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		return s
	}
	trackFile := func(s *store.Store, dir, rel, content string) *entry.Snapshot {
		p := filepath.Join(dir, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
		res, err := Ingest(s, p, entry.TierPersonal, "", "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Track(res.ID, s.RepoRelative(p))).To(Succeed())
		snap, err := s.Get(res.ID)
		Expect(err).NotTo(HaveOccurred())
		return snap
	}

	It("reports in-sync, drifted, and missing", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := trackFile(s, dir, "n.md", "# N\n\nbody\n")

		st, err := DriftState(s, snap)
		Expect(err).NotTo(HaveOccurred())
		Expect(st).To(Equal("in-sync"))

		Expect(os.WriteFile(filepath.Join(dir, "n.md"), []byte("# N\n\nchanged\n"), 0o644)).To(Succeed())
		st, err = DriftState(s, snap)
		Expect(err).NotTo(HaveOccurred())
		Expect(st).To(Equal("drifted"))

		Expect(os.Remove(filepath.Join(dir, "n.md"))).To(Succeed())
		st, err = DriftState(s, snap)
		Expect(err).NotTo(HaveOccurred())
		Expect(st).To(Equal("missing"))
	})

	It("dry-run reports a drifted file as would-sync without mutating the entry", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := trackFile(s, dir, "n.md", "# N\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "n.md"), []byte("# N\n\nnew\n"), 0o644)).To(Succeed())

		res, err := Reconcile(s, snap, nil, true /*dryRun*/, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("synced"))

		got, err := s.Get(snap.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Body).To(ContainSubstring("old")) // not mutated
		Expect(got.Body).NotTo(ContainSubstring("new"))
	})

	It("dry-run reports a moved file as relocated without re-pointing TrackedPath", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := trackFile(s, dir, "a.md", "# A\n\nb\n")
		raw, _ := os.ReadFile(filepath.Join(dir, "a.md"))
		Expect(os.WriteFile(filepath.Join(dir, "b.md"), raw, 0o644)).To(Succeed())
		Expect(os.Remove(filepath.Join(dir, "a.md"))).To(Succeed())

		idx, _ := BuildTrailerIndex(s.Root())
		res, err := Reconcile(s, snap, idx, true /*dryRun*/, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("relocated"))

		got, err := s.Get(snap.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.TrackedPath).To(Equal("a.md")) // not re-pointed in dry-run
	})
})

var _ = Describe("WriteBack", func() {
	newStore := func(dir string) *store.Store {
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		return s
	}
	// track ingests rel under dir (stamping a kref-id trailer) and marks the
	// entry tracked, returning its snapshot.
	track := func(s *store.Store, dir, rel, content string) *entry.Snapshot {
		p := filepath.Join(dir, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
		res, err := Ingest(s, p, entry.TierPersonal, "", "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Track(res.ID, s.RepoRelative(p))).To(Succeed())
		snap, err := s.Get(res.ID)
		Expect(err).NotTo(HaveOccurred())
		return snap
	}

	It("is in-sync when the file equals the entry head", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "note.md", "# N\n\nbody\n")
		res, err := WriteBack(s, snap, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("in-sync"))
	})

	It("pushes the entry to the file when the file is a past version (fast-forward)", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "note.md", "# N\n\nold\n")
		// Advance the entry; the file still holds the old (past) body.
		Expect(s.Update(snap.ID, "# N\n\nnew", "N")).To(Succeed())
		snap, err := s.Get(snap.ID)
		Expect(err).NotTo(HaveOccurred())

		res, err := WriteBack(s, snap, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("written"))

		after, err := os.ReadFile(filepath.Join(dir, "note.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("new"))
		Expect(string(after)).NotTo(ContainSubstring("old"))
		Expect(string(after)).To(ContainSubstring("kref-id: " + snap.ID.String()))
	})

	It("reports missing when the tracked file is gone, without writing", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "gone.md", "# G\n\nb\n")
		Expect(os.Remove(filepath.Join(dir, "gone.md"))).To(Succeed())
		res, err := WriteBack(s, snap, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("missing"))
		_, statErr := os.Stat(filepath.Join(dir, "gone.md"))
		Expect(os.IsNotExist(statErr)).To(BeTrue()) // not re-created
	})

	It("does not write the file under dry-run", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "note.md", "# N\n\nold\n")
		Expect(s.Update(snap.ID, "# N\n\nnew", "N")).To(Succeed())
		snap, err := s.Get(snap.ID)
		Expect(err).NotTo(HaveOccurred())

		res, err := WriteBack(s, snap, true, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("written"))
		after, err := os.ReadFile(filepath.Join(dir, "note.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("old")) // unchanged on disk
	})

	It("reports diverged with a diff and leaves the file untouched", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "note.md", "# N\n\nold\n")
		// Edit the file to content the entry never had (no trailer needed).
		diverged := "# N\n\nlocal edit\n"
		Expect(os.WriteFile(filepath.Join(dir, "note.md"), []byte(diverged), 0o644)).To(Succeed())

		res, err := WriteBack(s, snap, false, false, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("diverged"))
		Expect(res.Diff).To(ContainSubstring("--- entry"))
		Expect(res.Diff).To(ContainSubstring("+++ file"))
		Expect(res.Diff).To(ContainSubstring("-old"))        // entry-only line removed
		Expect(res.Diff).To(ContainSubstring("+local edit")) // file-only line added

		after, err := os.ReadFile(filepath.Join(dir, "note.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(Equal(diverged)) // not written
	})

	It("overwrites a diverged file under force", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "note.md", "# N\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "note.md"), []byte("# N\n\nlocal edit\n"), 0o644)).To(Succeed())

		res, err := WriteBack(s, snap, false, true, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("forced"))

		after, err := os.ReadFile(filepath.Join(dir, "note.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("old")) // entry body restored
		Expect(string(after)).NotTo(ContainSubstring("local edit"))
		Expect(string(after)).To(ContainSubstring("kref-id: " + snap.ID.String()))
	})

	It("does not overwrite a diverged file under force + dry-run", func() {
		dir := gitRepo()
		s := newStore(dir)
		snap := track(s, dir, "note.md", "# N\n\nold\n")
		Expect(os.WriteFile(filepath.Join(dir, "note.md"), []byte("# N\n\nlocal edit\n"), 0o644)).To(Succeed())

		res, err := WriteBack(s, snap, true, true, "t", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("forced"))
		Expect(res.Diff).To(ContainSubstring("local edit"))
		after, err := os.ReadFile(filepath.Join(dir, "note.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(ContainSubstring("local edit")) // untouched
	})
})

var _ = Describe("ingest content types", func() {
	It("ingests a non-markdown file content-only with the detected type and no trailer", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		p := filepath.Join(dir, "cfg.json")
		original := []byte("{\n  \"a\": 1\n}\n")
		Expect(os.WriteFile(p, original, 0o644)).To(Succeed())

		res, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Action).To(Equal("created"))

		snap, err := s.Get(res.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.ContentType).To(Equal("application/json"))
		Expect(snap.Tracked).To(BeFalse())

		after, err := os.ReadFile(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(original)) // no kref-id trailer stamped into JSON
	})

	It("rejects a binary file", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		p := filepath.Join(dir, "blob.bin")
		Expect(os.WriteFile(p, []byte{0x00, 0xff, 0x10}, 0o644)).To(Succeed())
		_, err = Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).To(HaveOccurred())
	})

	It("still stamps a trailer into markdown", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "Tester", "tester@example.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		p := filepath.Join(dir, "note.md")
		Expect(os.WriteFile(p, []byte("# Note\n\nbody\n"), 0o644)).To(Succeed())
		res, err := Ingest(s, p, entry.TierShared, "", "tester", "human")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(res.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.ContentType).To(Equal("text/markdown"))
		after, _ := os.ReadFile(p)
		Expect(string(after)).To(ContainSubstring("kref-id:"))
	})
})
