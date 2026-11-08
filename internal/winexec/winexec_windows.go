// Package winexec runs helper programs (netsh, schtasks, tasklist…) without
// flashing a console window. dualnet has no console of its own in app mode, so
// Windows would open and close a black window for every child process.
package winexec

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// Command is exec.Command with no console window.
func Command(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c
}
