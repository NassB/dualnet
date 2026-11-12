// Package wifi tells which Wi-Fi network the PC is connected to, so that a
// profile can be applied by itself (home, office, hotel).
package wifi

import "strings"

// ParseSSID reads the output of `netsh wlan show interfaces` and returns the
// name of the connected network, or "" when none is. The field is called SSID
// in every Windows language (BSSID, the access point's address, is another field).
func ParseSSID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "SSID") {
			continue
		}
		if v := strings.TrimSpace(val); v != "" {
			return v
		}
	}
	return ""
}

// Valid reports whether s can be a network name (1 to 32 bytes, no control character).
func Valid(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
