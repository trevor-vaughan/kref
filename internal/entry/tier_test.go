package entry_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/riotbox/kref/internal/entry"
)

var _ = Describe("TierDef", func() {
	It("identifies the built-ins", func() {
		defs := entry.BuiltinTierDefs()
		Expect(defs).To(HaveLen(3))
		for _, d := range defs {
			Expect(d.Builtin()).To(BeTrue())
			Expect(d.Declared).To(BeTrue())
			Expect(d.Name).To(Equal(d.Type))
		}
		Expect(entry.TierDef{Name: "research", Type: entry.TierPersonal}.Builtin()).To(BeFalse())
	})

	DescribeTable("ValidateTierName",
		func(name string, ok bool) {
			err := entry.ValidateTierName(name)
			if ok {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		EntryDescription("%q -> valid=%t"),
		Entry(nil, "research", true),
		Entry(nil, "team-x", true),
		Entry(nil, "a1", true),
		Entry(nil, "private", false),
		Entry(nil, "personal", false),
		Entry(nil, "shared", false),
		Entry(nil, "pushed", false),
		Entry(nil, "incoming", false),
		Entry(nil, "a", false),
		Entry(nil, "1abc", false),
		Entry(nil, "Team", false),
		Entry(nil, "has_underscore", false),
		Entry(nil, "abcdefghijklmnopqrstuvwxyz-0123456789", false),
		Entry(nil, "", false),
	)

	It("accepts only personal|shared as custom tier types", func() {
		Expect(entry.ValidTierType(entry.TierPersonal)).To(BeTrue())
		Expect(entry.ValidTierType(entry.TierShared)).To(BeTrue())
		Expect(entry.ValidTierType(entry.TierPrivate)).To(BeFalse())
		Expect(entry.ValidTierType(entry.Tier("bogus"))).To(BeFalse())
	})

	It("sorts private, then personal-typed (builtin first, customs alpha), then shared-typed", func() {
		defs := []entry.TierDef{
			{Name: "zeta", Type: entry.TierShared, Declared: false},
			{Name: entry.TierShared, Type: entry.TierShared, Declared: true},
			{Name: "research", Type: entry.TierPersonal, Declared: true},
			{Name: entry.TierPrivate, Type: entry.TierPrivate, Declared: true},
			{Name: "alpha", Type: entry.TierShared, Declared: true},
			{Name: entry.TierPersonal, Type: entry.TierPersonal, Declared: true},
		}
		entry.SortTierDefs(defs)
		names := make([]entry.Tier, len(defs))
		for i, d := range defs {
			names[i] = d.Name
		}
		Expect(names).To(Equal([]entry.Tier{"private", "personal", "research", "shared", "alpha", "zeta"}))
	})
})
