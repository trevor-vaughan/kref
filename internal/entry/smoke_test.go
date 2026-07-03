package entry_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/git-bug/git-bug/entity/dag"
)

var _ = Describe("git-bug linkage", func() {
	It("constructs a dag.Definition", func() {
		def := dag.Definition{Typename: "kref entry", Namespace: "kref-shared", FormatVersion: 1}
		Expect(def.Namespace).To(Equal("kref-shared"))
	})
})
