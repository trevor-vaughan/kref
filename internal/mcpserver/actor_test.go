package mcpserver_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/store"
)

// MCP carries no model identity, so a write declares its own. Provenance used
// to be recorded only on create, and with the constant "mcp" — an entry could
// not say which agent last touched it.
var _ = Describe("MCP write attribution", func() {
	seed := func() (dir string, id entity.Id) {
		GinkgoHelper()
		dir = gitRepo()
		s, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err = s.Add(entry.TierPersonal, "doc", "Doc", "body")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		return dir, id
	}

	snapOf := func(dir string, id entity.Id) *entry.Snapshot {
		GinkgoHelper()
		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = s.Close() }()
		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		return snap
	}

	It("records the declared model as the actor on create", func() {
		dir := gitRepo()
		_, err := store.Init(dir, "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		res := call(dir, "kref_remember", map[string]any{
			"title": "T", "body": "b", "tier": "personal", "model": "claude-opus-5",
		})
		Expect(res.IsError).To(BeFalse(), text(res))

		s, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = s.Close() }()
		list, err := s.List(store.ListFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Provenance).NotTo(BeEmpty())
		Expect(list[0].Provenance[0].Actor).To(ContainSubstring("claude-opus-5"))
		Expect(list[0].Provenance[0].ActorKind).To(Equal("agent"))
	})

	It("records provenance for an update, not just the create", func() {
		dir, id := seed()
		res := call(dir, "kref_update", map[string]any{
			"id": id.String(), "body": "new body", "model": "claude-opus-5",
		})
		Expect(res.IsError).To(BeFalse(), text(res))
		prov := snapOf(dir, id).Provenance
		Expect(prov).NotTo(BeEmpty())
		last := prov[len(prov)-1]
		Expect(last.Actor).To(ContainSubstring("claude-opus-5"))
		Expect(last.ActorKind).To(Equal("agent"))
	})

	It("records provenance for a lifecycle change", func() {
		dir, id := seed()
		res := call(dir, "kref_lifecycle", map[string]any{
			"id": id.String(), "action": "set_status", "status": "active", "model": "claude-opus-5",
		})
		Expect(res.IsError).To(BeFalse(), text(res))
		prov := snapOf(dir, id).Provenance
		Expect(prov).NotTo(BeEmpty())
		Expect(prov[len(prov)-1].Actor).To(ContainSubstring("claude-opus-5"))
	})

	It("names the model on a comment so it does not read as the repo owner", func() {
		dir, id := seed()
		res := call(dir, "kref_comment", map[string]any{
			"id": id.String(), "action": "add", "body": "from an agent", "model": "claude-opus-5",
		})
		Expect(res.IsError).To(BeFalse(), text(res))
		cs := snapOf(dir, id).Comments
		Expect(cs).To(HaveLen(1))
		Expect(cs[0].Actor).To(ContainSubstring("claude-opus-5"))
		Expect(cs[0].AuthorKind).To(Equal("agent"))
	})

	It("falls back to a recorded unknown rather than a blank actor", func() {
		dir, id := seed()
		res := call(dir, "kref_update", map[string]any{
			"id": id.String(), "body": "new body", "model": "   ",
		})
		Expect(res.IsError).To(BeFalse(), text(res))
		prov := snapOf(dir, id).Provenance
		Expect(prov[len(prov)-1].Actor).To(ContainSubstring("unknown"))
	})
})
