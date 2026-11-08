package installer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/NassB/dualnet/internal/sysproxy"
	"github.com/NassB/dualnet/internal/winexec"
)

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + Name

// Version is shown in "Apps & features" (set by the main package).
var Version = "dev"

// DefaultDir is %ProgramFiles%\dualnet.
func DefaultDir() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, Name)
}

func shortcutPath() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, `Microsoft\Windows\Start Menu\Programs`, Name+".lnk")
}

// Installed reports whether the uninstall entry exists.
func Installed() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// isAdmin tells whether the process can write under HKLM and Program Files:
// only an elevated process may open the physical drive.
func isAdmin() bool {
	f, err := os.Open(`\\.\PHYSICALDRIVE0`)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// replacing a running exe fails: Windows lets us rename it away first
	if _, err := os.Stat(dst); err == nil {
		os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	os.Remove(dst + ".old")
	return nil
}

// Install copies the running program into dir (DefaultDir when empty), adds a
// Start Menu shortcut and the uninstall entry. It needs administrator rights.
func Install(dir string) (*Result, error) {
	if !isAdmin() {
		return nil, errors.New("installing needs administrator rights: run it from an administrator terminal")
	}
	if dir == "" {
		dir = DefaultDir()
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	res := &Result{}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	dst := exeIn(dir)
	if samePath(self, dst) {
		res.add("already running from " + dir)
	} else {
		if err := copyFile(self, dst); err != nil {
			return res, fmt.Errorf("copy to %s: %w (is dualnet running from there? quit it first)", dir, err)
		}
		res.add("copied dualnet.exe to " + dir)
	}

	lnk := shortcutPath()
	script := `$s=(New-Object -ComObject WScript.Shell).CreateShortcut($env:DN_LNK);` +
		`$s.TargetPath=$env:DN_EXE;$s.WorkingDirectory=$env:DN_DIR;$s.IconLocation=$env:DN_EXE+',0';$s.Save()`
	cmd := winexec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "DN_LNK="+lnk, "DN_EXE="+dst, "DN_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return res, fmt.Errorf("Start Menu shortcut: %v: %s", err, strings.TrimSpace(string(out)))
	}
	res.add("Start Menu shortcut: " + lnk)

	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return res, err
	}
	defer k.Close()
	for name, val := range map[string]string{
		"DisplayName":     Name,
		"DisplayVersion":  Version,
		"Publisher":       "NassB",
		"InstallLocation": dir,
		"DisplayIcon":     dst,
		"UninstallString": uninstallString(dst),
		"URLInfoAbout":    "https://github.com/NassB/dualnet",
	} {
		if err := k.SetStringValue(name, val); err != nil {
			return res, err
		}
	}
	k.SetDWordValue("NoModify", 1)
	k.SetDWordValue("NoRepair", 1)
	res.add(`uninstall entry in "Apps & features"`)
	return res, nil
}

func schtasks(args ...string) error {
	_, err := winexec.Command("schtasks", args...).CombinedOutput()
	return err
}

// Uninstall stops dualnet, removes the start-with-Windows task, a Windows proxy
// that still points to dualnet, the shortcut, the uninstall entry and the
// program folder (and with Purge, the data folder).
func Uninstall(o Options) (*Result, error) {
	if !isAdmin() {
		return nil, errors.New("uninstalling needs administrator rights: run it from an administrator terminal")
	}
	res := &Result{}
	self, _ := os.Executable()

	// the folder to remove: the registered one, else the one we run from
	dir := ""
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallKey, registry.QUERY_VALUE); err == nil {
		dir, _, _ = k.GetStringValue("InstallLocation")
		k.Close()
	}
	if dir == "" && self != "" && samePath(filepath.Dir(self), DefaultDir()) {
		dir = DefaultDir()
	}

	// other running instances would keep files open
	if err := winexec.Command("taskkill", "/F", "/IM", Name+".exe", "/FI", fmt.Sprintf("PID ne %d", os.Getpid())).Run(); err == nil {
		res.add("stopped the running dualnet")
		time.Sleep(500 * time.Millisecond)
	}

	if schtasks("/Query", "/TN", Name) == nil {
		if err := schtasks("/Delete", "/F", "/TN", Name); err == nil {
			res.add("removed the start-with-Windows task")
		}
	}
	if sysproxy.ClearOurs("127.0.0.1:1080") == nil {
		res.add("checked the Windows proxy setting")
	}
	if err := os.Remove(shortcutPath()); err == nil {
		res.add("removed the Start Menu shortcut")
	}
	if err := registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey); err == nil {
		res.add(`removed the "Apps & features" entry`)
	}

	if dir != "" {
		if self != "" && inside(self, dir) {
			// we are running from the folder: a helper deletes it after we exit
			winexec.Command("cmd", "/c", selfDeleteCommand(dir)).Start()
			res.add("the program folder will be deleted in a few seconds: " + dir)
		} else if err := os.RemoveAll(dir); err != nil {
			return res, err
		} else {
			res.add("deleted " + dir)
		}
	}
	if o.Purge && o.DataDir != "" {
		if err := os.RemoveAll(o.DataDir); err != nil {
			return res, err
		}
		res.add("deleted the data folder " + o.DataDir)
	}
	return res, nil
}
