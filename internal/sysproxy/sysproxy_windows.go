// Package sysproxy turns the Windows per-user proxy (Settings → Network → Proxy)
// on and off, and always puts the user's previous settings back.
//
// The previous settings are also saved to a backup file, so if dualnet is killed
// while the proxy is on, the next launch restores them (otherwise the PC would
// have no internet: the proxy would point to a program that is not running).
package sysproxy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// KeyPath is the registry key holding the proxy settings (HKCU). Tests point it elsewhere.
var KeyPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// BackupFile stores the settings to restore after a crash.
var BackupFile = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "dualnet", "proxy-backup.json")
}()

// Settings is the part of the Windows proxy configuration dualnet touches.
type Settings struct {
	Enable   uint32 `json:"enable"`
	Server   string `json:"server"`
	Override string `json:"override"`
}

var mu sync.Mutex

func read() (Settings, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, KeyPath, registry.QUERY_VALUE)
	if err != nil {
		return Settings{}, err
	}
	defer k.Close()
	var s Settings
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		s.Enable = uint32(v)
	}
	s.Server, _, _ = k.GetStringValue("ProxyServer")
	s.Override, _, _ = k.GetStringValue("ProxyOverride")
	return s, nil
}

func write(s Settings) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, KeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetDWordValue("ProxyEnable", s.Enable); err != nil {
		return err
	}
	for name, v := range map[string]string{"ProxyServer": s.Server, "ProxyOverride": s.Override} {
		if v == "" {
			k.DeleteValue(name)
		} else if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	refresh()
	return nil
}

// refresh tells running programs (WinINet: Edge, Steam…) that the settings changed.
func refresh() {
	if KeyPath != `Software\Microsoft\Windows\CurrentVersion\Internet Settings` {
		return
	}
	dll := syscall.NewLazyDLL("wininet.dll")
	set := dll.NewProc("InternetSetOptionW")
	const settingsChanged, refreshOpt = 39, 37
	set.Call(0, settingsChanged, uintptr(unsafe.Pointer(nil)), 0)
	set.Call(0, refreshOpt, uintptr(unsafe.Pointer(nil)), 0)
}

// Status reports whether the system proxy is on and to which address.
func Status() (enabled bool, server string, err error) {
	s, err := read()
	return s.Enable == 1, s.Server, err
}

// Enable points the Windows proxy to addr, saving the current settings first.
func Enable(addr string) error {
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(BackupFile); err != nil { // keep the oldest backup: it's the user's real one
		cur, err := read()
		if err != nil {
			return err
		}
		b, _ := json.Marshal(cur)
		os.MkdirAll(filepath.Dir(BackupFile), 0o755)
		if err := os.WriteFile(BackupFile, b, 0o600); err != nil {
			return err
		}
	}
	return write(Settings{Enable: 1, Server: addr, Override: "<local>;localhost;127.*"})
}

// Disable restores the settings saved by Enable. It is a no-op if nothing was saved.
func Disable() error {
	mu.Lock()
	defer mu.Unlock()
	b, err := os.ReadFile(BackupFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var prev Settings
	if err := json.Unmarshal(b, &prev); err != nil {
		return err
	}
	if err := write(prev); err != nil {
		return err
	}
	return os.Remove(BackupFile)
}

// Pending reports whether a backup exists (dualnet changed the proxy and has not restored it).
func Pending() bool {
	_, err := os.Stat(BackupFile)
	return err == nil
}
