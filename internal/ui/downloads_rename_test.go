package ui

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/store"
)

func TestRenameAndFolder(t *testing.T) {
	data := make([]byte, 6<<20)
	for i := range data {
		data[i] = byte(i * 5)
	}
	gate := make(chan struct{})
	var blocked atomic.Int32
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rg := r.Header.Get("Range"); strings.HasPrefix(r.URL.Path, "/p") && rg != "" && !strings.HasPrefix(rg, "bytes=0-") {
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
	srv.Store.SetMaxDownloads(1)
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
	rename := func(id int, name string) int { return post(fmt.Sprintf("/api/jobname?id=%d&name=%s", id, url.QueryEscape(name)), "") }
	waitState := func(i int, want string) {
		t.Helper()
		var st string
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			srv.mu.Lock()
			st = srv.jobs[i].state
			srv.mu.Unlock()
			if st == want {
				return
			}
		}
		t.Fatalf("job %d state %s, want %s", i, st, want)
	}
	check := func(path string) {
		t.Helper()
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s wrong (err %v, %d bytes)", path, err, len(got))
		}
	}

	// 1: runs and blocks (limit 1); 2: queued, gets a name and a folder
	post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/p1"))
	for end := time.Now().Add(10 * time.Second); blocked.Load() == 0 && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
	}
	post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/q2"))
	other := filepath.Join(t.TempDir(), "sub")
	if c := rename(2, "../evil/new name.bin"); c != http.StatusBadRequest {
		t.Errorf("name with a folder part: HTTP %d, want 400", c)
	}
	if c := rename(2, "new name.bin"); c != http.StatusNoContent {
		t.Fatalf("rename queued: HTTP %d", c)
	}
	if c := post("/api/jobdir?id=2", `{"dir":"relative/dir"}`); c != http.StatusBadRequest {
		t.Errorf("relative folder: HTTP %d, want 400", c)
	}
	if c := post("/api/jobdir?id=2", fmt.Sprintf(`{"dir":%q}`, other)); c != http.StatusNoContent {
		t.Fatalf("folder of queued: HTTP %d", c)
	}
	// running: refused
	if c := rename(1, "x.bin"); c != http.StatusConflict {
		t.Errorf("rename running: HTTP %d, want 409", c)
	}
	// pause 1 with a .part, rename it, resume
	post("/api/pause?id=1", "")
	waitState(0, "paused")
	if c := rename(1, "moved.bin"); c != http.StatusNoContent {
		t.Fatalf("rename paused: HTTP %d", c)
	}
	for _, n := range []string{"moved.bin.part"} {
		if _, err := os.Stat(filepath.Join(out, n)); err != nil {
			t.Errorf("%s missing: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "p1.part")); err == nil {
		t.Error("old .part still there")
	}
	// an existing file is never replaced
	os.WriteFile(filepath.Join(out, "taken.bin"), []byte("x"), 0o644)
	if c := rename(1, "taken.bin"); c != http.StatusConflict {
		t.Errorf("existing file: HTTP %d, want 409", c)
	}
	close(gate)
	post("/api/resume?id=1", "")
	waitState(0, "done")
	waitState(1, "done")
	check(filepath.Join(out, "moved.bin"))
	check(filepath.Join(other, "new name.bin")) // queued job used its name and folder

	// done: rename and move; Open / reveal paths follow
	if c := rename(1, "final.bin"); c != http.StatusNoContent {
		t.Fatalf("rename done: HTTP %d", c)
	}
	check(filepath.Join(out, "final.bin"))
	if _, err := os.Stat(filepath.Join(out, "moved.bin")); err == nil {
		t.Error("old file still there")
	}
	srv.mu.Lock()
	f, _, _ := srv.jobs[0].stats.Snapshot()
	srv.mu.Unlock()
	if f != filepath.Join(out, "final.bin") {
		t.Errorf("job file %q", f)
	}
	var opened string
	openPath = func(p string) error { opened = p; return nil }
	if c := post("/api/openfile?id=1", ""); c != http.StatusNoContent || opened != filepath.Join(out, "final.bin") {
		t.Errorf("open: HTTP %d, opened %q", c, opened)
	}
	if c := rename(1, "new name.bin"); c != http.StatusNoContent { // same name as job 2's, other folder: ok
		t.Errorf("rename to a free name: HTTP %d", c)
	}
	if c := post("/api/jobdir?id=1", fmt.Sprintf(`{"dir":%q}`, other)); c != http.StatusConflict {
		t.Errorf("move onto an existing file: HTTP %d, want 409", c)
	}
	moved := filepath.Join(t.TempDir(), "d")
	if c := post("/api/jobdir?id=1", fmt.Sprintf(`{"dir":%q}`, moved)); c != http.StatusNoContent {
		t.Fatalf("move done: HTTP %d", c)
	}
	check(filepath.Join(moved, "new name.bin"))
	if c := post("/api/jobsha?id=1&sha256="+strings.Repeat("0", 64), ""); c != http.StatusNoContent {
		t.Fatalf("sha: HTTP %d", c)
	}
	srv.mu.Lock()
	st := srv.jobs[0].shaState
	srv.mu.Unlock()
	if st != "mismatch" { // the hash was computed on the moved file (a missing file is a mismatch with no hash)
		t.Errorf("shaState %q", st)
	} else if srv.jobs[0].shaGot == "" {
		t.Error("sha check could not read the moved file")
	}
}
