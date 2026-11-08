package ui

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/store"
)

func TestTailLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	if got := tailLines(p, 5); got != "" {
		t.Fatalf("missing file: %q", got)
	}
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&sb, "line %d\r\n", i)
	}
	os.WriteFile(p, []byte(sb.String()), 0o644)
	if got := tailLines(p, 3); got != "line 8\nline 9\nline 10" {
		t.Fatalf("last 3 lines = %q", got)
	}
	if got := tailLines(p, 50); !strings.HasPrefix(got, "line 1\n") || strings.Contains(got, "\r") {
		t.Fatalf("all lines = %q", got)
	}
	// a file bigger than the read window: the cut first line is dropped
	big := strings.Repeat("0123456789abcdef\n", 40000) // ~680 KB
	os.WriteFile(p, []byte(big+"the end\n"), 0o644)
	got := tailLines(p, 2)
	if got != "0123456789abcdef\nthe end" {
		t.Fatalf("big file tail = %q", got)
	}
}

func TestDiagnosticsEndpoints(t *testing.T) {
	dir := t.TempDir()
	st := store.Open(dir)
	os.WriteFile(filepath.Join(dir, "dualnet.log"), []byte("2026/11/08 12:00:00 hello from the log\n"), 0o644)
	srv := &Server{
		Store:   st,
		Pool:    links.PoolOf(links.New("Wi-Fi", net.IPv4(192, 168, 1, 2))),
		Tun:     &TunControl{},
		Version: "v9.9.9",
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(path string) string {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: HTTP %d", path, resp.StatusCode)
		}
		return string(b)
	}
	d := get("/api/diagnostics")
	for _, want := range []string{"dualnet v9.9.9", "Wi-Fi", "192.168.1.2", "hello from the log", "administrator:"} {
		if !strings.Contains(d, want) {
			t.Errorf("diagnostics lacks %q:\n%s", want, d)
		}
	}
	if l := get("/api/log"); !strings.Contains(l, "hello from the log") {
		t.Errorf("log endpoint: %q", l)
	}
}

// The new setting and the update endpoints go through the same guard.
func TestUpdateSettingEndpoint(t *testing.T) {
	st := store.Open(t.TempDir())
	ts := httptest.NewServer((&Server{Store: st}).Handler())
	defer ts.Close()
	post := func(path string, header bool) int {
		req, _ := http.NewRequest("POST", ts.URL+path, nil)
		if header {
			req.Header.Set(CSRFHeader, "1")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if !st.UpdateCheckEnabled() {
		t.Fatal("update check should be on by default")
	}
	if post("/api/updatecheck?on=0", false) != 403 || !st.UpdateCheckEnabled() {
		t.Fatal("setting changed without the header")
	}
	if post("/api/updatecheck?on=0", true) != 204 || st.UpdateCheckEnabled() {
		t.Fatal("setting not turned off")
	}
	if post("/api/updatecheck?on=1", true) != 204 || !st.UpdateCheckEnabled() {
		t.Fatal("setting not turned on")
	}
	if post("/api/update/open", true) != 404 {
		t.Fatal("opening an update that does not exist must be refused")
	}
}

func TestBudgetEndpoint(t *testing.T) {
	st := store.Open(t.TempDir())
	wifi := links.New("Wi-Fi", net.IPv4(192, 168, 1, 2))
	srv := &Server{Store: st, Pool: links.PoolOf(wifi)}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	post := func(path string, header bool) int {
		req, _ := http.NewRequest("POST", ts.URL+path, nil)
		if header {
			req.Header.Set(CSRFHeader, "1")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if post("/api/budget?link=Wi-Fi&gb=20", false) != 403 || st.Budget("Wi-Fi") != 0 {
		t.Fatal("budget changed without the header")
	}
	if post("/api/budget?link=Wi-Fi&gb=20", true) != 204 || st.Budget("Wi-Fi") != 20 {
		t.Fatalf("budget not set: %v", st.Budget("Wi-Fi"))
	}
	for _, bad := range []string{"gb=-1", "gb=abc", "gb=NaN", "gb=1e9", "gb="} {
		if got := post("/api/budget?link=Wi-Fi&"+bad, true); got != 400 {
			t.Errorf("%s: HTTP %d, want 400", bad, got)
		}
	}
	if post("/api/budget?link=Nope&gb=5", true) != 404 {
		t.Error("unknown link must be a 404")
	}
	if post("/api/budget?link=Wi-Fi&gb=0", true) != 204 || st.Budget("Wi-Fi") != 0 {
		t.Error("0 must remove the budget")
	}
}

func TestRuleEndpoint(t *testing.T) {
	st := store.Open(t.TempDir())
	srv := &Server{Store: st, Tun: &TunControl{}, Pool: links.PoolOf(links.New("Wi-Fi", net.IPv4(192, 168, 1, 2)))}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	post := func(path string, header bool) int {
		req, _ := http.NewRequest("POST", ts.URL+path, nil)
		if header {
			req.Header.Set(CSRFHeader, "1")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if post("/api/rule?app=game.exe&link=Wi-Fi", false) != 403 || len(st.Rules()) != 0 {
		t.Fatal("rule added without the header")
	}
	if post("/api/rule?app=game.exe&link=Wi-Fi", true) != 204 || st.RuleLink("game.exe") != "Wi-Fi" {
		t.Fatal("rule not added")
	}
	for _, bad := range []string{"app=&link=Wi-Fi", "app=..%5Cevil.exe&link=Wi-Fi", "app=a%2Fb&link=Wi-Fi", "app=C%3Agame.exe&link=Wi-Fi"} {
		if got := post("/api/rule?"+bad, true); got != 400 {
			t.Errorf("%s: HTTP %d, want 400", bad, got)
		}
	}
	if post("/api/rule?app=game.exe&link=Nope", true) != 404 {
		t.Error("a rule for an unknown link must be refused")
	}
	if post("/api/rule?app=game.exe&link=", true) != 204 || st.RuleLink("game.exe") != "" {
		t.Error("an empty link must remove the rule")
	}
	// the events stream carries apps and rules (empty here: nothing captured)
	if v := srv.ruleViews(); len(v) != 0 {
		t.Errorf("ruleViews = %v", v)
	}
	if v := srv.appViews(srv.Pool.All(), time.Second, map[string]int64{}); v == nil || len(v) != 0 {
		t.Errorf("appViews = %#v, want a non-nil empty list (JSON [])", v)
	}
}
