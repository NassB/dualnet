package main

import (
	"sync"
	"time"

	"github.com/NassB/dualnet/internal/ui"
)

// autostartHooks exposes "start with Windows" to the UI. Querying the scheduled
// task spawns a process, so the state is cached and refreshed every 30 s.
func autostartHooks() *ui.Autostart {
	if !autostartSupported {
		return nil
	}
	var mu sync.Mutex
	var cached bool
	var at time.Time
	return &ui.Autostart{
		Enabled: func() bool {
			mu.Lock()
			defer mu.Unlock()
			if time.Since(at) > 30*time.Second {
				cached, at = autostartEnabled(), time.Now()
			}
			return cached
		},
		Set: func(on bool) error {
			err := setAutostart(on)
			mu.Lock()
			cached, at = autostartEnabled(), time.Now()
			mu.Unlock()
			return err
		},
	}
}
