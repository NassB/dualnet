package speedtest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// bulk serves an endless body until the client goes away.
func bulk() *httptest.Server {
	chunk := make([]byte, 64<<10)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
}

func TestRunMeasuresTheLink(t *testing.T) {
	srv := bulk()
	defer srv.Close()
	l := links.New("lo", net.IPv4(127, 0, 0, 1))
	res, err := Run(context.Background(), l, srv.URL, 2*time.Second, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mbps < 50 || res.Bytes < 1<<20 {
		t.Fatalf("loopback should be fast: %+v", res)
	}
	if res.Duration < 1500*time.Millisecond || res.Duration > 4*time.Second {
		t.Fatalf("test lasted %v, want about 2 s", res.Duration)
	}
	// the data went through the link: it shows in its counters (and its budget)
	if l.BytesIn.Load() < res.Bytes/2 {
		t.Fatalf("link counted %d bytes, test downloaded %d", l.BytesIn.Load(), res.Bytes)
	}
}

// The speed cap of a link applies to the test: it measures what dualnet can use.
func TestRunObeysTheSpeedCap(t *testing.T) {
	srv := bulk()
	defer srv.Close()
	l := links.New("lo", net.IPv4(127, 0, 0, 1))
	l.SetLimitMbps(80)
	res, err := Run(context.Background(), l, srv.URL, 3*time.Second, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mbps < 45 || res.Mbps > 110 {
		t.Fatalf("capped at 80 Mb/s, measured %.1f", res.Mbps)
	}
}

func TestRunErrors(t *testing.T) {
	l := links.New("lo", net.IPv4(127, 0, 0, 1))
	// nothing listens
	if _, err := Run(context.Background(), l, "http://127.0.0.1:1/x", time.Second, 1); err == nil {
		t.Error("an unreachable server must be an error")
	}
	// the server refuses
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 403) }))
	defer bad.Close()
	if _, err := Run(context.Background(), l, bad.URL, time.Second, 1); err == nil {
		t.Error("HTTP 403 must be an error")
	}
	// a cancelled test returns promptly
	srv := bulk()
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	t0 := time.Now()
	Run(ctx, l, srv.URL, 10*time.Second, 1)
	if time.Since(t0) > 3*time.Second {
		t.Errorf("cancelled test took %v", time.Since(t0))
	}
}
