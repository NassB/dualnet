//go:build !windows

package sysproxy

import "errors"

var errUnsupported = errors.New("system proxy: Windows only")

func Status() (bool, string, error) { return false, "", errUnsupported }
func Enable(string) error           { return errUnsupported }
func Disable() error                { return nil }
func Pending() bool                 { return false }
