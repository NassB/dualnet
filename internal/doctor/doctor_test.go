package doctor

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/NassB/dualnet/internal/links"
)

func link(name string, ip byte) *links.Link {
	l := links.New(name, net.IPv4(10, 0, 0, ip))
	l.SetLatencyMs(20)
	return l
}

func byID(cs []Check) map[string]Check {
	m := map[string]Check{}
	for _, c := range cs {
		m[c.ID] = c
	}
	return m
}

// healthy: administrator, two good links, transparent mode running.
func healthyEnv() Env {
	return Env{
		Pool:                links.PoolOf(link("Wi-Fi", 1), link("Phone", 2)),
		Admin:               true,
		TunOn:               true,
		PowerMode:           "transparent",
		ProxyAddr:           "127.0.0.1:1080",
		DataDir:             `C:\data`,
		ExePath:             `C:\Program Files\dualnet\dualnet.exe`,
		WebView2:            func() string { return "130.0.1" },
		SimultaneousAllowed: func() bool { return true },
		ProxyStatus:         func() (bool, string) { return false, "" },
		AdapterUp:           func(string) bool { return true },
		Reach:               func() error { return nil },
		DataWritable:        func(string) error { return nil },
		Autostart:           func() bool { return true },
	}
}

func TestHealthySetup(t *testing.T) {
	cs := Run(healthyEnv())
	if w := Worst(cs); w != OK {
		t.Fatalf("worst = %s, want ok:\n%s", w, Format(cs))
	}
	m := byID(cs)
	for _, id := range []string{"admin", "links", "link:Wi-Fi", "link:Phone", "wifi", "tun", "proxy", "net", "webview", "autostart", "exe", "data"} {
		if _, ok := m[id]; !ok {
			t.Errorf("missing check %q", id)
		}
	}
	if m["links"].Args["n"] != "2" || m["link:Wi-Fi"].Args["ms"] != "20" {
		t.Errorf("arguments for the translations: %+v / %+v", m["links"].Args, m["link:Wi-Fi"].Args)
	}
	for _, c := range cs {
		if c.Msg == "" || c.Text == "" {
			t.Errorf("%s: needs a message key and an English text", c.ID)
		}
	}
}

func TestOneLinkAndWifiPolicy(t *testing.T) {
	e := healthyEnv()
	e.Pool = links.PoolOf(link("Wi-Fi", 1))
	e.SimultaneousAllowed = func() bool { return false }
	m := byID(Run(e))
	if m["links"].Level != Warn || m["links"].Msg != "links.one" {
		t.Errorf("one link: %+v", m["links"])
	}
	if m["wifi"].Level != Warn || m["wifi"].Fix != "simultaneous" || !m["wifi"].Core {
		t.Errorf("Wi-Fi policy with one link: %+v", m["wifi"])
	}
	// both links work although the policy is not set: only a hint
	e.Pool = links.PoolOf(link("Wi-Fi", 1), link("Phone", 2))
	if w := byID(Run(e))["wifi"]; w.Level != Info || w.Fix != "simultaneous" {
		t.Errorf("Wi-Fi policy with two links: %+v", w)
	}
}

func TestLinkProblems(t *testing.T) {
	e := healthyEnv()
	down := link("Down", 3)
	down.SetUp(false)
	silent := link("Silent", 4)
	silent.SetHealthy(false)
	broke := link("Budget", 5)
	broke.SetOverBudget(true)
	e.Pool = links.PoolOf(link("Wi-Fi", 1), down, silent, broke)
	m := byID(Run(e))
	if m["link:Down"].Level != Info || m["link:Down"].Msg != "link.down" {
		t.Errorf("down: %+v", m["link:Down"])
	}
	if m["link:Silent"].Level != Fail || m["link:Silent"].Msg != "link.noanswer" {
		t.Errorf("not answering: %+v", m["link:Silent"])
	}
	if m["link:Budget"].Level != Warn || m["link:Budget"].Msg != "link.budget" {
		t.Errorf("over budget: %+v", m["link:Budget"])
	}
	if m["links"].Level != Warn { // only Wi-Fi is usable
		t.Errorf("links: %+v", m["links"])
	}

	e.Pool = links.PoolOf(down, silent)
	cs := Run(e)
	if byID(cs)["links"].Level != Fail || Worst(cs) != Fail {
		t.Errorf("no usable link must fail:\n%s", Format(cs))
	}
}

func TestStaleProxy(t *testing.T) {
	e := healthyEnv()
	e.ProxyStatus = func() (bool, string) { return true, "127.0.0.1:1080" }
	e.PowerMode = "" // dualnet off but Windows still points to it
	if p := byID(Run(e))["proxy"]; p.Level != Warn || p.Fix != "clear-proxy" {
		t.Errorf("stale proxy: %+v", p)
	}
	e.PowerMode = "proxy" // legitimately in use
	if p := byID(Run(e))["proxy"]; p.Level != OK {
		t.Errorf("proxy in use: %+v", p)
	}
	e.PowerMode = ""
	e.ProxyStatus = func() (bool, string) { return true, "corp-proxy:8080" } // someone else's
	if p := byID(Run(e))["proxy"]; p.Level != OK {
		t.Errorf("foreign proxy must be left alone: %+v", p)
	}
}

func TestTransparentAndAdmin(t *testing.T) {
	e := healthyEnv()
	e.AdapterUp = func(string) bool { return false }
	if tun := byID(Run(e))["tun"]; tun.Level != Fail || tun.Msg != "tun.missing" {
		t.Errorf("adapter missing: %+v", tun)
	}
	e = healthyEnv()
	e.TunOn, e.PowerMode = false, ""
	if tun := byID(Run(e))["tun"]; tun.Level != Info || tun.Fix != "power" {
		t.Errorf("transparent off as admin: %+v", tun)
	}
	e.Admin = false
	m := byID(Run(e))
	if m["admin"].Level != Info || m["admin"].Msg != "admin.no" {
		t.Errorf("not admin: %+v", m["admin"])
	}
	if _, ok := m["tun"]; ok {
		t.Error("no transparent-mode advice without administrator rights")
	}
}

func TestOtherChecks(t *testing.T) {
	e := healthyEnv()
	e.ExePath = `D:\PROGRAMMATION\dualnet\dualnet.exe`
	e.Reach = func() error { return errors.New("DNS lookup failed") }
	e.DataWritable = func(string) error { return errors.New("access denied") }
	e.WebView2 = func() string { return "" }
	e.Autostart = func() bool { return false }
	m := byID(Run(e))
	if m["exe"].Level != Info || !strings.Contains(m["exe"].Text, `C:\Program Files`) {
		t.Errorf("elevated from an unprotected folder: %+v", m["exe"])
	}
	if m["net"].Level != Fail || m["net"].Args["err"] != "DNS lookup failed" {
		t.Errorf("net: %+v", m["net"])
	}
	if m["data"].Level != Fail || m["webview"].Level != Warn {
		t.Errorf("data/webview: %+v / %+v", m["data"], m["webview"])
	}
	if a := m["autostart"]; a.Level != Info || a.Fix != "autostart" {
		t.Errorf("autostart: %+v", a)
	}
	// without administrator rights the folder does not matter
	e.Admin = false
	if byID(Run(e))["exe"].Level != OK {
		t.Error("exe location only matters when elevated")
	}
	// checks the platform cannot answer are skipped
	if m := byID(Run(Env{})); len(m) != 2 { // admin + links
		t.Errorf("empty env produced %d checks", len(m))
	}
}

func TestProtectedDirs(t *testing.T) {
	for dir, want := range map[string]bool{
		`C:\Program Files\dualnet`:       true,
		`c:\program files (x86)\dualnet`: true,
		`C:\Windows\System32`:            true,
		`C:\Program Files Evil\dualnet`:  false,
		`C:\Users\me\Downloads`:          false,
		`D:\PROGRAMMATION\dualnet`:       false,
	} {
		if got := isProtectedDir(dir); got != want {
			t.Errorf("isProtectedDir(%q) = %v, want %v", dir, got, want)
		}
	}
}

func TestFormat(t *testing.T) {
	out := Format([]Check{{Level: OK, Text: "fine"}, {Level: Fail, Text: "broken"}})
	if out != "[ ok ] fine\n[FAIL] broken\n" {
		t.Errorf("Format = %q", out)
	}
}
