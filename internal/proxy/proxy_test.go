package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// A link that goes down must stop being used, and be used again when it returns.
func TestPickFollowsLinkState(t *testing.T) {
	needSecondLoopback(t)
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
	s := &Server{Pool: links.PoolOf(a, b), Sticky: 0}

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

// sink accepts TCP connections and keeps them open.
func sink(t *testing.T) net.Listener {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			if _, err := ln.Accept(); err != nil {
				return
			}
		}
	}()
	return ln
}

// Connections are spread in proportion to the links' measured capacity.
func TestPickWeightsByCapacity(t *testing.T) {
	needSecondLoopback(t)
	ln := sink(t)
	defer ln.Close()
	fast := links.New("fast", net.IPv4(127, 0, 0, 1))
	slow := links.New("slow", net.IPv4(127, 0, 0, 2))
	s := &Server{Pool: links.PoolOf(fast, slow)}
	s.init()
	s.mu.Lock()
	s.rate[fast], s.rate[slow] = 400, 80
	s.mu.Unlock()
	got := map[string]int{}
	for i := 0; i < 24; i++ {
		c, l, err := s.dial(ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		got[l.Name]++
	}
	// 400:80 = 5:1 -> 20 / 4
	if got["fast"] < 19 || got["fast"] > 21 {
		t.Fatalf("want ~20/4 split, got %v", got)
	}
}

// Idle connections (apps keeping sockets open) must not make a link look busy.
func TestIdleConnectionsDoNotCount(t *testing.T) {
	ln := sink(t)
	defer ln.Close()
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	b := links.New("b", net.IPv4(127, 0, 0, 2))
	for i := 0; i < 10; i++ {
		c, err := a.DialContext(context.Background(), "tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
	}
	if a.Busy(time.Hour) != 10 {
		t.Fatalf("fresh connections should count as busy, got %d", a.Busy(time.Hour))
	}
	time.Sleep(60 * time.Millisecond)
	if n := a.Busy(50 * time.Millisecond); n != 0 {
		t.Fatalf("idle connections counted as busy: %d", n)
	}
	s := &Server{Pool: links.PoolOf(a, b), BusyWindow: 50 * time.Millisecond}
	s.init()
	s.mu.Lock()
	s.rate[a], s.rate[b] = 400, 80
	s.mu.Unlock()
	// a has 10 idle sockets but 5x the capacity: it must still be chosen.
	if l := s.pick("h")[0]; l != a {
		t.Fatalf("picked %s, want a (idle sockets ignored)", l.Name)
	}
}

// Plain HTTP requests through the proxy reuse one upstream connection.
func TestHTTPKeepAlive(t *testing.T) {
	var conns atomic.Int32
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok " + r.URL.Path))
	}))
	up.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns.Add(1)
		}
	}
	up.Start()
	defer up.Close()

	s := &Server{Pool: links.PoolOf(links.New("a", net.IPv4(127, 0, 0, 1)))}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	pu, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(up.URL + "/x")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(b) != "ok /x" {
			t.Fatalf("body %q", b)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("upstream connections = %d, want 1 (keep-alive)", n)
	}
}

// Links whose scores are within 10 % are a tie: the lowest ping wins.
func TestTieBreakByPing(t *testing.T) {
	near := links.New("near", net.IPv4(127, 0, 0, 1))
	far := links.New("far", net.IPv4(127, 0, 0, 2))
	near.SetLatencyMs(15)
	far.SetLatencyMs(70)
	s := &Server{Pool: links.PoolOf(far, near)} // far first on purpose
	s.init()

	set := func(rn, rf float64) {
		s.mu.Lock()
		s.rate[near], s.rate[far] = rn, rf
		s.mu.Unlock()
	}
	set(100, 100)
	if l := s.pick("h")[0]; l != near {
		t.Fatalf("equal capacity: picked %s, want near (lower ping)", l.Name)
	}
	set(100, 105) // 5 % better: still a tie
	if l := s.pick("h")[0]; l != near {
		t.Fatalf("5%% apart: picked %s, want near", l.Name)
	}
	set(100, 200) // clearly better: capacity wins over ping
	if l := s.pick("h")[0]; l != far {
		t.Fatalf("2x capacity: picked %s, want far", l.Name)
	}
	// an unknown ping never beats a known one
	far.SetLatencyMs(0)
	set(100, 100)
	if l := s.pick("h")[0]; l != near {
		t.Fatalf("unknown ping: picked %s, want near", l.Name)
	}
}

// DNS goes through the lowest-ping link even when another link has more capacity.
func TestDNSUsesLowestPing(t *testing.T) {
	near := links.New("near", net.IPv4(127, 0, 0, 1))
	far := links.New("far", net.IPv4(127, 0, 0, 2))
	near.SetLatencyMs(15)
	far.SetLatencyMs(70)
	s := &Server{Pool: links.PoolOf(far, near)}
	s.init()
	s.mu.Lock()
	s.rate[near], s.rate[far] = 10, 500
	s.mu.Unlock()

	if l := s.pick("h")[0]; l != far {
		t.Fatalf("bulk traffic: picked %s, want far (capacity)", l.Name)
	}
	if got := s.pickLowestLatency(); got[0] != near || got[1] != far {
		t.Fatalf("DNS order = %s,%s, want near,far", got[0].Name, got[1].Name)
	}
	if !isDNSPort("53") || !isDNSPort("853") || isDNSPort("443") || isDNSPort("5353") {
		t.Fatal("DNS port detection")
	}

	d := TunDialer{S: s}
	pc, err := d.DialUDP(&M.Metadata{Network: M.UDP, DstIP: netip.MustParseAddr("1.1.1.1"), DstPort: 53})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if ip := pc.LocalAddr().(*net.UDPAddr).IP; !ip.Equal(near.IP()) {
		t.Fatalf("UDP DNS left through %s, want the near link %s", ip, near.IP())
	}
	t.Run("non-DNS UDP", func(t *testing.T) {
		probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: far.IP()})
		if err != nil {
			t.Skipf("second loopback address unavailable for UDP: %v", err)
		}
		probe.Close()
		other, err := d.DialUDP(&M.Metadata{Network: M.UDP, DstIP: netip.MustParseAddr("1.1.1.1"), DstPort: 3478})
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		if ip := other.LocalAddr().(*net.UDPAddr).IP; !ip.Equal(far.IP()) {
			t.Fatalf("other UDP left through %s, want the high-capacity link %s", ip, far.IP())
		}
	})
}

// A sticky host must not stay on a link that became unusable (over budget, down…).
func TestStickyIgnoresUnusableLink(t *testing.T) {
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	b := links.New("b", net.IPv4(127, 0, 0, 2))
	s := &Server{Pool: links.PoolOf(a, b), Sticky: time.Hour}
	s.init()
	s.mu.Lock()
	s.rate[a], s.rate[b] = 500, 10
	s.mu.Unlock()
	if l := s.pick("site")[0]; l != a {
		t.Fatalf("first pick %s, want a", l.Name)
	}
	if l := s.pick("site")[0]; l != a {
		t.Fatal("sticky: must stay on a")
	}
	a.SetOverBudget(true)
	if l := s.pick("site")[0]; l != b {
		t.Fatalf("a is over budget: picked %s, want b", l.Name)
	}
}
