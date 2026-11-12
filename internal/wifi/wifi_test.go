package wifi

import "testing"

func TestParseSSID(t *testing.T) {
	en := "There is 1 interface on the system:\r\n\r\n    Name                   : Wi-Fi\r\n    State                  : connected\r\n    SSID                   : Home Net\r\n    BSSID                  : aa:bb:cc:dd:ee:ff\r\n    Network type           : Infrastructure\r\n"
	if got := ParseSSID(en); got != "Home Net" {
		t.Fatalf("%q", got)
	}
	fr := "    Nom                    : Wi-Fi\n    État                   : connecté\n    SSID                   : Café: Wifi\n    BSSID                  : aa:bb\n"
	if got := ParseSSID(fr); got != "Café: Wifi" {
		t.Fatalf("a name containing a colon: %q", got)
	}
	off := "    Name                   : Wi-Fi\n    State                  : disconnected\n"
	if ParseSSID(off) != "" || ParseSSID("") != "" || ParseSSID("The Wireless AutoConfig Service (wlansvc) is not running.") != "" {
		t.Fatal("no network, no name")
	}
	if ParseSSID("    BSSID                  : aa:bb\n") != "" {
		t.Fatal("BSSID is not the network name")
	}
}

func TestValid(t *testing.T) {
	for s, want := range map[string]bool{"Home": true, "": false, "a\x00b": false, "x\ny": false,
		"12345678901234567890123456789012": true, "123456789012345678901234567890123": false} {
		if Valid(s) != want {
			t.Errorf("Valid(%q) != %v", s, want)
		}
	}
}
