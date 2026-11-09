package ui

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/NassB/dualnet/internal/store"
)

func TestHistoryCSV(t *testing.T) {
	var b bytes.Buffer
	writeHistoryCSV(csv.NewWriter(&b), []store.Day{{Date: "2026-11-09", Links: map[string]store.Usage{
		"Wi-Fi": {Rx: 10, Tx: 2}, "Ethernet, 2": {Rx: 5, Tx: 1}}}})
	want := "date,link,rx_bytes,tx_bytes\n2026-11-09,\"Ethernet, 2\",5,1\n2026-11-09,Wi-Fi,10,2\n"
	if got := b.String(); got != want {
		t.Fatalf("got %q\nwant %q", got, strings.TrimSpace(want))
	}
}
