package watermark_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/watermark"
)

var _ = Describe("watermark store", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
	})

	It("reports not-present before anything is set", func() {
		_, ok, err := watermark.Get(watermark.Key("/repo", "id1", "me@x"))
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("round-trips a seen body per key", func() {
		k := watermark.Key("/repo", "id1", "me@x")
		Expect(watermark.Set(k, "# body one")).To(Succeed())
		got, ok, err := watermark.Get(k)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal("# body one"))
	})

	It("keeps distinct keys independent and overwrites the same key", func() {
		k1 := watermark.Key("/repo", "id1", "me@x")
		k2 := watermark.Key("/repo", "id2", "me@x")
		Expect(watermark.Set(k1, "one")).To(Succeed())
		Expect(watermark.Set(k2, "two")).To(Succeed())
		Expect(watermark.Set(k1, "one-b")).To(Succeed())
		g1, _, _ := watermark.Get(k1)
		g2, _, _ := watermark.Get(k2)
		Expect(g1).To(Equal("one-b"))
		Expect(g2).To(Equal("two"))
	})
})
