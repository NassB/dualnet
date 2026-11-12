package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// A Wi-Fi rule applies a profile by itself when the PC joins a Wi-Fi network:
// "Home" at home, "Travel" on the hotel's network. The profile is applied when
// the network changes, not continuously, so a manual change stays until the
// next network.

// WifiRule is one network and the profile to apply on it.
type WifiRule struct {
	SSID    string `json:"ssid"`
	Profile string `json:"profile"`
}

// WifiRules returns the rules, sorted by network name.
func (s *Store) WifiRules() []WifiRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []WifiRule{}
	for ssid, p := range s.settings.WifiProfiles {
		out = append(out, WifiRule{ssid, p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SSID < out[j].SSID })
	return out
}

// WifiProfile returns the profile to apply on a network ("" = none).
func (s *Store) WifiProfile(ssid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.WifiProfiles[ssid]
}

// SetWifiProfile sets the profile to apply on a network; an empty profile removes
// the rule. It reports false if the profile does not exist.
func (s *Store) SetWifiProfile(ssid, profile string) bool {
	profile = strings.TrimSpace(profile)
	s.mu.Lock()
	if profile == "" {
		delete(s.settings.WifiProfiles, ssid)
	} else {
		if _, ok := s.settings.Profiles[profile]; !ok {
			s.mu.Unlock()
			return false
		}
		if s.settings.WifiProfiles == nil {
			s.settings.WifiProfiles = map[string]string{}
		}
		s.settings.WifiProfiles[ssid] = profile
	}
	s.mu.Unlock()
	s.Save()
	return true
}

// FollowWifi applies the profile of the Wi-Fi network the PC is on, now and
// each time the network changes, until ctx is done. current returns the network
// name ("" = none); onApply is told about each profile applied.
func (s *Store) FollowWifi(ctx context.Context, pool *links.Pool, every time.Duration, current func() string, onApply func(ssid, profile string)) {
	last, first := "", true
	for {
		if ssid := current(); first || ssid != last {
			first, last = false, ssid
			if name := s.WifiProfile(ssid); ssid != "" && name != "" && s.ApplyProfile(name, pool) {
				s.Enforce(pool, time.Now())
				if onApply != nil {
					onApply(ssid, name)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
