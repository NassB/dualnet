package store

import (
	"net"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

func TestRecordAndReload(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	l := links.New("Wi-Fi", net.IPv4(127, 0, 0, 1))
	day1 := time.Date(2026, 11, 7, 23, 0, 0, 0, time.Local)

	l.BytesIn.Store(100) // traffic before the first sample is not counted
	s.Record([]*links.Link{l}, day1)
	l.BytesIn.Store(1100)
	l.BytesOut.Store(50)
	s.Record([]*links.Link{l}, day1.Add(time.Minute))
	l.BytesIn.Store(1600)
	s.Record([]*links.Link{l}, day1.Add(2*time.Hour)) // next day

	l.SetLimitMbps(40)
	s.RememberLimits([]*links.Link{l})

	r := Open(dir)
	h := r.History(10)
	if len(h) != 2 || h[0].Date != "2026-11-08" || h[1].Date != "2026-11-07" {
		t.Fatalf("days = %+v", h)
	}
	if got := h[1].Links["Wi-Fi"]; got.Rx != 1000 || got.Tx != 50 {
		t.Fatalf("day 1 = %+v, want rx 1000 tx 50", got)
	}
	if got := h[0].Links["Wi-Fi"]; got.Rx != 500 {
		t.Fatalf("day 2 = %+v, want rx 500", got)
	}
	l2 := links.New("Wi-Fi", net.IPv4(127, 0, 0, 1))
	r.ApplyLimit(l2)
	if l2.LimitMbps() != 40 {
		t.Fatalf("saved cap not applied: %d", l2.LimitMbps())
	}
}
