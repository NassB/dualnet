//go:build !windows

package ui

func osVersion() string { return "unknown OS version" }

func openFolder(string) {}
