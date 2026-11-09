package proxy

import (
	"bufio"
	"crypto/subtle"
	"io"
	"net"
	"net/http"
	"strings"
)

func (s *Server) authRequired() bool { return s.Username != "" }

func (s *Server) credsOK(user, pass string) bool {
	u := subtle.ConstantTimeCompare([]byte(user), []byte(s.Username))
	p := subtle.ConstantTimeCompare([]byte(pass), []byte(s.Password))
	return u&p == 1
}

// allowed reports whether a client address may use the proxy.
func (s *Server) allowed(a net.Addr) bool {
	if len(s.Allow) == 0 {
		return true
	}
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return false
	}
	for _, n := range s.Allow {
		if n.Contains(tcp.IP) {
			return true
		}
	}
	return false
}

// ParseAllow turns "192.168.1.0/24,10.0.0.5" into networks (a bare address is a /32 or /128).
func ParseAllow(list string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, f := range splitList(list) {
		if _, n, err := net.ParseCIDR(f); err == nil {
			out = append(out, n)
			continue
		}
		ip := net.ParseIP(f)
		if ip == nil {
			return nil, &net.ParseError{Type: "network", Text: f}
		}
		bits := 128
		if ip4 := ip.To4(); ip4 != nil {
			ip, bits = ip4, 32
		}
		out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// socksAuth answers the SOCKS5 greeting and, when credentials are required,
// runs the RFC 1929 username/password exchange. It reports whether to go on.
func (s *Server) socksAuth(c net.Conn, br *bufio.Reader, methods []byte) bool {
	want := byte(0x00)
	if s.authRequired() {
		want = 0x02
	}
	offered := false
	for _, m := range methods {
		if m == want {
			offered = true
		}
	}
	if !offered {
		c.Write([]byte{0x05, 0xff})
		return false
	}
	c.Write([]byte{0x05, want})
	if want == 0x00 {
		return true
	}
	// VER(1) ULEN UNAME PLEN PASSWD
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr[:2]); err != nil || hdr[0] != 0x01 {
		return false
	}
	user := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, user); err != nil {
		return false
	}
	pl, err := br.ReadByte()
	if err != nil {
		return false
	}
	pass := make([]byte, pl)
	if _, err := io.ReadFull(br, pass); err != nil {
		return false
	}
	if !s.credsOK(string(user), string(pass)) {
		c.Write([]byte{0x01, 0x01})
		return false
	}
	c.Write([]byte{0x01, 0x00})
	return true
}

// checkHTTPAuth reports whether the request carries valid proxy credentials
// (always true when none are required).
func (s *Server) checkHTTPAuth(req *http.Request) bool {
	if !s.authRequired() {
		return true
	}
	r := http.Request{Header: http.Header{"Authorization": {req.Header.Get("Proxy-Authorization")}}}
	user, pass, ok := r.BasicAuth()
	return ok && s.credsOK(user, pass)
}
