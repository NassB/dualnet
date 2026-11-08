//go:build !windows

package winsys

import "errors"

var errUnsupported = errors.New("Windows only")

func SimultaneousAllowed() bool     { return false }
func SetSimultaneous(bool) error    { return errUnsupported }
func Metrics() map[string]int       { return nil }
func Prefer(string, []string) error { return errUnsupported }
func RestartSteam() error           { return errUnsupported }
