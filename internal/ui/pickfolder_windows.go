package ui

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// pickFolder shows Windows' folder picker, starting at start, and returns the
// chosen folder ("" = cancelled). The dialog belongs to a hidden, topmost
// window so it opens above dualnet's window and the browser.
func pickFolder(start string) (string, error) {
	const script = `[Console]::OutputEncoding = [Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.SelectedPath = $env:DUALNET_START
$d.ShowNewFolderButton = $true
$o = New-Object System.Windows.Forms.Form -Property @{TopMost = $true}
if ($d.ShowDialog($o) -eq 'OK') { [Console]::Out.Write($d.SelectedPath) }`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "DUALNET_START="+start)
	// No console window; not HideWindow, which would also hide the dialog.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
