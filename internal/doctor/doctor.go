// Package doctor checks that dualnet is set up properly: administrator rights,
// the links, Windows' Wi-Fi policy, the virtual adapter, a proxy left behind… It
// powers "dualnet doctor", the Settings page and the first-run checklist.
package doctor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NassB/dualnet/internal/links"
)

// Level says how serious a finding is.
type Level string

const (
	OK   Level = "ok"
	Info Level = "info"
	Warn Level = "warn"
	Fail Level = "fail"
)

// Check is one finding. Msg and Args let the interface translate it; Text is the
// English sentence (used by the command line and as a fallback).
type Check struct {
	ID    string            `json:"id"`
	Level Level             `json:"level"`
	Msg   string            `json:"msg"`
	Args  map[string]string `json:"args,omitempty"`
	Text  string            `json:"text"`
	Fix   string            `json:"fix,omitempty"` // an action the interface can offer: simultaneous, clear-proxy, power, autostart
	Core  bool              `json:"core"`          // part of the first-run checklist
}

// Env is everything the checks look at. Function fields are nil when the
// platform cannot answer (the matching check is skipped), and are replaced by
// fakes in the tests.
type Env struct {
	Pool      *links.Pool
	Admin     bool
	TunOn     bool   // transparent mode is on
	PowerMode string // "transparent", "proxy" or "" (dualnet off)
	ProxyAddr string // dualnet's own proxy address
	DataDir   string
	ExePath   string

	WebView2            func() string // runtime version, "" if missing
	SimultaneousAllowed func() bool   // Windows keeps Wi-Fi next to a wired link
	ProxyStatus         func() (on bool, server string)
	AdapterUp           func(name string) bool
	Reach               func() error // DNS + internet through the system route
	DataWritable        func(dir string) error
	Autostart           func() bool // nil = not available
}

func c(id string, lvl Level, msg, text string, args map[string]string) Check {
	return Check{ID: id, Level: lvl, Msg: msg, Args: args, Text: text}
}

// Run performs all the checks, most important first.
func Run(e Env) []Check {
	var out []Check
	add := func(ck Check, core bool, fix string) {
		ck.Core, ck.Fix = core, fix
		out = append(out, ck)
	}

	// administrator
	if e.Admin {
		add(c("admin", OK, "admin.ok", "Running as administrator.", nil), true, "")
	} else {
		add(c("admin", Info, "admin.no", "Not running as administrator: transparent mode is unavailable (proxy mode still works).", nil), true, "")
	}

	// links
	usable := 0
	if e.Pool != nil {
		usable = len(e.Pool.Up())
	}
	switch {
	case usable >= 2:
		add(c("links", OK, "links.ok", fmt.Sprintf("%d links are usable: they can be combined.", usable), map[string]string{"n": fmt.Sprint(usable)}), true, "")
	case usable == 1:
		add(c("links", Warn, "links.one", "Only one usable link: there is nothing to combine.", nil), true, "")
	default:
		add(c("links", Fail, "links.none", "No usable link: dualnet cannot reach the internet.", nil), true, "")
	}
	if e.Pool != nil {
		for _, l := range e.Pool.All() {
			a := map[string]string{"name": l.Name}
			switch {
			case !l.Up():
				add(c("link:"+l.Name, Info, "link.down", l.Name+": disconnected.", a), false, "")
			case !l.Healthy():
				add(c("link:"+l.Name, Fail, "link.noanswer", l.Name+": connected but not reaching the internet.", a), false, "")
			case l.OverBudget():
				add(c("link:"+l.Name, Warn, "link.budget", l.Name+": monthly budget used, no new connections are sent to it.", a), false, "")
			default:
				ms := l.LatencyMs()
				a["ms"] = fmt.Sprint(ms)
				add(c("link:"+l.Name, OK, "link.ok", fmt.Sprintf("%s: reaches the internet (ping %d ms).", l.Name, ms), a), false, "")
			}
		}
	}

	// Windows drops Wi-Fi when a wired link (USB tethering) appears
	if e.SimultaneousAllowed != nil {
		switch {
		case e.SimultaneousAllowed():
			add(c("wifi", OK, "wifi.ok", "Windows keeps Wi‑Fi connected next to a wired link.", nil), true, "")
		case usable >= 2:
			add(c("wifi", Info, "wifi.info", "Windows may drop Wi‑Fi when a wired link appears; both links work for now.", nil), true, "simultaneous")
		default:
			add(c("wifi", Warn, "wifi.warn", "By default Windows drops Wi‑Fi when the phone is plugged in over USB.", nil), true, "simultaneous")
		}
	}

	// transparent mode
	switch {
	case e.TunOn && e.AdapterUp != nil && !e.AdapterUp("dualnet"):
		add(c("tun", Fail, "tun.missing", "Transparent mode should be active but the virtual adapter is missing.", nil), true, "")
	case e.TunOn:
		add(c("tun", OK, "tun.ok", "Transparent mode is active.", nil), true, "")
	case e.Admin:
		add(c("tun", Info, "tun.off", "Transparent mode is off.", nil), true, "power")
	}

	// a Windows proxy left pointing to a dualnet that is not using it
	if e.ProxyStatus != nil && e.ProxyAddr != "" {
		on, server := e.ProxyStatus()
		if on && server == e.ProxyAddr && e.PowerMode != "proxy" {
			add(c("proxy", Warn, "proxy.stale", "The Windows proxy points to dualnet although dualnet is not using it.", nil), true, "clear-proxy")
		} else {
			add(c("proxy", OK, "proxy.ok", "The Windows proxy does not point to a stopped dualnet.", nil), false, "")
		}
	}

	// internet through the route Windows itself uses
	if e.Reach != nil {
		if err := e.Reach(); err != nil {
			add(c("net", Fail, "net.fail", "Internet check through the system route failed: "+err.Error()+".", map[string]string{"err": err.Error()}), false, "")
		} else {
			add(c("net", OK, "net.ok", "DNS and internet work through the system route.", nil), false, "")
		}
	}

	if e.WebView2 != nil {
		if v := e.WebView2(); v != "" {
			add(c("webview", OK, "webview.ok", "Microsoft Edge WebView2 "+v+" is installed.", map[string]string{"v": v}), false, "")
		} else {
			add(c("webview", Warn, "webview.no", "WebView2 is not installed: the interface opens in your browser.", nil), false, "")
		}
	}

	if e.Autostart != nil {
		if e.Autostart() {
			add(c("autostart", OK, "autostart.on", "dualnet starts with Windows.", nil), false, "")
		} else {
			add(c("autostart", Info, "autostart.off", "dualnet does not start with Windows.", nil), true, "autostart")
		}
	}

	if e.ExePath != "" {
		dir := filepath.Dir(e.ExePath)
		if e.Admin && !isProtectedDir(dir) {
			add(c("exe", Info, "exe.info", "dualnet runs as administrator from "+dir+": keep it in a folder only administrators can modify (for example C:\\Program Files\\dualnet).", map[string]string{"dir": dir}), false, "")
		} else {
			add(c("exe", OK, "exe.ok", "dualnet runs from "+dir+".", map[string]string{"dir": dir}), false, "")
		}
	}

	if e.DataWritable != nil && e.DataDir != "" {
		if err := e.DataWritable(e.DataDir); err != nil {
			add(c("data", Fail, "data.fail", "Cannot write to the data folder "+e.DataDir+": "+err.Error()+".", map[string]string{"dir": e.DataDir, "err": err.Error()}), false, "")
		} else {
			add(c("data", OK, "data.ok", "The data folder "+e.DataDir+" is writable.", map[string]string{"dir": e.DataDir}), false, "")
		}
	}
	return out
}

// isProtectedDir: folders only administrators can write to by default.
func isProtectedDir(dir string) bool {
	d := strings.ToLower(filepath.Clean(dir))
	for _, p := range []string{`c:\program files`, `c:\program files (x86)`, `c:\windows`} {
		if d == p || strings.HasPrefix(d, p+`\`) {
			return true
		}
	}
	return false
}

// Worst returns the most serious level among the checks.
func Worst(cs []Check) Level {
	w := OK
	rank := map[Level]int{OK: 0, Info: 1, Warn: 2, Fail: 3}
	for _, ck := range cs {
		if rank[ck.Level] > rank[w] {
			w = ck.Level
		}
	}
	return w
}

// Format renders the checks as text for the command line and bug reports.
func Format(cs []Check) string {
	var b strings.Builder
	tag := map[Level]string{OK: " ok ", Info: "info", Warn: "warn", Fail: "FAIL"}
	for _, ck := range cs {
		fmt.Fprintf(&b, "[%s] %s\n", tag[ck.Level], ck.Text)
	}
	return b.String()
}
