package ui

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// budgetOf returns a link's monthly budget and its use this month, both in GB.
func (s *Server) budgetOf(l *links.Link, now time.Time) (budget, usedGB float64) {
	if s.Store == nil {
		return 0, 0
	}
	return s.Store.Budget(l.Name), float64(s.Store.MonthUsage(l.Name, now)) / 1e9
}

// setBudgetWarn sets the share of a budget at which a warning is given: ?percent=<50-99>.
func (s *Server) setBudgetWarn(w http.ResponseWriter, r *http.Request) {
	p, err := strconv.Atoi(r.URL.Query().Get("percent"))
	if err != nil || s.Store == nil || !s.Store.SetWarnPercent(p) {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setBudget sets a link's monthly data budget: ?link=<name>&gb=<n> (0 = none).
func (s *Server) setBudget(w http.ResponseWriter, r *http.Request) {
	gb, err := strconv.ParseFloat(r.URL.Query().Get("gb"), 64)
	if err != nil || gb < 0 || gb > 1e6 || math.IsNaN(gb) {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	name := r.URL.Query().Get("link")
	for _, l := range s.Pool.All() {
		if l.Name == name {
			s.Store.SetBudget(name, gb)
			s.Store.Enforce(s.Pool, time.Now()) // apply at once, not at the next tick
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.NotFound(w, r)
}
