package mcpserver_test

import (
	"testing"

	gogit "github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestMCPServer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MCP Server Suite")
}

func gitRepo() string {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	_, err := gogit.PlainInit(dir, false)
	Expect(err).NotTo(HaveOccurred())
	return dir
}
