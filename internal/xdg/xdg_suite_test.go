package xdg

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestXDG(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "XDG Suite")
}
