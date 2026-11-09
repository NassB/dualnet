package proxy

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

func startServer(t *testing.T, s *Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	go s.ListenAndServe(addr)
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("proxy did not start")
	return ""
}

// A client that connects and says nothing must be dropped after the handshake timeout.
func TestHandshakeTimeout(t *testing.T) {
	s := &Server{Pool: links.PoolOf(links.New("a", net.IPv4(127, 0, 0, 1))), HandshakeTimeout: 200 * time.Millisecond}
	addr := startServer(t, s)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("want the proxy to close the silent connection (EOF), got %v", err)
	}
}

// Connections beyond MaxConns are refused, and a slot frees up when one closes.
func TestMaxConns(t *testing.T) {
	s := &Server{Pool: links.PoolOf(links.New("a", net.IPv4(127, 0, 0, 1))), MaxConns: 2, HandshakeTimeout: time.Minute}
	addr := startServer(t, s)
	var held []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	time.Sleep(100 * time.Millisecond)
	extra, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	extra.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := extra.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("third connection should be closed at once, got %v", err)
	}
	held[0].Close()
	time.Sleep(200 * time.Millisecond)
	again, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	again.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := again.Read(make([]byte, 1)); err == io.EOF {
		t.Fatal("a freed slot should accept a new connection")
	}
	for _, c := range held[1:] {
		c.Close()
	}
}

func TestDomainRuleChoosesLink(t *testing.T) {
	needSecondLoopback(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	b := links.New("b", net.IPv4(127, 0, 0, 2))
	s := &Server{Pool: links.PoolOf(a, b), DomainRule: func(host string) string {
		if host == "127.0.0.1" {
			return "b"
		}
		return ""
	}}
	for i := 0; i < 4; i++ {
		c, l, err := s.dial(ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
		if l.Name != "b" {
			t.Fatalf("connection %d used link %s, the domain rule says b", i, l.Name)
		}
	}
}
