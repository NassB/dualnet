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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

type Server struct {
	Pool *links.Pool
	// Sticky keeps a destination host on the same link for this long, so sites
	// that dislike IP changes (logins, banks) keep seeing one IP.
	Sticky time.Duration

	initOnce sync.Once
	mu       sync.Mutex
	sticky   map[string]stickyEntry
	rate     map[*links.Link]float64 // recent Mb/s per link, used for weighting
}

type stickyEntry struct {
	link *links.Link
	seen time.Time
}

func (s *Server) init() {
	s.initOnce.Do(func() {
		s.sticky = map[string]stickyEntry{}
		s.rate = map[*links.Link]float64{}
		go s.stats()
	})
}

func (s *Server) ListenAndServe(addr string) error {
	s.init()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("Proxy SOCKS5/HTTP à l'écoute sur %s", addr)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
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

// pick returns the connected links in the order to try: the sticky one for this
// host if still up, otherwise the one with the most spare capacity
// (measured rate / active connections) first.
func (s *Server) pick(host string) []*links.Link {
	s.init()
	up := s.Pool.Up()
	if len(up) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var first *links.Link
	if e, ok := s.sticky[host]; ok && time.Since(e.seen) < s.Sticky && e.link.Up() {
		first = e.link
	} else {
		bestScore := -1.0
		for _, l := range up {
			score := (s.rate[l] + 1) / float64(l.Active.Load()+1)
			if score > bestScore {
				first, bestScore = l, score
			}
		}
	}
	if s.Sticky > 0 {
		s.sticky[host] = stickyEntry{first, time.Now()}
	}
	order := []*links.Link{first}
	for _, l := range up {
		if l != first {
			order = append(order, l)
		}
	}
	return order
}

func (s *Server) dial(addr string) (net.Conn, *links.Link, error) {
	host, _, _ := net.SplitHostPort(addr)
	lastErr := errors.New("aucun lien connecté")
	for _, l := range s.pick(host) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
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
	if _, err := io.ReadFull(br, make([]byte, hdr[1])); err != nil {
		return
	}
	c.Write([]byte{0x05, 0x00}) // no auth

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
	pipe(c, br, up, l)
}

func (s *Server) http(c net.Conn, br *bufio.Reader) {
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
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
		// Plain HTTP forwarding.
		host := req.Host
		if !strings.Contains(host, ":") {
			host += ":80"
		}
		up, l, err := s.dial(host)
		if err != nil {
			fmt.Fprint(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
			return
		}
		l.Active.Add(1)
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		req.Header.Set("Connection", "close")
		if err := req.Write(up); err != nil {
			up.Close()
			l.Active.Add(-1)
			return
		}
		io.Copy(c, up)
		up.Close()
		l.Active.Add(-1)
		return
	}
}

func pipe(client net.Conn, clientR io.Reader, up net.Conn, l *links.Link) {
	l.Active.Add(1)
	defer l.Active.Add(-1)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(up, clientR)
		closeWrite(up)
	}()
	go func() {
		defer wg.Done()
		io.Copy(client, up)
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

func (s *Server) stats() {
	t := time.NewTicker(5 * time.Second)
	prev := map[*links.Link]int64{}
	for range t.C {
		var parts []string
		s.mu.Lock()
		for _, l := range s.Pool.Up() {
			cur := l.BytesIn.Load() + l.BytesOut.Load()
			r := links.Mbps(cur-prev[l], 5*time.Second)
			prev[l] = cur
			// keep the best recent rate as capacity estimate, decaying slowly
			s.rate[l] = maxf(r, s.rate[l]*0.9)
			parts = append(parts, fmt.Sprintf("%s: %.1f Mb/s, %d conn", l.Name, r, l.Active.Load()))
		}
		for h, e := range s.sticky {
			if time.Since(e.seen) > s.Sticky {
				delete(s.sticky, h)
			}
		}
		s.mu.Unlock()
		log.Println(strings.Join(parts, "  |  "))
	}
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
