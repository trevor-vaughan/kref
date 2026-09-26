package entry_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// The pair is deliberately not a complement, and the gap between them is the
// unresolved state. Folding the two together would let entries nobody has
// verified into `kref list --unsigned`, where every row asserts that the entry
// carries no signature.
var _ = Describe("SigState predicates", func() {
	It("treats every resolved verdict other than unsigned as carrying a signature", func() {
		Expect(entry.SigGood.Signed()).To(BeTrue())
		Expect(entry.SigBad.Signed()).To(BeTrue())
		Expect(entry.SigUntrusted.Signed()).To(BeTrue())
		Expect(entry.SigUnsigned.Signed()).To(BeFalse())
	})

	It("reports only the resolved unsigned verdict as unsigned", func() {
		Expect(entry.SigUnsigned.Unsigned()).To(BeTrue())
		Expect(entry.SigGood.Unsigned()).To(BeFalse())
		Expect(entry.SigBad.Unsigned()).To(BeFalse())
		Expect(entry.SigUntrusted.Unsigned()).To(BeFalse())
	})

	It("calls an unresolved state neither signed nor unsigned", func() {
		Expect(entry.SigUnresolved.Signed()).To(BeFalse())
		Expect(entry.SigUnresolved.Unsigned()).To(BeFalse())
	})
})
