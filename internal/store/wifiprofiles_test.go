package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

func TestWifiRules(t *testing.T) {
	s := Open(t.TempDir())
	if s.SetWifiProfile("Home", "Home") {
		t.Fatal("a rule cannot point to a profile that does not exist")
	}
	s.SaveProfile("Home")
	s.SaveProfile("Travel")
	if !s.SetWifiProfile("Home Net", "Home") || !s.SetWifiProfile("Hotel", "Travel") {
		t.Fatal("rules refused")
	}
	if r := s.WifiRules(); len(r) != 2 || r[0].SSID != "Home Net" || r[1].SSID != "Hotel" {
		t.Fatalf("%+v", r)
	}
	if Open(s.Dir).WifiProfile("Hotel") != "Travel" {
		t.Fatal("the rules must survive a restart")
	}
	s.DeleteProfile("Travel")
	if s.WifiProfile("Hotel") != "" || len(s.WifiRules()) != 1 {
		t.Fatal("deleting a profile must remove the rules that point to it")
	}
	s.SetWifiProfile("Home Net", "")
	if len(s.WifiRules()) != 0 {
		t.Fatal("an empty profile removes the rule")
	}
}

// The profile is applied when the network changes, not over and over.
func TestFollowWifi(t *testing.T) {
	s := Open(t.TempDir())
	pool := links.PoolOf()
	setLimit(s, 1) // something that differs between the two profiles
	s.SaveProfile("Capped")
	setLimit(s, 0)
	s.SaveProfile("Free")
	s.SetWifiProfile("Home", "Free")
	s.SetWifiProfile("Hotel", "Capped")

	var mu sync.Mutex
	ssid := "Home"
	var applied []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.FollowWifi(ctx, pool, 10*time.Millisecond, func() string { mu.Lock(); defer mu.Unlock(); return ssid },
			func(n, p string) { mu.Lock(); applied = append(applied, n+">"+p); mu.Unlock() })
	}()
	wait := func(n int) {
		t.Helper()
		for i := 0; i < 200; i++ {
			mu.Lock()
			got := len(applied)
			mu.Unlock()
			if got >= n {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("waiting for %d applications, got %v", n, applied)
	}
	wait(1) // at start, for the network the PC is already on
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if len(applied) != 1 {
		t.Fatalf("the profile must not be re-applied while the network stays: %v", applied)
	}
	ssid = "Hotel"
	mu.Unlock()
	wait(2)
	mu.Lock()
	ssid = "" // no Wi-Fi: nothing to apply
	mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	ssid = "Unknown net" // a network without a rule
	mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(applied) != 2 || applied[0] != "Home>Free" || applied[1] != "Hotel>Capped" {
		t.Fatalf("applied %v", applied)
	}
	if limitOf(s) != 1 {
		t.Fatal("the Hotel profile (capped) should be the one in force")
	}
}

func setLimit(s *Store, v float64) {
	s.mu.Lock()
	s.settings.Limits["x"] = v
	s.mu.Unlock()
}

func limitOf(s *Store) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.Limits["x"]
}
