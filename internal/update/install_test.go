package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchAndSwap(t *testing.T) {
	program := append([]byte("MZ"), make([]byte, 2<<20)...)
	sum := fmt.Sprintf("%x  dualnet.exe\n", sha256.Sum256(program))
	served := program
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/o/r/releases/download/v1.2.3/dualnet.exe":
			w.Write(served)
		case "/o/r/releases/download/v1.2.3/dualnet.exe.sha256":
			w.Write([]byte(sum))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	exe := filepath.Join(dir, "dualnet.exe")
	os.WriteFile(exe, []byte("old program"), 0o755)
	c := &Checker{Current: "v1.0.0", Repo: "o/r", Download: srv.URL}

	if _, err := c.Fetch(context.Background(), exe); err == nil {
		t.Fatal("nothing to fetch before a check found an update")
	}
	c.info = Info{Available: true, Latest: "v1.2.3"}

	next, err := c.Fetch(context.Background(), exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := Swap(exe, next); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); len(got) != len(program) {
		t.Fatalf("the program was not replaced (%d bytes)", len(got))
	}
	if got, _ := os.ReadFile(exe + ".old"); string(got) != "old program" {
		t.Fatal("the previous program must be kept until the new one runs")
	}
	RemoveOld(exe)
	if _, err := os.Stat(exe + ".old"); err == nil {
		t.Fatal(".old left behind")
	}

	// a damaged download is refused and the program is left alone
	served = append([]byte("MZ"), make([]byte, 2<<20-1)...)
	if _, err := c.Fetch(context.Background(), exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want a checksum error, got %v", err)
	}
	if _, err := os.Stat(exe + ".new"); err == nil {
		t.Fatal("an unverified file must not be written")
	}

	// a version that is not a plain tag never becomes part of a URL
	c.info = Info{Available: true, Latest: "../../evil"}
	if _, err := c.Fetch(context.Background(), exe); err == nil {
		t.Fatal("unexpected version accepted")
	}
}

func TestParseSum(t *testing.T) {
	if _, err := parseSum([]byte("")); err == nil {
		t.Fatal("empty")
	}
	if _, err := parseSum([]byte("abcd  dualnet.exe")); err == nil {
		t.Fatal("too short")
	}
	if s, err := parseSum([]byte(strings.Repeat("ab", 32) + " *dualnet.exe\n")); err != nil || len(s) != 32 {
		t.Fatal(err)
	}
}
