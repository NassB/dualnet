//go:build integration && windows

package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var exePath string // dualnet.exe built once for all tests

func isAdmin() bool {
	f, err := os.Open(`\\.\PHYSICALDRIVE0`)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// inCI is true on GitHub Actions. The tests stop every running dualnet and
// change the network, so on a developer's machine they only run if asked
// (DUALNET_IT=1).
func inCI() bool { return os.Getenv("GITHUB_ACTIONS") != "" }

func allowed() bool { return inCI() || os.Getenv("DUALNET_IT") == "1" }

func TestMain(m *testing.M) {
	if !isAdmin() {
		if inCI() {
			fmt.Println("FAIL: the CI runner is not an administrator: nothing was tested")
			os.Exit(1)
		}
		fmt.Println("integration tests need an administrator shell: skipped")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "dualnet-it-")
	if err != nil {
		panic(err)
	}
	exePath = filepath.Join(dir, "dualnet.exe")
	out, err := exec.Command("go", "build", "-o", exePath, "..").CombinedOutput()
	if err != nil {
		fmt.Println("build failed:", err, string(out))
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func run(t *testing.T, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func regKeyExists() bool {
	return exec.Command("reg", "query", `HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall\dualnet`).Run() == nil
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// install -> files, shortcut and registry entry exist -> uninstall -purge -> all gone.
func TestInstallUninstall(t *testing.T) {
	if !allowed() {
		t.Skip("stops every running dualnet: set DUALNET_IT=1 to run it on this machine")
	}
	if regKeyExists() {
		t.Skip("dualnet is already installed on this machine")
	}
	base := t.TempDir()
	inst := filepath.Join(base, "Program Files", "dualnet")
	data := filepath.Join(base, "dualnet")
	os.MkdirAll(data, 0o755)
	os.WriteFile(filepath.Join(data, "settings.json"), []byte("{}"), 0o644)
	env := []string{"DUALNET_DATA=" + data}

	out := run(t, env, exePath, "install", "-dir", inst)
	t.Log(out)
	if !exists(filepath.Join(inst, "dualnet.exe")) {
		t.Fatal("dualnet.exe was not copied")
	}
	if !regKeyExists() {
		t.Fatal("no uninstall entry in the registry")
	}
	lnk := filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs\dualnet.lnk`)
	if !exists(lnk) {
		t.Fatal("no Start Menu shortcut")
	}
	// the installed copy answers
	if v := run(t, env, filepath.Join(inst, "dualnet.exe"), "version"); !strings.Contains(v, "dualnet") {
		t.Fatalf("installed exe: %q", v)
	}
	// installing again updates in place
	run(t, env, filepath.Join(inst, "dualnet.exe"), "install", "-dir", inst)

	t.Log(run(t, env, filepath.Join(inst, "dualnet.exe"), "uninstall", "-force", "-purge"))
	if regKeyExists() {
		t.Error("uninstall entry still in the registry")
	}
	if exists(lnk) {
		t.Error("Start Menu shortcut still there")
	}
	if exists(data) {
		t.Error("data folder not purged")
	}
	// the program folder is deleted by a helper shortly after the exit
	waitFor(t, "the install folder to disappear", 30*time.Second, func() bool { return !exists(inst) })
}

type snapshot struct {
	Tun struct {
		On  bool   `json:"on"`
		Err string `json:"err"`
	} `json:"tun"`
	Apps []struct {
		Name   string  `json:"name"`
		Rx     int64   `json:"rx"`
		RxMbps float64 `json:"rxMbps"`
		Limit  int     `json:"limit"`
	} `json:"apps"`
}

// latest reads one snapshot from the interface's event stream.
func latest(base string) (snapshot, error) {
	var s snapshot
	req, _ := http.NewRequest("GET", base+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data:") {
			return s, json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &s)
		}
	}
	return s, sc.Err()
}

func post(t *testing.T, base, path string) {
	t.Helper()
	req, _ := http.NewRequest("POST", base+path, nil)
	req.Header.Set("X-Dualnet", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: %d %s", path, resp.StatusCode, b)
	}
}

// download fetches size bytes from a CDN, outside any proxy, and returns the time it took.
func download(t *testing.T, size int) time.Duration {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", size), nil)
	req.Header.Set("User-Agent", "dualnet-integration-test")
	c := &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	start := time.Now()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil || n < int64(size) {
		t.Fatalf("downloaded %d of %d bytes: %v", n, size, err)
	}
	return time.Since(start)
}

// Transparent mode with real traffic: the adapter comes up, the test's own
// download is attributed to this program, a per-application cap slows it, and
// switching it off removes the adapter.
func TestTransparentMode(t *testing.T) {
	if !allowed() {
		t.Skip("changes the machine's routes: set DUALNET_IT=1 to run it on this machine")
	}
	base := t.TempDir()
	data := filepath.Join(base, "data")
	exe := filepath.Join(base, "dualnet.exe")
	b, _ := os.ReadFile(exePath)
	if err := os.WriteFile(exe, b, 0o755); err != nil {
		t.Fatal(err)
	}
	const ui = "127.0.0.1:18090"
	cmd := exec.Command(exe, "ui", "-listen", ui, "-proxy", "127.0.0.1:11080", "-dir", filepath.Join(base, "dl"), "-sticky", "0", "-transparent")
	cmd.Env = append(os.Environ(), "DUALNET_DATA="+data)
	var logs strings.Builder
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// switch the mode off first so a failed test never leaves routes behind
		req, _ := http.NewRequest("POST", "http://"+ui+"/api/transparent?on=0", nil)
		req.Header.Set("X-Dualnet", "1")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			time.Sleep(2 * time.Second)
		}
		cmd.Process.Kill()
		cmd.Wait()
		t.Log("dualnet output:\n" + logs.String())
	}()
	url := "http://" + ui

	waitFor(t, "the interface", 30*time.Second, func() bool { _, err := latest(url); return err == nil })
	waitFor(t, "transparent mode", 60*time.Second, func() bool {
		s, err := latest(url)
		return err == nil && s.Tun.On
	})
	if out := run(t, nil, "powershell", "-NoProfile", "-Command", "Get-NetAdapter | Select-Object -ExpandProperty Name"); !strings.Contains(out, "dualnet") {
		t.Fatalf("no dualnet adapter:\n%s", out)
	}

	// a real download through the captured stack, attributed to this test binary
	const size = 30_000_000
	el := download(t, size)
	t.Logf("%d MB through transparent mode in %v (%.1f Mb/s)", size/1_000_000, el.Round(time.Millisecond), float64(size)*8/1e6/el.Seconds())
	me := strings.ToLower(filepath.Base(os.Args[0]))
	var found bool
	waitFor(t, "this program in the applications list", 15*time.Second, func() bool {
		s, err := latest(url)
		if err != nil {
			return false
		}
		for _, a := range s.Apps {
			if strings.ToLower(a.Name) == me && a.Rx >= size*9/10 {
				found = true
			}
		}
		return found
	})

	// a cap of 8 Mb/s (1 MB/s) on this program: 5 MB must take several seconds
	post(t, url, "/api/applimit?app="+me+"&mbps=8")
	time.Sleep(2 * time.Second) // the cap is re-read every second
	capped := download(t, 5_000_000)
	t.Logf("5 MB with an 8 Mb/s cap: %v", capped.Round(time.Millisecond))
	if capped < 3*time.Second || capped > 20*time.Second {
		t.Errorf("5 MB at 1 MB/s took %v, want about 5 s", capped)
	}
	post(t, url, "/api/applimit?app="+me+"&mbps=0")

	// off: the adapter goes away
	post(t, url, "/api/transparent?on=0")
	waitFor(t, "the adapter to be removed", 30*time.Second, func() bool {
		out, _ := exec.Command("powershell", "-NoProfile", "-Command", "Get-NetAdapter | Select-Object -ExpandProperty Name").CombinedOutput()
		return !strings.Contains(string(out), "dualnet")
	})
	// and the network still works
	download(t, 1_000_000)
}
