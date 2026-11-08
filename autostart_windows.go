package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// The autostart is a scheduled task at logon with highest privileges, so the
// transparent mode starts without a UAC prompt.
const taskName = "dualnet"

const autostartSupported = true

func schtasks(args ...string) ([]byte, error) {
	cmd := exec.Command("schtasks", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.CombinedOutput()
}

func autostartEnabled() bool {
	_, err := schtasks("/Query", "/TN", taskName)
	return err == nil
}

func setAutostart(on bool) error {
	var out []byte
	var err error
	if on {
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		out, err = schtasks("/Create", "/F", "/TN", taskName, "/SC", "ONLOGON", "/RL", "HIGHEST",
			"/TR", "\""+exe+"\" tray")
	} else {
		out, err = schtasks("/Delete", "/F", "/TN", taskName)
	}
	if err != nil {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}
