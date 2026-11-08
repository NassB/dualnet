package ui

import (
	"fmt"
	"os/exec"

	"golang.org/x/sys/windows"
)

func osVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("Windows %d.%d build %d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

func openFolder(dir string) { exec.Command("explorer", dir).Start() }
