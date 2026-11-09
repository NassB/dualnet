package proxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// a = primary Wi-Fi (400 Mb/s measured), b = overflow phone (80 Mb/s measured)
func overflowSetup() (*Server, *links.Link, *links.Link) {
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	b := links.New("b", net.IPv4(127, 0, 0, 2))
	s := &Server{Pool: links.PoolOf(a, b), Overflow: func(name string) bool { return name == "b" }}
	s.SetSpeedTest(a, 400, time.Now())
	s.SetSpeedTest(b, 80, time.Now())
	return s, a, b
}

func eval(s *Server, cur map[*links.Link]float64) {
	s.mu.Lock()
	s.evalOverflow(cur)
	s.mu.Unlock()
}

func spread(t *testing.T, s *Server, n int) map[string]int {
	t.Helper()
	ln := sink(t)
	defer ln.Close()
	got := map[string]int{}
	for i := 0; i < n; i++ {
		c, l, err := s.dial(ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		got[l.Name]++
	}
	return got
}

func TestOverflowStaysIdleUntilBusy(t *testing.T) {
	needSecondLoopback(t)
	s, a, b := overflowSetup()
	if got := spread(t, s, 20); got["b"] != 0 || got["a"] != 20 {
		t.Fatalf("overflow link used while the primary is free: %v", got)
	}
	if !s.Standby(b) || s.Standby(a) {
		t.Fatal("b should be on standby, a not")
	}
	// it is still the failover: listed last
	if order := s.pick("site"); len(order) != 2 || order[0] != a || order[1] != b {
		t.Fatalf("order = %v, want a then b", order)
	}

	eval(s, map[*links.Link]float64{a: 350}) // 87 % of 400
	if s.Standby(b) {
		t.Fatal("the primary is nearly full: b must be in play")
	}
	if got := spread(t, s, 24); got["b"] < 3 || got["a"] < 15 {
		t.Fatalf("with the overflow open the load follows the capacities (~20/4): %v", got)
	}
}

func TestOverflowHysteresis(t *testing.T) {
	s, a, b := overflowSetup()
	low, high := map[*links.Link]float64{a: 100}, map[*links.Link]float64{a: 300}
	eval(s, map[*links.Link]float64{a: 330}) // 82 %: opens
	if s.Standby(b) {
		t.Fatal("did not open")
	}
	for i := 0; i < 4; i++ {
		eval(s, low)
	}
	if s.Standby(b) {
		t.Fatal("closed after only 4 s of low load")
	}
	eval(s, high) // busy again: the countdown starts over
	for i := 0; i < 4; i++ {
		eval(s, low)
	}
	if s.Standby(b) {
		t.Fatal("the countdown was not reset by a busy second")
	}
	eval(s, low) // the 5th low second in a row
	if !s.Standby(b) {
		t.Fatal("did not close after 5 low seconds in a row")
	}
	// 60 % is between the two thresholds: stays as it is, in both states
	eval(s, map[*links.Link]float64{a: 240})
	if !s.Standby(b) {
		t.Fatal("60% load must not open the overflow")
	}
}

func TestOverflowNeedsAKnownCapacity(t *testing.T) {
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	b := links.New("b", net.IPv4(127, 0, 0, 2))
	s := &Server{Pool: links.PoolOf(a, b), Overflow: func(n string) bool { return n == "b" }}
	s.init()
	s.mu.Lock()
	s.rate[a] = 8 // a tiny peak: 5 of 8 Mb/s would look "full" but means nothing
	s.mu.Unlock()
	eval(s, map[*links.Link]float64{a: 7})
	if !s.Standby(b) {
		t.Fatal("opened on an unreliable, tiny capacity estimate")
	}
}

func TestOverflowNeverCutsAnyoneOff(t *testing.T) {
	needSecondLoopback(t)
	s, a, b := overflowSetup()
	// no usable primary: the overflow link serves everything
	a.SetUp(false)
	if got := spread(t, s, 6); got["b"] != 6 {
		t.Fatalf("primary down: %v, want everything on b", got)
	}
	if s.Standby(b) {
		t.Fatal("b is the only link: it cannot be on standby")
	}
	a.SetUp(true)
	// every link marked overflow: nothing is held back
	s.Overflow = func(string) bool { return true }
	if got := spread(t, s, 12); got["a"] == 0 || got["b"] == 0 {
		t.Fatalf("all links overflow: %v, want both used", got)
	}
}

func TestOverflowAndDNSAndSticky(t *testing.T) {
	s, a, b := overflowSetup()
	a.SetLatencyMs(30)
	b.SetLatencyMs(5) // the phone answers faster, but it is on standby
	if got := s.pickLowestLatency(); got[0] != a || got[1] != b {
		t.Fatalf("DNS order with b on standby = %s,%s, want a,b", got[0].Name, got[1].Name)
	}
	eval(s, map[*links.Link]float64{a: 350})
	if got := s.pickLowestLatency(); got[0] != b {
		t.Fatalf("DNS order with b in play = %s first, want b (lowest ping)", got[0].Name)
	}
	// a site stuck to b while it was in play leaves it when it goes back on standby
	s.Sticky = time.Hour
	s.mu.Lock()
	s.sticky["site"] = stickyEntry{b, time.Now()}
	s.mu.Unlock()
	if l := s.pick("site")[0]; l != b {
		t.Fatalf("sticky while open: %s, want b", l.Name)
	}
	for i := 0; i < 5; i++ {
		eval(s, map[*links.Link]float64{a: 10})
	}
	if l := s.pick("site")[0]; l != a {
		t.Fatalf("b is back on standby: sticky site went to %s, want a", l.Name)
	}
}

// A rule that names the overflow link is an explicit choice: it wins.
func TestOverflowRuleWins(t *testing.T) {
	needSecondLoopback(t)
	ln := echoServer(t)
	defer ln.Close()
	s, _, b := overflowSetup()
	s.Rules = func(app string) []string {
		if app == "game.exe" {
			return []string{"b"}
		}
		return nil
	}
	d := TunDialer{S: s, AppOf: fakeApps}
	if !s.Standby(b) {
		t.Fatal("b must be on standby for this test")
	}
	c, err := d.DialContext(context.Background(), tcpFlow(ln, 1)) // game.exe
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := linkOf(t, c); got != "b" {
		t.Fatalf("game.exe is restricted to b by a rule but went through %s", got)
	}
	other, err := d.DialContext(context.Background(), tcpFlow(ln, 2)) // Launcher.exe: no rule
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if got := linkOf(t, other); got != "a" {
		t.Fatalf("Launcher.exe went through %s while b is on standby, want a", got)
	}
}
