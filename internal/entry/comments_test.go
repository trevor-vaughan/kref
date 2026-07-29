package entry_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("Snapshot.ResolveCommentID", func() {
	snap := func() *entry.Snapshot {
		return &entry.Snapshot{Comments: []entry.Comment{
			{ID: "aaaa1111bbbb2222"},
			{ID: "aaaa3333cccc4444"},
			{ID: "ffff5555dddd6666"},
		}}
	}

	It("returns the full id for an unambiguous prefix", func() {
		got, err := snap().ResolveCommentID("ffff5555")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("ffff5555dddd6666"))
	})

	It("returns the full id when given the full id", func() {
		got, err := snap().ResolveCommentID("aaaa1111bbbb2222")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("aaaa1111bbbb2222"))
	})

	It("errors when no comment matches", func() {
		_, err := snap().ResolveCommentID("deadbeef")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no comment matches"))
	})

	It("errors when the prefix matches more than one comment", func() {
		_, err := snap().ResolveCommentID("aaaa")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("ambiguous"))
	})

	It("errors on an empty prefix rather than matching everything", func() {
		_, err := snap().ResolveCommentID("")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Snapshot.FindComment", func() {
	snap := &entry.Snapshot{Comments: []entry.Comment{
		{ID: "aaaa1111", Question: true},
		{ID: "bbbb2222"},
	}}

	It("returns the comment with the given full id", func() {
		c := snap.FindComment("aaaa1111")
		Expect(c).NotTo(BeNil())
		Expect(c.Question).To(BeTrue())
	})

	It("returns nil for an id that is not present", func() {
		Expect(snap.FindComment("cccc3333")).To(BeNil())
	})

	It("does not match on a prefix", func() {
		Expect(snap.FindComment("aaaa")).To(BeNil())
	})
})

var _ = Describe("ActorModel", func() {
	It("returns the model half of a composed actor", func() {
		Expect(entry.ActorModel("claude-opus-5 via claude-code/2.1.220")).To(Equal("claude-opus-5"))
	})

	It("returns an uncomposed actor unchanged", func() {
		Expect(entry.ActorModel("some-agent")).To(Equal("some-agent"))
	})

	It("returns empty for an empty actor", func() {
		Expect(entry.ActorModel("")).To(BeEmpty())
	})

	It("splits on the first separator so a client name containing it survives", func() {
		Expect(entry.ActorModel("m via a via b")).To(Equal("m"))
	})
})
