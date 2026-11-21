package ui

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/store"
)

// With a limit of 1, the second download waits; a queued one can be cancelled;
// when the first ends, the next queued one starts; raising the limit starts
// waiting downloads at once.
func TestDownloadQueue(t *testing.T) {
	gate := make(chan struct{})
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/slow") {
			<-gate
		}
		w.Write([]byte("data"))
	}))
	defer files.Close()
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()

	st := store.Open(t.TempDir())
	if st.MaxDownloads() != store.DefaultMaxDownloads {
		t.Fatalf("default limit %d", st.MaxDownloads())
	}
	st.SetMaxDownloads(1)
	srv := &Server{Store: st, OutDir: t.TempDir(), Pool: links.PoolOf(links.New("lo", net.IPv4(127, 0, 0, 1)))}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	post := func(path, body string) int {
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(body))
		req.Header.Set(CSRFHeader, "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	download := func(name string) {
		if c := post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/"+name)); c != http.StatusAccepted {
			t.Fatalf("download %s: HTTP %d", name, c)
		}
	}
	states := func() string {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		var s []string
		for _, j := range srv.jobs {
			s = append(s, j.state)
		}
		return strings.Join(s, ",")
	}
	waitFor := func(want string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if states() == want {
				return
			}
		}
		t.Fatalf("states %s, want %s", states(), want)
	}

	download("slow1.bin")
	download("slow2.bin")
	download("fast.bin")
	waitFor("running,queued,queued")
	if c := post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/fast.bin")); c != http.StatusConflict {
		t.Errorf("same address while queued: HTTP %d, want 409", c)
	}

	post("/api/cancel?id=2", "")
	waitFor("running,cancelled,queued")

	close(gate) // the first ends: the next queued one starts and finishes
	waitFor("done,cancelled,done")

	gate = make(chan struct{})
	download("slow3.bin")
	download("slow4.bin")
	waitFor("done,cancelled,done,running,queued")
	if c := post("/api/maxdownloads?n=0", ""); c != http.StatusNoContent {
		t.Fatalf("maxdownloads: HTTP %d", c)
	}
	waitFor("done,cancelled,done,running,running")
	if c := post("/api/maxdownloads?n=9", ""); c != http.StatusBadRequest {
		t.Errorf("limit 9: HTTP %d, want 400", c)
	}
	close(gate)
	waitFor("done,cancelled,done,done,done")
}
