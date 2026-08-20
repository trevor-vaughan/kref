//go:build !windows

package store

import (
	"os"
	"syscall"
)

// detachSysProcAttr returns the attributes that put the background refresh
// child in its own session. Setsid detaches it from the controlling terminal,
// so it outlives the shell that spawned the completion helper and can never
// steal terminal input from it.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// pidAlive reports whether a process with pid exists. Signal 0 performs error
// checking without delivering a signal (ESRCH => gone).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
