package proxy

import (
	"net"
	"testing"
	"time"
)

// needSecondLoopback skips tests that bind a link to 127.0.0.2: some sandboxes
// (containers, gVisor) cannot connect from that source address to 127.0.0.1.
func needSecondLoopback(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback:", err)
	}
	defer ln.Close()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2)}, Timeout: time.Second}
	c, err := d.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Skip("cannot connect from 127.0.0.2:", err)
	}
	c.Close()
}
