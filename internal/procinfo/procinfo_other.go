//go:build !windows

package procinfo

import "net/netip"

// AppFor is not available outside Windows.
func AppFor(proto string, ip netip.Addr, port uint16) string { return "" }
