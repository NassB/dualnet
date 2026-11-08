//go:build !windows

package main

import (
	"context"
	"fmt"
)

func runTray(context.Context) { fmt.Println("tray mode: Windows only") }
