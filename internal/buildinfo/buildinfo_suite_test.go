package buildinfo_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBuildinfo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Buildinfo Suite")
}
