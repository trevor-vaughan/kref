package render_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity"
	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
)

var _ = Describe("WhatsNew rendering", func() {
	It("prints incoming and unpushed sections", func() {
		var b bytes.Buffer
		render.WhatsNew(&b,
			[]*entry.Snapshot{{ID: entity.Id("aaa1"), Tier: "shared", Kind: "spec", Status: "open", Title: "FromTeam"}},
			[]*entry.Snapshot{{ID: entity.Id("bbb2"), Tier: "shared", Kind: "spec", Status: "open", Title: "MyLocal"}},
			false)
		out := b.String()
		Expect(out).To(ContainSubstring("Incoming"))
		Expect(out).To(ContainSubstring("FromTeam"))
		Expect(out).To(ContainSubstring("Unpushed"))
		Expect(out).To(ContainSubstring("MyLocal"))
	})
	It("reports nothing new when both are empty", func() {
		var b bytes.Buffer
		render.WhatsNew(&b, nil, nil, false)
		Expect(b.String()).To(ContainSubstring("nothing new"))
	})
})
