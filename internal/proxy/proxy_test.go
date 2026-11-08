package proxy

import (
	"net"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// A link that goes down must stop being used, and be used again when it returns.
func TestPickFollowsLinkState(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c // keep open
		}
	}()

	a := links.New("a", net.IPv4(127, 0, 0, 1))
	b := links.New("b", net.IPv4(127, 0, 0, 2))
	s := &Server{Pool: links.PoolOf(a, b), Sticky: 0, sticky: map[string]stickyEntry{}, rate: map[*links.Link]float64{}}

	count := func(n int) map[string]int {
		got := map[string]int{}
		var conns []net.Conn
		for i := 0; i < n; i++ {
			c, l, err := s.dial(ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			l.Active.Add(1) // as pipe() does while a connection is open
			conns = append(conns, c)
			got[l.Name]++
		}
		for _, c := range conns {
			c.Close()
		}
		a.Active.Store(0)
		b.Active.Store(0)
		return got
	}

	if got := count(6); got["a"] != 3 || got["b"] != 3 {
		t.Fatalf("both up: want 3/3 spread, got %v", got)
	}
	b.SetUp(false)
	if got := count(4); got["b"] != 0 {
		t.Fatalf("b down: still used %v", got)
	}
	b.SetUp(true)
	if got := count(6); got["b"] == 0 {
		t.Fatalf("b back up: not used %v", got)
	}
	_ = time.Second
}
