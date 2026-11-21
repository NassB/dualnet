//go:build !windows

package ui

// pickFolder: no folder picker outside Windows; the folder is typed instead.
func pickFolder(string) (string, error) { return "", errNoPicker }
