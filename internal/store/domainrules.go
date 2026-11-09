package store

import "strings"

// A DomainRule sends the connections to a domain (and its subdomains) over one
// link while it is usable; if it is not, the other links are used (a site must
// never be cut off). Unlike an application rule it also works in proxy mode.
type DomainRule struct {
	Domain string `json:"domain"`
	Link   string `json:"link"`
}

// NormalizeDomain lowercases a pattern and strips "*." / "." prefixes and a trailing dot.
// It returns "" when the result cannot be a domain name.
func NormalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(d, "*"), "."), ".")
	if d == "" || len(d) > 253 || strings.ContainsAny(d, " /\\:*?\"<>|@") {
		return ""
	}
	return d
}

// DomainRules returns the domain rules, in the order they were added.
func (s *Store) DomainRules() []DomainRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DomainRule(nil), s.settings.DomainRules...)
}

// DomainRuleLink returns the link host must use ("" = none). The most specific
// (longest) matching domain wins.
func (s *Store) DomainRuleLink(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	best, link := -1, ""
	snap := s.domains.Load()
	if snap == nil {
		return ""
	}
	for _, r := range *snap { // an immutable copy: no lock on the hot path
		if (host == r.Domain || strings.HasSuffix(host, "."+r.Domain)) && len(r.Domain) > best {
			best, link = len(r.Domain), r.Link
		}
	}
	return link
}

// publishDomains refreshes the lock-free copy read by DomainRuleLink. The
// caller holds s.mu (or the store is not shared yet).
func (s *Store) publishDomains() {
	cp := append([]DomainRule(nil), s.settings.DomainRules...)
	s.domains.Store(&cp)
}

// SetDomainRule sends domain over link; an empty link removes the rule.
// It reports false when domain is not valid.
func (s *Store) SetDomainRule(domain, link string) bool {
	domain = NormalizeDomain(domain)
	if domain == "" {
		return false
	}
	s.mu.Lock()
	rules := s.settings.DomainRules[:0:0]
	done := false
	for _, r := range s.settings.DomainRules {
		if r.Domain == domain {
			if link == "" || done {
				continue
			}
			r.Link, done = link, true
		}
		rules = append(rules, r)
	}
	if !done && link != "" {
		rules = append(rules, DomainRule{Domain: domain, Link: link})
	}
	s.settings.DomainRules = rules
	s.publishDomains()
	s.mu.Unlock()
	s.Save()
	return true
}
