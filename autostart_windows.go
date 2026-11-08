package main

import (
	"os"

	"github.com/NassB/dualnet/internal/installer"
)

const autostartSupported = true

func autostartEnabled() bool { return installer.AutostartEnabled() }

func setAutostart(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return installer.SetAutostart(exe, on)
}
