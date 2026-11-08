package proxy

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/NassB/dualnet/internal/links"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// TunDialer lets the transparent mode (tun2socks) open connections through the
// same link picker as the proxy, without a SOCKS hop.
type TunDialer struct{ S *Server }

var errIPv6 = errors.New("IPv6 non pris en charge par dualnet")

// DialContext opens a TCP connection to the destination over the best link.
// IPv6 destinations are refused at once so applications fall back to IPv4.
func (d TunDialer) DialContext(_ context.Context, md *M.Metadata) (net.Conn, error) {
	ip := md.DstIP.Unmap()
	if !ip.Is4() {
		return nil, errIPv6
	}
	c, l, err := d.S.dial(md.DestinationAddress())
	if err != nil {
		return nil, err
	}
	l.Active.Add(1)
	return &activeConn{Conn: c, link: l}, nil
}

// DialUDP returns a UDP socket bound to the chosen link (one link per flow).
func (d TunDialer) DialUDP(md *M.Metadata) (net.PacketConn, error) {
	ip := md.DstIP.Unmap()
	if !ip.Is4() {
		return nil, errIPv6
	}
	order := d.S.pick(ip.String())
	if len(order) == 0 {
		return nil, errors.New("aucun lien connecté")
	}
	l := order[0]
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: l.IP()})
	if err != nil {
		return nil, err
	}
	return &countingPacketConn{PacketConn: pc, link: l}, nil
}

// activeConn keeps the link's active-connection count while the connection is open.
type activeConn struct {
	net.Conn
	link *links.Link
	once sync.Once
}

func (c *activeConn) Close() error {
	c.once.Do(func() { c.link.Active.Add(-1) })
	return c.Conn.Close()
}

type countingPacketConn struct {
	net.PacketConn
	link *links.Link
}

func (c *countingPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, a, err := c.PacketConn.ReadFrom(p)
	c.link.BytesIn.Add(int64(n))
	return n, a, err
}

func (c *countingPacketConn) WriteTo(p []byte, a net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(p, a)
	c.link.BytesOut.Add(int64(n))
	return n, err
}
