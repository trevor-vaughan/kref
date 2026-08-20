//go:build windows

package store

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// stillActive is the value GetExitCodeProcess reports for a process that has
// not exited (the Win32 STILL_ACTIVE constant). x/sys/windows does not export
// it, so it is spelled out here.
const stillActive uint32 = 259

// detachSysProcAttr returns the attributes that detach the background refresh
// child, the Windows counterpart to setsid. DETACHED_PROCESS gives the child
// no console, so it neither inherits nor holds the parent's terminal, and
// CREATE_NEW_PROCESS_GROUP stops a console Ctrl+C from propagating into it.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// pidAlive reports whether a process with pid exists and is still running.
//
// Windows has no signal 0, and OpenProcess succeeding is not on its own proof
// of life: a handle to an already-exited process still opens until the last
// handle to it closes. Querying the exit code is what actually separates
// running from finished, which is what acquireBuildLock needs in order to
// reclaim a lock left behind by a crashed refresh.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
