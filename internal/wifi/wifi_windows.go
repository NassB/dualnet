//go:build windows

package wifi

import "github.com/NassB/dualnet/internal/winexec"

// CurrentSSID returns the Wi-Fi network the PC is connected to ("" = none or no Wi-Fi).
func CurrentSSID() string {
	out, err := winexec.Command("netsh", "wlan", "show", "interfaces").Output()
	if err != nil {
		return ""
	}
	return ParseSSID(string(out))
}
