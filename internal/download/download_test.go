package download

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// slowWriter throttles responses so the two simulated links have different speeds.
type slowWriter struct {
	w     http.ResponseWriter
	delay time.Duration
}

func (s slowWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := min(len(p), 32<<10)
		k, err := s.w.Write(p[:n])
		total += k
		if err != nil {
			return total, err
		}
		p = p[n:]
		time.Sleep(s.delay)
	}
	return total, nil
}

func TestRunIntegrity(t *testing.T) {
	data := make([]byte, 20<<20+12345)
	rand.Read(data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Link "slow" dials from 127.0.0.2: throttle it.
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		var d time.Duration
		if host == "127.0.0.2" {
			d = 2 * time.Millisecond
		}
		http.ServeContent(slowResp{w, d}, r, "f", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()

	ls := []*links.Link{links.New("fast", net.IPv4(127, 0, 0, 1)), links.New("slow", net.IPv4(127, 0, 0, 2))}
	for i := 0; i < 5; i++ {
		out := filepath.Join(t.TempDir(), "out")
		err := Run(context.Background(), ls, Options{URL: srv.URL, Output: out, ChunkSize: 2 << 20, WorkersPerLnk: 4})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		got, _ := os.ReadFile(out)
		if !bytes.Equal(got, data) {
			t.Fatalf("run %d: content mismatch (len %d vs %d)", i, len(got), len(data))
		}
	}
}

type slowResp struct {
	http.ResponseWriter
	delay time.Duration
}

func (s slowResp) Write(p []byte) (int, error) {
	return slowWriter{s.ResponseWriter, s.delay}.Write(p)
}
