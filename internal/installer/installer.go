// Package installer puts dualnet in its own folder (Program Files), gives it a
// Start Menu entry and an "Apps & features" uninstall entry, and removes all of
// that again. Nothing here runs unless the user asks for install or uninstall.
package installer

import (
	"path/filepath"
	"strings"
)

// Name of the program, its folder, its shortcut and its uninstall key.
const Name = "dualnet"

// Options of Uninstall.
type Options struct {
	// Purge also deletes the data folder (history, settings, rules, log).
	Purge bool
	// DataDir is the data folder removed by Purge.
	DataDir string
}

// Result lists what was done, one line each, for the user.
type Result struct{ Steps []string }

func (r *Result) add(s string) { r.Steps = append(r.Steps, s) }

// exeIn is where the program lives inside an install folder.
func exeIn(dir string) string { return filepath.Join(dir, Name+".exe") }

// samePath compares two Windows paths without regard to case or separators.
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// inside reports whether path is dir itself or lives under it.
func inside(path, dir string) bool {
	p, d := strings.ToLower(filepath.Clean(path)), strings.ToLower(filepath.Clean(dir))
	return p == d || strings.HasPrefix(p, d+`\`)
}

// selfDeleteCommand is the cmd.exe line that removes dir once the running
// program (which lives in it) has exited: it waits a few seconds, then deletes.
func selfDeleteCommand(dir string) string {
	return `ping -n 4 127.0.0.1 >nul & rmdir /s /q "` + dir + `"`
}

// uninstallString is the command "Apps & features" runs.
func uninstallString(exe string) string { return `"` + exe + `" uninstall` }
