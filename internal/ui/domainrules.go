package ui

import (
	"encoding/json"
	"net/http"

	"github.com/NassB/dualnet/internal/store"
)

func (s *Server) getDomainRules(w http.ResponseWriter, r *http.Request) {
	rules := []store.DomainRule{}
	if s.Store != nil {
		rules = append(rules, s.Store.DomainRules()...)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rules)
}

// setDomainRule sends a domain (and its subdomains) over a link:
// ?domain=<name>&link=<link name>. An empty link removes the rule.
func (s *Server) setDomainRule(w http.ResponseWriter, r *http.Request) {
	domain, link := r.URL.Query().Get("domain"), r.URL.Query().Get("link")
	if s.Store == nil || store.NormalizeDomain(domain) == "" {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	if link != "" {
		known := false
		for _, l := range s.Pool.All() {
			known = known || l.Name == link
		}
		if !known {
			http.NotFound(w, r)
			return
		}
	}
	s.Store.SetDomainRule(domain, link)
	w.WriteHeader(http.StatusNoContent)
}
