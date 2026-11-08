package proxy

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

func TestTunDialer(t *testing.T) {
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	d := TunDialer{S: &Server{Pool: links.PoolOf(a)}}

	// TCP: echo server
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			buf := make([]byte, 4)
			n, _ := c.Read(buf)
			c.Write(buf[:n])
			c.Close()
		}
	}()
	ap := netip.MustParseAddrPort(ln.Addr().String())
	c, err := d.DialContext(context.Background(), &M.Metadata{Network: M.TCP, DstIP: ap.Addr(), DstPort: ap.Port()})
	if err != nil {
		t.Fatal(err)
	}
	if a.Active.Load() != 1 {
		t.Fatalf("active = %d, want 1 while open", a.Active.Load())
	}
	c.Write([]byte("ping"))
	buf := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, _ := c.Read(buf); string(buf[:n]) != "ping" {
		t.Fatalf("echo = %q", buf[:n])
	}
	c.Close()
	c.Close()
	if a.Active.Load() != 0 {
		t.Fatalf("active = %d after close, want 0", a.Active.Load())
	}

	// UDP: echo server
	us, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer us.Close()
	go func() {
		b := make([]byte, 16)
		n, from, err := us.ReadFrom(b)
		if err == nil {
			us.WriteTo(b[:n], from)
		}
	}()
	uap := netip.MustParseAddrPort(us.LocalAddr().String())
	pc, err := d.DialUDP(&M.Metadata{Network: M.UDP, DstIP: uap.Addr(), DstPort: uap.Port()})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	pc.WriteTo([]byte("dns?"), us.LocalAddr())
	pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "dns?" {
		t.Fatalf("udp echo = %q, %v", buf[:n], err)
	}
	if a.BytesIn.Load() == 0 || a.BytesOut.Load() == 0 {
		t.Fatal("UDP bytes not counted on the link")
	}

	// IPv6 is refused (applications fall back to IPv4)
	if _, err := d.DialContext(context.Background(), &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("2a01::1"), DstPort: 443}); err == nil {
		t.Fatal("IPv6 TCP should be refused")
	}
}
