package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// The download folder chosen in Settings is used for new downloads, a relative
// path is refused, and "" goes back to the default folder.
func TestDownloadDir(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	}))
	defer files.Close()
	st := store.Open(t.TempDir())
	def := t.TempDir()
	srv := &Server{Store: st, OutDir: def, Pool: links.PoolOf(links.New("lo", net.IPv4(127, 0, 0, 1)))}
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
	waitFile := func(path string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(path); err == nil {
				return
			}
		}
		t.Fatalf("%s was not written", path)
	}

	chosen := filepath.Join(t.TempDir(), "new", "folder") // created on save
	body, _ := json.Marshal(map[string]string{"dir": chosen})
	if c := post("/api/downloaddir", string(body)); c != http.StatusNoContent {
		t.Fatalf("save: HTTP %d", c)
	}
	if srv.downloadDir() != chosen || st.DownloadDir() != chosen {
		t.Fatalf("folder %q / %q", srv.downloadDir(), st.DownloadDir())
	}
	post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/a.txt"))
	waitFile(filepath.Join(chosen, "a.txt"))

	if c := post("/api/downloaddir", `{"dir":"relative\folder"}`); c != http.StatusBadRequest {
		t.Errorf("relative folder: HTTP %d, want 400", c)
	}
	if c := post("/api/downloaddir", `{"dir":""}`); c != http.StatusNoContent || srv.downloadDir() != def {
		t.Errorf("back to default: HTTP %d, folder %q", c, srv.downloadDir())
	}
	post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/b.txt"))
	waitFile(filepath.Join(def, "b.txt"))
}

// Pause keeps the .part file, resume finishes with the right content, and
// cancelling a paused download deletes the partial file.
func TestPauseResume(t *testing.T) {
	data := make([]byte, 6<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	gate := make(chan struct{})
	var blocked atomic.Int32
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rg := r.Header.Get("Range"); rg != "" && !strings.HasPrefix(rg, "bytes=0-") {
			blocked.Add(1)
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(data))
	}))
	defer files.Close()

	out := t.TempDir()
	srv := &Server{Store: store.Open(t.TempDir()), OutDir: out, Pool: links.PoolOf(links.New("lo", net.IPv4(127, 0, 0, 1)))}
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
	state := func() string {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return srv.jobs[0].state
	}
	waitState := func(want string) {
		t.Helper()
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if state() == want {
				return
			}
		}
		t.Fatalf("state %s, want %s", state(), want)
	}
	part := filepath.Join(out, "f.part")

	post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/f"))
	for end := time.Now().Add(10 * time.Second); blocked.Load() == 0 && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
	}
	post("/api/pause?id=1", "")
	waitState("paused")
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("the partial file was not kept: %v", err)
	}
	if c := post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/f")); c != http.StatusConflict {
		t.Errorf("same address while paused: HTTP %d, want 409", c)
	}

	close(gate)
	post("/api/resume?id=1", "")
	waitState("done")
	got, err := os.ReadFile(filepath.Join(out, "f"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("resumed file is wrong (err %v, %d bytes)", err, len(got))
	}

	// cancel while paused deletes the partial file
	gate = make(chan struct{})
	blocked.Store(0)
	post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/f2"))
	for end := time.Now().Add(10 * time.Second); blocked.Load() == 0 && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
	}
	post("/api/pause?id=2", "")
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		srv.mu.Lock()
		s := srv.jobs[1].state
		srv.mu.Unlock()
		if s == "paused" {
			break
		}
	}
	post("/api/cancel?id=2", "")
	srv.mu.Lock()
	s := srv.jobs[1].state
	srv.mu.Unlock()
	if s != "cancelled" {
		t.Errorf("state %s, want cancelled", s)
	}
	if _, err := os.Stat(filepath.Join(out, "f2.part")); err == nil {
		t.Error("the partial file of a cancelled download was kept")
	}
}
