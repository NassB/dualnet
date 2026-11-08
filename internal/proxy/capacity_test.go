package proxy

import (
	"net"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

func TestCapacityEstimate(t *testing.T) {
	l := links.New("a", net.IPv4(127, 0, 0, 1))
	s := &Server{Pool: links.PoolOf(l)}
	s.init()
	set := func(rate float64) { s.mu.Lock(); s.rate[l] = rate; s.mu.Unlock() }

	set(10)
	s.SetSpeedTest(l, 400, time.Now())
	if got := s.Capacity(l); got != 400 {
		t.Fatalf("a fresh speed test seeds the capacity: %v, want 400", got)
	}
	set(500) // traffic showed more than the test
	if got := s.Capacity(l); got != 500 {
		t.Fatalf("a higher measured peak wins: %v, want 500", got)
	}
	set(10)
	s.SetSpeedTest(l, 400, time.Now().Add(-8*24*time.Hour))
	if got := s.Capacity(l); got != 10 {
		t.Fatalf("a week-old test is ignored: %v, want 10", got)
	}
	s.SetSpeedTest(l, 400, time.Now())
	l.SetLimitMbps(50)
	if got := s.Capacity(l); got != 50 {
		t.Fatalf("the speed cap bounds the capacity: %v, want 50", got)
	}
}

// A speed test changes how connections are spread: 5x the capacity, 5x the connections.
func TestSpeedTestWeightsTheSpread(t *testing.T) {
	ln := sink(t)
	defer ln.Close()
	fast := links.New("fast", net.IPv4(127, 0, 0, 1))
	slow := links.New("slow", net.IPv4(127, 0, 0, 2))
	s := &Server{Pool: links.PoolOf(fast, slow)}
	s.SetSpeedTest(fast, 400, time.Now())
	s.SetSpeedTest(slow, 80, time.Now())
	got := map[string]int{}
	for i := 0; i < 24; i++ {
		c, l, err := s.dial(ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		got[l.Name]++
	}
	if got["fast"] < 19 || got["fast"] > 21 {
		t.Fatalf("want ~20/4 from the seeded capacities, got %v", got)
	}
}
