package proxy

import (
	"bufio"
	"crypto/subtle"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
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
	if !s.authRequired() {
		c.Write([]byte{0x05, 0x00}) // as before: whatever the client offered
		return true
	}
	const want = byte(0x02)
	offered := false
	for _, m := range methods {
		if m == want {
			offered = true
		}
	}
	if !offered || s.authBlocked(c.RemoteAddr()) {
		c.Write([]byte{0x05, 0xff})
		return false
	}
	c.Write([]byte{0x05, want})
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
		s.authFailed(c.RemoteAddr())
		c.Write([]byte{0x01, 0x01})
		return false
	}
	c.Write([]byte{0x01, 0x00})
	return true
}

// checkHTTPAuth reports whether the request carries valid proxy credentials
// (always true when none are required).
func (s *Server) checkHTTPAuth(req *http.Request, from net.Addr) bool {
	if !s.authRequired() {
		return true
	}
	if s.authBlocked(from) {
		return false
	}
	r := http.Request{Header: http.Header{"Authorization": {req.Header.Get("Proxy-Authorization")}}}
	user, pass, ok := r.BasicAuth()
	if ok && s.credsOK(user, pass) {
		return true
	}
	s.authFailed(from)
	return false
}

const (
	maxAuthFailures = 5
	authBlockTime   = time.Minute
)

type failState struct {
	n     int
	until time.Time
}

func clientIP(a net.Addr) string {
	if t, ok := a.(*net.TCPAddr); ok {
		return t.IP.String()
	}
	return ""
}

// authBlocked reports whether a client address failed to log in too often lately.
func (s *Server) authBlocked(a net.Addr) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	f := s.fails[clientIP(a)]
	return f != nil && time.Now().Before(f.until)
}

// authFailed counts a wrong password; after maxAuthFailures the address is
// refused for authBlockTime.
func (s *Server) authFailed(a net.Addr) {
	ip := clientIP(a)
	s.failMu.Lock()
	defer s.failMu.Unlock()
	if s.fails == nil {
		s.fails = map[string]*failState{}
	}
	if len(s.fails) > 4096 { // forget old entries rather than grow without bound
		for k, f := range s.fails {
			if time.Now().After(f.until) {
				delete(s.fails, k)
			}
		}
	}
	f := s.fails[ip]
	if f == nil {
		f = &failState{}
		s.fails[ip] = f
	}
	if f.n++; f.n >= maxAuthFailures {
		f.n, f.until = 0, time.Now().Add(authBlockTime)
	}
}
