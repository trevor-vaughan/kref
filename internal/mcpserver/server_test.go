package mcpserver_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/riotbox/kref/internal/entry"
	"github.com/riotbox/kref/internal/mcpserver"
	"github.com/riotbox/kref/internal/store"
)

func call(dir, tool string, args map[string]any) *mcp.CallToolResult {
	GinkgoHelper()
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	srv := mcpserver.New(dir, "test")
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

func text(res *mcp.CallToolResult) string {
	GinkgoHelper()
	Expect(res.Content).NotTo(BeEmpty())
	tc, ok := res.Content[0].(*mcp.TextContent)
	Expect(ok).To(BeTrue())
	return tc.Text
}

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

	It("kref_get returns a tool error for an unknown id", func() {
		dir := gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		_ = s.Close()
		got := call(dir, "kref_get", map[string]any{"id": "deadbeef"})
		Expect(got.IsError).To(BeTrue())
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
