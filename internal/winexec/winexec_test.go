//go:build windows

package winexec

import "testing"

// Helper programs must never open a console window (they would flash on screen
// every time dualnet refreshes something in the background).
func TestCommandHasNoWindow(t *testing.T) {
	c := Command("netsh", "interface", "ipv4", "show", "interfaces")
	if c.SysProcAttr == nil || !c.SysProcAttr.HideWindow || c.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("SysProcAttr = %+v, want HideWindow and CREATE_NO_WINDOW", c.SysProcAttr)
	}
	if out, err := c.Output(); err != nil || len(out) == 0 {
		t.Fatalf("netsh still has to work: %v (%d bytes)", err, len(out))
	}
}
