package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/store"
)

// The dashboard is one big page of JavaScript: a typo there breaks it for
// everybody and no Go test notices. This test opens the real page in a headless
// Chrome or Edge (through the DevTools protocol), in every language, visits
// every page, and fails on any JavaScript error, any untranslated text and any
// leftover {placeholder}.
//
// It runs where a browser is found: always on Linux CI (and fails there if there
// is none), on a developer's machine when Chrome or Edge is installed, and
// elsewhere with DUALNET_UI_TEST=1. DUALNET_BROWSER names the program to use.

func findBrowser() string {
	if p := os.Getenv("DUALNET_BROWSER"); p != "" {
		return p
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, p := range []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// cdp is a minimal DevTools protocol client for one page.
type cdp struct {
	ws     *websocket.Conn
	mu     sync.Mutex
	id     int
	reply  map[int]chan json.RawMessage
	errors []string
}

func (c *cdp) readLoop() {
	for {
		var m struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := websocket.JSON.Receive(c.ws, &m); err != nil {
			return
		}
		c.mu.Lock()
		if m.ID != 0 {
			if ch := c.reply[m.ID]; ch != nil {
				ch <- m.Result
			}
		} else if msg := problem(m.Method, m.Params); msg != "" {
			c.errors = append(c.errors, msg)
		}
		c.mu.Unlock()
	}
}

// problem turns a protocol event into a description if it is a page error.
func problem(method string, params json.RawMessage) string {
	switch method {
	case "Runtime.exceptionThrown":
		var p struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		json.Unmarshal(params, &p)
		return "exception: " + p.ExceptionDetails.Text + " " + p.ExceptionDetails.Exception.Description
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Value       any    `json:"value"`
				Description string `json:"description"`
			} `json:"args"`
		}
		json.Unmarshal(params, &p)
		if p.Type != "error" {
			return ""
		}
		var parts []string
		for _, a := range p.Args {
			parts = append(parts, fmt.Sprint(a.Value, a.Description))
		}
		return "console.error: " + strings.Join(parts, " ")
	}
	return ""
}

func (c *cdp) call(method string, params map[string]any) json.RawMessage {
	c.mu.Lock()
	c.id++
	id := c.id
	ch := make(chan json.RawMessage, 1)
	c.reply[id] = ch
	c.mu.Unlock()
	websocket.JSON.Send(c.ws, map[string]any{"id": id, "method": method, "params": params})
	select {
	case r := <-ch:
		return r
	case <-time.After(20 * time.Second):
		return nil
	}
}

// eval runs a JavaScript expression in the page and returns its value as text.
func (c *cdp) eval(expr string) string {
	r := c.call("Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true})
	var v struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	json.Unmarshal(r, &v)
	if v.Result.Value == nil {
		return ""
	}
	return fmt.Sprint(v.Result.Value)
}

func (c *cdp) waitFor(expr string, d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if c.eval(expr) == "true" {
			return true
		}
	}
	return false
}

func startBrowser(t *testing.T, exe string) *cdp {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cmd := exec.Command(exe, "--headless=new", "--disable-gpu", "--no-sandbox", "--no-first-run", "--disable-extensions",
		fmt.Sprintf("--remote-debugging-port=%d", port), "--remote-allow-origins=*", "--user-data-dir="+t.TempDir(), "about:blank")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start %s: %v", exe, err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	var wsURL string
	for i := 0; i < 100 && wsURL == ""; i++ {
		time.Sleep(200 * time.Millisecond)
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var tabs []struct{ Type, WebSocketDebuggerUrl string }
		json.Unmarshal(b, &tabs)
		for _, tab := range tabs {
			if tab.Type == "page" {
				wsURL = tab.WebSocketDebuggerUrl
			}
		}
	}
	if wsURL == "" {
		t.Fatalf("%s did not open a debugging page", exe)
	}
	ws, err := websocket.Dial(wsURL, "", fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	c := &cdp{ws: ws, reply: map[int]chan json.RawMessage{}}
	go c.readLoop()
	t.Cleanup(func() { ws.Close() })
	c.call("Runtime.enable", nil)
	c.call("Page.enable", nil)
	c.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": 1200, "height": 900, "deviceScaleFactor": 1, "mobile": false})
	return c
}

func TestDashboardInBrowser(t *testing.T) {
	exe := findBrowser()
	ci := os.Getenv("GITHUB_ACTIONS") != ""
	if exe == "" {
		if ci && runtime.GOOS == "linux" {
			t.Fatal("no Chrome or Chromium found on the CI runner: the dashboard was not tested")
		}
		t.Skip("no Chrome, Chromium or Edge found (set DUALNET_BROWSER)")
	}
	if ci && runtime.GOOS != "linux" && os.Getenv("DUALNET_UI_TEST") != "1" {
		t.Skip("the browser test runs on the Linux CI runner")
	}

	st := store.Open(t.TempDir())
	st.SaveProfile("Home")
	srv := &Server{
		Store: st, Tun: &TunControl{}, Version: "vtest (build 1)", ProxyAddr: "127.0.0.1:1080", Journal: &Journal{},
		Pool:        links.PoolOf(links.New("Wi-Fi", net.IPv4(192, 168, 1, 20)), links.New("Phone", net.IPv4(10, 0, 0, 2))),
		CurrentSSID: func() string { return "Home Net" },
	}
	srv.Journal.Add("Phone", "disconnected")
	ts := httptest.NewServer(srv.Handler())
	// Cleanups run last-in first-out: the browser (started below) is stopped
	// first, which ends its live event stream; only then can the server close.
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })

	c := startBrowser(t, exe)
	// Every translated element must have text that is not its own key, and no
	// placeholder such as {n} may be left in what the user reads.
	const audit = `(() => {
	  const bad = [];
	  document.querySelectorAll('[data-i18n]').forEach(el => {
	    const k = el.getAttribute('data-i18n'), s = el.textContent.trim();
	    if (!s || s === k) bad.push('untranslated ' + k);
	  });
	  const left = document.body.innerText.match(/\{[a-zA-Z]+\}/g);
	  if (left) bad.push('placeholder left: ' + left.join(','));
	  return bad.join('; ');
	})()`

	for _, lang := range []string{"en", "fr", "es", "de"} {
		c.mu.Lock()
		c.errors = nil
		c.mu.Unlock()
		c.call("Page.navigate", map[string]any{"url": ts.URL + "/?lang=" + lang})
		if !c.waitFor(`document.getElementById('heroTitle') && document.getElementById('heroTitle').textContent !== ''`, 15*time.Second) {
			t.Fatalf("[%s] the dashboard did not show its first state", lang)
		}
		if got := c.eval(`document.documentElement.lang`); got != lang {
			t.Errorf("[%s] page language is %q", lang, got)
		}
		if !c.waitFor(`document.getElementById('welcome').hidden === false`, 10*time.Second) { // after the first snapshot
			t.Errorf("[%s] the welcome card must show on a fresh install", lang)
		}
		for _, page := range []string{"home", "downloads", "history", "settings"} {
			c.eval(fmt.Sprintf(`document.querySelector('[data-page="%s"]').click()`, page))
			if !c.waitFor(fmt.Sprintf(`document.getElementById('page-%s').classList.contains('active')`, page), 5*time.Second) {
				t.Errorf("[%s] page %s did not open", lang, page)
			}
			time.Sleep(600 * time.Millisecond) // let the page load its data
			if bad := c.eval(audit); bad != "" {
				t.Errorf("[%s] page %s: %s", lang, page, bad)
			}
		}
		c.eval(`document.querySelector('[data-page="home"]').click()`)
		c.eval(`document.getElementById('welcomeGot').click()`)
		if !c.waitFor(`document.getElementById('welcome').hidden === true`, 3*time.Second) {
			t.Errorf("[%s] \"Got it\" did not hide the welcome card", lang)
		}
		if lang != "de" {
			st.SetWelcomed(false) // shown again for the next language
		}
		c.mu.Lock()
		errs := append([]string(nil), c.errors...)
		c.mu.Unlock()
		for _, e := range errs {
			t.Errorf("[%s] %s", lang, e)
		}
	}
}
