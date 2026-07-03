package textdiff

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTextdiff(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Textdiff Suite")
}
