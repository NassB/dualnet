package procinfo

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The test process owns both ends of a loopback connection, so the tables must
// name this very executable.
func TestAppForOwnConnections(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Base(exe)
	lo := netip.MustParseAddr("127.0.0.1")

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			if _, err := ln.Accept(); err != nil {
				return
			}
		}
	}()
	c, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	port := uint16(c.LocalAddr().(*net.TCPAddr).Port)
	if got := AppFor("tcp", lo, port); !strings.EqualFold(got, want) {
		t.Errorf("tcp port %d: %q, want %q", port, got, want)
	}

	u, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	if got := AppFor("udp", lo, uint16(u.LocalAddr().(*net.UDPAddr).Port)); !strings.EqualFold(got, want) {
		t.Errorf("udp: %q, want %q", got, want)
	}
	// an UDP socket bound to every address is found by its port too
	w, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if got := AppFor("udp", lo, uint16(w.LocalAddr().(*net.UDPAddr).Port)); !strings.EqualFold(got, want) {
		t.Errorf("udp wildcard: %q, want %q", got, want)
	}

	// a connection that does not exist
	if got := AppFor("tcp", lo, 1); got != "" {
		t.Errorf("unknown endpoint: %q, want empty", got)
	}
	// same port, wrong local address: must not match a TCP socket of another address
	if got := AppFor("tcp", netip.MustParseAddr("198.18.0.1"), port); got != "" {
		t.Errorf("wrong local address matched: %q", got)
	}
}

// A new connection is found even if the cached tables are older than it.
func TestAppForSeesNewConnections(t *testing.T) {
	AppFor("tcp", netip.MustParseAddr("127.0.0.1"), 1) // fill the cache
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			if _, err := ln.Accept(); err != nil {
				return
			}
		}
	}()
	for i := 0; i < 20; i++ {
		c, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		port := uint16(c.LocalAddr().(*net.TCPAddr).Port)
		if AppFor("tcp", netip.MustParseAddr("127.0.0.1"), port) == "" {
			c.Close()
			t.Fatalf("connection %d (port %d) not found right after it was opened", i, port)
		}
		c.Close()
	}
}

// A burst of simultaneous new connections (a browser, a download tool…) must all be named.
func TestAppForBurst(t *testing.T) {
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			if _, err := ln.Accept(); err != nil {
				return
			}
		}
	}()
	exe, _ := os.Executable()
	want := filepath.Base(exe)
	const n = 100
	errs := make(chan string, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			c, err := net.Dial("tcp4", ln.Addr().String())
			if err != nil {
				errs <- err.Error()
				return
			}
			defer c.Close()
			port := uint16(c.LocalAddr().(*net.TCPAddr).Port)
			if got := AppFor("tcp", netip.MustParseAddr("127.0.0.1"), port); !strings.EqualFold(got, want) {
				errs <- "port " + string(rune('0')) + ": got " + got
				return
			}
			errs <- ""
		}()
	}
	t0 := timeNow()
	close(start)
	bad := 0
	for i := 0; i < n; i++ {
		if e := <-errs; e != "" {
			bad++
			if bad <= 3 {
				t.Error(e)
			}
		}
	}
	t.Logf("%d concurrent connections identified in %v (%d missed)", n, sinceNow(t0), bad)
	if bad > 0 {
		t.Fatalf("%d of %d connections not identified", bad, n)
	}
}
