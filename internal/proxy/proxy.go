// Package proxy is a local SOCKS5 + HTTP proxy that spreads outgoing connections
// over several links. One connection always uses one link; many connections use all.
package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

type Server struct {
	Pool *links.Pool
	// Sticky keeps a destination host on the same link for this long, so sites
	// that dislike IP changes (logins, banks) keep seeing one IP.
	Sticky time.Duration
	// BusyWindow: connections idle longer than this do not count as load (default 10 s).
	BusyWindow time.Duration
	// Rules returns the links an application is restricted to (transparent mode);
	// nil or empty = unrestricted.
	Rules func(app string) []string
	// AppLimit returns an application's download cap in Mb/s (0 = none).
	AppLimit func(app string) int
	// Apps counts the traffic per application (transparent mode).
	Apps *AppRegistry
	// Overflow reports whether a link only serves when the others are busy.
	Overflow func(linkName string) bool
	// DomainRule returns the name of the link a destination host must use
	// ("" = no rule). It wins over an application's rule.
	DomainRule func(host string) string
	// Username and Password, when Username is set, are required from every client
	// (SOCKS5 username/password, HTTP Proxy-Authorization: Basic).
	Username, Password string
	// Allow, when not empty, lists the client networks that may connect (others are dropped).
	Allow []*net.IPNet
	// MaxConns caps the open client connections (default 4096); more are refused.
	MaxConns int
	// HandshakeTimeout is how long a client has to send its request (default 30 s);
	// IdleTimeout how long a keep-alive HTTP connection may wait for the next one (default 2 min).
	HandshakeTimeout, IdleTimeout time.Duration

	initOnce     sync.Once
	mu           sync.Mutex
	sticky       map[string]stickyEntry
	rate         map[*links.Link]float64         // recent Mb/s per link, used for weighting
	transports   map[*links.Link]*http.Transport // keep-alive HTTP pools per link
	seed         map[*links.Link]seed            // measured capacities (speed tests)
	overflowOpen atomic.Bool                     // overflow links are in play
	overflowLow  int                             // seconds the load stayed low while open
}

type stickyEntry struct {
	link *links.Link
	seen time.Time
}

func (s *Server) init() {
	s.initOnce.Do(func() {
		if s.MaxConns <= 0 {
			s.MaxConns = 4096
		}
		if s.HandshakeTimeout <= 0 {
			s.HandshakeTimeout = 30 * time.Second
		}
		if s.IdleTimeout <= 0 {
			s.IdleTimeout = 2 * time.Minute
		}
		s.sticky = map[string]stickyEntry{}
		s.rate = map[*links.Link]float64{}
		s.seed = map[*links.Link]seed{}
		if s.Apps == nil {
			s.Apps = NewAppRegistry()
		}
		go s.stats()
	})
}

func (s *Server) ListenAndServe(addr string) error {
	s.init()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok && !tcp.IP.IsLoopback() && !s.authRequired() && len(s.Allow) == 0 {
		log.Printf("WARNING: proxy %s is reachable from the network and has no authentication and no allow-list", addr)
	}
	log.Printf("SOCKS5/HTTP proxy listening on %s", addr)
	slots := make(chan struct{}, s.MaxConns)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		select {
		case slots <- struct{}{}:
		default: // too many open connections: refuse rather than exhaust memory
			c.Close()
			continue
		}
		go func() {
			defer func() { <-slots }()
			s.handle(c)
		}()
	}
}

var pipeBufs = sync.Pool{New: func() any { b := make([]byte, 64*1024); return &b }}

func copyBuf(dst io.Writer, src io.Reader) {
	bp := pipeBufs.Get().(*[]byte)
	defer pipeBufs.Put(bp)
	io.CopyBuffer(dst, src, *bp)
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	s.init()
	if !s.allowed(c.RemoteAddr()) {
		return
	}
	c.SetReadDeadline(time.Now().Add(s.HandshakeTimeout))
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	if first[0] == 0x05 {
		s.socks5(c, br)
	} else {
		s.http(c, br)
	}
}

// pick returns the usable links in the order to try. Overflow links stay out
// (but come last, for failover) until the other links are busy. First comes the
// sticky link for this host if it is still allowed, otherwise the one with the
// most spare capacity (estimated capacity / busy connections). Links whose score
// is within 10 % of the best are considered equal: among them the lowest ping wins.
func (s *Server) pick(host string) []*links.Link {
	s.init()
	up := s.Pool.Up()
	if len(up) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	use := s.allowedNow(up)
	var first *links.Link
	if e, ok := s.sticky[host]; ok && time.Since(e.seen) < s.Sticky && e.link.Usable() && contains(use, e.link) {
		first = e.link
	} else {
		first = s.bestLink(use)
	}
	if s.Sticky > 0 {
		s.sticky[host] = stickyEntry{first, time.Now()}
	}
	order := []*links.Link{first}
	for _, l := range use {
		if l != first {
			order = append(order, l)
		}
	}
	for _, l := range up { // links kept in reserve, only for failover or a rule
		if !contains(use, l) {
			order = append(order, l)
		}
	}
	return order
}

// bestLink returns the link with the best score; among the links within 10 % of
// that score (a tie, for all practical purposes) the one with the lowest ping.
func (s *Server) bestLink(up []*links.Link) *links.Link {
	scores := make([]float64, len(up))
	best := 0.0
	for i, l := range up {
		scores[i] = s.score(l)
		if scores[i] > best {
			best = scores[i]
		}
	}
	var pick *links.Link
	for i, l := range up {
		if scores[i] < best*0.9 {
			continue
		}
		if pick == nil || lowerPing(l, pick) {
			pick = l
		}
	}
	return pick
}

// lowerPing reports whether a answers faster than b; an unknown ping (0) is worst.
func lowerPing(a, b *links.Link) bool {
	pa, pb := a.LatencyMs(), b.LatencyMs()
	if pa == 0 {
		return false
	}
	return pb == 0 || pa < pb
}

// pickLowestLatency returns the usable links, lowest ping first (links kept in
// reserve last). DNS lookups use it: they are tiny, and their delay is added to
// every new connection.
func (s *Server) pickLowestLatency() []*links.Link {
	up := s.Pool.Up()
	use := s.allowedNow(up)
	var rest []*links.Link
	for _, l := range up {
		if !contains(use, l) {
			rest = append(rest, l)
		}
	}
	use = append([]*links.Link(nil), use...)
	byPing := func(ls []*links.Link) {
		sort.SliceStable(ls, func(i, j int) bool { return lowerPing(ls[i], ls[j]) })
	}
	byPing(use)
	byPing(rest)
	return append(use, rest...)
}

// isDNSPort: DNS (53) and DNS over TLS (853).
func isDNSPort(port string) bool { return port == "53" || port == "853" }

func (s *Server) dial(addr string) (net.Conn, *links.Link, error) { return s.dialFor(addr, nil) }

func (s *Server) domainLinks(order []*links.Link, host string, allow func(*links.Link) bool) []*links.Link {
	if s.DomainRule != nil {
		if name := s.DomainRule(host); name != "" {
			allow = func(l *links.Link) bool { return l.Name == name }
		}
	}
	return filterLinks(order, allow)
}

// dialFor connects over the best link allow accepts (nil = any), falling back to
// the other links when none of the accepted ones can be used.
func (s *Server) dialFor(addr string, allow func(*links.Link) bool) (net.Conn, *links.Link, error) {
	host, port, _ := net.SplitHostPort(addr)
	order := s.pick(host)
	if isDNSPort(port) {
		order = s.pickLowestLatency()
	}
	order = s.domainLinks(order, host, allow)
	lastErr := errNoLink
	for _, l := range order {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		c, err := l.DialContext(ctx, "tcp", addr)
		cancel()
		if err == nil {
			return c, l, nil
		}
		lastErr = err
	}
	return nil, nil, lastErr
}

func (s *Server) socks5(c net.Conn, br *bufio.Reader) {
	// Greeting: VER NMETHODS METHODS...
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	if !s.socksAuth(c, br, methods) {
		return
	}

	// Request: VER CMD RSV ATYP DST.ADDR DST.PORT
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	if req[1] != 0x01 { // only CONNECT
		c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	switch req[3] {
	case 0x01:
		b := make([]byte, 4)
		io.ReadFull(br, b)
		host = net.IP(b).String()
	case 0x03:
		l, _ := br.ReadByte()
		b := make([]byte, l)
		io.ReadFull(br, b)
		host = string(b)
	case 0x04:
		b := make([]byte, 16)
		io.ReadFull(br, b)
		host = net.IP(b).String()
	default:
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return
	}
	addr := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb))))

	up, l, err := s.dial(addr)
	if err != nil {
		c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	c.SetReadDeadline(time.Time{})
	pipe(c, br, up, l)
}

func (s *Server) http(c net.Conn, br *bufio.Reader) {
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		c.SetReadDeadline(time.Time{})
		if !s.checkHTTPAuth(req) {
			fmt.Fprint(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"dualnet\"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return
		}
		if req.Method == http.MethodConnect {
			up, l, err := s.dial(req.Host)
			if err != nil {
				fmt.Fprint(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
				return
			}
			defer up.Close()
			fmt.Fprint(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
			pipe(c, br, up, l)
			return
		}
		// Plain HTTP: forward over a per-link keep-alive pool, and keep serving
		// requests on this client connection (download tools fetch many chunks this way).
		if !s.forward(c, req) || req.Close {
			return
		}
		c.SetReadDeadline(time.Now().Add(s.IdleTimeout))
	}
}

var hopHeaders = []string{"Proxy-Connection", "Proxy-Authorization", "Connection", "Keep-Alive", "Te", "Trailer", "Upgrade"}

// forward sends one plain-HTTP request and writes the response to the client.
// It returns false when the client connection must be closed.
func (s *Server) forward(c net.Conn, req *http.Request) bool {
	req.RequestURI = ""
	if req.URL.Host == "" {
		req.URL.Host = req.Host
	}
	if req.URL.Scheme == "" {
		req.URL.Scheme = "http"
	}
	for _, h := range hopHeaders {
		req.Header.Del(h)
	}
	host := req.URL.Hostname()
	var resp *http.Response
	var used *links.Link
	err := errNoLink
	for _, l := range s.domainLinks(s.pick(host), host, nil) {
		l.Active.Add(1)
		resp, err = s.transport(l).RoundTrip(req)
		if err == nil {
			used = l
			break
		}
		l.Active.Add(-1)
		// Retry on another link only if the body was not consumed.
		if req.Body != nil && req.Body != http.NoBody {
			break
		}
	}
	if err != nil {
		fmt.Fprint(c, badGateway)
		return false
	}
	defer used.Active.Add(-1)
	defer resp.Body.Close()
	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}
	keep := !resp.Close
	resp.Close = !keep
	return resp.Write(c) == nil && keep
}

// transport returns the keep-alive HTTP client pool bound to link l.
func (s *Server) transport(l *links.Link) *http.Transport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transports == nil {
		s.transports = map[*links.Link]*http.Transport{}
	}
	t, ok := s.transports[l]
	if !ok {
		t = &http.Transport{
			DialContext:           l.DialContext,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			DisableCompression:    true, // pass bytes through unchanged
		}
		s.transports[l] = t
	}
	return t
}

func pipe(client net.Conn, clientR io.Reader, up net.Conn, l *links.Link) {
	l.Active.Add(1)
	defer l.Active.Add(-1)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		copyBuf(up, clientR)
		closeWrite(up)
	}()
	go func() {
		defer wg.Done()
		copyBuf(client, up)
		closeWrite(client)
	}()
	wg.Wait()
}

func closeWrite(c net.Conn) {
	type cw interface{ CloseWrite() error }
	if x, ok := c.(cw); ok {
		if err := x.CloseWrite(); err == nil {
			return
		}
	}
	c.SetReadDeadline(time.Now())
}

// stats estimates each link's capacity once a second (peak throughput, slowly
// decaying) and logs a summary every 5 s.
func (s *Server) stats() {
	t := time.NewTicker(time.Second)
	prev := map[*links.Link]int64{}
	for tick := 1; ; tick++ {
		<-t.C
		curRates := map[*links.Link]float64{}
		var parts []string
		s.mu.Lock()
		for _, l := range s.Pool.Up() {
			cur := l.BytesIn.Load() + l.BytesOut.Load()
			r := links.Mbps(cur-prev[l], time.Second)
			prev[l] = cur
			curRates[l] = r
			s.rate[l] = maxf(r, s.rate[l]*0.995) // half-life ~2 min
			parts = append(parts, fmt.Sprintf("%s: %.1f Mb/s (capacity ~%.0f), %d conn, %d active", l.Name, r, s.rate[l], l.Active.Load(), l.Busy(10*time.Second)))
		}
		s.evalOverflow(curRates)
		for h, e := range s.sticky {
			if time.Since(e.seen) > s.Sticky {
				delete(s.sticky, h)
			}
		}
		s.mu.Unlock()
		if tick%5 == 0 && len(parts) > 0 {
			log.Println(strings.Join(parts, "  |  "))
		}
	}
}

// score is how attractive a link is for one more connection: its estimated
// capacity divided among its open connections. Unmeasured links get a floor so
// they still receive connections and get measured.
func (s *Server) score(l *links.Link) float64 {
	capacity := maxf(s.capacityOf(l), 10)
	return capacity / float64(l.Busy(s.busyWindow())+1)
}

// busyWindow: a connection without traffic for this long counts as idle.
func (s *Server) busyWindow() time.Duration {
	if s.BusyWindow > 0 {
		return s.BusyWindow
	}
	return 10 * time.Second
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// badGateway is the reply when no link can reach the server.
const badGateway = "HTTP/1.1 502 Bad Gateway" + crlf + "Content-Length: 0" + crlf + crlf

const crlf = "\r\n"

// errNoLink is returned when no link is connected and answering.
var errNoLink = errors.New("no connected link")
