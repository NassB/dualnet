// Package winsys wraps the Windows settings dualnet can change for the user,
// each from an explicit click in the UI:
//   - allow Wi-Fi and a wired link at the same time (fMinimizeConnections policy);
//   - which link Windows prefers for traffic that does not go through dualnet
//     (interface metric);
//   - restarting Steam so it reopens its connections.
package winsys

import (
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

const wcmKey = `SOFTWARE\Policies\Microsoft\Windows\WcmSvc\GroupPolicy`

// SimultaneousAllowed reports whether Windows keeps Wi-Fi connected when a
// wired link (e.g. USB tethering) is present.
func SimultaneousAllowed() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, wcmKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("fMinimizeConnections")
	return err == nil && v == 0
}

// SetSimultaneous allows (true) or restores Windows' default (false). Needs admin.
func SetSimultaneous(allow bool) error {
	if !allow {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, wcmKey, registry.SET_VALUE)
		if err != nil {
			return nil
		}
		defer k.Close()
		err = k.DeleteValue("fMinimizeConnections")
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, wcmKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue("fMinimizeConnections", 0)
}

var (
	metricMu   sync.Mutex
	metricAt   time.Time
	metricMemo map[string]int
	rowRe      = regexp.MustCompile(`^\s*\d+\s+(\d+)\s+\d+\s+\S+\s+(.+?)\s*$`)
)

// Metrics returns the IPv4 interface metric per interface name (lower = preferred).
// Cached for 5 s.
func Metrics() map[string]int {
	metricMu.Lock()
	defer metricMu.Unlock()
	if time.Since(metricAt) < 5*time.Second && metricMemo != nil {
		return metricMemo
	}
	out, err := exec.Command("netsh", "interface", "ipv4", "show", "interfaces").Output()
	m := map[string]int{}
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if r := rowRe.FindStringSubmatch(line); r != nil {
				v, _ := strconv.Atoi(r[1])
				m[r[2]] = v
			}
		}
	}
	metricMemo, metricAt = m, time.Now()
	return m
}

// Prefer makes Windows route its own traffic over link `name` first (metric 10)
// and the other given links after it (metric 50). Empty name restores automatic
// metrics on all of them. IPv4 and IPv6. Needs admin.
func Prefer(name string, all []string) error {
	for _, n := range all {
		for _, fam := range []string{"ipv4", "ipv6"} {
			metric := "50"
			switch {
			case name == "":
				metric = "0" // 0 = automatic
			case n == name:
				metric = "10"
			}
			if out, err := exec.Command("netsh", "interface", fam, "set", "interface", n, "metric="+metric).CombinedOutput(); err != nil {
				return errors.New(strings.TrimSpace(string(out)))
			}
		}
	}
	metricMu.Lock()
	metricAt = time.Time{}
	metricMu.Unlock()
	return nil
}

// RestartSteam closes Steam and opens it again as the logged-in user (not
// elevated, even if dualnet runs as administrator).
func RestartSteam() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Valve\Steam`, registry.QUERY_VALUE)
	if err != nil {
		return errors.New("Steam is not installed")
	}
	exe, _, err := k.GetStringValue("SteamExe")
	k.Close()
	if err != nil || exe == "" {
		return errors.New("Steam is not installed")
	}
	exec.Command(exe, "-shutdown").Run()
	for i := 0; i < 40 && steamRunning(); i++ {
		time.Sleep(500 * time.Millisecond)
	}
	// explorer.exe opens the URL through the user's shell, so Steam does not
	// inherit dualnet's administrator rights.
	return exec.Command("explorer.exe", "steam://open/main").Start()
}

func steamRunning() bool {
	out, _ := exec.Command("tasklist", "/FI", "IMAGENAME eq steam.exe", "/NH").Output()
	return strings.Contains(strings.ToLower(string(out)), "steam.exe")
}
