package commentguard_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCommentguard(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Commentguard Suite")
}
