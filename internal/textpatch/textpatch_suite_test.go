package textpatch

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTextpatch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Textpatch Suite")
}
