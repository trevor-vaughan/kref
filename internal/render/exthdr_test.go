package render_test

import (
	"bytes"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
)

var _ = Describe("ExtendedHeader", func() {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	snap := &entry.Snapshot{
		ID: "abcd1234abcd", Kind: "note", Title: "T", Status: "open",
		Tier: "personal", TierType: "personal", CreatedBy: "Trevor", CreatedByEmail: "t@e",
	}
	log := []entry.LogEntry{
		{Op: "create", Author: "Trevor", Time: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)},
		{Op: "set-body", Author: "Trevor", Time: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC), Version: 1, Detail: "v1  +10/-0 chars, +2/-0 lines"},
		{Op: "set-body", Author: "agent", Time: time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC), Version: 2, Detail: "v2  +5/-3 chars, +1/-1 lines"},
	}
	links := entry.LinkView{
		Outgoing: []entry.LinkRef{{ID: "ffff0000ffff", Type: "relates", Title: "Other"}},
		Incoming: []entry.LinkRef{},
	}

	render6 := func() string {
		var b bytes.Buffer
		render.ExtendedHeader(&b, snap, now, log, links, false, "", nil)
		return b.String()
	}

	It("keeps the base rows", func() {
		Expect(render6()).To(ContainSubstring("Title"))
	})
	It("adds Created with date and author", func() {
		Expect(render6()).To(ContainSubstring("Created"))
		Expect(render6()).To(ContainSubstring("2026-07-01 by Trevor"))
	})
	It("adds Edited with relative, absolute and version", func() {
		Expect(render6()).To(ContainSubstring("Edited"))
		Expect(render6()).To(ContainSubstring("(2026-07-08)"))
		Expect(render6()).To(ContainSubstring("v2"))
	})
	It("adds Editors with counts, most active first", func() {
		Expect(render6()).To(ContainSubstring("Trevor (2), agent (1)"))
	})
	It("lists versions newest first", func() {
		out := render6()
		Expect(out).To(ContainSubstring("v2"))
		Expect(out).To(ContainSubstring("v1"))
	})
	It("includes the per-version change summary without a duplicate version prefix", func() {
		out := render6()
		Expect(out).To(ContainSubstring("+1/-1 lines"))
		Expect(out).To(ContainSubstring("+2/-0 lines"))
		Expect(out).NotTo(ContainSubstring("v2  v2")) // Detail's leading "v2  " is stripped
	})
	It("renders outgoing links and marks no incoming", func() {
		out := render6()
		Expect(out).To(ContainSubstring("relates"))
		Expect(out).To(ContainSubstring(render.ShortID(entity.Id("ffff0000ffff"))))
	})
	It("suppresses Edited when there is no set-body op", func() {
		var b bytes.Buffer
		render.ExtendedHeader(&b, snap, now, []entry.LogEntry{{Op: "create", Author: "Trevor", Time: now}}, entry.LinkView{}, false, "", nil)
		Expect(b.String()).NotTo(ContainSubstring("Edited"))
	})
})
