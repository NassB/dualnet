package proxy

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"

	"github.com/NassB/dualnet/internal/links"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// TunDialer lets the transparent mode (tun2socks) open connections through the
// same link picker as the proxy, without a SOCKS hop.
type TunDialer struct {
	S *Server
	// AppOf names the application that opened a captured flow ("" = unknown). It
	// drives the per-application rules and statistics; nil disables both.
	AppOf func(md *M.Metadata) string
}

var errIPv6 = errors.New("IPv6 is not supported by dualnet")

// app returns the application behind a flow and its counters.
func (d TunDialer) app(md *M.Metadata) (string, *AppStats) {
	d.S.init()
	if d.AppOf == nil {
		return "", nil
	}
	name := d.AppOf(md)
	if name == "" {
		name = UnknownApp
	}
	var capOf func(string) int
	if name != UnknownApp {
		capOf = d.S.AppLimit
	}
	return name, d.S.Apps.get(name, capOf)
}

// DialContext opens a TCP connection to the destination over the best link the
// application's rule allows. IPv6 destinations are refused at once so
// applications fall back to IPv4.
func (d TunDialer) DialContext(_ context.Context, md *M.Metadata) (net.Conn, error) {
	ip := md.DstIP.Unmap()
	if !ip.Is4() {
		return nil, errIPv6
	}
	app, st := d.app(md)
	c, l, err := d.S.dialFor(md.DestinationAddress(), app)
	if err != nil {
		return nil, err
	}
	l.Active.Add(1)
	if st != nil {
		st.opened()
	}
	return &activeConn{Conn: c, link: l, app: st}, nil
}

// DialUDP returns a UDP socket bound to the chosen link (one link per flow).
func (d TunDialer) DialUDP(md *M.Metadata) (net.PacketConn, error) {
	ip := md.DstIP.Unmap()
	if !ip.Is4() {
		return nil, errIPv6
	}
	app, st := d.app(md)
	order := d.S.order(ip.String(), strconv.Itoa(int(md.DstPort)), app)
	if len(order) == 0 {
		return nil, errNoLink
	}
	l := order[0]
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: l.IP()})
	if err != nil {
		return nil, err
	}
	if st != nil {
		st.opened()
	}
	return &countingPacketConn{PacketConn: pc, link: l, app: st}, nil
}

// activeConn keeps the link's (and the application's) open-connection count
// while the connection is open, and counts the application's traffic.
type activeConn struct {
	net.Conn
	link *links.Link
	app  *AppStats
	once sync.Once
}

func (c *activeConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.app != nil && n > 0 {
		c.app.addRx(c.link.Name, n)
		c.app.throttle(n)
	}
	return n, err
}

func (c *activeConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if c.app != nil && n > 0 {
		c.app.addTx(n)
	}
	return n, err
}

func (c *activeConn) Close() error {
	c.once.Do(func() {
		c.link.Active.Add(-1)
		if c.app != nil {
			c.app.closed()
		}
	})
	return c.Conn.Close()
}

type countingPacketConn struct {
	net.PacketConn
	link *links.Link
	app  *AppStats
	once sync.Once
}

func (c *countingPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, a, err := c.PacketConn.ReadFrom(p)
	c.link.BytesIn.Add(int64(n))
	c.link.Throttle(n)
	if c.app != nil && n > 0 {
		c.app.addRx(c.link.Name, n)
		c.app.throttle(n)
	}
	return n, a, err
}

func (c *countingPacketConn) WriteTo(p []byte, a net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(p, a)
	c.link.BytesOut.Add(int64(n))
	if c.app != nil && n > 0 {
		c.app.addTx(n)
	}
	return n, err
}

func (c *countingPacketConn) Close() error {
	c.once.Do(func() {
		if c.app != nil {
			c.app.closed()
		}
	})
	return c.PacketConn.Close()
}
