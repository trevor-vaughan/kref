package todoguard_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTodoguard(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Todoguard Suite")
}
