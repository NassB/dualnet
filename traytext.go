package main

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

type linkSpeed struct {
	Name string
	Mbps float64
}

func fmtMbps(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// trayTooltip builds the notification-area tooltip: the version, the state and,
// when data is flowing, the download speed in total and per link.
func trayTooltip(version, state string, speeds []linkSpeed) string {
	var total float64
	var parts []string
	for _, s := range speeds {
		total += s.Mbps
		if s.Mbps >= 0.5 {
			parts = append(parts, s.Name+" "+fmtMbps(s.Mbps))
		}
	}
	text := "dualnet " + version + " — " + state
	if total >= 0.5 {
		text += ", " + fmtMbps(total) + " Mb/s"
		if len(parts) > 1 {
			text += " (" + strings.Join(parts, " · ") + ")"
		}
	}
	return truncateUTF16(text, 127)
}

// truncateUTF16 cuts s so that it fits in n UTF-16 code units (Windows keeps at
// most 127 characters of a tooltip), ending with an ellipsis when it was cut.
func truncateUTF16(s string, n int) string {
	if len(utf16.Encode([]rune(s))) <= n {
		return s
	}
	var out []rune
	units := 0
	for _, r := range s {
		w := len(utf16.Encode([]rune{r}))
		if units+w > n-1 { // leave room for the ellipsis
			break
		}
		out = append(out, r)
		units += w
	}
	return string(out) + "…"
}
