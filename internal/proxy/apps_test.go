package proxy

import (
	"context"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

func echoServer(t *testing.T) net.Listener {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go io.Copy(c, c)
		}
	}()
	return ln
}

// the source port of a flow selects the fake application
func fakeApps(md *M.Metadata) string {
	switch md.SrcPort {
	case 1:
		return "game.exe"
	case 2:
		return "Launcher.exe"
	}
	return "" // unknown
}

func tcpFlow(ln net.Listener, srcPort uint16) *M.Metadata {
	ap := netip.MustParseAddrPort(ln.Addr().String())
	return &M.Metadata{Network: M.TCP, SrcPort: srcPort, DstIP: ap.Addr(), DstPort: ap.Port()}
}

func linkOf(t *testing.T, c net.Conn) string {
	ac, ok := c.(*activeConn)
	if !ok {
		t.Fatalf("connection is a %T, want *activeConn", c)
	}
	return ac.link.Name
}

func TestRulesRestrictApplications(t *testing.T) {
	needSecondLoopback(t)
	ln := echoServer(t)
	defer ln.Close()
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	b := links.New("b", net.IPv4(127, 0, 0, 2))
	rules := map[string][]string{"game.exe": {"b"}}
	s := &Server{Pool: links.PoolOf(a, b), Rules: func(app string) []string { return rules[app] }}
	d := TunDialer{S: s, AppOf: fakeApps}

	dial := func(srcPort uint16) net.Conn {
		t.Helper()
		c, err := d.DialContext(context.Background(), tcpFlow(ln, srcPort))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	var open []net.Conn
	defer func() {
		for _, c := range open {
			c.Close()
		}
	}()

	// the restricted application only uses b
	for i := 0; i < 6; i++ {
		c := dial(1)
		open = append(open, c)
		if got := linkOf(t, c); got != "b" {
			t.Fatalf("game.exe went through %s, want b", got)
		}
	}
	// an unrestricted application is spread over both links
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		c := dial(2)
		open = append(open, c)
		seen[linkOf(t, c)] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("Launcher.exe should use both links, used %v", seen)
	}

	// the chosen link goes away: the app keeps working on the other one
	b.SetUp(false)
	c := dial(1)
	open = append(open, c)
	if got := linkOf(t, c); got != "a" {
		t.Fatalf("b is down: game.exe went through %s, want a (never cut an app off)", got)
	}
	b.SetUp(true)

	// UDP follows the same rule
	pc, err := d.DialUDP(&M.Metadata{Network: M.UDP, SrcPort: 1, DstIP: netip.MustParseAddr("1.1.1.1"), DstPort: 3478})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if ip := pc.LocalAddr().(*net.UDPAddr).IP; !ip.Equal(b.IP()) {
		t.Fatalf("game.exe UDP left through %s, want b (%s)", ip, b.IP())
	}

	// without AppOf nothing is restricted or counted
	plain, err := TunDialer{S: s}.DialContext(context.Background(), tcpFlow(ln, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
}

func TestAppStats(t *testing.T) {
	needSecondLoopback(t)
	ln := echoServer(t)
	defer ln.Close()
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	b := links.New("b", net.IPv4(127, 0, 0, 2))
	s := &Server{Pool: links.PoolOf(a, b), Rules: func(app string) []string {
		if app == "game.exe" {
			return []string{"b"}
		}
		return nil
	}}
	d := TunDialer{S: s, AppOf: fakeApps}

	game, err := d.DialContext(context.Background(), tcpFlow(ln, 1))
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.DialContext(context.Background(), tcpFlow(ln, 3)) // unknown app
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("0123456789")
	game.Write(payload)
	buf := make([]byte, len(payload))
	game.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(game, buf); err != nil {
		t.Fatal(err)
	}

	byName := map[string]AppView{}
	for _, v := range s.Apps.Snapshot() {
		byName[v.Name] = v
	}
	g := byName["game.exe"]
	if g.Active != 1 || g.Total != 1 || g.Tx != 10 || g.Rx != 10 || g.RxByLink["b"] != 10 || g.RxByLink["a"] != 0 {
		t.Fatalf("game.exe stats: %+v", g)
	}
	if u, ok := byName[UnknownApp]; !ok || u.Active != 1 {
		t.Fatalf("unknown traffic must be counted under %q: %+v", UnknownApp, byName)
	}

	game.Close()
	game.Close() // closing twice must not count twice
	other.Close()
	byName = map[string]AppView{}
	for _, v := range s.Apps.Snapshot() {
		byName[v.Name] = v
	}
	if byName["game.exe"].Active != 0 || byName["game.exe"].Total != 1 || byName[UnknownApp].Active != 0 {
		t.Fatalf("after closing: %+v", byName)
	}
}

func TestAppRegistry(t *testing.T) {
	r := NewAppRegistry()
	x := r.Get("Launcher.exe")
	if r.Get("launcher.exe") != x {
		t.Fatal("names must be compared without regard to case")
	}
	if x.Name != "Launcher.exe" {
		t.Fatalf("first spelling must be kept, got %q", x.Name)
	}
	x.opened()
	x.addRx("a", 500)
	y := r.Get("small.exe")
	y.opened()
	y.addRx("a", 5)
	snap := r.Snapshot()
	if len(snap) != 2 || snap[0].Name != "Launcher.exe" {
		t.Fatalf("busiest first: %+v", snap)
	}
	// idle and closed for 10+ minutes: forgotten; one still connected is kept
	x.closed()
	x.last.Store(time.Now().Add(-11 * time.Minute).UnixNano())
	y.last.Store(time.Now().Add(-11 * time.Minute).UnixNano())
	snap = r.Snapshot()
	if len(snap) != 1 || snap[0].Name != "small.exe" {
		t.Fatalf("after idling: %+v (only the app with an open connection stays)", snap)
	}
	// at most 20 are listed
	for i := 0; i < 30; i++ {
		r.Get("app" + string(rune('a'+i%26)) + string(rune('a'+i/26))).addTx(i + 1)
	}
	if n := len(r.Snapshot()); n != 20 {
		t.Fatalf("listed %d apps, want at most 20", n)
	}
}

// A cap on an application slows all its connections together, and only its.
func TestAppLimit(t *testing.T) {
	ln := echoServer(t)
	defer ln.Close()
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	limit := 8 // Mb/s = 1 MB/s
	s := &Server{Pool: links.PoolOf(a), AppLimit: func(app string) int {
		if app == "game.exe" {
			return limit
		}
		return 0
	}}
	d := TunDialer{S: s, AppOf: fakeApps}

	pump := func(srcPort uint16, size int) time.Duration {
		t.Helper()
		c, err := d.DialContext(context.Background(), tcpFlow(ln, srcPort))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		go func() { c.Write(make([]byte, size)) }()
		start := time.Now()
		buf := make([]byte, 32*1024)
		for got := 0; got < size; {
			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := c.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			got += n
		}
		return time.Since(start)
	}

	if el := pump(1, 500_000); el < 300*time.Millisecond || el > 1200*time.Millisecond {
		t.Fatalf("500 KB at 1 MB/s took %v, want ~0.5 s", el)
	}
	if el := pump(2, 500_000); el > 200*time.Millisecond {
		t.Fatalf("an application without a cap took %v", el)
	}
	// raising the cap applies to the next second without reconnecting
	limit = 0
	time.Sleep(1100 * time.Millisecond)
	if el := pump(1, 500_000); el > 200*time.Millisecond {
		t.Fatalf("cap removed but still slow: %v", el)
	}
	var seen bool
	for _, v := range s.Apps.Snapshot() {
		if v.Name == "game.exe" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("game.exe missing from the stats")
	}
}
