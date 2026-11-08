package store

import (
	"strings"
)

// A Rule restricts one application to one link: its connections only use that
// link while it is usable, and fall back to the others if it is not (an app
// must never be cut off). Applications are named by their executable
// ("game.exe"), compared without regard to case.
type Rule struct {
	App  string `json:"app"`
	Link string `json:"link"`
}

// Rules returns the rules, in the order they were added.
func (s *Store) Rules() []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Rule(nil), s.settings.Rules...)
}

// RuleLink returns the link an application is restricted to ("" = none).
func (s *Store) RuleLink(app string) string {
	app = strings.ToLower(app)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.settings.Rules {
		if r.App == app {
			return r.Link
		}
	}
	return ""
}

// RuleLinks is RuleLink as the list of allowed links the proxy expects
// (nil = unrestricted).
func (s *Store) RuleLinks(app string) []string {
	if l := s.RuleLink(app); l != "" {
		return []string{l}
	}
	return nil
}

// SetRule restricts an application to a link; an empty link removes the rule.
func (s *Store) SetRule(app, link string) {
	app = strings.ToLower(strings.TrimSpace(app))
	if app == "" {
		return
	}
	s.mu.Lock()
	rules := s.settings.Rules[:0:0]
	replaced := false
	for _, r := range s.settings.Rules {
		switch {
		case r.App != app:
			rules = append(rules, r)
		case link != "" && !replaced:
			rules = append(rules, Rule{App: app, Link: link})
			replaced = true
		}
	}
	if link != "" && !replaced {
		rules = append(rules, Rule{App: app, Link: link})
	}
	s.settings.Rules = rules
	s.mu.Unlock()
	s.Save()
}
