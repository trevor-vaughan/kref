package mcpserver_test

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/mcpserver"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

var _ = Describe("kref_quarantine (read-only)", func() {
	const secret = "ghp_012345678901234567890123456789abcdef"

	seed := func() (dir, qid string) {
		dir = gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierShared, "doc", "Doc", "clean current")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		find := []scan.Finding{{RuleID: "github-pat", Description: "GitHub PAT", Secret: secret, StartLine: 1}}
		parked, err := s.QuarantineUpdate(id, "new body with "+secret, snap.Version, find, "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		return dir, parked.ItemID.String()
	}

	It("lists the review queue without leaking the secret", func() {
		dir, qid := seed()
		res := call(dir, "kref_quarantine", map[string]any{"action": "list"})
		Expect(res.IsError).To(BeFalse())
		out := text(res)
		Expect(out).To(ContainSubstring(qid[:12]))
		Expect(out).To(ContainSubstring("github-pat"))
		Expect(out).NotTo(ContainSubstring(secret))
	})

	It("shows a held item's findings + metadata but withholds the secret content", func() {
		dir, qid := seed()
		res := call(dir, "kref_quarantine", map[string]any{"action": "show", "id": qid})
		Expect(res.IsError).To(BeFalse())
		out := text(res)
		Expect(out).To(ContainSubstring("github-pat"))
		Expect(out).To(ContainSubstring("withheld"))
		Expect(out).NotTo(ContainSubstring(secret))
	})

	It("does not expose approve/reject (human-only) as an action", func() {
		dir, qid := seed()
		res := call(dir, "kref_quarantine", map[string]any{"action": "approve", "id": qid})
		Expect(res.IsError).To(BeTrue())
	})
})

func callServer(srv *mcp.Server, tool string, args map[string]any) *mcp.CallToolResult {
	GinkgoHelper()
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	Expect(err).NotTo(HaveOccurred())
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = cs.Close(); _ = ss.Wait() })
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	Expect(err).NotTo(HaveOccurred())
	return res
}

func call(dir, tool string, args map[string]any) *mcp.CallToolResult {
	GinkgoHelper()
	return callServer(mcpserver.New(mcpserver.Config{Dir: dir, Version: "test"}), tool, args)
}

func callG(pinned string, roots []string, tool string, args map[string]any) *mcp.CallToolResult {
	GinkgoHelper()
	return callServer(mcpserver.New(mcpserver.Config{Dir: pinned, Version: "test", AllowRoots: roots}), tool, args)
}

// callC drives a ClientRoots server, advertising clientRootDirs as the client's
// file:// roots BEFORE connecting (so the server's per-call ListRoots sees them).
// It mirrors callServer's connect/cleanup idiom exactly.
func callC(clientRootDirs []string, dir, tool string, args map[string]any) *mcp.CallToolResult {
	GinkgoHelper()
	ctx := context.Background()
	srv := mcpserver.New(mcpserver.Config{Dir: dir, Version: "test", ClientRoots: true})
	clientT, serverT := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	Expect(err).NotTo(HaveOccurred())
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	for _, d := range clientRootDirs {
		client.AddRoots(&mcp.Root{URI: "file://" + d})
	}
	cs, err := client.Connect(ctx, clientT, nil)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = cs.Close(); _ = ss.Wait() })
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	Expect(err).NotTo(HaveOccurred())
	return res
}

func text(res *mcp.CallToolResult) string {
	GinkgoHelper()
	Expect(res.Content).NotTo(BeEmpty())
	tc, ok := res.Content[0].(*mcp.TextContent)
	Expect(ok).To(BeTrue())
	return tc.Text
}

var _ = Describe("MCP global mode (--allow roots)", func() {
	It("serves a repo addressed by a dir inside an allowed root", func() {
		root := gitRepo()
		s, err := store.Init(root, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "spec", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()

		res := callG("", []string{root}, "kref_get", map[string]any{"id": id.String(), "dir": root})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("body"))
	})

	It("refuses a dir outside the allowed roots", func() {
		root := gitRepo()
		other := gitRepo()
		res := callG("", []string{root}, "kref_get", map[string]any{"id": "deadbeef", "dir": other})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("outside"))
	})

	It("Serve rejects a relative --allow root", func() {
		err := mcpserver.Serve(context.Background(), ".", "test", []string{"relative/root"}, false)
		Expect(err).To(MatchError(ContainSubstring("absolute")))
	})

	It("Serve rejects a non-existent --allow root", func() {
		err := mcpserver.Serve(context.Background(), ".", "test", []string{filepath.Join(gitRepo(), "nope")}, false)
		Expect(err).To(MatchError(ContainSubstring("does not exist")))
	})
})

var _ = Describe("MCP client-roots mode (--client-roots)", func() {
	seed := func() (string, string) {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "spec", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir, id.String()
	}

	It("serves a repo inside an advertised client root", func() {
		dir, id := seed()
		res := callC([]string{dir}, dir, "kref_get", map[string]any{"id": id, "dir": dir})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("body"))
	})

	It("defaults dir to the sole advertised root", func() {
		dir, id := seed()
		res := callC([]string{dir}, "", "kref_get", map[string]any{"id": id})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("body"))
	})

	It("refuses a dir outside every advertised root", func() {
		dir, id := seed()
		other := gitRepo()
		res := callC([]string{dir}, other, "kref_get", map[string]any{"id": id, "dir": other})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("outside"))
	})

	It("fails closed when the client advertises no roots", func() {
		dir, id := seed()
		res := callC(nil, dir, "kref_get", map[string]any{"id": id, "dir": dir})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("no usable"))
	})

	It("Serve rejects --allow together with --client-roots", func() {
		root := gitRepo()
		err := mcpserver.Serve(context.Background(), root, "test", []string{root}, true)
		Expect(err).To(MatchError(ContainSubstring("mutually exclusive")))
	})
})

var _ = Describe("MCP tier scoping (global/client-roots mode)", func() {
	seed := func() (dir, privID, sharedID string) {
		GinkgoHelper()
		dir = gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		pid, err := s.Add(entry.TierPrivate, "spec", "Secret", "private body")
		Expect(err).NotTo(HaveOccurred())
		sid, err := s.Add(entry.TierShared, "spec", "Public", "shared body")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir, pid.String(), sid.String()
	}

	It("recall in --allow mode omits private-typed entries", func() {
		dir, _, _ := seed()
		res := callG("", []string{dir}, "kref_recall", map[string]any{"dir": dir})
		Expect(res.IsError).To(BeFalse())
		out := text(res)
		Expect(out).To(ContainSubstring("Public"))
		Expect(out).NotTo(ContainSubstring("Secret"))
	})

	It("recall in locked mode still shows private-typed entries", func() {
		dir, _, _ := seed()
		res := call(dir, "kref_recall", map[string]any{})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("Secret"))
	})

	It("get in --allow mode refuses a private-typed target", func() {
		dir, privID, sharedID := seed()
		res := callG("", []string{dir}, "kref_get", map[string]any{"id": privID, "dir": dir})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("private-typed tier"))

		ok := callG("", []string{dir}, "kref_get", map[string]any{"id": sharedID, "dir": dir})
		Expect(ok.IsError).To(BeFalse())
		Expect(text(ok)).To(ContainSubstring("shared body"))
	})

	It("get for a sole-root client (home) still serves the private tier", func() {
		dir, privID, _ := seed()
		res := callC([]string{dir}, dir, "kref_get", map[string]any{"id": privID, "dir": dir})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("private body"))
	})

	It("get for a multi-root client (not home) refuses the private tier", func() {
		dir, privID, _ := seed()
		res := callC([]string{dir, gitRepo()}, dir, "kref_get", map[string]any{"id": privID, "dir": dir})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("private-typed tier"))
	})

	It("get in locked mode still serves the private tier", func() {
		dir, privID, _ := seed()
		res := call(dir, "kref_get", map[string]any{"id": privID})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("private body"))
	})

	It("remember to a private tier is refused in --allow mode", func() {
		dir, _, _ := seed()
		res := callG("", []string{dir}, "kref_remember",
			map[string]any{"tier": "private", "title": "x", "body": "y", "dir": dir})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("private-typed tier"))
	})

	It("remember to the default (personal) tier still works in --allow mode", func() {
		dir, _, _ := seed()
		res := callG("", []string{dir}, "kref_remember",
			map[string]any{"title": "ok", "body": "z", "dir": dir})
		Expect(res.IsError).To(BeFalse())
	})

	It("lifecycle on a private-typed target is refused in --allow mode", func() {
		dir, privID, _ := seed()
		res := callG("", []string{dir}, "kref_lifecycle",
			map[string]any{"id": privID, "action": "archive", "dir": dir})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("private-typed tier"))
	})

	It("supersede refuses when either side is private-typed in --allow mode", func() {
		dir, privID, sharedID := seed()
		res := callG("", []string{dir}, "kref_supersede",
			map[string]any{"old": sharedID, "new": privID, "dir": dir})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("private-typed tier"))
	})

	It("quarantine read is refused in --allow mode", func() {
		dir, _, _ := seed()
		res := callG("", []string{dir}, "kref_quarantine", map[string]any{"action": "list", "dir": dir})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("not served"))
	})
})

var _ = Describe("MCP per-call dir (locked mode)", func() {
	seed := func() (string, string) {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "spec", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir, id.String()
	}

	It("works with no dir param (uses the pinned repo)", func() {
		dir, id := seed()
		res := call(dir, "kref_get", map[string]any{"id": id})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("body"))
	})

	It("accepts a dir equal to the pinned repo", func() {
		dir, id := seed()
		res := call(dir, "kref_get", map[string]any{"id": id, "dir": dir})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("body"))
	})

	It("accepts an equivalent (canonicalized) form of the pinned repo", func() {
		dir, id := seed()
		equiv := filepath.Join(dir, ".git", "..") // resolves back to dir
		res := call(dir, "kref_get", map[string]any{"id": id, "dir": equiv})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("body"))
	})

	It("refuses a dir that names a different repo", func() {
		dir, id := seed()
		other := gitRepo()
		res := call(dir, "kref_get", map[string]any{"id": id, "dir": other})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("does not match"))
	})

	It("refuses a mismatched dir on a write tool and performs no write", func() {
		dir, id := seed()
		other := gitRepo()
		res := call(dir, "kref_update", map[string]any{"id": id, "body": "changed", "dir": other})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("does not match"))

		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		snap, err := s.Get(entity.Id(id))
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Body).To(Equal("body")) // untouched
	})
})

var _ = Describe("kref_remember", func() {
	It("creates an entry the store can read back", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()

		res := call(dir, "kref_remember", map[string]any{
			"title": "Auth design", "body": "the body", "tier": "personal", "kind": "spec",
		})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("remembered"))

		s2, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		items, err := s2.List(store.ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		titles := make([]string, 0, len(items))
		for _, it := range items {
			titles = append(titles, it.Title)
		}
		Expect(titles).To(ContainElement("Auth design"))
	})

	It("returns a tool error for an invalid tier", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		res := call(dir, "kref_remember", map[string]any{"title": "X", "body": "y", "tier": "bogus"})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("tier"))
	})

})

var _ = Describe("kref_recall and kref_get", func() {
	It("recalls entries by search and gets one by id", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "spec", "Auth flow", "secret-free body about auth")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()

		rec := call(dir, "kref_recall", map[string]any{"search": "auth"})
		Expect(rec.IsError).To(BeFalse())
		Expect(text(rec)).To(ContainSubstring("Auth flow"))

		got := call(dir, "kref_get", map[string]any{"id": id.String()})
		Expect(got.IsError).To(BeFalse())
		Expect(text(got)).To(ContainSubstring("auth"))
	})

	It("surfaces the version so a caller can supply if_version", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "todo", "T", "# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()

		got := call(dir, "kref_get", map[string]any{"id": id.String()})
		Expect(text(got)).To(ContainSubstring("version: 1"))

		rec := call(dir, "kref_recall", map[string]any{"search": "T"})
		Expect(text(rec)).To(ContainSubstring("v1"))
	})

	It("kref_get returns a tool error for an unknown id", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		got := call(dir, "kref_get", map[string]any{"id": "deadbeef"})
		Expect(got.IsError).To(BeTrue())
	})

	It("kref_get reports kind, content-type, updated, labels, and links", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "spec", "Auth flow", "body about auth")
		Expect(err).NotTo(HaveOccurred())
		other, err := s.Add(entry.TierPersonal, "note", "Related note", "x")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.AddLabel(id, "area:auth")).To(Succeed())
		Expect(s.AddLink(id, other.String(), "relates")).To(Succeed())
		_ = s.Close()

		out := text(call(dir, "kref_get", map[string]any{"id": id.String()}))
		Expect(out).To(ContainSubstring("kind: spec"))
		Expect(out).To(ContainSubstring("content-type: text/markdown"))
		Expect(out).To(MatchRegexp(`updated: \d{4}-\d{2}-\d{2}`))
		Expect(out).To(ContainSubstring("labels: area:auth"))
		Expect(out).To(ContainSubstring("links: relates " + other.String()))
	})

	It("kref_recall reports kind, updated, and per-entry match counts", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "spec", "Auth flow", "auth auth auth")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.AddLabel(id, "area:auth")).To(Succeed())
		_ = s.Close()

		out := text(call(dir, "kref_recall", map[string]any{"search": "auth"}))
		Expect(out).To(ContainSubstring("kind:spec"))
		Expect(out).To(MatchRegexp(`updated \d{4}-\d{2}-\d{2}`))
		Expect(out).To(ContainSubstring("matches:4")) // 1 in title + 3 in body
		Expect(out).To(ContainSubstring("labels:area:auth"))
	})

	It("kref_recall honors a result limit and reports the truncation", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Add(entry.TierPersonal, "note", "alpha", "match match match")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Add(entry.TierPersonal, "note", "beta", "match match")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Add(entry.TierPersonal, "note", "gamma", "match")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()

		out := text(call(dir, "kref_recall", map[string]any{"search": "match", "limit": 2}))
		Expect(out).To(ContainSubstring("alpha"))
		Expect(out).To(ContainSubstring("beta"))
		Expect(out).NotTo(ContainSubstring("gamma"))
		Expect(out).To(ContainSubstring("showing 2 of 3"))
	})
})

var _ = Describe("kref_update and kref_supersede", func() {
	It("updates an entry's body", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "spec", "Doc", "v1")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()

		res := call(dir, "kref_update", map[string]any{"id": id.String(), "body": "v2 body"})
		Expect(res.IsError).To(BeFalse())

		s2, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		snap, err := s2.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Body).To(Equal("v2 body"))
	})

	It("supersedes one entry with another", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		oldID, err := s.Add(entry.TierPersonal, "spec", "Old", "a")
		Expect(err).NotTo(HaveOccurred())
		newID, err := s.Add(entry.TierPersonal, "spec", "New", "b")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()

		res := call(dir, "kref_supersede", map[string]any{"old": oldID.String(), "new": newID.String()})
		Expect(res.IsError).To(BeFalse())

		s2, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		snap, err := s2.Get(oldID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Status).To(Equal("superseded"))
	})
})

var _ = Describe("kref_update labels and links", func() {
	const secretBody = "look: awsToken := \"ghp_012345678901234567890123456789abcdef\"\n"

	BeforeEach(func() { GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir()) })

	seedEntry := func(tier entry.Tier, kind, body string) (string, string) {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(tier, kind, "T", body)
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir, id.String()
	}
	labelsOf := func(dir, id string) []string {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		snap, err := s.Get(entity.Id(id))
		Expect(err).NotTo(HaveOccurred())
		return snap.Labels
	}
	linksOf := func(dir, id string) []entry.Link {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		snap, err := s.Get(entity.Id(id))
		Expect(err).NotTo(HaveOccurred())
		return snap.Links
	}
	bodyOf := func(dir, id string) string {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		snap, err := s.Get(entity.Id(id))
		Expect(err).NotTo(HaveOccurred())
		return snap.Body
	}
	It("adds and removes labels via a body update", func() {
		dir, id := seedEntry(entry.TierPersonal, "spec", "body")
		res := call(dir, "kref_update", map[string]any{
			"id": id, "body": "body v2", "add_labels": []any{"area:x", "prio:hi"},
		})
		Expect(res.IsError).To(BeFalse())
		Expect(labelsOf(dir, id)).To(ContainElements("area:x", "prio:hi"))

		res = call(dir, "kref_update", map[string]any{"id": id, "remove_labels": []any{"prio:hi"}})
		Expect(res.IsError).To(BeFalse())
		Expect(labelsOf(dir, id)).To(ContainElement("area:x"))
		Expect(labelsOf(dir, id)).NotTo(ContainElement("prio:hi"))
	})

	It("adds a link with an explicit type and removes it", func() {
		dir, from := seedEntry(entry.TierPersonal, "spec", "from")
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		to, err := s.Add(entry.TierPersonal, "spec", "To", "to")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()

		res := call(dir, "kref_update", map[string]any{
			"id": from, "add_links": []any{map[string]any{"to": to.String(), "type": "blocks"}},
		})
		Expect(res.IsError).To(BeFalse())
		var got string
		for _, l := range linksOf(dir, from) {
			if l.To == to.String() {
				got = l.Type
			}
		}
		Expect(got).To(Equal("blocks"))

		res = call(dir, "kref_update", map[string]any{"id": from, "remove_links": []any{to.String()}})
		Expect(res.IsError).To(BeFalse())
		Expect(linksOf(dir, from)).To(BeEmpty())
	})

	It("defaults a link type to relates when omitted", func() {
		dir, from := seedEntry(entry.TierPersonal, "spec", "from")
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		to, err := s.Add(entry.TierPersonal, "spec", "To", "to")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		res := call(dir, "kref_update", map[string]any{
			"id": from, "add_links": []any{map[string]any{"to": to.String()}},
		})
		Expect(res.IsError).To(BeFalse())
		Expect(linksOf(dir, from)[0].Type).To(Equal("relates"))
	})

	It("applies a label-only update (no body) with no if_version on a todo", func() {
		dir, id := seedEntry(entry.TierPersonal, "todo", "# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n")
		res := call(dir, "kref_update", map[string]any{"id": id, "add_labels": []any{"triaged"}})
		Expect(res.IsError).To(BeFalse())
		Expect(labelsOf(dir, id)).To(ContainElement("triaged"))
	})

	It("errors when the call has neither a body nor any label/link array", func() {
		dir, id := seedEntry(entry.TierPersonal, "spec", "body")
		res := call(dir, "kref_update", map[string]any{"id": id})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("nothing to update"))
	})

	It("refuses a secret-bearing add_labels value on a syncable tier, naming the rule not the value", func() {
		dir, id := seedEntry(entry.TierPersonal, "spec", "body")
		res := call(dir, "kref_update", map[string]any{
			"id": id, "add_labels": []any{"token=" + secretBody},
		})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("secret"))
		Expect(text(res)).NotTo(ContainSubstring("ghp_012345678901234567890123456789abcdef"))
		Expect(labelsOf(dir, id)).NotTo(ContainElement("token=" + secretBody))
	})

	It("allows a secret-bearing add_labels value on the private tier", func() {
		dir, id := seedEntry(entry.TierPrivate, "spec", "body")
		res := call(dir, "kref_update", map[string]any{
			"id": id, "add_labels": []any{"token=" + secretBody},
		})
		Expect(res.IsError).To(BeFalse())
		Expect(labelsOf(dir, id)).To(ContainElement("token=" + secretBody))
	})

	It("parks a secret body but still applies a clean label to the live entry", func() {
		dir, id := seedEntry(entry.TierPersonal, "spec", "clean body")
		res := call(dir, "kref_update", map[string]any{
			"id": id, "body": secretBody, "add_labels": []any{"reviewed"},
		})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("quarantined"))
		Expect(bodyOf(dir, id)).To(Equal("clean body")) // body held for review
		Expect(labelsOf(dir, id)).To(ContainElement("reviewed"))
	})
})

var _ = Describe("kref_patch", func() {
	seed := func() (string, string) {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "spec", "Doc", "line one\nline two\nline three")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir, id.String()
	}

	It("applies a unified diff (line numbers as hints) and reports the new version", func() {
		dir, id := seed()
		res := call(dir, "kref_patch", map[string]any{
			"id":   id,
			"diff": "@@ -42,3 +42,3 @@\n line one\n-line two\n+line 2\n line three\n",
		})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("version 2"))

		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		snap, err := s.Get(entity.Id(id))
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Body).To(Equal("line one\nline 2\nline three"))
	})

	It("returns a tool error (not partial application) on a stale hunk", func() {
		dir, id := seed()
		res := call(dir, "kref_patch", map[string]any{
			"id": id,
			"diff": "@@ -1,1 +1,1 @@\n-line one\n+line ONE\n" +
				"@@ -9,1 +9,1 @@\n-absent\n+x\n",
		})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("hunk 2"))

		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		snap, err := s.Get(entity.Id(id))
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Body).To(Equal("line one\nline two\nline three"), "all-or-nothing")
	})
})

var _ = Describe("kref_lifecycle", func() {
	seed := func() (string, string) {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "spec", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir, id.String()
	}
	snap := func(dir, id string) *entry.Snapshot {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		sn, err := s.Get(entity.Id(id))
		Expect(err).NotTo(HaveOccurred())
		return sn
	}

	It("sets a lifecycle status", func() {
		dir, id := seed()
		res := call(dir, "kref_lifecycle", map[string]any{"id": id, "action": "set_status", "status": "accepted"})
		Expect(res.IsError).To(BeFalse())
		Expect(snap(dir, id).Status).To(Equal("accepted"))
	})

	It("tombstones with delete and undoes with restore", func() {
		dir, id := seed()
		Expect(call(dir, "kref_lifecycle", map[string]any{"id": id, "action": "delete"}).IsError).To(BeFalse())
		Expect(snap(dir, id).Deleted).To(BeTrue())
		Expect(call(dir, "kref_lifecycle", map[string]any{"id": id, "action": "restore"}).IsError).To(BeFalse())
		Expect(snap(dir, id).Deleted).To(BeFalse())
	})

	It("archives and unarchives", func() {
		dir, id := seed()
		Expect(call(dir, "kref_lifecycle", map[string]any{"id": id, "action": "archive"}).IsError).To(BeFalse())
		Expect(snap(dir, id).Archived).To(BeTrue())
		Expect(call(dir, "kref_lifecycle", map[string]any{"id": id, "action": "unarchive"}).IsError).To(BeFalse())
		Expect(snap(dir, id).Archived).To(BeFalse())
	})

	It("rejects unknown actions, a missing status, and an invalid status", func() {
		dir, id := seed()
		res := call(dir, "kref_lifecycle", map[string]any{"id": id, "action": "purge"})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("action"))

		res = call(dir, "kref_lifecycle", map[string]any{"id": id, "action": "set_status"})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("status"))

		res = call(dir, "kref_lifecycle", map[string]any{"id": id, "action": "set_status", "status": "bogus"})
		Expect(res.IsError).To(BeTrue())
	})

	It("does not expose a retier tool or action", func() {
		dir, id := seed()
		res := call(dir, "kref_lifecycle", map[string]any{"id": id, "action": "retier"})
		Expect(res.IsError).To(BeTrue())
	})
})

var _ = Describe("custom tiers", func() {
	seed := func() string {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.TierAdd("research", entry.TierPersonal, "", "")).To(Succeed())
		_ = s.Close()
		return dir
	}

	It("kref_remember writes to a declared custom tier", func() {
		dir := seed()
		res := call(dir, "kref_remember", map[string]any{
			"title": "Custom tier note", "body": "b", "tier": "research",
		})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("remembered"))

		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		items, err := s.List(store.ListFilter{Tier: "research"})
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Title).To(Equal("Custom tier note"))
		Expect(items[0].Tier).To(Equal("research"))
	})

	It("kref_remember rejects an undeclared tier as a tool error", func() {
		dir := seed()
		res := call(dir, "kref_remember", map[string]any{"title": "X", "body": "y", "tier": "ghost"})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("unknown tier"))
	})

	It("kref_recall filters by a custom tier and rejects unknown ones", func() {
		dir := seed()
		res := call(dir, "kref_remember", map[string]any{
			"title": "Custom tier note", "body": "b", "tier": "research",
		})
		Expect(res.IsError).To(BeFalse())

		rec := call(dir, "kref_recall", map[string]any{"tier": "research"})
		Expect(rec.IsError).To(BeFalse())
		Expect(text(rec)).To(ContainSubstring("Custom tier note"))

		rec = call(dir, "kref_recall", map[string]any{"tier": "ghost"})
		Expect(rec.IsError).To(BeTrue())
		Expect(text(rec)).To(ContainSubstring("unknown tier"))
	})
})

var _ = Describe("tool error paths", func() {
	freshDir := func() string {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir
	}

	It("kref_recall reports an invalid tier as a tool error", func() {
		res := call(freshDir(), "kref_recall", map[string]any{"tier": "bogus"})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("tier"))
	})

	It("kref_update reports an unknown id as a tool error", func() {
		res := call(freshDir(), "kref_update", map[string]any{"id": "deadbeef", "body": "x"})
		Expect(res.IsError).To(BeTrue())
	})

	It("kref_supersede reports an unknown id as a tool error", func() {
		res := call(freshDir(), "kref_supersede", map[string]any{"old": "deadbeef", "new": "cafef00d"})
		Expect(res.IsError).To(BeTrue())
	})
})

var _ = Describe("kref_comment", func() {
	const secretBody = "look: awsToken := \"ghp_012345678901234567890123456789abcdef\"\n"

	seed := func(tier entry.Tier, body string) (string, string) {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(tier, "spec", "Doc", body)
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir, id.String()
	}
	comments := func(dir, id string) []entry.Comment {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		sn, err := s.Get(entity.Id(id))
		Expect(err).NotTo(HaveOccurred())
		return sn.Comments
	}

	It("adds a plain comment, a question, and a threaded reply", func() {
		dir, id := seed(entry.TierPersonal, "body")
		Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": "a note"}).IsError).To(BeFalse())
		q := call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": "ship it?", "question": true})
		Expect(q.IsError).To(BeFalse())
		cs := comments(dir, id)
		Expect(cs).To(HaveLen(2))
		var question entry.Comment
		for _, c := range cs {
			if c.Question {
				question = c
			}
		}
		Expect(question.Body).To(Equal("ship it?"))

		reply := call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": "yes", "reply_to": question.ID})
		Expect(reply.IsError).To(BeFalse())
		cs = comments(dir, id)
		var replied bool
		for _, c := range cs {
			if c.Body == "yes" {
				Expect(c.ReplyTo).To(Equal(question.ID))
				replied = true
			}
		}
		Expect(replied).To(BeTrue())
	})

	It("edits a comment's body", func() {
		dir, id := seed(entry.TierPersonal, "body")
		add := call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": "typo"})
		Expect(add.IsError).To(BeFalse())
		target := comments(dir, id)[0].ID
		Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "edit", "target": target, "body": "fixed"}).IsError).To(BeFalse())
		Expect(comments(dir, id)[0].Body).To(Equal("fixed"))
	})

	It("deletes (tombstones) a comment", func() {
		dir, id := seed(entry.TierPersonal, "body")
		Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": "oops"}).IsError).To(BeFalse())
		target := comments(dir, id)[0].ID
		Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "delete", "target": target}).IsError).To(BeFalse())
		Expect(comments(dir, id)[0].Deleted).To(BeTrue())
	})

	It("resolves a question, posting an optional note reply first", func() {
		dir, id := seed(entry.TierPersonal, "body")
		Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": "which way?", "question": true}).IsError).To(BeFalse())
		target := comments(dir, id)[0].ID
		Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "resolve", "target": target, "note": "went with A"}).IsError).To(BeFalse())
		cs := comments(dir, id)
		Expect(cs).To(HaveLen(2)) // the question + the note reply
		var resolved, hasNote bool
		for _, c := range cs {
			if c.ID == target {
				resolved = c.Resolved
			}
			if c.Body == "went with A" {
				hasNote = c.ReplyTo == target
			}
		}
		Expect(resolved).To(BeTrue())
		Expect(hasNote).To(BeTrue())
	})

	It("resolves without a note when none is given", func() {
		dir, id := seed(entry.TierPersonal, "body")
		Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": "q?", "question": true}).IsError).To(BeFalse())
		target := comments(dir, id)[0].ID
		Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "resolve", "target": target}).IsError).To(BeFalse())
		Expect(comments(dir, id)).To(HaveLen(1)) // no extra note comment
	})

	It("rejects an unknown action and a missing target", func() {
		dir, id := seed(entry.TierPersonal, "body")
		bad := call(dir, "kref_comment", map[string]any{"id": id, "action": "frobnicate"})
		Expect(bad.IsError).To(BeTrue())
		Expect(text(bad)).To(ContainSubstring("action"))

		noTarget := call(dir, "kref_comment", map[string]any{"id": id, "action": "edit", "body": "x"})
		Expect(noTarget.IsError).To(BeTrue())
		Expect(text(noTarget)).To(ContainSubstring("target"))

		noBody := call(dir, "kref_comment", map[string]any{"id": id, "action": "add"})
		Expect(noBody.IsError).To(BeTrue())
		Expect(text(noBody)).To(ContainSubstring("body"))
	})

	Describe("secret scanning (fail-closed, no work lost)", func() {
		BeforeEach(func() { GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir()) })

		It("quarantines a secret-bearing added comment; the target keeps only the review thread", func() {
			dir, id := seed(entry.TierPersonal, "body")
			res := call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": secretBody})
			Expect(res.IsError).To(BeFalse()) // held, not an error
			Expect(text(res)).To(ContainSubstring("quarantined"))
			cs := comments(dir, id)
			Expect(cs).To(HaveLen(1)) // only the review question-comment, not the held comment
			Expect(cs[0].Question).To(BeTrue())
			Expect(cs[0].Body).NotTo(ContainSubstring("ghp_012345678901234567890123456789abcdef"))
		})

		It("parks a secret on edit without clobbering the stored comment", func() {
			dir, id := seed(entry.TierPersonal, "body")
			Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": "clean"}).IsError).To(BeFalse())
			target := comments(dir, id)[0].ID
			res := call(dir, "kref_comment", map[string]any{"id": id, "action": "edit", "target": target, "body": secretBody})
			Expect(res.IsError).To(BeFalse()) // held, not an error
			Expect(text(res)).To(ContainSubstring("quarantined"))
			// The edit is held: the original comment body is unchanged.
			clean := false
			for _, c := range comments(dir, id) {
				if c.Body == "clean" {
					clean = true
				}
			}
			Expect(clean).To(BeTrue())
		})

		It("allows a secret-bearing comment on a private entry (private cannot push)", func() {
			dir, id := seed(entry.TierPrivate, "body")
			res := call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": secretBody})
			Expect(res.IsError).To(BeFalse())
			Expect(comments(dir, id)).To(HaveLen(1))
		})

		It("does not accept a force argument on an agent comment (force is human-only)", func() {
			dir, id := seed(entry.TierPersonal, "body")
			res := call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": secretBody, "force": true})
			Expect(res.IsError).To(BeTrue())        // force is not an accepted argument (schema-rejected)
			Expect(comments(dir, id)).To(BeEmpty()) // nothing written or parked
		})

		It("parks a secret-bearing resolve note and never suggests force", func() {
			dir, id := seed(entry.TierPersonal, "body")
			Expect(call(dir, "kref_comment", map[string]any{"id": id, "action": "add", "body": "ok?", "question": true}).IsError).To(BeFalse())
			target := comments(dir, id)[0].ID
			res := call(dir, "kref_comment", map[string]any{"id": id, "action": "resolve", "target": target, "note": secretBody})
			Expect(res.IsError).To(BeFalse()) // held, not an error
			Expect(text(res)).To(ContainSubstring("quarantined"))
			Expect(text(res)).NotTo(ContainSubstring("force")) // MCP never forces
			// The resolve is held: the question stays open.
			for _, c := range comments(dir, id) {
				if c.ID == target {
					Expect(c.Resolved).To(BeFalse())
				}
			}
		})
	})
})

var _ = Describe("kref entry-body secret scanning (fail-closed, no work lost)", func() {
	const secretBody = "look: awsToken := \"ghp_012345678901234567890123456789abcdef\"\n"

	BeforeEach(func() { GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir()) })

	freshStore := func() string {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir
	}
	seed := func(tier entry.Tier, body string) (string, string) {
		GinkgoHelper()
		dir := freshStore()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(tier, "spec", "Doc", body)
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir, id.String()
	}
	bodyOf := func(dir, id string) string {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		sn, err := s.Get(entity.Id(id))
		Expect(err).NotTo(HaveOccurred())
		return sn.Body
	}
	count := func(dir string) int {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		items, err := s.List(store.ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		return len(items)
	}
	quarantined := func(dir string) []*entry.Snapshot {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		items, err := s.List(store.ListFilter{Tier: entry.TierQuarantine})
		Expect(err).NotTo(HaveOccurred())
		return items
	}

	It("kref_remember quarantines a secret instead of writing to the syncable tier", func() {
		dir := freshStore()
		res := call(dir, "kref_remember", map[string]any{"title": "Leak", "body": secretBody, "tier": "personal"})
		Expect(res.IsError).To(BeFalse()) // held, not an error
		Expect(text(res)).To(ContainSubstring("quarantined"))
		Expect(text(res)).NotTo(ContainSubstring("ghp_012345678901234567890123456789abcdef")) // never echoes the secret
		Expect(count(dir)).To(Equal(0))                                                       // nothing on the intended tier
		Expect(quarantined(dir)).To(HaveLen(1))                                               // held as a draft
	})

	It("kref_remember allows a secret into a private tier (private cannot push)", func() {
		dir := freshStore()
		res := call(dir, "kref_remember", map[string]any{"title": "Priv", "body": secretBody, "tier": "private"})
		Expect(res.IsError).To(BeFalse())
		Expect(count(dir)).To(Equal(1))
	})

	It("does not let an agent force a secret into a syncable tier (force is human-only)", func() {
		dir := freshStore()
		res := call(dir, "kref_remember", map[string]any{"title": "F", "body": secretBody, "tier": "personal", "force": true})
		Expect(res.IsError).To(BeTrue()) // force is not an accepted argument
		Expect(count(dir)).To(Equal(0))  // nothing created
	})

	It("kref_update quarantines a secret on a syncable entry, leaving the target intact", func() {
		dir, id := seed(entry.TierPersonal, "clean body")
		res := call(dir, "kref_update", map[string]any{"id": id, "body": secretBody})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("quarantined"))
		Expect(bodyOf(dir, id)).To(Equal("clean body")) // target untouched
		Expect(quarantined(dir)).To(HaveLen(1))         // the held update
	})

	It("does not let an agent force a secret onto a syncable entry (force is human-only)", func() {
		dir, id := seed(entry.TierPersonal, "clean body")
		res := call(dir, "kref_update", map[string]any{"id": id, "body": secretBody, "force": true})
		Expect(res.IsError).To(BeTrue())                // force is not an accepted argument
		Expect(bodyOf(dir, id)).To(Equal("clean body")) // original intact
	})

	It("kref_patch quarantines a diff that introduces a secret, leaving the body intact", func() {
		dir, id := seed(entry.TierPersonal, "clean body\n")
		diff := "@@ -1 +1,2 @@\n clean body\n+" + secretBody
		res := call(dir, "kref_patch", map[string]any{"id": id, "diff": diff})
		Expect(res.IsError).To(BeFalse())
		Expect(text(res)).To(ContainSubstring("quarantined"))
		Expect(bodyOf(dir, id)).To(Equal("clean body\n")) // target untouched
		Expect(quarantined(dir)).To(HaveLen(1))
	})
})

var _ = Describe("kref MCP todo guard", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
	})

	seedTodo := func(body string) (string, string) {
		GinkgoHelper()
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "todo", "T", body)
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		return dir, id.String()
	}

	It("kref_update requires if_version for a todo", func() {
		dir, id := seedTodo("# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n")
		res := call(dir, "kref_update", map[string]any{
			"id":   id,
			"body": "# T\n\n## Open\n- [ ] a\n- [ ] b\n\n## Done (compact)\n",
		})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("if_version"))
	})

	It("kref_update refuses a stale if_version and names the recovery file", func() {
		dir, id := seedTodo("# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n") // version 1
		// Move it to version 2 with the correct token.
		Expect(call(dir, "kref_update", map[string]any{
			"id": id, "if_version": 1,
			"body": "# T\n\n## Open\n- [ ] a\n- [ ] b\n\n## Done (compact)\n",
		}).IsError).To(BeFalse())
		// A writer still holding version 1 must be refused.
		res := call(dir, "kref_update", map[string]any{
			"id": id, "if_version": 1,
			"body": "# T\n\n## Open\n- [ ] a\n- [ ] c stale\n\n## Done (compact)\n",
		})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("stale todo write"))
		Expect(text(res)).To(ContainSubstring("saved to"))
	})

	It("kref_update leaves a non-todo entry unaffected by if_version", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierPersonal, "document", "D", "# D\n\nprose\n")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		res := call(dir, "kref_update", map[string]any{"id": id.String(), "body": "# D\n\nnew prose\n"})
		Expect(res.IsError).To(BeFalse())
	})

	It("kref_update auto-formats a todo body", func() {
		dir, id := seedTodo("# T\n\n## Open\n- [ ] a\n\n## Done (compact)\n")
		res := call(dir, "kref_update", map[string]any{
			"id":         id,
			"if_version": 1,
			"body":       "# T\n\n## Open\n- [x] done it\n- [ ] a\n\n## Done (compact)\n",
		})
		Expect(res.IsError).To(BeFalse())

		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		oid, err := s.Resolve(id)
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Get(oid)
		Expect(err).NotTo(HaveOccurred())
		// todo.Format emits a blank line after the "## Done (compact)" heading.
		Expect(snap.Body).To(ContainSubstring("## Done (compact)\n\n- [x] done it"))
	})

	It("kref_update refuses a malformed todo body and names the recovery file", func() {
		dir, id := seedTodo("# T\n\n## Open\n\n## Done (compact)\n")
		res := call(dir, "kref_update", map[string]any{
			"id":         id,
			"if_version": 1,
			"body":       "# T\n\n## Opne\n\n## Open\n\n## Done (compact)\n",
		})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("unknown-heading"))
		Expect(text(res)).To(ContainSubstring("rejected body was saved to"))
	})

	It("kref_remember refuses a malformed todo at creation", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		res := call(dir, "kref_remember", map[string]any{
			"kind":  "todo",
			"title": "T",
			"body":  "# T\n\n## Opne\n\n## Open\n\n## Done (compact)\n",
		})
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("unknown-heading"))
	})
})
