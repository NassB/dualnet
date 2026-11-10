// Package links discovers the network interfaces to use (e.g. Wi-Fi box + USB phone
// tethering) and provides dialers bound to each of them.
package links

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NassB/dualnet/internal/ratelimit"
)

// Link is one outgoing internet path: a network interface and its IPv4.
// The IP can change (e.g. the phone is re-plugged); Pool keeps the Link object,
// so its counters survive.
type Link struct {
	Name string

	mu     sync.RWMutex
	ip     net.IP
	dialer *net.Dialer
	up     atomic.Bool

	overBudget atomic.Bool  // monthly data budget used
	unhealthy  atomic.Bool  // internet checks failing
	fails      atomic.Int32 // consecutive failed checks
	latency    atomic.Int64 // ms
	limitMbps  atomic.Int64

	connsMu sync.Mutex
	conns   map[*countingConn]struct{} // open connections, to tell busy from idle
	lim     ratelimit.Bucket

	BytesIn  atomic.Int64
	BytesOut atomic.Int64
	Active   atomic.Int64
}

func (l *Link) String() string { return fmt.Sprintf("%s (%s)", l.Name, l.IP()) }

// IP returns the current local address of the link.
func (l *Link) IP() net.IP {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.ip
}

// Up reports whether the interface is currently connected.
func (l *Link) Up() bool { return l.up.Load() }

func (l *Link) setIP(ip net.IP) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ip.Equal(ip) {
		return
	}
	l.ip = ip
	l.dialer = &net.Dialer{
		LocalAddr: &net.TCPAddr{IP: ip},
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
}

// DialContext opens a TCP connection that leaves through this link only.
// Windows uses the strong host model: binding the source IP forces the matching interface.
func (l *Link) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network == "tcp" {
		network = "tcp4"
	}
	l.mu.RLock()
	d := l.dialer
	l.mu.RUnlock()
	c, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	cc := &countingConn{Conn: c, link: l}
	cc.lastIO.Store(time.Now().UnixNano())
	cc.lastRx.Store(cc.lastIO.Load())
	l.connsMu.Lock()
	if l.conns == nil {
		l.conns = map[*countingConn]struct{}{}
	}
	l.conns[cc] = struct{}{}
	l.connsMu.Unlock()
	return cc, nil
}

// New returns a link bound to ip (used by tests; Discover builds real ones).
func New(name string, ip net.IP) *Link { return newLink(name, ip) }

func newLink(name string, ip net.IP) *Link {
	l := &Link{Name: name}
	l.setIP(ip)
	l.up.Store(true)
	return l
}

var skipNames = []string{"dualnet", "wintun", "loopback", "vethernet", "virtualbox", "vmware", "hyper-v", "bluetooth", "wsl", "tailscale", "zerotier", "wireguard", "openvpn", "tap-"}

// isHotspot reports whether an interface is Windows' own mobile hotspot or
// Internet Connection Sharing side: dualnet would share a connection with the
// very devices it serves, and the interface never reaches the internet by itself.
// The virtual Wi-Fi Direct adapters are named "Local Area Connection* N"
// (in French "Connexion au réseau local* N"); the sharing address is always
// 192.168.137.1. An interface chosen by name or address (-if) is never refused.
func isHotspot(name string, ip net.IP) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "local area connection*") || strings.Contains(l, "réseau local*") ||
		strings.Contains(l, "wi-fi direct") || ip.Equal(net.IPv4(192, 168, 137, 1))
}

// Discover returns the links to use. If selectors is non-empty, it is a list of
// interface names or IPv4 addresses (case-insensitive); otherwise all plausible
// physical interfaces with an IPv4 address are returned.
func Discover(selectors []string) ([]*Link, error) {
	out := NewPool(selectors).Up()
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable network interface found (use -if)")
	}
	return out, nil
}

func matches(selectors []string, name string, ip net.IP) bool {
	for _, s := range selectors {
		s = strings.TrimSpace(s)
		if strings.EqualFold(s, name) || s == ip.String() {
			return true
		}
	}
	return false
}

func firstIPv4(ifc net.Interface) net.IP {
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipn.IP.To4()
		if ip4 == nil || ip4.IsLinkLocalUnicast() || ip4.IsLoopback() {
			continue
		}
		return ip4
	}
	return nil
}

// ListAll prints every interface with its IPv4, for the "interfaces" command.
func ListAll() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, ifc := range ifaces {
		ip := firstIPv4(ifc)
		state := "down"
		if ifc.Flags&net.FlagUp != 0 {
			state = "up"
		}
		ipStr := "-"
		if ip != nil {
			ipStr = ip.String()
		}
		lines = append(lines, fmt.Sprintf("%-40s %-16s %s", ifc.Name, ipStr, state))
	}
	return lines, nil
}

type countingConn struct {
	net.Conn
	link   *Link
	closed atomic.Bool
	lastIO atomic.Int64 // unix nanoseconds of the last byte read or written
	lastRx atomic.Int64 // unix nanoseconds of the last byte received (or of the opening)
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.link.BytesIn.Add(int64(n))
	if n > 0 {
		now := time.Now().UnixNano()
		c.lastIO.Store(now)
		c.lastRx.Store(now)
	}
	c.link.Throttle(n)
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.link.BytesOut.Add(int64(n))
	if n > 0 {
		c.lastIO.Store(time.Now().UnixNano())
	}
	return n, err
}

// CloseWrite lets proxies half-close TCP connections.
func (c *countingConn) CloseWrite() error {
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		return tc.CloseWrite()
	}
	return nil
}

func (c *countingConn) Close() error {
	if !c.closed.Swap(true) {
		c.link.connsMu.Lock()
		delete(c.link.conns, c)
		c.link.connsMu.Unlock()
	}
	return c.Conn.Close()
}

// CloseStalled closes the link's connections that received nothing for idle,
// and returns how many. It is used when the link stopped working: without it
// the applications would wait on those connections until Windows gives up
// (minutes); closed, they reconnect at once, over a link that works.
//
// Connections that still receive data are left alone, so a link wrongly
// reported dead (a firewall that only blocks the check) keeps its traffic.
func (l *Link) CloseStalled(idle time.Duration) int {
	cut := time.Now().Add(-idle).UnixNano()
	var stalled []*countingConn
	l.connsMu.Lock()
	for c := range l.conns {
		if c.lastRx.Load() <= cut {
			stalled = append(stalled, c)
		}
	}
	l.connsMu.Unlock()
	for _, c := range stalled {
		c.Close()
	}
	return len(stalled)
}

// HumanBytes formats a byte count.
func HumanBytes(n float64) string {
	units := []string{"o", "Ko", "Mo", "Go"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

// Mbps converts bytes over a duration to megabits per second.
func Mbps(bytes int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(bytes) * 8 / d.Seconds() / 1e6
}

// Healthy reports whether the last internet checks through this link succeeded.
func (l *Link) Healthy() bool { return !l.unhealthy.Load() }

// Usable reports whether new connections may use this link.
func (l *Link) Usable() bool { return l.Up() && l.Healthy() && !l.OverBudget() }

// OverBudget reports whether the link used its monthly data budget and is
// excluded for new connections.
func (l *Link) OverBudget() bool { return l.overBudget.Load() }

// SetOverBudget marks or releases the link for budget reasons.
func (l *Link) SetOverBudget(v bool) { l.overBudget.Store(v) }

// LatencyMs is the TCP connect time to the internet, smoothed over the last
// checks (0 = unknown).
func (l *Link) LatencyMs() int64 { return l.latency.Load() }

// Busy counts the open connections that moved data within the last `within`
// (new connections count as busy). Idle ones (apps keeping a socket open) do
// not make a link look loaded.
func (l *Link) Busy(within time.Duration) int {
	cut := time.Now().Add(-within).UnixNano()
	l.connsMu.Lock()
	defer l.connsMu.Unlock()
	n := 0
	for c := range l.conns {
		if c.lastIO.Load() >= cut {
			n++
		}
	}
	return n
}
