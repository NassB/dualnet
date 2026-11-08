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

	unhealthy atomic.Bool  // internet checks failing
	fails     atomic.Int32 // consecutive failed checks
	latency   atomic.Int64 // ms

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
	return &countingConn{Conn: c, link: l}, nil
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

// Discover returns the links to use. If selectors is non-empty, it is a list of
// interface names or IPv4 addresses (case-insensitive); otherwise all plausible
// physical interfaces with an IPv4 address are returned.
func Discover(selectors []string) ([]*Link, error) {
	out := NewPool(selectors).Up()
	if len(out) == 0 {
		return nil, fmt.Errorf("aucune interface utilisable trouvée (utilisez -if)")
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
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.link.BytesIn.Add(int64(n))
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.link.BytesOut.Add(int64(n))
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
	c.closed.Store(true)
	return c.Conn.Close()
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
func (l *Link) Usable() bool { return l.Up() && l.Healthy() }

// LatencyMs is the last measured TCP connect time to the internet (0 = unknown).
func (l *Link) LatencyMs() int64 { return l.latency.Load() }
