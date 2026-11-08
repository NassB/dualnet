//go:build windows

package sysproxy

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// Uses a throwaway registry key, never the real proxy settings.
func TestEnableDisableRestores(t *testing.T) {
	KeyPath = `Software\dualnet-test\Internet Settings`
	BackupFile = filepath.Join(t.TempDir(), "backup.json")
	defer registry.DeleteKey(registry.CURRENT_USER, `Software\dualnet-test\Internet Settings`)
	defer registry.DeleteKey(registry.CURRENT_USER, `Software\dualnet-test`)

	orig := Settings{Enable: 1, Server: "corp-proxy:8080", Override: "*.corp"}
	if err := write(orig); err != nil {
		t.Fatal(err)
	}
	if err := Enable("127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	on, server, _ := Status()
	if !on || server != "127.0.0.1:1080" || !Pending() {
		t.Fatalf("after Enable: on=%v server=%q pending=%v", on, server, Pending())
	}
	// Enabling twice must keep the user's original settings as the backup.
	if err := Enable("127.0.0.1:1081"); err != nil {
		t.Fatal(err)
	}
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	got, _ := read()
	if got != orig || Pending() {
		t.Fatalf("after Disable: got %+v want %+v (pending=%v)", got, orig, Pending())
	}
	// Disable without a backup is a no-op.
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
}
