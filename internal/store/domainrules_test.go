package store

import "testing"

func TestDomainRules(t *testing.T) {
	s := Open(t.TempDir())
	if !s.SetDomainRule("*.Example.com", "wifi") || !s.SetDomainRule("cdn.example.com", "usb") {
		t.Fatal("valid domains refused")
	}
	if s.SetDomainRule("bad/domain", "wifi") || s.SetDomainRule("", "wifi") {
		t.Fatal("invalid domain accepted")
	}
	for host, want := range map[string]string{
		"example.com":         "wifi",
		"www.example.com":     "wifi",
		"cdn.example.com":     "usb", // most specific wins
		"a.cdn.example.com.":  "usb",
		"notexample.com":      "",
		"example.com.evil.io": "",
	} {
		if got := s.DomainRuleLink(host); got != want {
			t.Errorf("%s: want %q, got %q", host, want, got)
		}
	}
	s.SetDomainRule("example.com", "")
	if got := s.DomainRuleLink("www.example.com"); got != "" {
		t.Errorf("removed rule still applies: %q", got)
	}
	if got := Open(s.Dir).DomainRuleLink("x.cdn.example.com"); got != "usb" {
		t.Errorf("rules are not persisted: %q", got)
	}
}
