package render_test

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
)

var _ = Describe("show header links", func() {
	snap := func() *entry.Snapshot {
		return &entry.Snapshot{
			ID: "abcd1234abcd", Kind: "note", Title: "T", Status: "open",
			Tier: "personal", TierType: "personal", CreatedBy: "Trevor", CreatedByEmail: "t@e",
		}
	}
	header := func(links entry.LinkView) string {
		var b bytes.Buffer
		render.ShowHeader(&b, snap(), render.ShowOptions{Links: links})
		return b.String()
	}

	It("renders outgoing and incoming links in the base header", func() {
		out := header(entry.LinkView{
			Outgoing: []entry.LinkRef{{ID: "ffff0000ffff", Type: "relates", Title: "Other"}},
			Incoming: []entry.LinkRef{{ID: "aaaa1111aaaa", Type: "supersedes", Title: "Older"}},
		})
		Expect(out).To(ContainSubstring("Links"))
		Expect(out).To(ContainSubstring("out:"))
		Expect(out).To(ContainSubstring("relates"))
		Expect(out).To(ContainSubstring("ffff0000ffff"))
		Expect(out).To(ContainSubstring("Other"))
		Expect(out).To(ContainSubstring("in:"))
		Expect(out).To(ContainSubstring("supersedes"))
		Expect(out).To(ContainSubstring("Older"))
	})

	It("omits the row entirely when there are no links", func() {
		// Consistent with Labels/Favorites/Tracked, which disappear when empty
		// rather than printing "none" on every entry that has none.
		Expect(header(entry.LinkView{})).NotTo(ContainSubstring("Links"))
	})

	It("caps the list and says how many were left out", func() {
		var many []entry.LinkRef
		for i := range 14 {
			many = append(many, entry.LinkRef{
				ID:    entity.Id(fmt.Sprintf("%012d", i)),
				Type:  "relates",
				Title: fmt.Sprintf("Entry %d", i),
			})
		}
		out := header(entry.LinkView{Outgoing: many})
		Expect(strings.Count(out, "out:")).To(Equal(10))
		Expect(out).To(ContainSubstring("+4 more"))
		// No key is named: the expanded header that will carry the rest does not
		// exist yet, and pointing at one that does not work is worse than a bare
		// count. The view-options work owns adding the pointer.
		Expect(out).NotTo(ContainSubstring("press"))
	})

	It("counts incoming links toward the same cap", func() {
		var outgoing, incoming []entry.LinkRef
		for i := range 6 {
			outgoing = append(outgoing, entry.LinkRef{ID: entity.Id(fmt.Sprintf("o%011d", i)), Type: "relates"})
			incoming = append(incoming, entry.LinkRef{ID: entity.Id(fmt.Sprintf("i%011d", i)), Type: "relates"})
		}
		out := header(entry.LinkView{Outgoing: outgoing, Incoming: incoming})
		Expect(strings.Count(out, "out:") + strings.Count(out, "in:")).To(Equal(10))
		Expect(out).To(ContainSubstring("+2 more"))
	})
})

var _ = Describe("ExtendedHeader links", func() {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	snap := &entry.Snapshot{
		ID: "abcd1234abcd", Kind: "note", Title: "T", Status: "open",
		Tier: "personal", TierType: "personal", CreatedBy: "Trevor", CreatedByEmail: "t@e",
	}

	It("lists every link, uncapped, and does not repeat the base header's", func() {
		var many []entry.LinkRef
		for i := range 14 {
			many = append(many, entry.LinkRef{
				ID: entity.Id(fmt.Sprintf("%012d", i)), Type: "relates", Title: fmt.Sprintf("Entry %d", i),
			})
		}
		var b bytes.Buffer
		render.ExtendedHeader(&b, snap, now, nil, entry.LinkView{Outgoing: many}, false, "", nil)
		out := b.String()
		// Expanding is what shows the whole set: 14 rows, exactly once each.
		Expect(strings.Count(out, "out:")).To(Equal(14))
		Expect(out).NotTo(ContainSubstring("more"))
	})
})
