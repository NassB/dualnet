//go:build !windows

package main

import (
	"context"
	"fmt"
)

func runTray(context.Context) { fmt.Println("mode barre des tâches : Windows uniquement") }
