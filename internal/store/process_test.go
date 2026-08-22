package store

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pidAlive and detachSysProcAttr have a per-GOOS implementation. These specs
// pin the contract both must satisfy, so the Windows port cannot silently
// regress the lock reclamation that acquireBuildLock depends on.
var _ = Describe("Process control", func() {
	Describe("pidAlive", func() {
		It("reports a running process as alive", func() {
			Expect(pidAlive(os.Getpid())).To(BeTrue())
		})

		It("reports an unused pid as dead, so a stale lock is reclaimable", func() {
			// Above any realistic pid_max on the platforms kref ships to, so
			// nothing can legitimately own it.
			Expect(pidAlive(2147483647)).To(BeFalse())
		})

		It("reports a reaped child as dead rather than alive", func() {
			// A process that has exited must not read as alive, or a crashed
			// refresh would wedge its tier's build lock forever.
			cmd := sleeperCmd()
			Expect(cmd.Start()).To(Succeed())
			pid := cmd.Process.Pid
			Expect(cmd.Wait()).To(Succeed())
			Expect(pidAlive(pid)).To(BeFalse())
		})
	})

	Describe("detachSysProcAttr", func() {
		It("returns attributes that detach the child from this process", func() {
			Expect(detachSysProcAttr()).NotTo(BeNil())
		})
	})
})
