//go:build windows

package winsys

import "testing"

func TestMetricRowParsing(t *testing.T) {
	cases := map[string][2]string{
		"  3          35        1500  connected     Wi-Fi\r":                       {"35", "Wi-Fi"},
		" 12          25        1500  connected     Ethernet 2":                    {"25", "Ethernet 2"},
		"  7          10        1500  disconnected  HotspotShield Network Adapter": {"10", "HotspotShield Network Adapter"},
	}
	for line, want := range cases {
		r := rowRe.FindStringSubmatch(line)
		if r == nil || r[1] != want[0] || r[2] != want[1] {
			t.Errorf("%q -> %v, want %v", line, r, want)
		}
	}
	if rowRe.FindStringSubmatch("---  ----------  ----------") != nil {
		t.Error("separator line must not match")
	}
}
