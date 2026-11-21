package ui

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/download"
	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/store"
)

func TestOpenCommand(t *testing.T) {
	for _, tt := range []struct {
		os, name string
		args     []string
	}{
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", "https://example.com"}},
		{"darwin", "open", []string{"https://example.com"}},
		{"linux", "xdg-open", []string{"https://example.com"}},
	} {
		name, args, err := openCommand(tt.os, "https://example.com")
		if err != nil || name != tt.name || !reflect.DeepEqual(args, tt.args) {
			t.Errorf("%s: %s %v, %v", tt.os, name, args, err)
		}
	}
	if _, _, err := openCommand("other", "x"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}

func TestOpenBrowserRejectsInvalidURLs(t *testing.T) {
	for _, target := range []string{"file:///tmp/a", "https:", "javascript:alert(1)", ""} {
		if err := OpenBrowser(target); err == nil {
			t.Errorf("accepted %q", target)
		}
	}
}

func TestOpenFileEndpoint(t *testing.T) {
	var opened []string
	old := openPath
	openPath = func(p string) error { opened = append(opened, p); return nil }
	defer func() { openPath = old }()

	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f", time.Time{}, strings.NewReader("hello"))
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
	for i, name := range []string{"b.txt", "gone.txt", "c.exe"} {
		post("/api/download", fmt.Sprintf(`{"url":%q}`, files.URL+"/"+name))
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			srv.mu.Lock()
			st := srv.jobs[i].state
			srv.mu.Unlock()
			if st == "done" {
				break
			}
		}
	}
	os.Remove(filepath.Join(out, "gone.txt"))
	srv.mu.Lock()
	srv.jobs = append(srv.jobs, &job{id: 4, state: "running", stats: &download.Stats{}})
	srv.mu.Unlock()

	for id, want := range map[int]int{4: 409, 2: 404, 3: 403, 9: 404, 1: 204} {
		if c := post(fmt.Sprintf("/api/openfile?id=%d", id), ""); c != want {
			t.Errorf("id %d: HTTP %d, want %d", id, c, want)
		}
	}
	if len(opened) != 1 || filepath.Base(opened[0]) != "b.txt" {
		t.Errorf("opened %v", opened)
	}
}
