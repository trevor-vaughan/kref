package entryguard_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEntryguard(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Entryguard Suite")
}
