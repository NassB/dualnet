//go:build !windows

package main

import "errors"

const autostartSupported = false

func autostartEnabled() bool  { return false }
func setAutostart(bool) error { return errors.New("Windows only") }
