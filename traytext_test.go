package main

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestTrayTooltip(t *testing.T) {
	cases := []struct {
		name   string
		state  string
		speeds []linkSpeed
		want   string
	}{
		{"off", "off", nil, "dualnet v0.4.0 — off"},
		{"idle", "on", []linkSpeed{{"Wi-Fi", 0.1}, {"Phone", 0}}, "dualnet v0.4.0 — on"},
		{"one link", "on", []linkSpeed{{"Wi-Fi", 312.4}}, "dualnet v0.4.0 — on, 312 Mb/s"},
		{"two links", "on", []linkSpeed{{"Wi-Fi", 250}, {"Phone", 61.5}}, "dualnet v0.4.0 — on, 312 Mb/s (Wi-Fi 250 · Phone 61.5)"},
		{"one link moving", "on", []linkSpeed{{"Wi-Fi", 42.25}, {"Phone", 0.2}}, "dualnet v0.4.0 — on, 42.5 Mb/s"},
		{"french", "actif", []linkSpeed{{"Wi-Fi", 8}}, "dualnet v0.4.0 — actif, 8.0 Mb/s"},
	}
	for _, c := range cases {
		if got := trayTooltip("v0.4.0", c.state, c.speeds); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// Windows keeps 127 UTF-16 units of a tooltip: longer text must be cut cleanly.
func TestTrayTooltipFitsWindowsLimit(t *testing.T) {
	long := []linkSpeed{{strings.Repeat("A very long adapter name ", 4), 100}, {strings.Repeat("😀", 30), 100}}
	got := trayTooltip("v0.4.0", "on", long)
	if n := len(utf16.Encode([]rune(got))); n > 127 {
		t.Fatalf("tooltip is %d UTF-16 units, Windows keeps 127: %q", n, got)
	}
	if !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "dualnet v0.4.0 — on, 200 Mb/s") {
		t.Fatalf("cut tooltip = %q", got)
	}
	// an emoji (two UTF-16 units) is never split in half
	if got := truncateUTF16("ab😀cd", 4); got != "ab…" {
		t.Fatalf("truncateUTF16 = %q, want %q", got, "ab…")
	}
	if got := truncateUTF16("short", 127); got != "short" {
		t.Fatalf("short text changed: %q", got)
	}
}
