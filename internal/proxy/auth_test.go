package proxy

import (
	"bufio"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

func authServer(t *testing.T) string {
	s := &Server{Pool: links.PoolOf(links.New("a", net.IPv4(127, 0, 0, 1))), Username: "bob", Password: "s3cret"}
	return startServer(t, s)
}

func socksGreet(t *testing.T, addr string, methods ...byte) (net.Conn, []byte) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write(append([]byte{0x05, byte(len(methods))}, methods...))
	reply := make([]byte, 2)
	c.Read(reply)
	return c, reply
}

func TestSocksAuth(t *testing.T) {
	addr := authServer(t)

	c, reply := socksGreet(t, addr, 0x00) // only "no auth" offered
	c.Close()
	if reply[1] != 0xff {
		t.Fatalf("no acceptable method expected, got %v", reply)
	}

	login := func(user, pass string) byte {
		c, reply := socksGreet(t, addr, 0x00, 0x02)
		defer c.Close()
		if reply[1] != 0x02 {
			t.Fatalf("username/password method expected, got %v", reply)
		}
		msg := append([]byte{0x01, byte(len(user))}, user...)
		msg = append(append(msg, byte(len(pass))), pass...)
		c.Write(msg)
		r := make([]byte, 2)
		c.Read(r)
		return r[1]
	}
	if login("bob", "s3cret") != 0x00 {
		t.Error("good credentials refused")
	}
	if login("bob", "wrong") == 0x00 {
		t.Error("bad password accepted")
	}
}

func TestHTTPProxyAuth(t *testing.T) {
	addr := authServer(t)
	do := func(user, pass string) int {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		req, _ := http.NewRequest("GET", "http://127.0.0.1:1/", nil)
		if user != "" {
			req.SetBasicAuth(user, pass)
			req.Header.Set("Proxy-Authorization", req.Header.Get("Authorization"))
			req.Header.Del("Authorization")
		}
		req.WriteProxy(c)
		resp, err := http.ReadResponse(bufio.NewReader(c), req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if got := do("", ""); got != http.StatusProxyAuthRequired {
		t.Errorf("no credentials: want 407, got %d", got)
	}
	if got := do("bob", "nope"); got != http.StatusProxyAuthRequired {
		t.Errorf("bad credentials: want 407, got %d", got)
	}
	if got := do("bob", "s3cret"); got == http.StatusProxyAuthRequired {
		t.Error("good credentials refused")
	}
}

func TestAllowList(t *testing.T) {
	nets, err := ParseAllow("192.168.1.0/24, 10.0.0.5")
	if err != nil || len(nets) != 2 {
		t.Fatal(nets, err)
	}
	s := &Server{Allow: nets}
	for ip, want := range map[string]bool{"192.168.1.77": true, "10.0.0.5": true, "10.0.0.6": false, "8.8.8.8": false} {
		if got := s.allowed(&net.TCPAddr{IP: net.ParseIP(ip)}); got != want {
			t.Errorf("%s: want %v", ip, want)
		}
	}
	if _, err := ParseAllow("nonsense"); err == nil {
		t.Error("invalid entry accepted")
	}
	if !(&Server{}).allowed(&net.TCPAddr{IP: net.ParseIP("8.8.8.8")}) {
		t.Error("empty allow-list must allow everyone")
	}
}

func TestAuthLockout(t *testing.T) {
	s := &Server{Username: "u", Password: "p"}
	addr := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 9), Port: 1}
	for i := 0; i < maxAuthFailures; i++ {
		if s.authBlocked(addr) {
			t.Fatalf("blocked after only %d failures", i)
		}
		s.authFailed(addr)
	}
	if !s.authBlocked(addr) {
		t.Fatal("not blocked after repeated failures")
	}
	if s.authBlocked(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 10)}) {
		t.Fatal("another address must not be blocked")
	}
}
