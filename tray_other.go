//go:build !windows

package main

import (
	"context"
	"fmt"
)

func runTray(context.Context, bool) { fmt.Println("tray mode: Windows only") }

const trayURL = "http://127.0.0.1:8090"

var windowsFrench = false

func openWindow(string) { fmt.Println("window: Windows only") }
