package store

import (
	"sort"
	"strings"

	"github.com/NassB/dualnet/internal/links"
)

// A Profile is a named snapshot of how links are used (speed caps, overflow
// links, budgets, application and domain rules), so that one click switches
// between, say, "Home" and "Travel".
type Profile struct {
	Limits      map[string]float64 `json:"limits,omitempty"`
	Overflow    map[string]bool    `json:"overflow,omitempty"`
	Budgets     map[string]float64 `json:"budgets,omitempty"`
	WarnPercent int                `json:"warnPercent,omitempty"`
	Rules       []Rule             `json:"rules,omitempty"`
	DomainRules []DomainRule       `json:"domainRules,omitempty"`
}

// NormalizeProfile trims a profile name; "" means it is not valid.
func NormalizeProfile(n string) string {
	n = strings.TrimSpace(n)
	if len(n) > 40 || strings.ContainsAny(n, "<>\"'\\/") {
		return ""
	}
	return n
}

func copyMap[V any](m map[string]V) map[string]V {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Profiles returns the saved profile names, sorted.
func (s *Store) Profiles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := []string{}
	for n := range s.settings.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SaveProfile stores the current choices under name (replacing any profile of that name).
func (s *Store) SaveProfile(name string) bool {
	if name = NormalizeProfile(name); name == "" {
		return false
	}
	s.mu.Lock()
	if s.settings.Profiles == nil {
		s.settings.Profiles = map[string]Profile{}
	}
	s.settings.Profiles[name] = Profile{
		Limits:      copyMap(s.settings.Limits),
		Overflow:    copyMap(s.settings.Overflow),
		Budgets:     copyMap(s.settings.Budgets),
		WarnPercent: s.settings.BudgetWarnPercent,
		Rules:       append([]Rule(nil), s.settings.Rules...),
		DomainRules: append([]DomainRule(nil), s.settings.DomainRules...),
	}
	s.mu.Unlock()
	s.Save()
	return true
}

// DeleteProfile removes a profile.
func (s *Store) DeleteProfile(name string) {
	s.mu.Lock()
	delete(s.settings.Profiles, strings.TrimSpace(name))
	s.mu.Unlock()
	s.Save()
}

// ApplyProfile makes a profile's choices current. It reports false when there
// is no such profile. Caps are applied to the links (a link the profile does
// not cap becomes uncapped) and budgets are enforced at once.
func (s *Store) ApplyProfile(name string, pool *links.Pool) bool {
	s.mu.Lock()
	p, ok := s.settings.Profiles[strings.TrimSpace(name)]
	if ok {
		s.settings.Limits = copyMap(p.Limits)
		if s.settings.Limits == nil {
			s.settings.Limits = map[string]float64{}
		}
		s.settings.Overflow = copyMap(p.Overflow)
		s.settings.Budgets = copyMap(p.Budgets)
		s.settings.BudgetWarnPercent = p.WarnPercent
		s.publishDomains()
		s.settings.Rules = append([]Rule(nil), p.Rules...)
		s.settings.DomainRules = append([]DomainRule(nil), p.DomainRules...)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	warnMu.Lock() // the new budgets may already be past their warning level
	clear(warned)
	warnMu.Unlock()
	s.Save()
	for _, l := range pool.All() {
		s.mu.Lock()
		v := s.settings.Limits[l.Name]
		s.mu.Unlock()
		l.SetLimitMbps(v) // 0 = no cap
	}
	return true
}
