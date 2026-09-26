package entry_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestEntry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Entry Suite")
}
