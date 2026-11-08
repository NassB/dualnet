//go:build !windows

package installer

import "errors"

var errUnsupported = errors.New("Windows only")

// Version is shown in "Apps & features" (set by the main package).
var Version = "dev"

// DefaultDir is where Install puts the program.
func DefaultDir() string { return "" }

// Installed reports whether dualnet is installed.
func Installed() bool { return false }

// Install copies the running program to dir and registers it.
func Install(dir string) (*Result, error) { return nil, errUnsupported }

// Uninstall removes everything Install and the autostart created.
func Uninstall(Options) (*Result, error) { return nil, errUnsupported }
