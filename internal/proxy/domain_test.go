package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NassB/dualnet/internal/links"
)

func TestDomainRuleHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	b := links.New("b", net.IPv4(127, 0, 0, 1))
	s := &Server{Pool: links.PoolOf(a, b), DomainRule: func(host string) string {
		if host == "127.0.0.1" {
			return "b"
		}
		return ""
	}}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	req, err := http.NewRequest(http.MethodGet, upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() { done <- s.forward(server, req) }()
	resp, err := http.ReadResponse(bufio.NewReader(client), req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != "ok" || !<-done {
		t.Fatalf("response: %q, %v", body, err)
	}
	if b.BytesIn.Load() == 0 || a.BytesIn.Load() != 0 {
		t.Fatalf("expected link b, received bytes: a=%d b=%d", a.BytesIn.Load(), b.BytesIn.Load())
	}
}

func TestDomainRuleFallback(t *testing.T) {
	a := links.New("a", net.IPv4(127, 0, 0, 1))
	s := &Server{DomainRule: func(string) string { return "unavailable" }}
	got := s.domainLinks([]*links.Link{a}, "example.com", nil)
	if len(got) != 1 || got[0] != a {
		t.Fatalf("expected fallback to link a: %v", got)
	}
}
