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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

type countResp struct {
	http.ResponseWriter
	n     *atomic.Int64
	delay time.Duration
}

func (c countResp) Write(p []byte) (int, error) {
	n, err := slowWriter{c.ResponseWriter, c.delay}.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// An interrupted download leaves <name>.part and its state; the next run of the
// same URL only fetches what is missing, and the result is intact.
func TestResume(t *testing.T) {
	data := make([]byte, 12<<20+777)
	rand.Read(data)
	size := int64(len(data))
	var served, delay atomic.Int64
	const v1 = `"v1"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", v1)
		http.ServeContent(countResp{w, &served, time.Duration(delay.Load())}, r, "f", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	ls := []*links.Link{links.New("a", net.IPv4(127, 0, 0, 1)), links.New("b", net.IPv4(127, 0, 0, 1))}
	out := filepath.Join(t.TempDir(), "file.bin")
	opts := Options{URL: srv.URL, Output: out, ChunkSize: 1 << 20, WorkersPerLnk: 2, Quiet: true}

	delay.Store(int64(10 * time.Millisecond)) // slow enough to be interrupted half-way
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	err := Run(ctx, ls, opts)
	cancel()
	if err == nil {
		t.Fatal("the first run should have been interrupted")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("an unfinished download must not have the final name")
	}
	have := resumable(out, srv.URL, size, "etag:"+v1)
	if total(have) == 0 || total(have) >= size {
		t.Fatalf("%d bytes recorded after the interruption", total(have))
	}

	delay.Store(0)
	served.Store(0)
	st := &Stats{}
	opts.Stats = st
	if err := Run(context.Background(), ls, opts); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, data) {
		t.Fatal("content mismatch after resuming")
	}
	if st.Resumed() != total(have) {
		t.Fatalf("resumed %d, recorded %d", st.Resumed(), total(have))
	}
	if n := served.Load(); n > size-total(have)+2<<20 { // some slack: ranges cut short by a steal still send a little
		t.Fatalf("the second run fetched %d bytes; only %d were missing", n, size-total(have))
	}
	for _, leftover := range []string{out + partSuffix, out + stateSuffix} {
		if _, err := os.Stat(leftover); err == nil {
			t.Fatalf("%s left behind", leftover)
		}
	}

	// the file changed on the server: nothing of an old partial file is reused
	saveState(out, state{URL: srv.URL, Size: size, Validator: "etag:" + v1, Done: []span{{0, 99}}})
	os.WriteFile(out+partSuffix, make([]byte, size), 0o644)
	if resumable(out, srv.URL, size, "etag:"+v1) == nil {
		t.Fatal("the same version must be resumable")
	}
	if resumable(out, srv.URL, size, `etag:"v2"`) != nil || resumable(out, srv.URL, size, "") != nil {
		t.Fatal("another version of the file must start over")
	}
	RemovePartial(out)

	// the same file twice at once is refused
	release, err := claim(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), ls, opts); err != ErrBusy {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	release()
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"file.iso":                  "file.iso",
		`..\..\Startup\evil.exe`:    "evil.exe",
		"../../etc/passwd":          "passwd",
		`C:\Windows\x.dll`:          "x.dll",
		"..":                        "download.bin",
		"":                          "download.bin",
		"a<b>:c?.txt":               "a_b__c_.txt",
		"name. ":                    "name",
		"folder/":                   "download.bin",
		string([]byte{'a', 0, 'b'}): "a_b",
	} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSpans(t *testing.T) {
	m := merge([]span{{10, 19}, {0, 4}, {5, 9}, {30, 39}, {35, 50}})
	if len(m) != 2 || m[0] != (span{0, 19}) || m[1] != (span{30, 50}) || total(m) != 41 {
		t.Fatal(m)
	}
	g := gaps(100, m)
	if len(g) != 2 || g[0] != (span{20, 29}) || g[1] != (span{51, 99}) {
		t.Fatal(g)
	}
	if g := gaps(10, nil); len(g) != 1 || g[0] != (span{0, 9}) {
		t.Fatal(g)
	}
}
