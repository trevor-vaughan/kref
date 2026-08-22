//go:build windows

package store

import "os/exec"

// sleeperCmd returns a command that starts and exits immediately, used to
// produce a pid that is certain to be dead by the time it is probed.
func sleeperCmd() *exec.Cmd { return exec.Command("cmd", "/c", "exit") }
