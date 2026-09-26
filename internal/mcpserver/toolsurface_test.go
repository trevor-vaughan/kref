package mcpserver_test

import (
	"context"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trevor-vaughan/kref/internal/mcpserver"
)

// The tool surface is what an agent may do unsupervised, so it is frozen here as
// a whole list rather than probed one name at a time: pinning the set is what
// catches an ADDITION, and an addition is the regression that matters.
//
// kref_attest is deliberately absent. An attestation is a trust assertion made
// with your signing key, and it can absorb a peer's untrusted signature into a
// good verdict — an agent able to call it unsupervised could launder untrusted
// history under your identity. Same reasoning that keeps resign's --force off
// this surface, and that leaves quarantine approve/reject to a human.
var _ = Describe("MCP tool surface", func() {
	It("serves exactly the frozen set of tools", func() {
		ctx := context.Background()
		srv := mcpserver.New(mcpserver.Config{Dir: gitRepo(), Version: "test"})
		clientT, serverT := mcp.NewInMemoryTransports()
		ss, err := srv.Connect(ctx, serverT, nil)
		Expect(err).NotTo(HaveOccurred())
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
		cs, err := client.Connect(ctx, clientT, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = cs.Close(); _ = ss.Wait() })

		res, err := cs.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		got := make([]string, 0, len(res.Tools))
		for _, t := range res.Tools {
			got = append(got, t.Name)
		}
		sort.Strings(got)

		Expect(got).To(Equal([]string{
			"kref_comment",
			"kref_get",
			"kref_lifecycle",
			"kref_patch",
			"kref_quarantine",
			"kref_recall",
			"kref_remember",
			"kref_supersede",
			"kref_update",
		}))
	})
})
