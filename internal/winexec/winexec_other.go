//go:build !windows

package winexec

import "os/exec"

// Command is exec.Command (nothing to hide outside Windows).
func Command(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
