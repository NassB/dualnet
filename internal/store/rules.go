package store

import (
	"strings"
)

// A Rule restricts one application to one link: its connections only use that
// link while it is usable, and fall back to the others if it is not (an app
// must never be cut off). Applications are named by their executable
// ("game.exe"), compared without regard to case.
//
// A rule can also cap the application's download speed (Limit, in Mb/s, over
// all its connections together; 0 = no cap). A rule with neither a link nor a
// cap does not exist.
type Rule struct {
	App   string `json:"app"`
	Link  string `json:"link"`
	Limit int    `json:"limit,omitempty"`
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

// RuleLimit returns an application's download cap in Mb/s (0 = none).
func (s *Store) RuleLimit(app string) int {
	app = strings.ToLower(app)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.settings.Rules {
		if r.App == app {
			return r.Limit
		}
	}
	return 0
}

// SetRule restricts an application to a link; an empty link removes the
// restriction (its speed cap is kept).
func (s *Store) SetRule(app, link string) {
	s.update(app, func(r *Rule) { r.Link = link })
}

// SetRuleLimit caps an application's download speed in Mb/s; 0 removes the cap.
func (s *Store) SetRuleLimit(app string, mbps int) {
	if mbps < 0 {
		mbps = 0
	}
	s.update(app, func(r *Rule) { r.Limit = mbps })
}

// update edits (or creates) an application's rule, keeping its place in the
// list, and drops the rule when nothing is left in it.
func (s *Store) update(app string, edit func(*Rule)) {
	app = strings.ToLower(strings.TrimSpace(app))
	if app == "" {
		return
	}
	s.mu.Lock()
	rules := s.settings.Rules[:0:0]
	found := false
	for _, r := range s.settings.Rules {
		if r.App == app {
			if found {
				continue
			}
			found = true
			edit(&r)
		}
		rules = append(rules, r)
	}
	if !found {
		r := Rule{App: app}
		edit(&r)
		rules = append(rules, r)
	}
	kept := rules[:0:0]
	for _, r := range rules {
		if r.Link != "" || r.Limit > 0 {
			kept = append(kept, r)
		}
	}
	s.settings.Rules = kept
	s.mu.Unlock()
	s.Save()
}
