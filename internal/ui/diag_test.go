package ui

import (
	"encoding/json"
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

	"github.com/NassB/dualnet/internal/doctor"
	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/proxy"
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
	if post("/api/rule?app=game.exe&link=*ping", true) != 204 || st.RuleLink("game.exe") != proxy.LowestPing {
		t.Fatal("the lowest-ping rule must be accepted")
	}
	if post("/api/rule?app=game.exe&link=", true) != 204 || st.RuleLink("game.exe") != "" {
		t.Error("an empty link must remove the rule")
	}
	// the events stream carries apps and rules (empty here: nothing captured)
	if post("/api/applimit?app=game.exe&mbps=20", false) != 403 || st.RuleLimit("game.exe") != 0 {
		t.Error("app limit accepted without the guard header")
	}
	if post("/api/applimit?app=game.exe&mbps=20", true) != 204 || st.RuleLimit("game.exe") != 20 {
		t.Error("app limit not set")
	}
	for _, bad := range []string{"app=&mbps=5", "app=a%2Fb&mbps=5", "app=x.exe&mbps=-1", "app=x.exe&mbps=abc", "app=x.exe&mbps=999999"} {
		if got := post("/api/applimit?"+bad, true); got != 400 {
			t.Errorf("%s: status %d, want 400", bad, got)
		}
	}
	if v := srv.ruleViews(); len(v) != 1 || v[0].Limit != 20 {
		t.Errorf("ruleViews = %v", v)
	}
	st.SetRule("game.exe", "Wi-Fi")
	if post("/api/rule?app=game.exe&link=&all=1", true) != 204 || len(st.Rules()) != 0 {
		t.Errorf("all=1 must remove the link and the cap: %+v", st.Rules())
	}
	if v := srv.ruleViews(); len(v) != 0 {
		t.Errorf("ruleViews = %v", v)
	}
	if v := srv.appViews(srv.Pool.All(), time.Second, map[string]int64{}); v == nil || len(v) != 0 {
		t.Errorf("appViews = %#v, want a non-nil empty list (JSON [])", v)
	}
}

func TestDoctorEndpointAndOnboarding(t *testing.T) {
	st := store.Open(t.TempDir())
	wifi := links.New("Wi-Fi", net.IPv4(192, 168, 1, 2))
	srv := &Server{Store: st, Pool: links.PoolOf(wifi), Tun: &TunControl{}, Version: "v9",
		DoctorEnv: func() doctor.Env {
			return doctor.Env{Pool: links.PoolOf(wifi), Admin: true, SimultaneousAllowed: func() bool { return false }}
		}}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/doctor")
	if err != nil {
		t.Fatal(err)
	}
	var checks []doctor.Check
	if err := json.NewDecoder(resp.Body).Decode(&checks); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	resp.Body.Close()
	by := map[string]doctor.Check{}
	for _, c := range checks {
		by[c.ID] = c
	}
	if by["links"].Msg != "links.one" || by["wifi"].Fix != "simultaneous" || !by["wifi"].Core {
		t.Fatalf("checks = %+v", by)
	}

	// the report people paste into bug reports includes the checks
	if d := srv.diagnostics(); !strings.Contains(d, "checks:") || !strings.Contains(d, "[warn] Only one usable link") {
		t.Fatalf("diagnostics lacks the checks:\n%s", d)
	}

	post := func(path string, header bool) int {
		req, _ := http.NewRequest("POST", ts.URL+path, nil)
		if header {
			req.Header.Set(CSRFHeader, "1")
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if st.OnboardingDone() {
		t.Fatal("the checklist must show on a fresh install")
	}
	if post("/api/onboarding?done=1", false) != 403 || st.OnboardingDone() {
		t.Fatal("onboarding changed without the header")
	}
	if post("/api/onboarding?done=1", true) != 204 || !st.OnboardingDone() {
		t.Fatal("dismissing the checklist did not stick")
	}
	if post("/api/onboarding?done=0", true) != 204 || st.OnboardingDone() {
		t.Fatal("the checklist could not be shown again")
	}

	// the welcome card is separate from the checklist
	if st.Welcomed() {
		t.Fatal("the welcome card must show on a fresh install")
	}
	if post("/api/welcome?done=1", false) != 403 || st.Welcomed() {
		t.Fatal("welcome changed without the header")
	}
	if post("/api/welcome?done=1", true) != 204 || !st.Welcomed() || st.OnboardingDone() {
		t.Fatal("dismissing the welcome card did not stick, or touched the checklist")
	}
	if post("/api/welcome?done=0", true) != 204 || st.Welcomed() {
		t.Fatal("the welcome card could not be shown again")
	}
}

func TestSpeedTestEndpoint(t *testing.T) {
	chunk := make([]byte, 64<<10)
	bulk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer bulk.Close()

	st := store.Open(t.TempDir())
	wifi := links.New("Wi-Fi", net.IPv4(127, 0, 0, 1))
	px := &proxy.Server{Pool: links.PoolOf(wifi)}
	srv := &Server{Store: st, Pool: links.PoolOf(wifi), Tun: &TunControl{Proxy: px}, SpeedTestURL: bulk.URL, SpeedWindow: 1500 * time.Millisecond}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	post := func(path string, header bool) (int, string) {
		req, _ := http.NewRequest("POST", ts.URL+path, nil)
		if header {
			req.Header.Set(CSRFHeader, "1")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := post("/api/speedtest?link=Wi-Fi", false); code != 403 {
		t.Fatalf("without the header: HTTP %d, want 403", code)
	}
	if code, _ := post("/api/speedtest?link=Nope", true); code != 404 {
		t.Fatalf("unknown link: HTTP %d, want 404", code)
	}
	wifi.SetUp(false)
	if code, body := post("/api/speedtest?link=Wi-Fi", true); code != 409 || !strings.Contains(body, "not usable") {
		t.Fatalf("unusable link: HTTP %d %q, want 409", code, body)
	}
	wifi.SetUp(true)

	// two tests at once: one runs, the other is refused
	done := make(chan int)
	go func() { code, _ := post("/api/speedtest?link=Wi-Fi", true); done <- code }()
	time.Sleep(400 * time.Millisecond)
	if code, body := post("/api/speedtest?link=Wi-Fi", true); code != 409 || !strings.Contains(body, "already running") {
		t.Fatalf("second test: HTTP %d %q, want 409 already running", code, body)
	}
	if code := <-done; code != 200 {
		t.Fatalf("first test: HTTP %d, want 200", code)
	}

	// the result is saved and used at once by the picker
	r, ok := st.SpeedTest("Wi-Fi")
	if !ok || r.Mbps < 10 || time.Since(r.At) > time.Minute {
		t.Fatalf("saved result: %+v (ok=%v)", r, ok)
	}
	if got := px.Capacity(wifi); got < r.Mbps*0.99 {
		t.Fatalf("picker capacity %.0f, want at least the measured %.0f", got, r.Mbps)
	}
	if mbps, at := srv.speedOf(wifi); mbps != r.Mbps || at == 0 {
		t.Fatalf("speedOf = %v, %v", mbps, at)
	}
	// a failing server is reported, not saved
	srv.SpeedTestURL = "http://127.0.0.1:1/none"
	if code, body := post("/api/speedtest?link=Wi-Fi", true); code != 502 || !strings.Contains(body, "speed test failed") {
		t.Fatalf("failing server: HTTP %d %q, want 502", code, body)
	}
}

func TestRoleEndpoint(t *testing.T) {
	st := store.Open(t.TempDir())
	srv := &Server{Store: st, Pool: links.PoolOf(links.New("Phone", net.IPv4(10, 0, 0, 2)))}
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
	if post("/api/role?link=Phone&mode=overflow", false) != 403 || st.IsOverflow("Phone") {
		t.Fatal("role changed without the header")
	}
	if post("/api/role?link=Phone&mode=overflow", true) != 204 || !st.IsOverflow("Phone") {
		t.Fatal("overflow not set")
	}
	if ov, standby := srv.overflowOf(srv.Pool.All()[0]); !ov || standby { // no proxy running: never reported on standby
		t.Fatalf("overflowOf = %v, %v", ov, standby)
	}
	if post("/api/role?link=Phone&mode=weird", true) != 400 || post("/api/role?link=Phone&mode=", true) != 400 {
		t.Fatal("an unknown mode must be refused")
	}
	if post("/api/role?link=Nope&mode=normal", true) != 404 {
		t.Fatal("an unknown link must be a 404")
	}
	if post("/api/role?link=Phone&mode=normal", true) != 204 || st.IsOverflow("Phone") {
		t.Fatal("back to normal not applied")
	}
}

func TestWifiProfileEndpoints(t *testing.T) {
	st := store.Open(t.TempDir())
	st.SaveProfile("Home")
	srv := httptest.NewServer((&Server{Store: st, Pool: links.PoolOf(), CurrentSSID: func() string { return "Home Net" }}).Handler())
	defer srv.Close()
	call := func(method, path string, header bool) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Host = "127.0.0.1"
		if header {
			req.Header.Set("X-Dualnet", "1")
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if c, _ := call("POST", "/api/wifiprofile?ssid=Home+Net&profile=Home", false); c != 403 || st.WifiProfile("Home Net") != "" {
		t.Fatal("changed without the header")
	}
	if c, _ := call("POST", "/api/wifiprofile?ssid=Home+Net&profile=Nope", true); c != 404 {
		t.Fatalf("unknown profile: %d", c)
	}
	if c, _ := call("POST", "/api/wifiprofile?ssid=&profile=Home", true); c != 400 {
		t.Fatalf("empty network name: %d", c)
	}
	if c, _ := call("POST", "/api/wifiprofile?ssid=Home+Net&profile=Home", true); c != 204 || st.WifiProfile("Home Net") != "Home" {
		t.Fatal("rule not saved")
	}
	c, body := call("GET", "/api/wifi", false)
	if c != 200 || !strings.Contains(body, `"current":"Home Net"`) || !strings.Contains(body, `"profile":"Home"`) {
		t.Fatalf("%d %s", c, body)
	}
	if c, _ := call("POST", "/api/wifiprofile?ssid=Home+Net&profile=", true); c != 204 || st.WifiProfile("Home Net") != "" {
		t.Fatal("rule not removed")
	}
}
