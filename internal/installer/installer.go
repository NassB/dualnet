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

// ownFiles are the only files dualnet puts in its program folder (wintun.dll is
// written next to the exe at run time; .old/.new are leftovers of an update).
var ownFiles = []string{"dualnet.exe", "wintun.dll", "dualnet.exe.old", "dualnet.exe.new"}

// selfDeleteCommand is the cmd.exe line that removes dualnet's own files from
// dir once the running program (which lives there) has exited, then the folder
// itself if nothing else is in it (rmdir without /s never deletes content).
func selfDeleteCommand(dir string) string {
	c := "ping -n 4 127.0.0.1 >nul"
	for _, f := range ownFiles {
		c += ` & del /f /q "` + filepath.Join(dir, f) + `"`
	}
	return c + ` & rmdir "` + dir + `"`
}

// installable reports whether a folder with these entries may receive the
// program: it must be empty or already hold dualnet.exe. Uninstall only removes
// what dualnet put there, but refusing a folder full of someone else's files
// keeps things tidy.
func installable(entries []string) bool {
	if len(entries) == 0 {
		return true
	}
	for _, e := range entries {
		if strings.EqualFold(e, "dualnet.exe") {
			return true
		}
	}
	return false
}

// dataDirOK reports whether dir looks like dualnet's own data folder: purge
// deletes it recursively, so DUALNET_DATA must not be able to aim it at, say,
// Documents.
func dataDirOK(dir string) bool {
	return dir != "" && strings.EqualFold(filepath.Base(filepath.Clean(dir)), Name)
}

// uninstallString is the command "Apps & features" runs.
func uninstallString(exe string) string { return `"` + exe + `" uninstall` }
