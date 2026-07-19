package watermark_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWatermark(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Watermark Suite")
}
