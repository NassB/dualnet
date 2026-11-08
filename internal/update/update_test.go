package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.3.0", "v0.2.2", true},
		{"v0.2.3", "v0.2.2", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.2.2", "v0.2.2", false},
		{"v0.2.1", "v0.2.2", false},
		{"v0.10.0", "v0.9.0", true}, // numeric, not alphabetical
		{"v0.2.2", "v0.2.2-dev", true},
		{"v0.2.2", "v0.2.2-local", true},
		{"v0.2.2", "dev", false},
		{"v0.2.2", "dev-abc1234", false},
		{"nonsense", "v0.2.2", false},
		{"v0.2.2", "", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func fakeAPI(t *testing.T, body string, hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/repos/NassB/dualnet/releases/latest" || r.Header.Get("User-Agent") == "" {
			http.Error(w, "bad request", 400)
			return
		}
		fmt.Fprint(w, body)
	}))
}

func TestCheck(t *testing.T) {
	var hits atomic.Int32
	rel := func(tag, url string, extra string) string {
		return fmt.Sprintf(`{"tag_name":%q,"html_url":%q%s}`, tag, url, extra)
	}
	good := "https://github.com/NassB/dualnet/releases/tag/v0.3.0"

	run := func(body, current string, enabled bool) (Info, int32) {
		hits.Store(0)
		srv := fakeAPI(t, body, &hits)
		defer srv.Close()
		c := &Checker{Current: current, Repo: "NassB/dualnet", API: srv.URL, Enabled: func() bool { return enabled }}
		if err := c.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		return c.Info(), hits.Load()
	}

	if i, _ := run(rel("v0.3.0", good, ""), "v0.2.2", true); !i.Available || i.Latest != "v0.3.0" || i.URL != good {
		t.Errorf("newer release not offered: %+v", i)
	}
	if i, _ := run(rel("v0.2.2", good, ""), "v0.2.2", true); i.Available {
		t.Errorf("same version offered: %+v", i)
	}
	if i, _ := run(rel("v0.3.0", good, `,"prerelease":true`), "v0.2.2", true); i.Available {
		t.Errorf("pre-release offered: %+v", i)
	}
	if i, _ := run(rel("v0.3.0", good, `,"draft":true`), "v0.2.2", true); i.Available {
		t.Errorf("draft offered: %+v", i)
	}
	// a page outside our repository is never offered, whatever the API says
	if i, _ := run(rel("v0.3.0", "https://evil.example/dualnet.exe", ""), "v0.2.2", true); i.Available {
		t.Errorf("foreign URL offered: %+v", i)
	}
	if i, _ := run(rel("v0.3.0", "https://github.com/other/repo/releases/tag/v0.3.0", ""), "v0.2.2", true); i.Available {
		t.Errorf("other repository offered: %+v", i)
	}
	// disabled: no request at all
	if _, hits := run(rel("v0.3.0", good, ""), "v0.2.2", false); hits != 0 {
		t.Errorf("disabled checker made %d requests", hits)
	}
}
