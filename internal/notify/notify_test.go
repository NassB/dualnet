package notify

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

type sink struct {
	mu   sync.Mutex
	msgs []string
}

func (s *sink) send(title, body string) error {
	s.mu.Lock()
	s.msgs = append(s.msgs, title+"|"+body)
	s.mu.Unlock()
	return nil
}

func (s *sink) get() []string {
	// events are sent from goroutines
	time.Sleep(50 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.msgs...)
}

func TestMessages(t *testing.T) {
	var s sink
	n := &Notifier{Send: s.send}
	fr := &Notifier{Send: s.send, French: true}
	cases := []struct {
		n            *Notifier
		ev           string
		up           int
		wantContains string
	}{
		{n, links.EventAdded, 2, "combining 2 links"},
		{n, links.EventReconnected, 1, "is connected."},
		{n, links.EventDisconnected, 1, "keeps using the other links"},
		{n, links.EventDisconnected, 0, "No link left"},
		{n, links.EventNotAnswering, 1, "skipped"},
		{n, links.EventAnswering, 2, "again"},
		{fr, links.EventAdded, 2, "2 liens combinés"},
		{fr, links.EventDisconnected, 0, "Plus aucun lien"},
	}
	for _, c := range cases {
		body, ok := c.n.message("Wi-Fi", c.ev, c.up)
		if !ok || !strings.Contains(body, c.wantContains) || !strings.Contains(body, "Wi-Fi") {
			t.Errorf("%s up=%d: %q (ok=%v), want it to contain %q", c.ev, c.up, body, ok, c.wantContains)
		}
	}
	if _, ok := n.message("x", "something else", 1); ok {
		t.Error("unknown events must not notify")
	}
}

func TestRateLimitAndSwitch(t *testing.T) {
	var s sink
	clock := time.Unix(1000, 0)
	on := true
	n := &Notifier{Send: s.send, Enabled: func() bool { return on }, now: func() time.Time { return clock }}

	n.LinkEvent("Wi-Fi", links.EventDisconnected, 1)
	n.LinkEvent("Wi-Fi", links.EventReconnected, 2) // 0 s later: flapping, skipped
	n.LinkEvent("Ethernet 2", links.EventAdded, 2)  // another link: not limited
	if got := s.get(); len(got) != 2 {
		t.Fatalf("flapping: %d notifications, want 2: %v", len(got), got)
	}
	clock = clock.Add(25 * time.Second)
	n.LinkEvent("Wi-Fi", links.EventReconnected, 2)
	if got := s.get(); len(got) != 3 {
		t.Fatalf("after the gap: %d notifications, want 3", len(got))
	}
	on = false
	clock = clock.Add(time.Hour)
	n.LinkEvent("Wi-Fi", links.EventDisconnected, 1)
	if got := s.get(); len(got) != 3 {
		t.Fatalf("switched off: still notified (%d)", len(got))
	}
	// the test button ignores the setting
	if err := n.Test(); err != nil || len(s.get()) != 4 {
		t.Fatalf("Test(): err=%v, notifications=%d, want 4", err, len(s.get()))
	}
}
