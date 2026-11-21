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

// A server without range requests nor a size, like most small text files:
// the UI must still get the file name, the size and the bytes received, and a
// name another download is already writing gets " (2)".
func TestSingleLinkStatsAndFreeName(t *testing.T) {
	body := bytes.Repeat([]byte("User-agent: *\n"), 80)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.(http.Flusher).Flush() // chunked: no Content-Length, no Accept-Ranges
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	release, err := claim(filepath.Join(dir, "robots.txt")) // another download is writing it
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	var st Stats
	ls := []*links.Link{links.New("lo", net.IPv4(127, 0, 0, 1))}
	if err := Run(context.Background(), ls, Options{URL: srv.URL + "/robots.txt", OutDir: dir, ChunkSize: 2 << 20, WorkersPerLnk: 2, Quiet: true, Stats: &st}); err != nil {
		t.Fatal(err)
	}
	file, size, per := st.Snapshot()
	if want := filepath.Join(dir, "robots (2).txt"); file != want {
		t.Errorf("file %q, want %q", file, want)
	}
	if size != int64(len(body)) || len(per) != 1 || per[0] != int64(len(body)) {
		t.Errorf("size %d, per link %v, want %d", size, per, len(body))
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(got, body) {
		t.Errorf("content: %d bytes", len(got))
	}
}

func TestClaimFree(t *testing.T) {
	dir := t.TempDir()
	var outs []string
	for i := 0; i < 3; i++ {
		out, release, err := claimFree(dir, "a.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		outs = append(outs, filepath.Base(out))
	}
	if outs[0] != "a.tar.gz" || outs[1] != "a.tar (2).gz" || outs[2] != "a.tar (3).gz" {
		t.Errorf("names: %v", outs)
	}
}
