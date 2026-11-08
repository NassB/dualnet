package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
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
